// Package docker is sbx's adapter for the Docker CLI, used only by
// `sbx build`: it builds the project's image and exports it to a temporary
// archive, which is then imported into msb's separate image store with
// `msb load --input`. No other command builds images.
//
// The command spellings follow scripts/sbx-load.sh, whose
// `docker build`/`docker save`/`msb load --input` sequence was verified
// against Docker and msb 0.7.6 on a real host; long flag spellings are
// always used.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ngscheurich/sbx/internal/subproc"
)

// waitDelay is how long a canceled docker subprocess may linger before it
// is killed outright: a killed client can leave a child holding the output
// streams open, and Wait would otherwise block until it exits.
const waitDelay = 10 * time.Second

// CLI invokes the docker executable. The zero value uses "docker" from PATH.
type CLI struct {
	// Binary is the docker executable name or path. Empty means "docker".
	Binary string
}

func (c CLI) binary() string {
	if c.Binary == "" {
		return "docker"
	}
	return c.Binary
}

// BuildOptions carries everything one `docker build` receives.
type BuildOptions struct {
	// ContextDir is the absolute build context directory.
	ContextDir string
	// Dockerfile is the absolute Dockerfile path; empty lets Docker default
	// to <context>/Dockerfile.
	Dockerfile string
	// Target is the optional Dockerfile stage to build.
	Target string
	// Platform is the target platform, such as "linux/arm64".
	Platform string
	// Tag is the image reference; docker save carries it into the archive,
	// so msb load imports the image under this name.
	Tag string
}

// BuildArgs renders the `docker build` argv: flags first, the context
// directory last. Argument order is fixed and pinned by tests.
func BuildArgs(o BuildOptions) []string {
	args := []string{"build", "--platform", o.Platform}
	if o.Dockerfile != "" {
		args = append(args, "--file", o.Dockerfile)
	}
	if o.Target != "" {
		args = append(args, "--target", o.Target)
	}
	return append(args, "--tag", o.Tag, o.ContextDir)
}

// Build runs `docker build`, streaming Docker's progress to the caller's
// streams. A canceled context terminates the client's whole process group,
// so no child lingers holding the streams open; the daemon-side build's
// fate is then Docker's own behavior.
func (c CLI) Build(ctx context.Context, o BuildOptions, stdout, stderr io.Writer) error {
	args := BuildArgs(o)
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cancelGroup(cmd)
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return notFoundError()
		}
		return fmt.Errorf("docker %s: %w", strings.Join(args[:1], " "), err)
	}
	return nil
}

// Save runs `docker save --output <archive> <image>`, exporting the tagged
// image to a single archive file.
func (c CLI) Save(ctx context.Context, image, archive string) error {
	cmd := exec.CommandContext(ctx, c.binary(), "save", "--output", archive, image)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	cancelGroup(cmd)
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return notFoundError()
		}
		return fmt.Errorf("exporting image %s: docker save: %s", image, firstLine(errOut.String()))
	}
	return nil
}

// cancelGroup gives the subprocess its own process group and makes context
// cancellation deliver SIGTERM to the group, with WaitDelay as the backstop
// before an outright kill.
func cancelGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = waitDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
}

// notFoundError is the actionable error for a missing docker executable.
func notFoundError() error {
	return fmt.Errorf("docker is not installed or not on PATH: sbx build needs Docker (with buildx) to build images; install Docker and retry")
}

// firstLine returns the first non-empty line of a string.
// firstLine reads the first readable line of a subprocess output stream,
// with the adapter's own nothing-to-read fallback.
func firstLine(s string) string {
	return subproc.FirstLine(s, "docker failed without output")
}
