// Persistent-sandbox commands: up, exec, status, logs, stop, and rm manage
// the one stable Sandbox identity each worktree owns (ADR-0005), created
// once and reused afterward (ADR-0002). Three safety rules shape every
// path here: sbx never adopts a same-named sandbox it did not create, never
// recreates a drifted sandbox — that could destroy private data — and never
// removes a Project volume or port reservation when removing a sandbox.
//
// Image checks gate both paths here: the creation path runs the declared
// check against the image's contents before creating, and an existing
// sandbox's own image is checked even with --allow-stale, failing closed
// when its contents cannot be re-checked (ADR-0004, imagecheck.go).
// Declared Project volumes are checked for compatibility with the
// backend's existing volumes before any creation (ADR-0003), and declared
// ports are reserved before creation and reconciled with the backend's
// inspection report after it (ports.go). Bootstrap runs here: once after
// each creation, recorded in host state only on success, and retried only
// through `up --retry-bootstrap` (bootstrap.go).
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/translate"
	"github.com/ngscheurich/sbx/internal/ui"
)

// runUp implements `sbx up [--allow-stale] [--retry-bootstrap]`: create the
// persistent sandbox once, or start it when stopped, bootstrapping each new
// sandbox, and refuse drift unless --allow-stale. An incomplete or changed
// Bootstrap is reported, never silently retried: only --retry-bootstrap
// runs it again.
func runUp(ctx context.Context, args []string, out *output) int {
	flags, err := parsePersistentFlags("up", args, []string{"--allow-stale", "--retry-bootstrap"})
	if err != nil {
		return out.usagef("%v", err)
	}
	allowStale := flags["--allow-stale"]
	retryBootstrap := flags["--retry-bootstrap"]

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	p, err := preparePersistent(ctx, out)
	if err != nil {
		return out.fail(err)
	}
	if retryBootstrap && !p.cfg.BootstrapDeclared() {
		return out.fail(fmt.Errorf("up: --retry-bootstrap was given, but sbx.toml declares no [bootstrap]; there is nothing to retry"))
	}
	action, boot, err := ensureRunning(ctx, msb.CLI{}, p, allowStale, retryBootstrap, out)
	if err != nil {
		return out.fail(err)
	}
	if boot == bootstrapIncomplete {
		// The sandbox itself may well be running; its Bootstrap never
		// completed for this incarnation, and plain up never retries it.
		return out.fail(fmt.Errorf("bootstrap for %s is incomplete: it never completed for this sandbox.\n\n%s", p.id.Sandbox, incompleteGuidance(p.id.Sandbox)))
	}
	fmt.Fprintf(out.stdout, "%s %s\n%s\n", out.styles.Heading.Render("persistent sandbox:"), p.id.Sandbox, action)
	if boot == bootstrapChanged {
		fmt.Fprintf(out.stdout, "bootstrap: complete, but the definition has changed since it ran; that is not Creation drift and does not block use\n")
	}
	return exitOK
}

