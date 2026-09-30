// Package cli wires command-line invocations to sbx behavior.
package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/plan"
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

The full v1 command set also includes build, up, exec, run, status, logs,
stop, rm, and port prune; later releases implement them one by one.

Run sbx from any directory inside a Git worktree that has an sbx.toml.
`

// Run dispatches one command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
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
		return runPlan(stdout, stderr)
	case "-V", "--version", "version":
		fmt.Fprintln(stdout, "sbx (development build)")
		return exitOK
	default:
		fmt.Fprintf(stderr, "sbx: unknown command %q\n\n%s", cmd, helpText)
		return exitUsage
	}
}

func runPlan(stdout, stderr io.Writer) int {
	info, err := gitx.Discover(".")
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitFailure
	}
	configPath := filepath.Join(info.WorktreeRoot, "sbx.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitFailure
	}
	p := plan.Compose(info, cfg)
	fmt.Fprint(stdout, p.Render())
	return exitOK
}

