// Disposable runs: `sbx run` creates a uniquely named sandbox, runs one
// guest command in it, and removes the sandbox on success, failure, or
// cancellation. It publishes no ports and keeps no state to repair.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ngscheurich/sbx/internal/msb"
)

// workspaceTarget is the default Workspace target: the worktree root is
// mounted read-write here, and it is also the guest working directory. The
// [workspace] table is rejected while untranslated, so no override exists.
const workspaceTarget = "/workspace"

// runDisposable implements `sbx run [-- <argv...>]`.
func runDisposable(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var argv []string
	switch {
	case len(args) == 1:
		// No guest command: the configured shell runs instead.
	case args[1] == "--":
		argv = args[2:]
	default:
		fmt.Fprintf(stderr, "sbx: run takes an optional %q separator before the guest command, got %q\n", "--", strings.Join(args[1:], " "))
		return exitUsage
	}

	// Interruptions cancel ctx, which msb.Exec forwards to the msb
	// subprocess. Whether the signal then reaches the guest itself is
	// UNVERIFIED on a real host (ticket 01); this interim implementation
	// must not be treated as verified signal forwarding.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	box := msb.CLI{}
	fatal := func(err error) int {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitFailure
	}

	info, cfg, err := discoverConfig()
	if err != nil {
		return fatal(err)
	}
	if cfg.Network.Egress == "allowlist" {
		return fatal(fmt.Errorf(`network egress "allowlist" is declared but not translated yet; this build of sbx supports egress = "public" or "none" only`))
	}

	if _, err := box.LocalContext(ctx); err != nil {
		return fatal(err)
	}
	if err := box.PullIfMissing(ctx, cfg.Image); err != nil {
		return fatal(err)
	}

	create := msb.CreateOptions{
		// No Name: msb run keeps a sandbox that is explicitly named, and
		// removes an auto-named one-shot when the command completes. The
		// sbx.* labels carry attribution instead.
		Image:  cfg.Image,
		CPUs:   cfg.CPUs,
		Memory: cfg.Memory,
		Mounts: []msb.Mount{{Source: info.WorktreeRoot, Target: workspaceTarget}},
		Labels: []msb.Label{
			{Key: "sbx.managed", Value: "1"},
			{Key: "sbx.mode", Value: "disposable"},
			{Key: "sbx.worktree", Value: info.WorktreeRoot},
		},
		NoNet: cfg.Network.Egress == "none",
	}
	guestArgv := argv
	if len(guestArgv) == 0 {
		guestArgv = []string{cfg.Shell}
	}
	code, err := box.Run(ctx, msb.RunOptions{
		CreateOptions: create,
		Workdir:       workspaceTarget,
		Argv:          guestArgv,
	}, stdin, stdout, stderr)

	// The sandbox's whole lifecycle — creation, the command, and whatever
	// happens to the sandbox afterwards — is msb run's own behavior; sbx
	// adds no cleanup step and propagates the guest's exit status.
	if err != nil && code < 0 {
		return fatal(fmt.Errorf("running %s: %w", strings.Join(guestArgv, " "), err))
	}
	return code
}