// runExec implements `sbx exec [--allow-stale] [-- <argv...>]`: run the
// guest command in the persistent sandbox, bringing the sandbox up first,
// and leave it running afterward. The guest's exit status is sbx's.
func runExec(ctx context.Context, args []string, stdin io.Reader, out *output) int {
	allowStale, argv, err := parseExecArgs(args)
	if err != nil {
		return out.usagef("%v", err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	p, err := preparePersistent(ctx, out)
	if err != nil {
		return out.fail(err)
	}
	_, boot, err := ensureRunning(ctx, msb.CLI{}, p, allowStale, false, out)
	var bf *bootstrapFailure
	switch {
	case errors.As(err, &bf):
		// The sandbox is up but its Bootstrap did not complete: exec
		// still runs, with a warning, so the user can repair it.
		fmt.Fprintf(out.stderr, "%s %v\n", out.styles.Warning.Render("warning:"), bf.err)
	case err != nil:
		return out.fail(err)
	case boot == bootstrapIncomplete:
		fmt.Fprintf(out.stderr, "%s bootstrap for %s is incomplete; the command runs anyway so you can repair the sandbox. `sbx up --retry-bootstrap` runs the bootstrap definition again.\n", out.styles.Warning.Render("warning:"), p.id.Sandbox)
	}

	guestArgv := argv
	if len(guestArgv) == 0 {
		guestArgv = []string{p.cfg.Shell}
	}
	code, err := msb.CLI{}.Exec(ctx, p.id.Sandbox, p.tr.Workspace, guestArgv, stdin, out.rawOut, out.rawErr)
	if err != nil && code < 0 {
		return out.fail(fmt.Errorf("running %s: %w", strings.Join(guestArgv, " "), err))
	}
	return code
}

// runStatus implements `sbx status`, a read-only report: identity, backend
// state, and Creation drift. It never mutates the backend and never writes
// sbx state, and it stays usable when the sandbox has drifted.
func runStatus(ctx context.Context, args []string, out *output) int {
	if _, err := parsePersistentFlags("status", args, nil); err != nil {
		return out.usagef("%v", err)
	}
	info, cfg, err := out.discoverConfig(ctx)
	if err != nil {
		return out.fail(err)
	}
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return out.fail(err)
	}

	fmt.Fprintf(out.stdout, "%s %s\n", out.styles.Heading.Render("persistent sandbox:"), id.Sandbox)
	_, exists, err := findSandbox(ctx, box, id.Sandbox)
	if err != nil {
		return out.fail(err)
	}
	if !exists {
		fmt.Fprintf(out.stdout, "status: not created; run `sbx up` to create it\n")
		if cfg.BootstrapDeclared() {
			fmt.Fprintf(out.stdout, "bootstrap: declared; it will run after the sandbox is created\n")
		}
		return exitOK
	}

	s, err := box.Inspect(ctx, id.Sandbox)
	if err != nil {
		return out.fail(err)
	}
	if !isOwned(s) {
		fmt.Fprintf(out.stdout, "status: %s, but sbx did not create it; sbx never adopts a sandbox it does not own\n", s.Status)
		return exitOK
	}
	fmt.Fprintf(out.stdout, "status: %s\n", s.Status)
	fmt.Fprintf(out.stdout, "created at: %s\n", formatCreated(s.CreatedAt))
	if eff := s.EffectiveConfig(); eff.ManifestDigest != "" {
		fmt.Fprintf(out.stdout, "created from image contents: %s\n", eff.ManifestDigest)
	}

	// Drift is reported here, never enforced: status stays usable exactly
	// when up and exec would refuse.
	p := &persistent{info: info, cfg: cfg, id: id}
	tr, trErr := translate.Sandbox(info, cfg, "persistent")
	if trErr != nil {
		// A configuration that cannot translate (a missing bind source,
		// say) still leaves the identity and backend state worth reporting.
		fmt.Fprintf(out.stdout, "%s\n", out.styles.Warning.Render(fmt.Sprintf("drift: unknown (the current configuration does not translate: %v)", trErr)))
		return exitOK
	}
	tr.Options.Name = id.Sandbox
	p.tr = tr
	notes, entries, err := driftReport(ctx, box, p)
	if err != nil {
		return out.fail(err)
	}
	if len(notes) == 0 && len(entries) == 0 {
		fmt.Fprintf(out.stdout, "%s\n", out.styles.Positive.Render("drift: none"))
	} else {
		fmt.Fprintf(out.stdout, "%s\n%s\n", out.styles.Heading.Render("drift (up and exec refuse this; status does not):"), renderDrift(out, notes, entries))
	}

	// Published ports are reported read-only: what the backend says, and
	// any registry discrepancy, without correcting or reserving.
	reportPorts(id.Sandbox, s, out.stdout, out.styles)

	// Bootstrap state is reported like drift: never enforced here, and a
	// changed definition is reported without counting as Creation drift.
	boot, err := assessBootstrap(p, s.CreatedAt)
	if err != nil {
		return out.fail(err)
	}
	switch boot {
	case bootstrapComplete:
		fmt.Fprintf(out.stdout, "bootstrap: complete (recorded for this sandbox’s creation)\n")
	case bootstrapChanged:
		fmt.Fprintf(out.stdout, "bootstrap: complete, but the definition has changed since it ran; that is not Creation drift and does not block use\n")
	case bootstrapIncomplete:
		fmt.Fprintf(out.stdout, "bootstrap: incomplete — it never completed for this sandbox; plain `sbx up` will not retry it (use `sbx up --retry-bootstrap`, or repair with `sbx exec`)\n")
	}
	return exitOK
}

// runLogs implements `sbx logs`, a read-only pass-through of msb's logs.
func runLogs(ctx context.Context, args []string, out *output) int {
	if _, err := parsePersistentFlags("logs", args, nil); err != nil {
		return out.usagef("%v", err)
	}
	id, err := discoverIdentity(ctx)
	if err != nil {
		return out.fail(err)
	}
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return out.fail(err)
	}
	if _, exists, err := findSandbox(ctx, box, id.Sandbox); err != nil {
		return out.fail(err)
	} else if !exists {
		return out.fail(fmt.Errorf("no persistent sandbox for this worktree; run `sbx up` first"))
	}
	logs, err := box.Logs(ctx, id.Sandbox)
	if err != nil {
		return out.fail(err)
	}
	// The logs are the sandbox's own content, not sbx's: they pass through
	// the raw writer, so a pipe receives them exactly as the backend wrote
	// them.
	fmt.Fprint(out.rawOut, logs)
	return exitOK
}

