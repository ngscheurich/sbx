// `sbx build` is the only sbx command that builds images: it runs Docker
// with the [build] recipe from project configuration, exports the image to
// a temporary archive outside the repository, and imports it into msb's
// separate image store with `msb load --input`. A missing image everywhere
// else means "run `sbx build`" for a buildable project and "may be pulled"
// for a prebuilt one — up, exec, and run never build.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/docker"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/translate"
)

// runBuild implements `sbx build [--keep-archive]`. The archive is removed
// on success, failure, and cancellation alike unless --keep-archive keeps
// it for debugging.
func runBuild(ctx context.Context, args []string, out *output) int {
	keep, err := parsePersistentFlags("build", args, []string{"--keep-archive", "--json"})
	if err != nil {
		return out.usagef("%v", err)
	}

	out.json = keep["--json"]
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fatal := func(err error) int { return out.fail(err) }

	info, cfg, err := out.discoverConfig(ctx)
	if err != nil {
		return fatal(err)
	}
	if cfg.Build == nil {
		return fatal(fmt.Errorf("this project declares no [build] recipe, so its image %s is prebuilt: pull it from a registry, or add a [build] table to sbx.toml — `sbx build` only builds images from a [build] recipe", cfg.Image))
	}
	contextDir, dockerfile, err := resolveBuildPaths(info, cfg.Build)
	if err != nil {
		return fatal(err)
	}

	// Both tools fail fast with actionable guidance, before the backend
	// confirmation and long before any image work.
	var missing []string
	if _, err := exec.LookPath("docker"); err != nil {
		missing = append(missing, "docker (install Docker with buildx: https://docs.docker.com/get-docker/)")
	}
	if _, err := exec.LookPath("msb"); err != nil {
		missing = append(missing, "msb (install microsandbox: https://docs.microsandbox.dev)")
	}
	if len(missing) > 0 {
		return fatal(fmt.Errorf("sbx build needs tools not found on PATH: %s", strings.Join(missing, "; ")))
	}

	// The import must land in the local backend, and a cloud profile that
	// overrides the environment is refused before Docker runs.
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return fatal(err)
	}

	archive, err := os.CreateTemp("", "sbx-build-*.tar")
	if err != nil {
		return fatal(fmt.Errorf("creating the temporary image archive: %w", err))
	}
	archivePath := archive.Name()
	if err := archive.Close(); err != nil {
		os.Remove(archivePath)
		return fatal(fmt.Errorf("creating the temporary image archive: %w", err))
	}
	keepArchive := keep["--keep-archive"]
	defer func() {
		if !keepArchive {
			os.Remove(archivePath)
		}
	}()

	dk := docker.CLI{}
	if err := dk.Build(ctx, docker.BuildOptions{
		ContextDir: contextDir,
		Dockerfile: dockerfile,
		Target:     cfg.Build.Target,
		Platform:   cfg.Build.Platform,
		Tag:        cfg.Image,
	}, out.subprocessStdout(), out.rawErr); err != nil {
		return fatal(err)
	}
	if err := dk.Save(ctx, cfg.Image, archivePath); err != nil {
		return fatal(err)
	}
	if err := box.Load(ctx, archivePath); err != nil {
		return fatal(err)
	}

	// A declared image check gates the freshly imported image: it runs in
	// an isolated sandbox from the image alone, and a failing check fails
	// the build — although the imported image stays cached.
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	if err := ensureImageChecked(ctx, box, cfg, info.WorktreeRoot, id.Sandbox, out); err != nil {
		return fatal(err)
	}

	if out.json {
		result := struct {
			Image    string `json:"image"`
			Platform string `json:"platform"`
			Archive  string `json:"archive,omitempty"`
		}{Image: cfg.Image, Platform: cfg.Build.Platform}
		if keepArchive {
			result.Archive = archivePath
		}
		return out.writeJSON(result)
	}
	fmt.Fprintf(out.stdout, "built %s for %s and imported it into msb’s image store\n", cfg.Image, cfg.Build.Platform)
	if keepArchive {
		fmt.Fprintf(out.stdout, "image archive kept for debugging: %s\n", archivePath)
	}
	return exitOK
}

// resolveBuildPaths resolves the recipe's context and Dockerfile against
// the worktree root with sbx's host-path rules (absolute,
// worktree-relative, or ~-prefixed; never interpolated), requiring an
// existing directory and file respectively. The resolved absolute paths —
// never the declared spellings — are what Docker receives.
func resolveBuildPaths(info gitx.Info, b *config.BuildConfig) (contextDir, dockerfile string, err error) {
	contextDir, isDir, err := translate.ResolveHostPath(info.WorktreeRoot, b.Context, "build context")
	if err != nil {
		return "", "", err
	}
	if !isDir {
		return "", "", fmt.Errorf("build context %q is not a directory (resolved to %s)", b.Context, contextDir)
	}
	if b.Dockerfile == "" {
		return contextDir, "", nil
	}
	dockerfile, isDir, err = translate.ResolveHostPath(info.WorktreeRoot, b.Dockerfile, "build dockerfile")
	if err != nil {
		return "", "", err
	}
	if isDir {
		return "", "", fmt.Errorf("build dockerfile %q is a directory (resolved to %s)", b.Dockerfile, dockerfile)
	}
	return contextDir, dockerfile, nil
}

// errImageNeedsBuild is the actionable error when a project's locally built
// image is absent from msb's image store: building is `sbx build`'s job
// alone, so the caller is told to build rather than offered a pull.
func errImageNeedsBuild(image string) error {
	return fmt.Errorf("image %s is not available to msb, and this project builds it from its [build] recipe: run `sbx build` to build and import it (msb’s image store is separate from Docker’s)", image)
}
