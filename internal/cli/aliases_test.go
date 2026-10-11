// Alias tests: project-configured shorthands dispatch as if typed, builtin
// commands win with a warning, chains stop at one hop, and an unknown word
// loads configuration exactly the way every command does.
package cli_test

import (
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

func TestAliasMatchesDirectInvocation(t *testing.T) {
	config := harness.PersistentTOML + "\n[aliases]\nls = 'exec -- ls'\nll = 'ls'\n"
	for _, args := range [][]string{{"ls"}, {"ls", "-la"}, {"ll", "-la", "", "two words"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			worktree, _ := harness.PersistentFixture(t, config)
			t.Setenv("FAKE_MSB_EXIT", "7")
			direct := append([]string{"exec", "--", "ls"}, args[1:]...)
			directCode, directOut, directErr := harness.SbxRun(t, worktree, direct, nil)
			code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
			if directCode != 7 || code != directCode || stdout != directOut || stderr != directErr {
				t.Fatalf("alias=(%d, %q, %q), direct=(%d, %q, %q)", code, stdout, stderr, directCode, directOut, directErr)
			}
		})
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

func TestAliasesHonorGlobalFlagPositions(t *testing.T) {
	goldenPath, err := filepath.Abs("testdata/list-plain.golden")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")
	config := harness.PersistentTOML + `
[aliases]
listing = "list"
plain = "--plain listing"
tail = "list --no-color"
`
	for _, args := range [][]string{
		{"listing", "--plain"},
		{"--no-color", "listing", "-p"},
		{"plain"},
		{"tail"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			worktree, fake := harness.PersistentFixture(t, config)
			fake.SeedSandbox(t, "proj-b-feat-x-ab12cd", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
			code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			testsupport.Golden(t, goldenPath, []byte(stdout))
		})
	}
}

func TestAliasChainTooDeep(t *testing.T) {
	cases := []struct {
		name    string
		aliases string
	}{
		{name: "self-reference", aliases: "a = 'a'\n"},
		{name: "cycle", aliases: "a = 'b'\nb = 'a'\n"},
		{name: "three aliases", aliases: "a = 'b'\nb = 'c'\nc = 'exec -- true'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worktree, fake := harness.PersistentFixture(t, harness.PersistentTOML+"\n[aliases]\n"+tc.aliases)
			code, stdout, stderr := harness.SbxRun(t, worktree, []string{"a"}, nil)
			if code != exitUsage || stdout != "" || !strings.Contains(stderr, "alias chain too deep") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Errorf("backend called before rejecting the alias chain:\n%s", harness.CallDump(calls))
			}
		})
	}
}