// runStop implements `sbx stop`: stop the persistent sandbox without
// deleting its state. Stopping touches only an owned sandbox, and drift
// never blocks it — stopping does not use the sandbox's contents. The
// per-sandbox lock is held across the inspect-and-stop section so a
// concurrent up cannot race a creation or bootstrap mid-stop.
func runStop(ctx context.Context, args []string, out *output) int {
	if _, err := parsePersistentFlags("stop", args, nil); err != nil {
		return out.usagef("%v", err)
	}
	id, err := discoverIdentity(ctx)
	if err != nil {
		return out.fail(err)
	}
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return out.fail(err)
	}
	lock, err := lockSandbox(id.Sandbox, out.stderr)
	if err != nil {
		return out.fail(err)
	}
	defer lock.Release()
	entry, exists, err := findSandbox(ctx, box, id.Sandbox)
	if err != nil {
		return out.fail(err)
	}
	if !exists {
		return out.fail(fmt.Errorf("no persistent sandbox for this worktree; run `sbx up` first"))
	}
	s, err := box.Inspect(ctx, id.Sandbox)
	if err != nil {
		return out.fail(err)
	}
	if !isOwned(s) {
		return out.fail(errUnowned(id.Sandbox))
	}
	if !msb.IsRunning(entry.Status) {
		fmt.Fprintf(out.stdout, "%s %s\nalready stopped; its state and volumes are kept\n", out.styles.Heading.Render("persistent sandbox:"), id.Sandbox)
		return exitOK
	}
	if err := box.Stop(ctx, id.Sandbox); err != nil {
		return out.fail(err)
	}
	fmt.Fprintf(out.stdout, "%s %s\nstopped; its state and volumes are kept, and `sbx up` starts it again\n", out.styles.Heading.Render("persistent sandbox:"), id.Sandbox)
	return exitOK
}

