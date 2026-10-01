// Disposable runs: `sbx run` creates a uniquely named sandbox, runs one
// guest command in it, and removes the sandbox on success, failure, or
// cancellation. It publishes no ports and keeps no state to repair.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
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

	name := disposableName(info)
	netConf := ""
	if cfg.Network.Egress == "none" {
		netConf, err = msb.WriteNetConfNone()
		if err != nil {
			return fatal(err)
		}
		defer os.Remove(netConf)
	}
	create := msb.CreateOptions{
		Name:   name,
		Image:  cfg.Image,
		CPUs:   cfg.CPUs,
		Memory: cfg.Memory,
		Mounts: []msb.Mount{{Source: info.WorktreeRoot, Target: workspaceTarget}},
		Labels: []msb.Label{
			{Key: "sbx.managed", Value: "1"},
			{Key: "sbx.mode", Value: "disposable"},
			{Key: "sbx.worktree", Value: info.WorktreeRoot},
		},
		NetConf: netConf,
	}
	if err := box.Create(ctx, create); err != nil {
		// A creation failure removes nothing: sbx does not own a sandbox
		// it never created, even if one exists with the same name.
		return fatal(err)
	}

	guestArgv := argv
	if len(guestArgv) == 0 {
		guestArgv = []string{cfg.Shell}
	}
	code, err := box.Exec(ctx, name, workspaceTarget, guestArgv, stdin, stdout, stderr)

	// Removal runs on success, failure, and cancellation alike, with a
	// context that survives the cancellation above.
	rmCtx := context.WithoutCancel(ctx)
	if rerr := box.Remove(rmCtx, name); rerr != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", rerr)
		if code == 0 {
			code = exitFailure
		}
	}
	if err != nil && code < 0 {
		return fatal(fmt.Errorf("running %s in %s: %w", strings.Join(guestArgv, " "), name, err))
	}
	return code
}

// disposableName derives a unique backend name for one disposable run: the
// worktree's Sandbox identity plus a random suffix, so a run never collides
// with the persistent sandbox or a concurrent run.
func disposableName(info gitx.Info) string {
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		// crypto/rand failure is effectively impossible; fall back to a
		// still-valid name rather than failing the run.
		suffix = [3]byte{0, 0, 1}
	}
	return id.Sandbox + "-run-" + hex.EncodeToString(suffix[:])
}
