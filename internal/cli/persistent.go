// Persistent-sandbox commands: up, exec, status, logs, stop, and rm manage
// the one stable Sandbox identity each worktree owns (ADR-0005), created
// once and reused afterward (ADR-0002). Three safety rules shape every
// path here: sbx never adopts a same-named sandbox it did not create, never
// recreates a drifted sandbox — that could destroy private data — and never
// removes a Project volume or port reservation when removing a sandbox.
//
// Bootstrap, image checks, and published ports belong to later tickets:
// declaring them already fails closed in configuration parsing, and the
// creation path re-checks nothing strict parsing has already refused.
// Declared Project volumes are checked for compatibility with the
// backend's existing volumes before any creation (ADR-0003).
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
)

// runUp implements `sbx up [--allow-stale]`: create the persistent sandbox
// once, or start it when stopped, and refuse drift unless --allow-stale.
func runUp(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags, err := parsePersistentFlags("up", args, []string{"--allow-stale"})
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitUsage
	}
	allowStale := flags["--allow-stale"]

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	p, err := preparePersistent(ctx)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	action, err := ensureRunning(ctx, msb.CLI{}, p, allowStale, stderr)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	fmt.Fprintf(stdout, "persistent sandbox: %s\n%s\n", p.id.Sandbox, action)
	return exitOK
}

// runExec implements `sbx exec [--allow-stale] [-- <argv...>]`: run the
// guest command in the persistent sandbox, bringing the sandbox up first,
// and leave it running afterward. The guest's exit status is sbx's.
func runExec(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	allowStale, argv, err := parseExecArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	p, err := preparePersistent(ctx)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if _, err := ensureRunning(ctx, msb.CLI{}, p, allowStale, stderr); err != nil {
		return persistentFatal(err, stderr)
	}

	guestArgv := argv
	if len(guestArgv) == 0 {
		guestArgv = []string{p.cfg.Shell}
	}
	code, err := msb.CLI{}.Exec(ctx, p.id.Sandbox, p.tr.Workspace, guestArgv, stdin, stdout, stderr)
	if err != nil && code < 0 {
		return persistentFatal(fmt.Errorf("running %s: %w", strings.Join(guestArgv, " "), err), stderr)
	}
	return code
}

// runStatus implements `sbx status`, a read-only report: identity, backend
// state, and Creation drift. It never mutates the backend and never writes
// sbx state, and it stays usable when the sandbox has drifted.
func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if _, err := parsePersistentFlags("status", args, nil); err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitUsage
	}
	info, cfg, err := discoverConfig(ctx)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return persistentFatal(err, stderr)
	}

	fmt.Fprintf(stdout, "persistent sandbox: %s\n", id.Sandbox)
	_, exists, err := findSandbox(ctx, box, id.Sandbox)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if !exists {
		fmt.Fprintf(stdout, "status: not created; run `sbx up` to create it\n")
		return exitOK
	}

	s, err := box.Inspect(ctx, id.Sandbox)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if !isOwned(s) {
		fmt.Fprintf(stdout, "status: %s, but sbx did not create it; sbx never adopts a sandbox it does not own\n", s.Status)
		return exitOK
	}
	fmt.Fprintf(stdout, "status: %s\n", s.Status)
	fmt.Fprintf(stdout, "created at: %s\n", s.CreatedAt)
	if eff := s.EffectiveConfig(); eff.ManifestDigest != "" {
		fmt.Fprintf(stdout, "created from image contents: %s\n", eff.ManifestDigest)
	}

	// Drift is reported here, never enforced: status stays usable exactly
	// when up and exec would refuse.
	p := &persistent{info: info, cfg: cfg, id: id}
	tr, trErr := translate.Sandbox(info, cfg, "persistent")
	if trErr != nil {
		// A configuration that cannot translate (a missing bind source,
		// say) still leaves the identity and backend state worth reporting.
		fmt.Fprintf(stdout, "drift: unknown (the current configuration does not translate: %v)\n", trErr)
		return exitOK
	}
	tr.Options.Name = id.Sandbox
	p.tr = tr
	drift, err := driftReport(ctx, box, p)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if len(drift) == 0 {
		fmt.Fprintf(stdout, "drift: none\n")
	} else {
		fmt.Fprintf(stdout, "drift (up and exec refuse this; status does not):\n%s\n", renderDrift(drift))
	}
	return exitOK
}