// runRm implements `sbx rm [--yes]`: after confirmation — which
// noninteractive use gives with --yes — remove the persistent sandbox and
// its Sandbox volumes, list what was lost, and keep Project volumes and
// port reservations untouched.
// runRm implements `sbx rm [--yes]`: after confirmation — which
// noninteractive use gives with --yes — remove the persistent sandbox and
// its Sandbox volumes, list what was lost, and keep Project volumes and
// port reservations untouched. The per-sandbox lock is held across the
// removal and its state cleanup, after the prompt, so an interactive
// confirmation never blocks a concurrent sbx process.
func runRm(ctx context.Context, args []string, stdin io.Reader, out *output) int {
	yes, err := parsePersistentFlags("rm", args, []string{"--yes"})
	if err != nil {
		return out.usagef("%v", err)
	}
	confirmed := yes["--yes"]
	id, err := discoverIdentity(ctx)
	if err != nil {
		return out.fail(err)
	}
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return out.fail(err)
	}
	if _, exists, err := findSandbox(ctx, box, id.Sandbox); err != nil {
		return out.fail(err)
	} else if !exists {
		return out.fail(fmt.Errorf("no persistent sandbox for this worktree; there is nothing to remove"))
	}
	s, err := box.Inspect(ctx, id.Sandbox)
	if err != nil {
		return out.fail(err)
	}
	if !isOwned(s) {
		return out.fail(errUnowned(id.Sandbox))
	}

	// The snapshot names the Sandbox volumes that die with the sandbox; it
	// is read before anything changes.
	snap, snapErr := state.LoadSnapshot(id.Sandbox)

	if !confirmed {
		if !msb.StdinIsTerminal(stdin) {
			return out.fail(fmt.Errorf("refusing to remove %s without confirmation; pass --yes in noninteractive use", id.Sandbox))
		}
		if !confirmRemoval(id.Sandbox, stdin, out.stderr) {
			return out.fail(fmt.Errorf("removal canceled; %s was not removed", id.Sandbox))
		}
	}

	// The lock is taken after the prompt — an interactive confirmation must
	// never block a concurrent sbx process — and held across the removal
	// and its state cleanup.
	lock, err := lockSandbox(id.Sandbox, out.stderr)
	if err != nil {
		return out.fail(err)
	}
	defer lock.Release()
	if err := box.Remove(ctx, id.Sandbox); err != nil {
		return out.fail(err)
	}
	// The Bootstrap marker dies with the sandbox it belongs to, so a later
	// sandbox of the same identity can never inherit a stale completion.
	// Both records are attempted even if the first fails: the sandbox is
	// already gone, so a stray record has no second chance to be cleared.
	delSnapErr := state.DeleteSnapshot(id.Sandbox)
	delMarkerErr := state.DeleteBootstrapMarker(id.Sandbox)
	if delSnapErr != nil || delMarkerErr != nil {
		return out.fail(errors.Join(delSnapErr, delMarkerErr))
	}

	fmt.Fprintf(out.stdout, "%s %s\nremoved.\n", out.styles.Heading.Render("persistent sandbox:"), id.Sandbox)
	fmt.Fprintf(out.stdout, "its creation snapshot and Bootstrap completion record (if any) were cleared from host state.\n")
	switch {
	case errors.Is(snapErr, state.ErrNoSnapshot):
		fmt.Fprintf(out.stdout, "its Sandbox volumes were removed with it (no creation snapshot recorded their list).\n")
	case snapErr != nil:
		fmt.Fprintf(out.stdout, "%s its creation snapshot was unreadable (%v), so the list of Sandbox volumes removed with it is unknown.\n", out.styles.Warning.Render("warning:"), snapErr)
	default:
		reportOwnedVolumes(out.stdout, out.styles, snap)
	}
	fmt.Fprintf(out.stdout, "Project volumes and port reservations were kept.\n")
	return exitOK
}

// reportOwnedVolumes lists the Sandbox volumes that were removed with the
// sandbox, or notes their absence.
func reportOwnedVolumes(w io.Writer, st ui.Styles, snap state.Snapshot) {
	owned := snap.Definition.Options.Owned
	if len(owned) == 0 {
		fmt.Fprintf(w, "it had no Sandbox volumes.\n")
		return
	}
	fmt.Fprintf(w, "%s\n", st.Heading.Render("Sandbox volumes removed with it:"))
	for _, o := range owned {
		if o.Kind == "disk" {
			fmt.Fprintf(w, "  - %s (disk, %s)\n", o.Target, o.Size)
		} else {
			fmt.Fprintf(w, "  - %s (directory)\n", o.Target)
		}
	}
}

