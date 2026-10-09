// Repository fixtures: a once-per-process seed repository that every
// fixture copies, and the constants the command tests share.
package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seedTemplateDir is the once-per-process seeded repository that fixture
// repos copy from. Built lazily by SeedRepo and removed by Main.
var (
	seedTemplateOnce sync.Once
	seedTemplateDir  string
)

// tmpBase is the process's temp root, captured at init — before any test's
// t.Setenv("TMPDIR") — because the template must outlive each test's
// private temp directory and must never appear inside one of them: build
// tests assert their private TMPDIR holds nothing but the sbx artifacts.
var tmpBase = os.TempDir()

// SeedRepo returns a fresh copy of a seeded repository, ready for a
// worktree. Building a repository per fixture the direct way — init, two
// configs, add, commit — costs six git subprocesses per test, and the
// command suites used to run nearly two thousand of them, which macOS
// prices at an order of magnitude above Linux. A plain filesystem copy of
// a repository is a valid repository, so the seed is built once per
// process and copied instead; only the worktree add still runs git,
// because worktree metadata bakes in absolute paths.
func SeedRepo(t *testing.T) string {
	t.Helper()
	seedTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp(tmpBase, "sbx-seed-template")
		if err != nil {
			t.Fatal(err)
		}
		seedTemplateDir = dir
		Git(t, dir, "init", "-b", "main")
		Git(t, dir, "config", "user.name", "sbx test")
		Git(t, dir, "config", "user.email", "sbx@example.com")
		if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		Git(t, dir, "add", "seed.txt")
		Git(t, dir, "commit", "-m", "seed")
	})
	repo := t.TempDir()
	if err := os.CopyFS(repo, os.DirFS(seedTemplateDir)); err != nil {
		t.Fatalf("copying the seed repository: %v", err)
	}
	return repo
}

// FixtureRepo creates a temporary Git repository with a linked worktree
// holding an sbx.toml, and returns the worktree path and the repository
// path.
func FixtureRepo(t *testing.T, toml string) (worktree, repo string) {
	t.Helper()
	repo = SeedRepo(t)

	worktree = filepath.Join(t.TempDir(), "wt1")
	Git(t, repo, "worktree", "add", worktree, "-b", "feature")
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	return Resolve(t, worktree), Resolve(t, repo)
}

// Resolve returns the absolute, symlink-free form of path — the same form
// gitx.Discover is required to produce, so argv expectations built from a
// fixture's paths match what sbx records. On macOS the temporary directory
// itself sits behind the /private symlink, so raw t.TempDir paths never
// match directly.
func Resolve(t *testing.T, path string) string {
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

// Git runs one git command in dir, failing the test on any error, with the
// system git configuration bypassed for hermeticity.
func Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// ValidTOML is a minimal valid configuration.
const ValidTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"
`

// PersistentTOML is a valid configuration for the persistent-sandbox tests.
const PersistentTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"
`

// DisposableTOML is a valid configuration for the disposable-run tests.
const DisposableTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"
`
