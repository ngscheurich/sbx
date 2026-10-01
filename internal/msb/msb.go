// Package msb is sbx's adapter seam for the microsandbox CLI: every
// backend-affecting action runs through msb as a subprocess with the local
// backend forced in its environment (ADR-0006). sbx never falls back to
// another backend and never swallows a subprocess error.
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

// Mount is one bind mount passed as --mount source:target[:ro].
type Mount struct {
	// Source is an absolute host path.
	Source string
	// Target is an absolute guest path.
	Target string
	// ReadOnly adds the :ro suffix.
	ReadOnly bool
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
	// Mounts are bind mounts in declaration order.
	Mounts []Mount
	// Labels are applied in order.
	Labels []Label
	// NetConf is the path of a generated network policy file, or "" when
	// the default policy suffices.
	NetConf string
}

// Create runs `msb create`. Argument order is fixed and pinned by tests.
// The image's ENTRYPOINT and CMD play no part here: msb boots the VM
// without running the image's default command, and sbx never consults
// either (ADR-0007).
func (c CLI) Create(ctx context.Context, o CreateOptions) error {
	args := []string{
		"create", o.Name,
		"--image", o.Image,
		"--cpus", FormatCPUs(o.CPUs),
		"--memory", o.Memory,
	}
	for _, m := range o.Mounts {
		spec := m.Source + ":" + m.Target
		if m.ReadOnly {
			spec += ":ro"
		}
		args = append(args, "--mount", spec)
	}
	for _, l := range o.Labels {
		args = append(args, "--label", l.Key+"="+l.Value)
	}
	if o.NetConf != "" {
		args = append(args, "--net-conf", o.NetConf)
	}
	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("creating sandbox %s: %w", o.Name, err)
	}
	return nil
}

// Exec runs argv in the named sandbox, forwarding the standard streams and
// returning the guest's exit status. Signals are delivered to the msb
// subprocess's process group; whether msb forwards them into the guest is
// UNVERIFIED (ticket 01), so the interim behavior is not relied upon.
func (c CLI) Exec(ctx context.Context, name, workdir string, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := []string{"exec", name}
	if workdir != "" {
		args = append(args, "--workdir", workdir) // UNVERIFIED flag spelling on a real host
	}
	args = append(args, "--")
	args = append(args, argv...)

	cmd := exec.CommandContext(ctx, c.binary(), args...)
	cmd.Env = localEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	// Put msb in its own process group so a cancellation reaches the
	// subprocess and its children without signaling sbx itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = execWaitDelay

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("starting msb exec: %w", err)
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
		return -1, fmt.Errorf("msb exec was interrupted: %w", ctx.Err())
	}
	return -1, fmt.Errorf("msb exec: %w", waitErr)
}

// Remove runs `msb rm <name>`.
func (c CLI) Remove(ctx context.Context, name string) error {
	if _, err := c.run(ctx, "rm", name); err != nil {
		return fmt.Errorf("removing sandbox %s: %w", name, err)
	}
	return nil
}

// PullIfMissing confirms the image exists with `msb image inspect` and may
// pull it with `msb image pull` when it does not. Inspection failure is not
// treated as proof the image is missing; the pull attempt surfaces the real
// error when the image cannot be obtained.
func (c CLI) PullIfMissing(ctx context.Context, image string) error {
	if _, err := c.run(ctx, "image", "inspect", image, "--format", "json"); err == nil {
		return nil
	}
	if _, err := c.run(ctx, "image", "pull", image); err != nil {
		return fmt.Errorf("pulling image %s: %w", image, err)
	}
	return nil
}

// WriteNetConfNone writes the network policy file passed to --net-conf when
// egress is "none": no allowed destinations and no nameservers. The file is
// created outside the repository, and the caller removes it when done. The
// exact msb schema is UNVERIFIED on a real host; sbx's side of the contract
// is pinned by tests.
func WriteNetConfNone() (path string, err error) {
	content := "# generated by sbx: egress = none\nallow: []\nnameservers: []\n"
	f, err := os.CreateTemp("", "sbx-net-conf-*.yaml")
	if err != nil {
		return "", fmt.Errorf("writing the network policy: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("writing the network policy: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("writing the network policy: %w", err)
	}
	return f.Name(), nil
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