// confirmRemoval asks an interactive user to confirm and reports whether
// they did. The prompt warns what is lost and what is kept; anything but
// an explicit yes cancels.
func confirmRemoval(sandbox string, stdin io.Reader, stderr io.Writer) bool {
	fmt.Fprintf(stderr, "Remove the persistent sandbox %s?\nIts Sandbox volumes are removed with it; Project volumes and port reservations are kept. [y/N] ", sandbox)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// persistent is one worktree's persistent-sandbox context: discovery,
// validated configuration, the Sandbox identity, and the translation the
// sandbox was — or would be — created from.
type persistent struct {
	info gitx.Info
	cfg  config.Config
	id   identity.Identity
	tr   translate.Translation
}

// preparePersistent discovers and validates everything the mutating
// persistent commands need, before any backend call: the fail-closed
// preflight, the declared secrets' host variables, and the translation.
func preparePersistent(ctx context.Context, out *output) (*persistent, error) {
	info, cfg, err := out.discoverConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := translate.CheckSecretEnv(cfg, os.LookupEnv); err != nil {
		return nil, err
	}
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	tr, err := translate.Sandbox(info, cfg, "persistent")
	if err != nil {
		return nil, err
	}
	tr.Options.Name = id.Sandbox
	return &persistent{info: info, cfg: cfg, id: id, tr: tr}, nil
}

// discoverIdentity resolves just the worktree and its Sandbox identity, for
// commands that act on an existing sandbox: logs, stop, and rm. The identity
// is derived from the Git layout alone and never from editable
// configuration, so a worktree whose sbx.toml is being edited — or is
// simply invalid — can still stop, log, or remove the sandbox it already
// has.
func discoverIdentity(ctx context.Context) (identity.Identity, error) {
	info, err := gitx.Discover(ctx, ".")
	if err != nil {
		return identity.Identity{}, err
	}
	return identity.Derive(info.CommonDir, info.WorktreeRoot), nil
}

// ensureRunning brings the worktree's persistent sandbox to a running
// state and returns the action taken plus the Bootstrap state, for the
// caller to report. Creation happens only when the backend holds no
// same-named sandbox; an existing one is started when stopped, checked for
// ownership and drift, and never recreated.
//
// The per-sandbox lock serializes creating, starting, and bootstrapping
// across sbx processes; it is released when ensureRunning returns, so it is
// never held while a user's guest command runs. Bootstrap runs once after
// each creation; a later incomplete state is retried only when the caller
// passed --retry-bootstrap, and completion is recorded only on success.
func ensureRunning(ctx context.Context, box msb.CLI, p *persistent, allowStale, retryBootstrap bool, out *output) (string, bootstrapState, error) {
	lock, err := lockSandbox(p.id.Sandbox, out.stderr)
	if err != nil {
		return "", bootstrapNotDeclared, err
	}
	defer lock.Release()

	if _, err := box.LocalContext(ctx); err != nil {
		return "", bootstrapNotDeclared, err
	}
	entry, exists, err := findSandbox(ctx, box, p.id.Sandbox)
	if err != nil {
		return "", bootstrapNotDeclared, err
	}

	if !exists {
		// Every declared Project volume is checked against the backend's
		// existing volumes before anything is created or pulled.
		if err := checkProjectVolumes(ctx, box, p.tr.ProjectVolumes); err != nil {
			return "", bootstrapNotDeclared, err
		}
		digest, err := ensureImage(ctx, box, p.cfg)
		if err != nil {
			return "", bootstrapNotDeclared, err
		}
		// The declared image check gates the image before anything is
		// created from it (ADR-0004).
		if err := ensureImageChecked(ctx, box, p.cfg, p.info.WorktreeRoot, p.id.Sandbox, out); err != nil {
			return "", bootstrapNotDeclared, err
		}
		cleanup, err := materializeGeneratedFiles(&p.tr)
		defer cleanup()
		if err != nil {
			return "", bootstrapNotDeclared, err
		}
		// Declared ports are reserved — or reused from the registry — before
		// anything is created; a failure here changes nothing at all.
		assigns, err := reservePorts(ctx, box, p)
		if err != nil {
			return "", bootstrapNotDeclared, err
		}
		if err := box.Create(ctx, p.tr.Options); err != nil {
			return "", bootstrapNotDeclared, err
		}
		// The snapshot is what drift detection compares against later; it
		// is written only after creation succeeded, so a failed creation
		// leaves no snapshot behind.
		if err := state.SaveSnapshot(p.id.Sandbox, state.Snapshot{
			ImageDigest: digest,
			Definition:  state.DefinitionOf(p.tr),
		}); err != nil {
			return "", bootstrapNotDeclared, err
		}
		// Once the sandbox exists, what msb inspect reports is authoritative:
		// the registry is corrected to match, failing rather than taking a
		// port another reservation holds. A failure here leaves the created
		// sandbox in place — sbx never removes a partially created sandbox
		// to retry a port.
		if err := reconcileCreatedPorts(ctx, box, p); err != nil {
			return "", bootstrapNotDeclared, err
		}
		if !p.cfg.BootstrapDeclared() {
			return withPorts(fmt.Sprintf("created from %s (image contents %s) and left running; run `sbx exec -- <command>` to work in it", p.cfg.Image, digest), assigns, out.styles), bootstrapNotDeclared, nil
		}
		// Bootstrap runs in the new sandbox before anything else uses it.
		// The marker binds its completion to this incarnation's created_at,
		// so a later sandbox of the same identity never inherits it.
		s, err := box.Inspect(ctx, p.id.Sandbox)
		if err != nil {
			return "", bootstrapIncomplete, err
		}
		if err := runBootstrap(ctx, box, p, s.CreatedAt, out); err != nil {
			return "", bootstrapIncomplete, &bootstrapFailure{err}
		}
		return withPorts(fmt.Sprintf("created from %s (image contents %s), bootstrapped, and left running; run `sbx exec -- <command>` to work in it", p.cfg.Image, digest), assigns, out.styles), bootstrapComplete, nil
	}

	// A same-named sandbox exists: sbx touches it only when it carries the
	// sbx.managed label.
	s, err := box.Inspect(ctx, p.id.Sandbox)
	if err != nil {
		return "", bootstrapNotDeclared, err
	}
	if !isOwned(s) {
		return "", bootstrapNotDeclared, errUnowned(p.id.Sandbox)
	}

	notes, entries, err := driftReport(ctx, box, p)
	if err != nil {
		return "", bootstrapNotDeclared, err
	}
	if len(notes) > 0 || len(entries) > 0 {
		if !allowStale {
			return "", bootstrapNotDeclared, fmt.Errorf("the persistent sandbox has drifted from sbx.toml:\n%s\nsbx never removes or recreates a sandbox that may hold private data; remove it yourself with `sbx rm` and run `sbx up` again, or pass --allow-stale to use it as-is", renderDrift(out, notes, entries))
		}
		fmt.Fprintf(out.stderr, "%s using %s despite drift:\n%s", out.styles.Warning.Render("warning:"), p.id.Sandbox, renderDrift(out, notes, entries))
	}

	// Even with --allow-stale, the sandbox's own image must have passed
	// the declared image check for the current script (ADR-0004): an
	// unmet or unverifiable check refuses use without touching the
	// sandbox or its data.
	if err := ensureSandboxImageChecked(ctx, box, p, s.EffectiveConfig().ManifestDigest, out); err != nil {
		return "", bootstrapNotDeclared, err
	}

	// The sandbox exists, so its inspection report is authoritative: a
	// stale registry entry is corrected here, on the mutating path, after
	// the drift gate — an edited guest port must surface as Creation
	// drift, whose report says how to proceed, not as a registry
	// verification failure that does not.
	if err := reconcilePorts(p, s); err != nil {
		return "", bootstrapNotDeclared, err
	}

	var action string
	if msb.IsRunning(entry.Status) {
		action = "already running"
	} else {
		// msb re-resolves each secret from its own environment on every
		// start (verified on a real host, ticket 01), so a restart carries
		// the declared host values again through the subprocess environment;
		// preparePersistent has already refused any missing variable before
		// any resource changed.
		if err := box.Start(ctx, p.id.Sandbox); err != nil {
			// The listing can lag behind the backend: a sandbox that is up may
			// not read as "running" there (observed on a real host — msb ls
			// reported "Running", which the comparison now handles, and a
			// start was still attempted in the gap). msb's own refusal is the
			// authoritative evidence that the sandbox is up, which is all
			// ensureRunning needs.
			if !errors.Is(err, msb.ErrAlreadyRunning) {
				return "", bootstrapNotDeclared, err
			}
			action = "already running"
		} else {
			action = "started; its state and volumes were kept"
		}
	}

	if len(p.tr.Ports) > 0 {
		// The reuse report names the endpoints inspection is authoritative
		// for, exactly as a creation report does.
		if actual, perr := s.PortsOf(); perr == nil && len(actual) > 0 {
			action += "; published ports:\n" + formatEndpoints(p.tr.Ports, actual)
		}
	}

	boot, err := assessBootstrap(p, s.CreatedAt)
	if err != nil {
		return "", bootstrapNotDeclared, err
	}
	// --retry-bootstrap runs the current definition for an incomplete or
	// a changed Bootstrap — the definition that ran no longer matching
	// sbx.toml is exactly the case the flag exists for — and records
	// completion only on success.
	if retryBootstrap && (boot == bootstrapIncomplete || boot == bootstrapChanged) {
		if err := runBootstrap(ctx, box, p, s.CreatedAt, out); err != nil {
			return action, boot, &bootstrapFailure{err}
		}
		action += "; bootstrap ran again and its completion was recorded"
		boot = bootstrapComplete
	}
	return action, boot, nil
}

// driftReport compares the sandbox's creation snapshot with the current
// configuration. A missing snapshot counts as drift; a corrupt one is an
// error, never silently treated as a match. The current image digest comes
// from msb, and an image that cannot be inspected fails closed as drift.
// A finding that is not an old/new pair, such as the missing snapshot,
// comes back as a note; every other difference is a DriftEntry.
func driftReport(ctx context.Context, box msb.CLI, p *persistent) (notes []string, entries []state.DriftEntry, err error) {
	snap, err := state.LoadSnapshot(p.id.Sandbox)
	if errors.Is(err, state.ErrNoSnapshot) {
		return []string{"no creation-time snapshot exists for this sandbox; an owned sandbox without a snapshot counts as drifted"}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	digest := ""
	if info, err := box.ImageInspect(ctx, p.cfg.Image); err == nil {
		digest = info.ManifestDigest
	} else {
		// Drift fails closed on an uninspectable image; the reason travels
		// with the report so the refusal is diagnosable without re-running
		// msb by hand.
		notes = append(notes, fmt.Sprintf("the current image %s could not be inspected (%v), so its contents are unconfirmed", p.cfg.Image, err))
	}
	return notes, state.Drift(snap, state.DefinitionOf(p.tr), digest), nil
}

// findSandbox locates the named sandbox in the backend's listing. A
// failure to list is never read as an empty backend.
func findSandbox(ctx context.Context, box msb.CLI, name string) (msb.ListEntry, bool, error) {
	entries, err := box.List(ctx)
	if err != nil {
		return msb.ListEntry{}, false, err
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true, nil
		}
	}
	return msb.ListEntry{}, false, nil
}

// isOwned reports whether sbx created this sandbox: the sbx.managed label
// is the attribution record, read from whichever configuration layer the
// backend keeps for the sandbox's current state.
func isOwned(s msb.Sandbox) bool {
	return s.EffectiveConfig().Labels["sbx.managed"] == "1"
}

// errUnowned explains the never-adopt rule and the way out.
func errUnowned(name string) error {
	return fmt.Errorf("a sandbox named %s already exists, but sbx did not create it; sbx never adopts, modifies, or removes a sandbox it does not own. If it is yours to remove, do so with `msb remove %s` and run sbx again", name, name)
}

// ensureImage resolves the image's manifest digest. A missing image with a
// [build] recipe is an instruction to run `sbx build` — creation never
// builds and never pulls it — while a missing prebuilt image may be
// pulled. PullIfMissing carries the inspect-then-pull sequence and the
// guidance message, so both execution paths describe msb's separate image
// store identically and cannot drift apart.
func ensureImage(ctx context.Context, box msb.CLI, cfg config.Config) (string, error) {
	image := cfg.Image
	info, err := box.ImageInspect(ctx, image)
	if err == nil {
		return info.ManifestDigest, nil
	}
	if cfg.Build != nil {
		return "", errImageNeedsBuild(image)
	}
	if err := box.PullIfMissing(ctx, image); err != nil {
		return "", err
	}
	info, err = box.ImageInspect(ctx, image)
	if err != nil {
		return "", fmt.Errorf("inspecting the pulled image %s: %w", image, err)
	}
	return info.ManifestDigest, nil
}

// materializeGeneratedFiles writes the secret-name map and the filesystem
// configuration to per-creation temporaries outside the repository and
// points the translation at them. The returned cleanup removes both.
func materializeGeneratedFiles(tr *translate.Translation) (func(), error) {
	var paths []string
	// The cleanup is built first and returned even on failure: a second
	// write that fails must not leak the first file, which holds secret
	// names and host-variable references, into the shared temp directory.
	cleanup := func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}
	if tr.SecretConfYAML != "" {
		path, err := writeTempFile("sbx-secrets-*.yaml", tr.SecretConfYAML)
		if err != nil {
			return cleanup, fmt.Errorf("writing the secret map: %w", err)
		}
		tr.Options.SecretConf = path
		paths = append(paths, path)
	}
	if tr.FsConfYAML != "" {
		path, err := writeTempFile("sbx-fs-conf-*.yaml", tr.FsConfYAML)
		if err != nil {
			return cleanup, fmt.Errorf("writing the filesystem configuration: %w", err)
		}
		tr.Options.FsConf = path
		paths = append(paths, path)
	}
	return cleanup, nil
}

