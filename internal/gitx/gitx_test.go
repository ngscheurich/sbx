package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a temporary Git repository with one commit and returns its
// path. Git's global configuration is bypassed for hermetic tests.
func initRepo(t *testing.T, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		return mustGit(t, dir, args...)
	}
	run("init", "-b", "main")
	run("config", "user.name", "sbx test")
	run("config", "user.email", "sbx@example.com")
	if len(extra) > 0 {
		run(extra...)
	}
	os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644)
	run("add", "seed.txt")
	run("commit", "-m", "seed")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func addWorktree(t *testing.T, repo, path string) {
	t.Helper()
	mustGit(t, repo, "worktree", "add", path, "-b", "feature")
}

func TestDiscoverInWorktreeRoot(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt1")
	addWorktree(t, repo, wt)

	info, err := Discover(context.Background(), wt)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.WorktreeRoot != resolve(t, wt) {
		t.Errorf("WorktreeRoot = %q, want symlink-free %q", info.WorktreeRoot, resolve(t, wt))
	}
	if info.CommonDir != resolve(t, filepath.Join(repo, ".git")) {
		t.Errorf("CommonDir = %q, want symlink-free %q", info.CommonDir, resolve(t, filepath.Join(repo, ".git")))
	}
}

func TestDiscoverFromNestedDirectory(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt1")
	addWorktree(t, repo, wt)
	nested := filepath.Join(wt, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := Discover(context.Background(), nested)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.WorktreeRoot != resolve(t, wt) {
		t.Errorf("WorktreeRoot = %q, want the worktree root %q, not the nested directory", info.WorktreeRoot, resolve(t, wt))
	}
}

func TestDiscoverSymlinkFreePaths(t *testing.T) {
	repo := initRepo(t)
	realDir := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(realDir, "wt1")
	addWorktree(t, repo, wt)

	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	info, err := Discover(context.Background(), filepath.Join(link, "wt1"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if strings.Contains(info.WorktreeRoot, linkParent) {
		t.Errorf("WorktreeRoot = %q, want the symlink-free path %q", info.WorktreeRoot, wt)
	}
}

func TestDiscoverOutsideGit(t *testing.T) {
	_, err := Discover(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("Discover succeeded outside a Git repository")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("error = %q, want it to mention the worktree requirement", err.Error())
	}
}

func TestDiscoverInsideDotGitDirectory(t *testing.T) {
	repo := initRepo(t)
	_, err := Discover(context.Background(), filepath.Join(repo, ".git"))
	if err == nil {
		t.Fatal("Discover succeeded inside the .git directory")
	}
}

func TestDiscoverBareRepoNamedDotGit(t *testing.T) {
	// A bare clone at /src/app.git yields the project basename "app".
	bare := filepath.Join(t.TempDir(), "app.git")
	repo := initRepo(t)
	mustGit(t, repo, "clone", "--bare", repo, bare)
	wt := filepath.Join(t.TempDir(), "wt1")
	mustGit(t, bare, "worktree", "add", wt, "-b", "feature")

	info, err := Discover(context.Background(), wt)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.CommonDir != resolve(t, bare) {
		t.Errorf("CommonDir = %q, want symlink-free %q", info.CommonDir, resolve(t, bare))
	}
}

// resolve returns the absolute, symlink-free form of path — the same form
// Discover is required to produce. On macOS the temporary directory itself
// sits behind the /private symlink, so raw t.TempDir paths never match
// directly.
func resolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
