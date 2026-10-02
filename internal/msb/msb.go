// Package msb is sbx's adapter seam for the microsandbox CLI: every
// backend-affecting action runs through msb as a subprocess with the local
// backend forced in its environment (ADR-0006). sbx never falls back to
// another backend and never swallows a subprocess error.
//
// Translation always uses msb's long flag spellings (--name, --cpus,
// --mount-dir, ...) for clarity; short aliases are never emitted.
//
// Flag spellings and output schemas follow the observed msb 0.7.3 behavior
// recorded in the spec's backend verification; behaviors not yet confirmed
// on a real host are marked UNVERIFIED.
package msb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// BackendEnv is the environment variable that selects msb's backend.
const BackendEnv = "MSB_BACKEND"

// execWaitDelay is how long a canceled exec subprocess may linger before it
// is killed outright.
const execWaitDelay = 10 * time.Second

// CLI invokes the msb executable. The zero value uses "msb" from PATH.
type CLI struct {
	// Binary is the msb executable name or path. Empty means "msb".
	Binary string
}

func (c CLI) binary() string {
	if c.Binary == "" {
		return "msb"
	}
	return c.Binary
}

// localEnv builds the subprocess environment: the host environment with
// MSB_BACKEND forced to local, so no inherited selection can override sbx.
// Secret values reach msb only through this environment, never through
// arguments or files.
func localEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, BackendEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, BackendEnv+"=local")
}

// run executes one msb subcommand, capturing its output.
func (c CLI) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	cmd.Env = localEnv()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errOut.String())
		if detail == "" {
			detail = err.Error()
		}
		return out.String(), fmt.Errorf("msb %s: %s", args[0], firstLine(detail))
	}
	return out.String(), nil
}

// Context is the effective msb backend selection.
type Context struct {
	Backend string `json:"backend"`
}

// LocalContext confirms the effective backend selection under a forced
// MSB_BACKEND=local and refuses anything else: a cloud profile that ignores
// the environment is exactly the override sbx must not proceed with.
func (c CLI) LocalContext(ctx context.Context) (Context, error) {
	out, err := c.run(ctx, "context", "--format", "json")
	if err != nil {
		return Context{}, fmt.Errorf("confirming the msb backend: %w", err)
	}
	cx, err := parseContext(out)
	if err != nil {
		return Context{}, fmt.Errorf("parsing msb context output: %w", err)
	}
	if cx.Backend != "local" {
		return Context{}, fmt.Errorf("msb selected the %q backend; sbx requires a local microsandbox installation and never falls back to another backend", cx.Backend)
	}
	return cx, nil
}

// parseContext extracts the effective backend from `msb context --format
// json` output. Real msb 0.7.3 reports `{"kind": "local", "source":
// "MSB_BACKEND"}`; the parser accepts that schema and, tolerantly, a
// "backend" field at the top level or one level down, case-insensitively.
// It reports the raw output when it finds nothing.
func parseContext(out string) (Context, error) {
	trimmed := strings.TrimSpace(out)
	var doc map[string]any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return Context{}, fmt.Errorf("%q is not JSON: %w", trimmed, err)
	}
	if v, ok := backendValue(doc); ok {
		return Context{Backend: v}, nil
	}
	for _, v := range doc {
		if nested, ok := v.(map[string]any); ok {
			if bv, ok := backendValue(nested); ok {
				return Context{Backend: bv}, nil
			}
		}
	}
	return Context{}, fmt.Errorf("no backend field found in %q", trimmed)
}

// backendValue reads the backend from one JSON object: msb's "kind" key,
// or a "backend" key, matched case-insensitively. A null or non-string
// value yields nothing.
func backendValue(doc map[string]any) (string, bool) {
	for _, want := range []string{"kind", "backend"} {
		for key, value := range doc {
			if !strings.EqualFold(key, want) {
				continue
			}
			switch v := value.(type) {
			case string:
				return v, true
			case map[string]any:
				if inner, ok := v["type"].(string); ok {
					return inner, true
				}
			}
		}
	}
	return "", false
}