// renderDrift formats drift for an error or report: any standalone notes
// first, then the changed settings as a table with the creation-time value
// on the left and the current value on the right. A plain output (the
// --plain lever) drops the table's borders, never its words.
func renderDrift(out *output, notes []string, entries []state.DriftEntry) string {
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	b.WriteString(state.RenderDrift(entries, "  ", out.plain))
	return b.String()
}

// parsePersistentFlags checks that args carries nothing beyond the given
// allowed flags, returning which of them are present.
func parsePersistentFlags(cmd string, args []string, allowed []string) (map[string]bool, error) {
	present := map[string]bool{}
	allowedSet := map[string]bool{}
	for _, a := range allowed {
		allowedSet[a] = true
	}
	for _, a := range args[1:] {
		switch {
		case allowedSet[a]:
			present[a] = true
		default:
			if len(allowed) == 0 {
				return nil, fmt.Errorf("%s takes no arguments or flags, got %q", cmd, a)
			}
			return nil, fmt.Errorf("%s takes only %s, got %q", cmd, strings.Join(allowed, " "), a)
		}
	}
	return present, nil
}

// parseExecArgs splits `exec`'s command line into flags and the guest argv
// that follows the -- separator, which is required once a command is given.
func parseExecArgs(args []string) (allowStale bool, argv []string, err error) {
	for i := 1; i < len(args); i++ {
		switch a := args[i]; a {
		case "--allow-stale":
			allowStale = true
		case "--":
			return allowStale, args[i+1:], nil
		default:
			return false, nil, fmt.Errorf("exec takes an optional %q separator before the guest command, got %q", "--", a)
		}
	}
	return allowStale, nil, nil
}