// runLogs implements `sbx logs`, a read-only pass-through of msb's logs.
func runLogs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if _, err := parsePersistentFlags("logs", args, nil); err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitUsage
	}
	id, err := discoverIdentity(ctx)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return persistentFatal(err, stderr)
	}
	if _, exists, err := findSandbox(ctx, box, id.Sandbox); err != nil {
		return persistentFatal(err, stderr)
	} else if !exists {
		return persistentFatal(fmt.Errorf("no persistent sandbox for this worktree; run `sbx up` first"), stderr)
	}
	out, err := box.Logs(ctx, id.Sandbox)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	fmt.Fprint(stdout, out)
	return exitOK
}

// runStop implements `sbx stop`: stop the persistent sandbox without
// deleting its state. Stopping touches only an owned sandbox, and drift
// never blocks it — stopping does not use the sandbox's contents.
func runStop(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if _, err := parsePersistentFlags("stop", args, nil); err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitUsage
	}
	id, err := discoverIdentity(ctx)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return persistentFatal(err, stderr)
	}
	entry, exists, err := findSandbox(ctx, box, id.Sandbox)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if !exists {
		return persistentFatal(fmt.Errorf("no persistent sandbox for this worktree; run `sbx up` first"), stderr)
	}
	s, err := box.Inspect(ctx, id.Sandbox)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if !isOwned(s) {
		return persistentFatal(errUnowned(id.Sandbox), stderr)
	}
	if entry.Status != "running" {
		fmt.Fprintf(stdout, "persistent sandbox: %s\nalready stopped; its state and volumes are kept\n", id.Sandbox)
		return exitOK
	}
	if err := box.Stop(ctx, id.Sandbox); err != nil {
		return persistentFatal(err, stderr)
	}
	fmt.Fprintf(stdout, "persistent sandbox: %s\nstopped; its state and volumes are kept, and `sbx up` starts it again\n", id.Sandbox)
	return exitOK
}

// runRm implements `sbx rm [--yes]`: after confirmation — which
// noninteractive use gives with --yes — remove the persistent sandbox and
// its Sandbox volumes, list what was lost, and keep Project volumes and
// port reservations untouched.
func runRm(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	yes, err := parsePersistentFlags("rm", args, []string{"--yes"})
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitUsage
	}
	confirmed := yes["--yes"]
	id, err := discoverIdentity(ctx)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return persistentFatal(err, stderr)
	}
	if _, exists, err := findSandbox(ctx, box, id.Sandbox); err != nil {
		return persistentFatal(err, stderr)
	} else if !exists {
		return persistentFatal(fmt.Errorf("no persistent sandbox for this worktree; there is nothing to remove"), stderr)
	}
	s, err := box.Inspect(ctx, id.Sandbox)
	if err != nil {
		return persistentFatal(err, stderr)
	}
	if !isOwned(s) {
		return persistentFatal(errUnowned(id.Sandbox), stderr)
	}

	// The snapshot names the Sandbox volumes that die with the sandbox; it
	// is read before anything changes.
	snap, snapErr := state.LoadSnapshot(id.Sandbox)

	if !confirmed {
		if !msb.StdinIsTerminal(stdin) {
			return persistentFatal(fmt.Errorf("refusing to remove %s without confirmation; pass --yes in noninteractive use", id.Sandbox), stderr)
		}
		if !confirmRemoval(id.Sandbox, stdin, stderr) {
			fmt.Fprintf(stderr, "sbx: removal canceled; %s was not removed\n", id.Sandbox)
			return exitFailure
		}
	}

	if err := box.Remove(ctx, id.Sandbox); err != nil {
		return persistentFatal(err, stderr)
	}
	if err := state.DeleteSnapshot(id.Sandbox); err != nil {
		return persistentFatal(err, stderr)
	}

	fmt.Fprintf(stdout, "persistent sandbox: %s\nremoved.\n", id.Sandbox)
	switch {
	case errors.Is(snapErr, state.ErrNoSnapshot):
		fmt.Fprintf(stdout, "its Sandbox volumes were removed with it (no creation snapshot recorded their list).\n")
	case snapErr != nil:
		fmt.Fprintf(stdout, "warning: its creation snapshot was unreadable (%v), so the list of Sandbox volumes removed with it is unknown.\n", snapErr)
	default:
		reportOwnedVolumes(stdout, snap)
	}
	fmt.Fprintf(stdout, "Project volumes and port reservations were kept.\n")
	return exitOK
}