// Label is one sandbox label passed as --label key=value.
type Label struct {
	Key   string
	Value string
}

// Mount is one host bind, passed as --mount-dir SOURCE:DEST[:OPTIONS] for
// a directory source or --mount-file SOURCE:DEST[:OPTIONS] for a file.
type Mount struct {
	// Source is an absolute host path.
	Source string
	// Target is an absolute guest path.
	Target string
	// ReadOnly adds the :ro suffix.
	ReadOnly bool
	// IsFile selects --mount-file; the zero value (a directory source)
	// selects --mount-dir.
	IsFile bool
}

// NamedMount mounts an existing named volume, passed as
// --mount-named NAME:DEST. Project volumes use it.
type NamedMount struct {
	// Name is the backend volume name.
	Name string
	// Target is an absolute guest path.
	Target string
}

// OwnedMount creates a private volume removed with its sandbox, passed as
// --mount-owned DEST[:OPTIONS]. Sandbox volumes use it.
type OwnedMount struct {
	// Target is an absolute guest path.
	Target string
	// Kind is "dir" (the msb default) or "disk".
	Kind string
	// Size is required for disks, in msb's format.
	Size string
}

// CreateOptions carries everything `msb create` receives for one sandbox.
type CreateOptions struct {
	// Name is the unique sandbox name.
	Name string
	// Image is the OCI image reference.
	Image string
	// CPUs is the CPU limit.
	CPUs float64
	// Memory is a size in msb's format, such as "2G".
	Memory string
	// Mounts are host binds in translation order (Workspace first).
	Mounts []Mount
	// FsConf is the path of a generated filesystem configuration passed as
	// --fs-conf, used for tmpfs mounts whose noexec option the --tmpfs
	// flag cannot express. Config-file mounts apply additively alongside
	// mount flags (verified on msb 0.7.5).
	FsConf string
	// Named are existing named volumes mounted into the sandbox.
	Named []NamedMount
	// Owned are private volumes created with the sandbox.
	Owned []OwnedMount
	// Env entries are passed as -e KEY=value, sorted by key.
	Env []string
	// Labels are applied in order.
	Labels []Label
	// NetRules are --net-rule tokens, such as "allow@example.com".
	NetRules []string
	// DNSNameservers are passed as repeatable --dns-nameserver flags.
	DNSNameservers []string
	// TLSIntercept turns on msb's TLS inspection, which allowlist egress and
	// declared secrets require.
	TLSIntercept bool
	// SecretConf is the path of a generated secret-name map passed as
	// --secret-conf. It contains secret names and host-variable references
	// only; msb resolves the values from its own environment at start time.
	SecretConf string
	// NoNet disables all network access (egress "none" in sbx terms).
	NoNet bool
}

// Create runs `msb create`. Argument order is fixed and pinned by tests.
// Flag spellings come from real msb 0.7.3: `msb create [OPTIONS] [IMAGE]`
// with `--name`, `-c/--cpus`, `-m/--memory`, `--mount-dir SOURCE:DEST`,
// repeatable `--label KEY=VALUE`, and `--no-net` for no network access.
// msb boots the VM without running the image's default command.
func (c CLI) Create(ctx context.Context, o CreateOptions) error {
	args := append([]string{"create"}, CreateArgs(o)...)
	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("creating sandbox %s: %w", o.Name, err)
	}
	return nil
}

