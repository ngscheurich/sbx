package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
)

// fixtureRepo creates a temporary Git repository with a linked worktree
// holding an sbx.toml, and returns the worktree path and the repository path.
func fixtureRepo(t *testing.T, toml string) (worktree, repo string) {
	t.Helper()
	repo = t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "sbx test")
	git(t, repo, "config", "user.email", "sbx@example.com")
	os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644)
	git(t, repo, "add", "seed.txt")
	git(t, repo, "commit", "-m", "seed")

	worktree = filepath.Join(t.TempDir(), "wt1")
	git(t, repo, "worktree", "add", worktree, "-b", "feature")
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	return worktree, repo
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

const validTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
egress = "public"
`

func TestHelpListsOnlyV1Commands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--help"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	out := stdout.String()
	for _, cmd := range []string{"plan", "build", "up", "exec", "run", "status", "logs", "stop", "rm", "port prune"} {
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
	if code := Run(context.Background(), nil, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Errorf("bare invocation did not show usage:\n%s", stdout.String())
	}
}

func TestPlanRejectsUnexpectedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"plan", "--verbose"}, nil, &stdout, &stderr); code == 0 {
		t.Fatal("plan accepted an unsupported flag")
	}
	if !strings.Contains(stderr.String(), "--verbose") {
		t.Errorf("stderr does not name the offending argument:\n%s", stderr.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"deploy"}, nil, &stdout, &stderr); code == 0 {
		t.Fatal("unknown command succeeded")
	}
	if !strings.Contains(stderr.String(), "deploy") {
		t.Errorf("stderr does not name the unknown command:\n%s", stderr.String())
	}
}

func TestPlanFromNestedDirectory(t *testing.T) {
	worktree, _ := fixtureRepo(t, validTOML)
	nested := filepath.Join(worktree, "cmd", "server")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := chdir(t, nested, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
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
	worktree, repo := fixtureRepo(t, validTOML)
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)

	beforeWorktree := dirSnapshot(t, worktree)
	beforeRepo := dirSnapshot(t, repo)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}

	if after := dirSnapshot(t, worktree); after != beforeWorktree {
		t.Errorf("plan changed the worktree:\nbefore:\n%s\nafter:\n%s", beforeWorktree, after)
	}
	if after := dirSnapshot(t, repo); after != beforeRepo {
		t.Errorf("plan changed the repository:\nbefore:\n%s\nafter:\n%s", beforeRepo, after)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "sbx")); !os.IsNotExist(err) {
		t.Error("plan created sbx state under XDG_STATE_HOME")
	}
}

func TestPlanFailsOutsideWorktree(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := chdir(t, t.TempDir(), func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
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
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "sbx test")
	git(t, repo, "config", "user.email", "sbx@example.com")
	os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644)
	git(t, repo, "add", "seed.txt")
	git(t, repo, "commit", "-m", "seed")

	var stdout, stderr bytes.Buffer
	code := chdir(t, repo, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("plan succeeded without sbx.toml")
	}
	if !strings.Contains(stderr.String(), "sbx.toml") {
		t.Errorf("stderr does not mention sbx.toml:\n%s", stderr.String())
	}
}

func TestPlanFailsOnInvalidConfig(t *testing.T) {
	worktree, _ := fixtureRepo(t, `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
egress = "bridged"
`)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("plan succeeded with an invalid network mode")
	}
	if !strings.Contains(stderr.String(), "egress") {
		t.Errorf("stderr does not mention the invalid field:\n%s", stderr.String())
	}
}

func TestIdentityStableAcrossConfigEditsAndBranchSwitches(t *testing.T) {
	worktree, _ := fixtureRepo(t, validTOML)

	first := planIdentity(t, worktree)

	// Edit the configuration.
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(`
image = "debian:12"
cpus = 4
memory = "8G"

[network]
egress = "none"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if second := planIdentity(t, worktree); second != first {
		t.Errorf("identity changed after a config edit: %q -> %q", first, second)
	}

	// Switch to a different branch with different configuration.
	git(t, worktree, "add", "sbx.toml")
	git(t, worktree, "config", "user.name", "sbx test")
	git(t, worktree, "config", "user.email", "sbx@example.com")
	git(t, worktree, "commit", "-m", "config")
	git(t, worktree, "switch", "-c", "other")
	if third := planIdentity(t, worktree); third != first {
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

	if planIdentity(t, w1) == planIdentity(t, w2) {
		t.Error("same-named worktrees at different paths share a Sandbox identity")
	}
}

// seedWorktree clones repo into a sibling directory and adds a worktree at
// path so that two worktrees can carry the same basename.
func seedWorktree(t *testing.T, repo, path string) {
	t.Helper()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "sbx test")
	git(t, repo, "config", "user.email", "sbx@example.com")
	os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644)
	git(t, repo, "add", "seed.txt")
	git(t, repo, "commit", "-m", "seed")
	git(t, repo, "worktree", "add", path, "-b", "feature")
	if err := os.WriteFile(filepath.Join(path, "sbx.toml"), []byte(validTOML), 0o644); err != nil {
		t.Fatal(err)
	}
}

// planIdentity runs sbx plan and extracts the sandbox identity from output.
func planIdentity(t *testing.T, dir string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := chdir(t, dir, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("plan failed in %s: %s", dir, stderr.String())
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.Contains(line, "sandbox identity:") {
			return strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	t.Fatalf("plan output has no sandbox identity line:\n%s", stdout.String())
	return ""
}

func dirSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == ".git" && info.IsDir() {
			return filepath.SkipDir
		}
		mode := info.Mode().Type()
		b.WriteString(rel)
		if mode&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			b.WriteString(" -> " + target)
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func chdir(t *testing.T, dir string, fn func() int) int {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	return fn()
}
