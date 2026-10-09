// The bootstrap completion marker's persistence-failure path, as a
// white-box test: it swaps the save function variable, which only a
// package-internal test can reach, because no filesystem state fails a
// marker save without also failing its load (root ignores permission
// bits, and the load path only recognizes a clean no-marker state).
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// bootstrapTOML declares a Bootstrap definition.
const bootstrapTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[bootstrap]
run = "echo bootstrapped > /workspace/.bootstrapped"

[network]
policy = "public"
`

// fixtureBootstrapRepo builds a seeded repository with a linked worktree
// holding the given configuration. A local copy of the harness fixture: a
// package-internal test cannot import the harness package, which imports
// this one.
func fixtureBootstrapRepo(t *testing.T, toml string) string {
	t.Helper()
	repo := t.TempDir()
	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	gitCmd("init", "-b", "main")
	gitCmd("config", "user.name", "sbx test")
	gitCmd("config", "user.email", "sbx@example.com")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", "seed.txt")
	gitCmd("commit", "-m", "seed")
	worktree := filepath.Join(t.TempDir(), "wt1")
	gitCmd("worktree", "add", worktree, "-b", "feature")
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// runSbx drives sbx inside the worktree.
func runSbx(t *testing.T, dir string, args []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	code := Run(context.Background(), args, nil, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// execCalls returns the recorded exec invocations.
func execCalls(fake testsupport.Log) []testsupport.Call {
	var out []testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" {
			out = append(out, c)
		}
	}
	return out
}

// TestUpMarkerPersistenceFailureStaysIncomplete checks that a bootstrap
// whose completion marker cannot be persisted leaves the sandbox
// incomplete: the next up stays incomplete, neither --allow-stale nor a
// read-only command marks it complete, and --retry-bootstrap completes it
// once persistence works again.
func TestUpMarkerPersistenceFailureStaysIncomplete(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree := fixtureBootstrapRepo(t, bootstrapTOML)
	info, err := gitx.Discover(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.Derive(info.CommonDir, info.WorktreeRoot).Sandbox

	// The guest code succeeds; persisting the marker fails.
	realSave := saveBootstrapMarker
	saveBootstrapMarker = func(string, state.BootstrapMarker) error {
		return errors.New("simulated marker-persistence failure")
	}
	t.Cleanup(func() { saveBootstrapMarker = realSave })

	code, _, stderr := runSbx(t, worktree, []string{"up"})
	if code == 0 {
		t.Fatalf("up succeeded although the completion marker could not be persisted (stderr: %s)", stderr)
	}
	for _, want := range []string{"bootstrap succeeded", "recording its completion failed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Fatalf("the bootstrap did not run exactly once: %d exec calls", len(got))
	}

	// The next up remains incomplete and does not rerun Bootstrap.
	code, _, stderr = runSbx(t, worktree, []string{"up"})
	if code == 0 || !strings.Contains(stderr, "incomplete") {
		t.Errorf("the next up did not stay incomplete: code=%d\n%s", code, stderr)
	}
	code, _, stderr = runSbx(t, worktree, []string{"up", "--allow-stale"})
	if code == 0 || !strings.Contains(stderr, "incomplete") {
		t.Errorf("--allow-stale did not stay incomplete: code=%d\n%s", code, stderr)
	}
	code, stdout, stderr := runSbx(t, worktree, []string{"status"})
	if code != 0 || !strings.Contains(stdout, "bootstrap: incomplete") {
		t.Errorf("status did not report the incomplete bootstrap: code=%d\n%s\n%s", code, stdout, stderr)
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Errorf("bootstrap was retried by a plain up, --allow-stale, or a read-only command: %d exec calls", len(got))
	}
	if _, err := state.LoadBootstrapMarker(id); !errors.Is(err, state.ErrNoBootstrapMarker) {
		t.Errorf("a marker exists despite the failed persistence: %v", err)
	}

	// Once persistence works again, the explicit retry completes it.
	saveBootstrapMarker = realSave
	if code, _, stderr := runSbx(t, worktree, []string{"up", "--retry-bootstrap"}); code != 0 {
		t.Fatalf("--retry-bootstrap failed: %s", stderr)
	}
	if _, err := state.LoadBootstrapMarker(id); err != nil {
		t.Errorf("no marker after the retry: %v", err)
	}
}