// CreateArgs renders the shared `msb create`-style argument prefix: the
// optional name, positional image, resources, mounts, environment, labels,
// network restriction, and secret map. An empty Name omits --name entirely:
// msb run treats an explicitly named sandbox as one to keep, while an
// auto-generated one-shot is removed when the command completes — so
// disposable runs pass no name and let msb own both naming and lifecycle.
//
// Flag spellings come from `msb create --help` (msb 0.7.5): --mount-dir
// SOURCE:DEST[:OPTIONS] and --mount-file SOURCE:DEST[:OPTIONS], --tmpfs
// PATH:SIZE[:OPTIONS], --mount-named NAME:DEST, --mount-owned
// DEST[:OPTIONS], -e KEY=value, --net-rule allow@<target>,
// --net-default-egress, --tls-intercept, --secret-conf PATH.
func CreateArgs(o CreateOptions) []string {
	var args []string
	if o.Name != "" {
		args = append(args, "--name", o.Name)
	}
	args = append(args, o.Image,
		"--cpus", FormatCPUs(o.CPUs),
		"--memory", o.Memory,
	)
	for _, m := range o.Mounts {
		spec := m.Source + ":" + m.Target
		if m.ReadOnly {
			// :ro verified on a real host for both --mount-file and
			// --mount-dir (ticket 04).
			spec += ":ro"
		}
		flag := "--mount-dir"
		if m.IsFile {
			flag = "--mount-file"
		}
		args = append(args, flag, spec)
	}
	for _, nm := range o.Named {
		args = append(args, "--mount-named", nm.Name+":"+nm.Target)
	}
	for _, om := range o.Owned {
		spec := om.Target
		if om.Kind == "disk" {
			spec += ":kind=disk,size=" + om.Size
		}
		args = append(args, "--mount-owned", spec)
	}
	for _, e := range o.Env {
		args = append(args, "--env", e)
	}
	for _, l := range o.Labels {
		args = append(args, "--label", l.Key+"="+l.Value)
	}
	for _, rule := range o.NetRules {
		args = append(args, "--net-rule", rule)
	}
	if len(o.NetRules) > 0 {
		// With rules present msb's egress default is already deny; stating it
		// keeps the allowlist policy explicit rather than incidental.
		args = append(args, "--net-default-egress", "deny")
	}
	if o.TLSIntercept {
		args = append(args, "--tls-intercept")
	}
	for _, ns := range o.DNSNameservers {
		args = append(args, "--dns-nameserver", ns)
	}
	if o.NoNet {
		args = append(args, "--no-net")
	}
	if o.SecretConf != "" {
		args = append(args, "--secret-conf", o.SecretConf)
	}
	if o.FsConf != "" {
		args = append(args, "--fs-conf", o.FsConf)
	}
	return args
}

// RunOptions carries everything `msb run` receives for one disposable run:
// the create-time settings plus the guest command.
type RunOptions struct {
	// CreateOptions carries the sandbox's creation settings.
	CreateOptions
	// Workdir is the guest working directory for the command.
	Workdir string
	// Argv is the guest command. msb run replaces the image CMD while
	// preserving its effective entrypoint, and sbx matches that native
	// behavior rather than neutralizing it.
	Argv []string
}

// Run is msb's native one-shot: it creates the sandbox and runs the command
// in a single subprocess. sbx passes no --name, so msb treats the sandbox as
// an ephemeral one-shot and removes it when the command completes; the
// sandbox's whole lifecycle is msb run's own behavior, and sbx adds no
// cleanup step.
func (c CLI) Run(ctx context.Context, o RunOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := append([]string{"run"}, CreateArgs(o.CreateOptions)...)
	if o.Workdir != "" {
		args = append(args, "--workdir", o.Workdir)
	}
	if stdinIsTerminal(stdin) {
		args = append(args, "--tty")
	} else {
		args = append(args, "--no-tty")
	}
	args = append(args, "--")
	args = append(args, o.Argv...)
	return c.runChild(ctx, args, stdin, stdout, stderr)
}