// reportOwnedVolumes lists the Sandbox volumes that were removed with the
// sandbox, or notes their absence.
func reportOwnedVolumes(w io.Writer, snap state.Snapshot) {
	owned := snap.Definition.Options.Owned
	if len(owned) == 0 {
		fmt.Fprintf(w, "it had no Sandbox volumes.\n")
		return
	}
	fmt.Fprintf(w, "Sandbox volumes removed with it:\n")
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
func preparePersistent(ctx context.Context) (*persistent, error) {
	info, cfg, err := discoverConfig(ctx)
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
// commands that act on an existing sandbox without translating the
// configuration: logs, stop, and rm.
func discoverIdentity(ctx context.Context) (identity.Identity, error) {
	info, _, err := discoverConfig(ctx)
	if err != nil {
		return identity.Identity{}, err
	}
	return identity.Derive(info.CommonDir, info.WorktreeRoot), nil
}

// ensureRunning brings the worktree's persistent sandbox to a running
// state and returns the action taken, for the caller to report. Creation
// happens only when the backend holds no same-named sandbox; an existing
// one is started when stopped, checked for ownership and drift, and never
// recreated.
func ensureRunning(ctx context.Context, box msb.CLI, p *persistent, allowStale bool, stderr io.Writer) (string, error) {
	if _, err := box.LocalContext(ctx); err != nil {
		return "", err
	}
	entry, exists, err := findSandbox(ctx, box, p.id.Sandbox)
	if err != nil {
		return "", err
	}

	if !exists {
		// Every declared Project volume is checked against the backend's
		// existing volumes before anything is created or pulled.
		if err := checkProjectVolumes(ctx, box, p.tr.ProjectVolumes); err != nil {
			return "", err
		}
		digest, err := ensureImage(ctx, box, p.cfg.Image)
		if err != nil {
			return "", err
		}
		cleanup, err := materializeGeneratedFiles(&p.tr)
		if err != nil {
			return "", err
		}
		defer cleanup()
		if err := box.Create(ctx, p.tr.Options); err != nil {
			return "", err
		}
		// The snapshot is what drift detection compares against later; it
		// is written only after creation succeeded, so a failed creation
		// leaves no snapshot behind.
		if err := state.SaveSnapshot(p.id.Sandbox, state.Snapshot{
			ImageDigest: digest,
			Definition:  state.DefinitionOf(p.tr),
		}); err != nil {
			return "", err
		}
		return fmt.Sprintf("created from %s (image contents %s) and left running; run `sbx exec -- <command>` to work in it", p.cfg.Image, digest), nil
	}

	// A same-named sandbox exists: sbx touches it only when it carries the
	// sbx.managed label.
	s, err := box.Inspect(ctx, p.id.Sandbox)
	if err != nil {
		return "", err
	}
	if !isOwned(s) {
		return "", errUnowned(p.id.Sandbox)
	}

	drift, err := driftReport(ctx, box, p)
	if err != nil {
		return "", err
	}
	if len(drift) > 0 {
		if !allowStale {
			return "", fmt.Errorf("the persistent sandbox has drifted from sbx.toml:\n%s\n\nsbx never removes or recreates a sandbox that may hold private data; remove it yourself with `sbx rm` and run `sbx up` again, or pass --allow-stale to use it as-is", renderDrift(drift))
		}
		fmt.Fprintf(stderr, "warning: using %s despite drift:\n%s", p.id.Sandbox, renderDrift(drift))
	}

	if entry.Status == "running" {
		return "already running", nil
	}
	// Whether a stopped sandbox's msb start re-reads a --secret from its
	// environment is UNVERIFIED (ticket 01's host check), so secret-bearing
	// restarts are refused outright rather than risk a sandbox that runs
	// with missing or stale secret wiring.
	if len(p.cfg.Secrets) > 0 {
		return "", fmt.Errorf("the persistent sandbox is stopped and its configuration declares secrets; whether `msb start` re-reads a secret from its environment is not yet verified on a real host, so sbx refuses secret-bearing restarts until a later release confirms the behavior")
	}
	if err := box.Start(ctx, p.id.Sandbox); err != nil {
		return "", err
	}
	return "started; its state and volumes were kept", nil
}

// driftReport compares the sandbox's creation snapshot with the current
// configuration. A missing snapshot counts as drift; a corrupt one is an
// error, never silently treated as a match. The current image digest comes
// from msb, and an image that cannot be inspected fails closed as drift.
func driftReport(ctx context.Context, box msb.CLI, p *persistent) ([]string, error) {
	snap, err := state.LoadSnapshot(p.id.Sandbox)
	if errors.Is(err, state.ErrNoSnapshot) {
		return []string{"no creation-time snapshot exists for this sandbox; an owned sandbox without a snapshot counts as drifted"}, nil
	}
	if err != nil {
		return nil, err
	}
	digest := ""
	if info, err := box.ImageInspect(ctx, p.cfg.Image); err == nil {
		digest = info.ManifestDigest
	}
	return state.Drift(snap, state.DefinitionOf(p.tr), digest), nil
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

// ensureImage resolves the image's manifest digest, pulling a missing
// prebuilt image first. When both inspection and pull fail, both errors
// and a concrete next step surface, exactly as on the disposable path.
func ensureImage(ctx context.Context, box msb.CLI, image string) (string, error) {
	info, err := box.ImageInspect(ctx, image)
	if err == nil {
		return info.ManifestDigest, nil
	}
	inspectErr := err
	if err := box.Pull(ctx, image); err != nil {
		return "", fmt.Errorf("image %s is not available to msb: inspect: %v; pull: %v. msb's image store is separate from Docker's: pull the image from a registry, or import one built locally with `docker save %s -o <archive> && msb load --input <archive>`; `sbx build` will automate this", image, inspectErr, err, image)
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
	if tr.SecretConfYAML != "" {
		path, err := writeTempFile("sbx-secrets-*.yaml", tr.SecretConfYAML)
		if err != nil {
			return nil, fmt.Errorf("writing the secret map: %w", err)
		}
		tr.Options.SecretConf = path
		paths = append(paths, path)
	}
	if tr.FsConfYAML != "" {
		path, err := writeTempFile("sbx-fs-conf-*.yaml", tr.FsConfYAML)
		if err != nil {
			return nil, fmt.Errorf("writing the filesystem configuration: %w", err)
		}
		tr.Options.FsConf = path
		paths = append(paths, path)
	}
	return func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}, nil
}

// renderDrift formats drift lines for an error or report.
func renderDrift(drift []string) string {
	var b strings.Builder
	for _, d := range drift {
		fmt.Fprintf(&b, "  - %s\n", d)
	}
	return b.String()
}

// parsePersistentFlags checks that args carries nothing beyond the given
// allowed flags, returning which of them are present. --retry-bootstrap is
// rejected by name: Bootstrap is a later ticket's feature, and silently
// ignoring it would hide an unrun step.
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
		case a == "--retry-bootstrap":
			return nil, fmt.Errorf("%s: --retry-bootstrap is not supported yet; Bootstrap arrives in a later sbx release", cmd)
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

// persistentFatal reports one persistent-command failure.
func persistentFatal(err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "sbx: %v\n", err)
	return exitFailure
}
