// Alias tests: project-configured shorthands dispatch as if typed, builtin
// commands win with a warning, chains stop at one hop, and an unknown word
// loads configuration exactly the way every command does.
package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// aliasTOML is the persistent configuration with two aliases: ls runs a
// guest command, ll goes through ls (one alias hop), and exec shadows a
// builtin to exercise the builtin-wins warning.
const aliasTOML = harness.PersistentTOML + `
[aliases]
ls = "exec -- ls"
ll = "ls"
exec = "status"
`

// TestAliasDispatchesAsIfTyped checks that `sbx ls -la` reaches the backend
// as `sbx exec -- ls -la`, with the guest's exit status and no shadowing
// warning: the expansion's `exec` was chosen by an alias, not typed.
func TestAliasDispatchesAsIfTyped(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, aliasTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"ls", "-la"}, nil)
	if code != 0 {
		t.Fatalf("alias exit code = %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "guest-stdout") {
		t.Errorf("guest stdout did not reach sbx stdout: %q", stdout)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("an expansion's builtin name must not warn about shadowing:\n%s", stderr)
	}

	var execCall *testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" {
			call := c
			execCall = &call
		}
	}
	if execCall == nil {
		t.Fatalf("no exec call recorded:\n%s", harness.CallDump(fake.Calls()))
	}
	want := []string{"exec", id, "--workdir", "/workspace", "--stream", "--", "ls", "-la"}
	if !harness.Equal(execCall.Args, want) {
		t.Errorf("exec argv mismatch:\n got: %q\nwant: %q", execCall.Args, want)
	}
}

// TestAliasThroughOneAlias checks that an alias may expand through one
// other alias: ll → ls → exec.
func TestAliasThroughOneAlias(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, aliasTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	code, _, stderr := harness.SbxRun(t, worktree, []string{"ll"}, nil)
	if code != 0 {
		t.Fatalf("alias exit code = %d, stderr:\n%s", code, stderr)
	}

	var execCall *testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" {
			call := c
			execCall = &call
		}
	}
	if execCall == nil {
		t.Fatalf("no exec call recorded:\n%s", harness.CallDump(fake.Calls()))
	}
	want := []string{"exec", id, "--workdir", "/workspace", "--stream", "--", "ls"}
	if !harness.Equal(execCall.Args, want) {
		t.Errorf("exec argv mismatch:\n got: %q\nwant: %q", execCall.Args, want)
	}
}

// TestAliasChainTooDeep checks that a self-referential alias fails fast
// with a usage error instead of recursing forever.
func TestAliasChainTooDeep(t *testing.T) {
	worktree, _ := harness.PersistentFixture(t, harness.PersistentTOML+"\n[aliases]\na = \"a\"\n")

	code, _, stderr := harness.SbxRun(t, worktree, []string{"a"}, nil)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d, stderr:\n%s", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "alias chain too deep") {
		t.Errorf("stderr = %q, want it to report the chain depth", stderr)
	}
}

// TestShadowedAliasWarnsAndBuiltinWins checks that invoking a name that is
// both a builtin and an alias runs the builtin, warns once on stderr, and
// never consults the alias.
func TestShadowedAliasWarnsAndBuiltinWins(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, aliasTOML)

	code, _, stderr := harness.SbxRun(t, worktree, []string{"exec", "--", "echo", "hello"}, nil)
	if code != 0 {
		t.Fatalf("exec exit code = %d, stderr:\n%s", code, stderr)
	}
	if want := `warning: "exec" is also declared in [aliases]; the builtin command runs`; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want the shadowing warning %q", stderr, want)
	}
	var execCall *testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" {
			call := c
			execCall = &call
		}
	}
	if execCall == nil {
		t.Fatalf("no exec call recorded:\n%s", harness.CallDump(fake.Calls()))
	}
	if n := strings.Count(stderr, "warning:"); n != 1 {
		t.Errorf("shadowing warning printed %d times, want once:\n%s", n, stderr)
	}
}

// TestUnknownCommandWithoutAliasUnchanged pins today's error for an unknown
// word when configuration is valid and declares no such alias.
func TestUnknownCommandWithoutAliasUnchanged(t *testing.T) {
	worktree, _ := harness.PersistentFixture(t, harness.PersistentTOML)

	code, _, stderr := harness.SbxRun(t, worktree, []string{"frobnicate"}, nil)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Errorf("stderr = %q, want the unknown-command error", stderr)
	}
}

// TestUnknownCommandWithBrokenConfigReportsLoadError checks that an unknown
// word loads configuration the way every command does: an invalid sbx.toml
// reports the load error instead of "unknown command".
func TestUnknownCommandWithBrokenConfigReportsLoadError(t *testing.T) {
	worktree, _ := harness.PersistentFixture(t, "image = \"alpine:3.20\"\nno_such_field = 1\n")

	code, _, stderr := harness.SbxRun(t, worktree, []string{"frobnicate"}, nil)
	if code != exitFailure {
		t.Errorf("exit code = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "sbx.toml") {
		t.Errorf("stderr = %q, want it to name sbx.toml", stderr)
	}
	if strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q, want the config error, not unknown command", stderr)
	}
}

// TestAliasesNeverDrift checks the standing agreed for aliases: declaring
// or editing them after creation is not Creation drift.
func TestAliasesNeverDrift(t *testing.T) {
	worktree, _ := harness.PersistentFixture(t, harness.PersistentTOML)
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	edited := harness.PersistentTOML + "\n[aliases]\nls = \"exec -- ls\"\n"
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	if !strings.Contains(stdout, "drift: none") {
		t.Errorf("adding aliases caused Creation drift:\n%s", stdout)
	}
}

// The usage-error and failure exit codes, pinned from the outside: the
// contract is the numbers themselves, not the identifiers.
const (
	exitUsage   = 2
	exitFailure = 1
)