// Exec runs argv in the named sandbox, forwarding the standard streams and
// returning the guest's exit status. The stream mode follows what stdin
// actually is: a terminal gets --tty (interactive session with echo and
// line editing), anything else gets --stream for byte-faithful piping —
// msb 0.7.3 rejects --stream on terminal stdin and keeps the two modes
// exclusive. Signals are delivered to the msb subprocess's process group;
// whether msb forwards them into the guest is UNVERIFIED (ticket 01), so
// the interim behavior is not relied upon.
func (c CLI) Exec(ctx context.Context, name, workdir string, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := []string{"exec", name}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	if stdinIsTerminal(stdin) {
		args = append(args, "--tty")
	} else {
		args = append(args, "--stream")
	}
	args = append(args, "--")
	args = append(args, argv...)
	return c.runChild(ctx, args, stdin, stdout, stderr)

}

// runChild runs one msb subprocess attached to the given standard streams
// and returns the guest's exit status. The stream-mode-dependent process
// setup lives here: with terminal stdin the child stays in sbx's foreground
// process group (a background group's tty setup is stopped by SIGTTOU, and
// terminal signals reach msb directly); with piped stdin it gets its own
// group so cancellation reaches the subprocess and its children. Whether
// msb forwards signals into the guest is UNVERIFIED (ticket 01).
func (c CLI) runChild(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	tty := stdinIsTerminal(stdin)

	cmd := exec.CommandContext(ctx, c.binary(), args...)
	cmd.Env = localEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = execWaitDelay
	if tty {
		// Keep msb in sbx's foreground process group: as a background
		// group relative to the terminal, its tty setup would be stopped
		// by SIGTTOU and the session would hang. In the foreground group,
		// terminal signals such as Ctrl-C reach msb directly.
		cmd.Cancel = func() error {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			return nil
		}
	} else {
		// Put msb in its own process group so a cancellation reaches the
		// subprocess and its children without signaling sbx itself.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			}
			return nil
		}
	}

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("starting msb %s: %w", args[0], err)
	}
	waitErr := cmd.Wait()
	if waitErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	if ctx.Err() != nil {
		return -1, fmt.Errorf("msb %s was interrupted: %w", args[0], ctx.Err())
	}
	return -1, fmt.Errorf("msb %s: %w", args[0], waitErr)
}

// Remove runs `msb remove --force <name>`: the sandbox is likely still
// running after exec, and --force stops it before removal.
func (c CLI) Remove(ctx context.Context, name string) error {
	if _, err := c.run(ctx, "remove", "--force", name); err != nil {
		return fmt.Errorf("removing sandbox %s: %w", name, err)
	}
	return nil
}

// PullIfMissing confirms the image exists with `msb image inspect` and may
// pull it with `msb image pull` when it does not. Inspection failure is not
// proof the image is absent — msb's image store is separate from Docker's,
// so a locally built image that was never imported also fails here — and
// when the pull fails too, both errors and a concrete next step surface
// instead of the pull alone.
func (c CLI) PullIfMissing(ctx context.Context, image string) error {
	_, inspectErr := c.run(ctx, "image", "inspect", image, "--format", "json")
	if inspectErr == nil {
		return nil
	}
	_, pullErr := c.run(ctx, "image", "pull", image)
	if pullErr == nil {
		return nil
	}
	return fmt.Errorf("image %s is not available to msb: inspect: %v; pull: %v. msb's image store is separate from Docker's: pull the image from a registry, or import one built locally with `docker save %s -o <archive> && msb load --input <archive>`; `sbx build` will automate this", image, firstLine(inspectErr.Error()), firstLine(pullErr.Error()), image)
}

// stdinIsTerminal reports whether the reader is an interactive terminal.
// Anything that is not an *os.File — buffers in tests, pipes from callers
// that never hand over a file — counts as a pipe.
func stdinIsTerminal(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// FormatCPUs renders a CPU count without a trailing ".0" for whole numbers.
func FormatCPUs(cpus float64) string {
	if cpus == float64(int(cpus)) {
		return fmt.Sprintf("%d", int(cpus))
	}
	return fmt.Sprintf("%g", cpus)
}

// firstLine returns the first non-empty line of a string.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "msb failed without output"
}
