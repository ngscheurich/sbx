// Package cli wires command-line invocations to sbx behavior.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/plan"
	"github.com/ngscheurich/sbx/internal/volumes"
)

// Exit codes follow common CLI conventions: 0 for success, 1 for runtime
// failures, 2 for usage errors.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const helpText = `sbx — worktree-scoped local development sandboxes

Usage:
  sbx <command> [flags]

Commands:
  plan    Show what sbx would create for this worktree's persistent sandbox, changing nothing
  build   Build this project's image with Docker and import it into msb (requires [build])
  run     Run a one-off guest command in a disposable sandbox, removed afterward
  up      Create or start this worktree's persistent sandbox, bootstrapping new ones
          (--allow-stale ignores drift; --retry-bootstrap retries an incomplete bootstrap)
  exec    Run a guest command in the persistent sandbox (created or started first), leaving it running
  status  Show the persistent sandbox's identity, state, and drift, changing nothing
  logs    Show the persistent sandbox's msb logs
  stop    Stop the persistent sandbox, keeping its state and volumes
  rm      Remove the persistent sandbox after confirmation (--yes in noninteractive use)

The remaining v1 command, port prune, arrives in a later release.

Run sbx from any directory inside a Git worktree that has an sbx.toml.
`

// Run dispatches one command line and returns the process exit code. The
// context carries cancellation (sbx forwards it to the running guest) and
// stdin is passed through to guest commands.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, helpText)
		return exitOK
	}
	cmd := args[0]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, helpText)
		return exitOK
	case "plan":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "sbx: plan takes no arguments or flags yet, got %q\n", strings.Join(args[1:], " "))
			return exitUsage
		}
		return runPlan(ctx, stdout, stderr)
	case "build":
		return runBuild(ctx, args, stdout, stderr)
	case "run":
		return runDisposable(ctx, args, stdin, stdout, stderr)
	case "up":
		return runUp(ctx, args, stdout, stderr)
	case "exec":
		return runExec(ctx, args, stdin, stdout, stderr)
	case "status":
		return runStatus(ctx, args, stdout, stderr)
	case "logs":
		return runLogs(ctx, args, stdout, stderr)
	case "stop":
		return runStop(ctx, args, stdout, stderr)
	case "rm":
		return runRm(ctx, args, stdin, stdout, stderr)
	case "-V", "--version", "version":
		fmt.Fprintln(stdout, "sbx (development build)")
		return exitOK
	default:
		fmt.Fprintf(stderr, "sbx: unknown command %q\n\n%s", cmd, helpText)
		return exitUsage
	}
}

func runPlan(ctx context.Context, stdout, stderr io.Writer) int {
	info, cfg, err := discoverConfig(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitFailure
	}
	p := plan.Compose(info, cfg)
	checkPlanVolumes(ctx, msb.CLI{}, &p)
	fmt.Fprint(stdout, p.Render())
	return exitOK
}

// checkPlanVolumes runs the Project volume compatibility check for the
// rendered plan. It is read-only: the backend's listing is inspected and
// nothing changes. A failed or malformed inspection is recorded for the
// report, never read as an empty listing or as compatible definitions.
func checkPlanVolumes(ctx context.Context, box msb.CLI, p *plan.Plan) {
	if p.TranslateErr != nil || len(p.Translation.ProjectVolumes) == 0 {
		return
	}
	report, err := inspectProjectVolumes(ctx, box, p.Translation.ProjectVolumes)
	if err != nil {
		p.VolumeCheckErr = err
		return
	}
	p.VolumeReport = &report
}

// inspectProjectVolumes reads the backend's volume listing and checks
// every declared Project volume against it. The listing's own failure and
// malformed records surface as errors; an unreadable backend is never an
// empty one.
func inspectProjectVolumes(ctx context.Context, box msb.CLI, declared []volumes.Declared) (volumes.Report, error) {
	existing, err := box.Volumes(ctx)
	if err != nil {
		return volumes.Report{}, err
	}
	return volumes.Check(declared, existing)
}

// checkProjectVolumes refuses a sandbox mutation when a declared Project
// volume clashes with the volume the backend already holds under its
// derived name (ADR-0003): every declared volume is compared before
// anything is created, pulled, or started. A missing or malformed
// inspection fails the command rather than passing as an empty listing or
// a compatible definition. sbx never resizes, overwrites, or deletes an
// existing volume.
func checkProjectVolumes(ctx context.Context, box msb.CLI, declared []volumes.Declared) error {
	if len(declared) == 0 {
		return nil
	}
	report, err := inspectProjectVolumes(ctx, box, declared)
	if err != nil {
		return fmt.Errorf("inspecting the backend's volumes before creation: %w", err)
	}
	if len(report.Conflicts) > 0 {
		return fmt.Errorf("declared Project volume(s) conflict with existing backend volumes; nothing was created or changed:\n\n%s", volumes.FormatConflicts(report.Conflicts))
	}
	return nil
}

// discoverConfig finds the worktree root from the current directory and
// loads that checkout's validated sbx.toml.
func discoverConfig(ctx context.Context) (gitx.Info, config.Config, error) {
	info, err := gitx.Discover(ctx, ".")
	if err != nil {
		return gitx.Info{}, config.Config{}, err
	}
	cfg, err := config.Load(filepath.Join(info.WorktreeRoot, "sbx.toml"))
	if err != nil {
		return gitx.Info{}, config.Config{}, err
	}
	return info, cfg, nil
}

// writeTempFile creates a temporary file outside the repository with the
// given content and returns its path.
func writeTempFile(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