func TestShadowedAliasWarnsAndBuiltinWins(t *testing.T) {
	cases := []struct {
		name       string
		forceColor string
		noColor    string
		plain      bool
		styled     bool
	}{
		{name: "pipe", forceColor: "0"},
		{name: "NO_COLOR", forceColor: "1", noColor: "1"},
		{name: "plain flag", forceColor: "1", plain: true},
		{name: "styled", forceColor: "1", styled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worktree, fake := harness.PersistentFixture(t, aliasTOML)
			t.Setenv("CLICOLOR_FORCE", tc.forceColor)
			t.Setenv("NO_COLOR", tc.noColor)
			args := []string{"exec", "--", "echo", "hello"}
			if tc.plain {
				args = append([]string{"--plain"}, args...)
			}
			code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
			if code != 0 || stdout != "guest-stdout\n" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			warning := `"exec" is also declared in [aliases]; the builtin command runs`
			if !strings.Contains(stderr, warning) || strings.Count(stderr, "warning:") != 1 {
				t.Errorf("expected one shadowing warning, got %q", stderr)
			}
			if got := strings.ContainsRune(stderr, '\x1b'); got != tc.styled {
				t.Errorf("ANSI=%t, want %t: %q", got, tc.styled, stderr)
			}
			if !tc.styled && stderr != "warning: "+warning+"\nguest-stderr\n" {
				t.Errorf("Plain warning or guest stderr changed: %q", stderr)
			}
			found := false
			for _, call := range fake.Calls() {
				if call.Args[0] == "exec" {
					found = true
				}
			}
			if !found {
				t.Fatal("the builtin exec did not reach the backend")
			}
			code, _, stderr = harness.SbxRun(t, worktree, []string{"status"}, nil)
			if code != 0 || stderr != "" {
				t.Errorf("an unrelated command warned: code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestAliasesOnlyResolveFirstWord(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, aliasTOML)
	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"port", "ls"}, nil)
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, `unknown port command "ls"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a subcommand alias reached the backend:\n%s", harness.CallDump(calls))
	}
}

func TestConfigFreeCommandsIgnoreShadowAliases(t *testing.T) {
	shadowed := harness.PersistentTOML + `
[aliases]
help = "exec -- wrong"
version = "exec -- wrong"
list = "exec -- wrong"
exec = "run -- wrong"
port = "exec -- wrong"
`
	for _, args := range [][]string{
		{"help"}, {"--help"}, {"help", "exec"}, {"exec", "--help"},
		{"version"}, {"--version"}, {"-V"}, {"list"},
		{"port", "--help"}, {"port", "prune", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			worktree, _ := harness.PersistentFixture(t, harness.PersistentTOML)
			code, want, stderr := harness.SbxRun(t, worktree, args, nil)
			if code != 0 || stderr != "" || want == "" {
				t.Fatalf("baseline: code=%d stdout=%q stderr=%q", code, want, stderr)
			}
			for _, config := range []string{shadowed, "no_such_field = true\n"} {
				harness.WriteFile(t, filepath.Join(worktree, "sbx.toml"), config)
				code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
				if code != 0 || stdout != want || stderr != "" {
					t.Errorf("config-free output changed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
			}
		})
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

func TestUnknownCommandOutsideWorktreeMatchesPlanError(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	outside := t.TempDir()
	planCode, planOut, planErr := harness.SbxRun(t, outside, []string{"plan"}, nil)
	code, stdout, stderr := harness.SbxRun(t, outside, []string{"frobnicate"}, nil)
	if planCode != exitFailure || code != planCode || stdout != "" || planOut != "" || stderr != planErr {
		t.Fatalf("unknown=(%d, %q, %q), plan=(%d, %q, %q)", code, stdout, stderr, planCode, planOut, planErr)
	}
	if !strings.HasPrefix(stderr, "sbx:") || strings.Contains(stderr, "unknown command") {
		t.Errorf("expected a discovery error, got %q", stderr)
	}
}

func TestAliasesNeverDrift(t *testing.T) {
	worktree, _ := harness.PersistentFixture(t, harness.PersistentTOML)
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	baseline := map[string]string{}
	for _, command := range []string{"status", "plan"} {
		code, stdout, stderr := harness.SbxRun(t, worktree, []string{command, "--json"}, nil)
		if code != 0 || stderr != "" {
			t.Fatalf("%s baseline: code=%d stderr=%q", command, code, stderr)
		}
		baseline[command] = stdout
	}
	for _, aliases := range []string{
		"ls = 'exec -- ls'\n",
		"ls = 'run -- echo changed'\nll = 'ls'\n",
	} {
		harness.WriteFile(t, filepath.Join(worktree, "sbx.toml"), harness.PersistentTOML+"\n[aliases]\n"+aliases)
		for _, command := range []string{"status", "plan"} {
			code, stdout, stderr := harness.SbxRun(t, worktree, []string{command, "--json"}, nil)
			if code != 0 || stderr != "" || stdout != baseline[command] {
				t.Errorf("aliases changed %s identity, translation, or drift: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
			}
		}
	}
}

// The usage-error and failure exit codes, pinned from the outside: the
// contract is the numbers themselves, not the identifiers.
const (
	exitUsage   = 2
	exitFailure = 1
)
