package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/identity"
)

// TestMain delegates to the shared command-test harness, which pins the
// process's local zone to UTC and cleans up the seed repository template.
func TestMain(m *testing.M) { harness.Main(m) }

const validTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"
`

func TestHelpListsOnlyV1Commands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"--help"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	out := stdout.String()
	for _, cmd := range []string{"plan", "build", "up", "exec", "run", "status", "logs", "stop", "rm", "port"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help does not mention v1 command %q:\n%s", cmd, out)
		}
	}
	for _, banned := range []string{"init", "task", "volume rm"} {
		if strings.Contains(out, "sbx "+banned) {
			t.Errorf("help mentions non-v1 command %q:\n%s", banned, out)
		}
	}
}

func TestNoArgsShowsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), nil, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Errorf("bare invocation did not show usage:\n%s", stdout.String())
	}
}

// TestPortWithoutSubcommandShowsHelp checks that bare `sbx port` succeeds
// by printing the port group's usage, keeping the top-level help's
// "Manage sandbox ports" promise truthful.
func TestPortWithoutSubcommandShowsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"port"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"sbx port <subcommand>", "prune"} {
		if !strings.Contains(out, want) {
			t.Errorf("port help does not mention %q:\n%s", want, out)
		}
	}
}

// TestPortUnknownSubcommandFails checks that a bad port subcommand is a
// usage error pointing at the group's own help.
func TestPortUnknownSubcommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"port", "list"}, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	for _, want := range []string{"list", "sbx port"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr does not mention %q:\n%s", want, stderr.String())
		}
	}
}

func TestPlanRejectsUnexpectedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"plan", "--verbose"}, nil, &stdout, &stderr); code == 0 {
		t.Fatal("plan accepted an unsupported flag")
	}
	if !strings.Contains(stderr.String(), "--verbose") {
		t.Errorf("stderr does not name the offending argument:\n%s", stderr.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	worktree, _ := harness.FixtureRepo(t, validTOML)
	var stdout, stderr bytes.Buffer
	if code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"deploy"}, nil, &stdout, &stderr)
	}); code == 0 {
		t.Fatal("unknown command succeeded")
	}
	if !strings.Contains(stderr.String(), "deploy") {
		t.Errorf("stderr does not name the unknown command:\n%s", stderr.String())
	}
}

func TestPlanFromNestedDirectory(t *testing.T) {
	worktree, _ := harness.FixtureRepo(t, validTOML)
	nested := filepath.Join(worktree, "cmd", "server")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, nested, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	info, err := gitx.Discover(context.Background(), nested)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := identity.Derive(info.CommonDir, info.WorktreeRoot).Sandbox
	if !strings.Contains(out, "sandbox identity: "+want) {
		t.Errorf("plan output lacks the sandbox identity %q:\n%s", want, out)
	}
	if !strings.Contains(out, "alpine:3.20") {
		t.Errorf("plan output lacks the configured image:\n%s", out)
	}
}

func TestPlanChangesNothing(t *testing.T) {
	worktree, repo := harness.FixtureRepo(t, validTOML)
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)

	beforeWorktree := harness.DirSnapshot(t, worktree)
	beforeRepo := harness.DirSnapshot(t, repo)

	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}

	if after := harness.DirSnapshot(t, worktree); after != beforeWorktree {
		t.Errorf("plan changed the worktree:\nbefore:\n%s\nafter:\n%s", beforeWorktree, after)
	}
	if after := harness.DirSnapshot(t, repo); after != beforeRepo {
		t.Errorf("plan changed the repository:\nbefore:\n%s\nafter:\n%s", beforeRepo, after)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "sbx")); !os.IsNotExist(err) {
		t.Error("plan created sbx state under XDG_STATE_HOME")
	}
}

func TestPlanFailsOutsideWorktree(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, t.TempDir(), func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("plan succeeded outside a Git worktree")
	}
	if !strings.Contains(stderr.String(), "worktree") {
		t.Errorf("stderr does not explain the worktree requirement:\n%s", stderr.String())
	}
}

func TestPlanFailsWithoutConfig(t *testing.T) {
	repo := t.TempDir()
	harness.Git(t, repo, "init", "-b", "main")
	harness.Git(t, repo, "config", "user.name", "sbx test")
	harness.Git(t, repo, "config", "user.email", "sbx@example.com")
	os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644)
	harness.Git(t, repo, "add", "seed.txt")
	harness.Git(t, repo, "commit", "-m", "seed")

	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, repo, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("plan succeeded without sbx.toml")
	}
	if !strings.Contains(stderr.String(), "sbx.toml") {
		t.Errorf("stderr does not mention sbx.toml:\n%s", stderr.String())
	}
}

func TestPlanFailsOnInvalidConfig(t *testing.T) {
	worktree, _ := harness.FixtureRepo(t, `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "bridged"
`)

	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("plan succeeded with an invalid network mode")
	}
	if !strings.Contains(stderr.String(), "policy") {
		t.Errorf("stderr does not mention the invalid field:\n%s", stderr.String())
	}
}

func TestIdentityStableAcrossConfigEditsAndBranchSwitches(t *testing.T) {
	worktree, _ := harness.FixtureRepo(t, validTOML)

	first := harness.PlanIdentity(t, worktree)

	// Edit the configuration.
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(`
image = "debian:12"
cpus = 4
memory = "8G"

[network]
policy = "none"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if second := harness.PlanIdentity(t, worktree); second != first {
		t.Errorf("identity changed after a config edit: %q -> %q", first, second)
	}

	// Switch to a different branch with different configuration.
	harness.Git(t, worktree, "add", "sbx.toml")
	harness.Git(t, worktree, "config", "user.name", "sbx test")
	harness.Git(t, worktree, "config", "user.email", "sbx@example.com")
	harness.Git(t, worktree, "commit", "-m", "config")
	harness.Git(t, worktree, "switch", "-c", "other")
	if third := harness.PlanIdentity(t, worktree); third != first {
		t.Errorf("identity changed after a branch switch: %q -> %q", first, third)
	}
}

func TestDistinctIdentitiesForSameNamedWorktrees(t *testing.T) {
	// Two clones at different paths, each with a worktree named "shared-wt".
	parent1 := filepath.Join(t.TempDir(), "clone-a")
	parent2 := filepath.Join(t.TempDir(), "clone-b")
	for _, p := range []string{parent1, parent2} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w1 := filepath.Join(parent1, "shared-wt")
	w2 := filepath.Join(parent2, "shared-wt")
	if w1 == w2 {
		t.Fatal("test setup produced identical worktree paths")
	}
	seedWorktree(t, parent1, w1)
	seedWorktree(t, parent2, w2)

	if harness.PlanIdentity(t, w1) == harness.PlanIdentity(t, w2) {
		t.Error("same-named worktrees at different paths share a Sandbox identity")
	}
}

// seedWorktree plants a seeded repository at repo and adds a worktree at
// path, so that two worktrees can carry the same basename.
func seedWorktree(t *testing.T, repo, path string) {
	t.Helper()
	copied := harness.SeedRepo(t)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(copied, repo); err != nil {
		t.Fatal(err)
	}
	harness.Git(t, repo, "worktree", "add", path, "-b", "feature")
	if err := os.WriteFile(filepath.Join(path, "sbx.toml"), []byte(validTOML), 0o644); err != nil {
		t.Fatal(err)
	}
}
