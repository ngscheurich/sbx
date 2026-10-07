// Bootstrap: the project-defined guest initialization command that runs
// once in every new sandbox, through the configured guest shell in the
// Workspace. Persistent completion is recorded in host state only after
// success, bound to the sandbox's incarnation and the definition's hash;
// a failed, interrupted, or unrecorded Bootstrap leaves the sandbox
// incomplete — never retried by plain `up`, repairable through `exec`,
// and retried only by `up --retry-bootstrap` (ADR-0002). A disposable run
// instead wraps its guest command so Bootstrap runs first, and a failure
// ends the run: msb removes the auto-named one-shot sandbox, Sandbox
// volumes and all.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
)

// bootstrapState is the persistent sandbox's Bootstrap completion state
// for the incarnation the backend currently reports.
type bootstrapState int

const (
	// bootstrapNotDeclared means sbx.toml carries no [bootstrap].
	bootstrapNotDeclared bootstrapState = iota
	// bootstrapComplete means a marker matches the Sandbox identity, the
	// sandbox's created_at, and the current definition's hash.
	bootstrapComplete
	// bootstrapChanged means Bootstrap completed for this sandbox, but the
	// definition has changed since it ran. That is reported, never
	// Creation drift, and never blocks use.
	bootstrapChanged
	// bootstrapIncomplete means Bootstrap never completed for this sandbox
	// incarnation: it failed, was interrupted, or its marker belongs to a
	// removed and recreated sandbox.
	bootstrapIncomplete
)

// bootstrapFailure wraps a Bootstrap run that did not complete: the guest
// code failed, the run was interrupted, or the completion marker could not
// be persisted. ensureRunning returns it so callers can distinguish "the
// sandbox is up but incomplete" from a lifecycle failure — exec proceeds
// with a warning so the sandbox stays repairable, while up refuses.
type bootstrapFailure struct{ err error }

func (e *bootstrapFailure) Error() string { return e.err.Error() }
func (e *bootstrapFailure) Unwrap() error { return e.err }

// assessBootstrap evaluates the recorded marker against the sandbox
// incarnation the backend reports and the current definition. A missing
// marker is incomplete; so is a stale one whose created_at belongs to an
// earlier incarnation of the sandbox. A corrupt marker is an error, never
// silently treated as completion.
func assessBootstrap(p *persistent, createdAt string) (bootstrapState, error) {
	if !p.cfg.BootstrapDeclared() {
		return bootstrapNotDeclared, nil
	}
	marker, err := state.LoadBootstrapMarker(p.id.Sandbox)
	if errors.Is(err, state.ErrNoBootstrapMarker) {
		return bootstrapIncomplete, nil
	}
	if err != nil {
		return bootstrapNotDeclared, err
	}
	if marker.Sandbox != p.id.Sandbox || marker.CreatedAt != createdAt {
		return bootstrapIncomplete, nil
	}
	if marker.DefinitionHash != state.BootstrapHash(p.cfg.Bootstrap.Run) {
		return bootstrapChanged, nil
	}
	return bootstrapComplete, nil
}

// saveBootstrapMarker persists the completion marker. It is a variable so
// tests can simulate the failure window where the guest code succeeded but
// the marker was never persisted.
var saveBootstrapMarker = state.SaveBootstrapMarker

// runBootstrap runs the Bootstrap definition through the configured guest
// shell in the Workspace and records completion only after success. Any
// failure — the guest code, an interruption, or the marker write itself —
// leaves the sandbox incomplete: sbx does not retry it implicitly, because
// partial initialization may already have run.
func runBootstrap(ctx context.Context, box msb.CLI, p *persistent, createdAt string, stdout, stderr io.Writer) error {
	fmt.Fprintf(stderr, "sbx: running bootstrap in %s (%s -c)\n", p.id.Sandbox, p.cfg.Shell)
	code, err := box.Exec(ctx, p.id.Sandbox, p.tr.Workspace,
		[]string{p.cfg.Shell, "-c", p.cfg.Bootstrap.Run}, nil, stdout, stderr)
	if err != nil && code < 0 {
		return fmt.Errorf("bootstrap was interrupted before it finished: %w\n\n%s", err, incompleteGuidance(p.id.Sandbox))
	}
	if code != 0 {
		return fmt.Errorf("bootstrap failed with exit status %d\n\n%s", code, incompleteGuidance(p.id.Sandbox))
	}
	marker := state.BootstrapMarker{
		Sandbox:        p.id.Sandbox,
		CreatedAt:      createdAt,
		DefinitionHash: state.BootstrapHash(p.cfg.Bootstrap.Run),
	}
	if err := saveBootstrapMarker(p.id.Sandbox, marker); err != nil {
		return fmt.Errorf("bootstrap succeeded, but recording its completion failed: %v\n\n%s", err, incompleteGuidance(p.id.Sandbox))
	}
	fmt.Fprintf(stderr, "sbx: bootstrap complete for %s\n", p.id.Sandbox)
	return nil
}

// incompleteGuidance is the way out of an incomplete Bootstrap, repeated in
// every report of one.
func incompleteGuidance(sandbox string) string {
	return fmt.Sprintf("the persistent sandbox %s stays incomplete: sbx never retries a failed or interrupted bootstrap on its own, because partial initialization may already have run. Run `sbx up --retry-bootstrap` to run the current bootstrap definition again, or repair the sandbox with `sbx exec` and then retry.", sandbox)
}

// lockSandbox takes the per-sandbox cross-process lock, saying so when
// another sbx process holds it. The lock serializes create, start, and
// bootstrap; callers release it before any user guest command runs.
func lockSandbox(sandbox string, stderr io.Writer) (*state.SandboxLock, error) {
	lock, held, err := state.TrySandboxLock(sandbox)
	if err != nil {
		return nil, err
	}
	if held {
		return lock, nil
	}
	fmt.Fprintf(stderr, "sbx: another sbx process is creating, starting, or bootstrapping %s; waiting for it to finish\n", sandbox)
	return state.LockSandbox(sandbox)
}

// bootstrapArgv wraps a disposable run's guest command so Bootstrap runs
// first, through the configured guest shell in the Workspace, and only on
// success does the wrapped command exec the user's argv. The argv travels
// as positional parameters, so it passes to the guest directly — never
// reinterpreted by a shell. On failure the shell exits with Bootstrap's
// status and msb run removes the auto-named one-shot sandbox, including
// its Sandbox volumes, as it does for any completed run.
func bootstrapArgv(shell, run string, argv []string) []string {
	script := strings.TrimRight(run, "\n") + `

sbx_bootstrap_status=$?
if [ "$sbx_bootstrap_status" -ne 0 ]; then
  printf 'sbx: bootstrap failed (exit status %s); the disposable run ends and its sandbox is removed\n' "$sbx_bootstrap_status" >&2
  exit "$sbx_bootstrap_status"
fi
exec "$@"
`
	return append([]string{shell, "-c", script, "sbx-bootstrap"}, argv...)
}
