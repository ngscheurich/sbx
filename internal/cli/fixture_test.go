// Fixture tests: the acceptance fixtures are copied into temporary Git
// repositories and exercised through the real CLI paths, keeping the
// fixtures in sync with the automated tests.
package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// copyFixture copies a fixture directory's files into dst, preserving
// relative paths, and returns the list of files copied.
func copyFixture(t *testing.T, fixture, dst string) []string {
	t.Helper()
	var copied []string
	err := filepath.Walk(fixture, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixture, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := copyFile(path, target, info.Mode()); err != nil {
			return err
		}
		copied = append(copied, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", fixture, err)
	}
	return copied
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// fixtureRepoFrom copies a fixture into a temporary Git repository as a
// linked worktree and returns the worktree path.
func fixtureRepoFrom(t *testing.T, fixture string) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "sbx test")
	git(t, repo, "config", "user.email", "sbx@example.com")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "seed.txt")
	git(t, repo, "commit", "-m", "seed")
	worktree := filepath.Join(t.TempDir(), "wt1")
	git(t, repo, "worktree", "add", worktree, "-b", "feature")
	copyFixture(t, fixture, worktree)
	return worktree
}

// fixtureRestrictedDir is the restricted CLI fixture's path relative to this
// package.
const fixtureRestrictedDir = "../../fixtures/restricted-cli"

// TestFixtureRestrictedFailsClosedVerbatim copies the complete fixture and
// checks that `sbx run` refuses it before any backend call: its Project
// volumes have no compatibility checks yet, so the preflight fails closed.
// Fail-closed is the contract; which gate fires first may change as those
// features land.
func TestFixtureRestrictedFailsClosedVerbatim(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("SBX_FIXTURE_TOKEN", "throwaway-fixture-token")
	worktree := fixtureRepoFrom(t, fixtureRestrictedDir)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "go", "version"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded on the verbatim fixture, which declares untranslated settings")
	}
	if got := fake.Calls(); len(got) != 0 {
		t.Errorf("msb was called before rejection: %v", got)
	}
}

// TestFixtureRestrictedBuildVerbatim copies the complete fixture and checks
// that `sbx build` builds and imports its image: the build needs neither
// the Project volumes' pending compatibility checks nor the secret's host
// variable, so the verbatim fixture builds as-is.
func TestFixtureRestrictedBuildVerbatim(t *testing.T) {
	msbLog := testsupport.FakeMSB(t)
	dockerLog := testsupport.FakeDocker(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("FAKE_MSB_LOAD_REQUIRES", dockerLog.SavedMarker())
	worktree := fixtureRepoFrom(t, fixtureRestrictedDir)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"build"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("build failed on the verbatim fixture: %s", stderr.String())
	}

	dockerCalls := dockerLog.Calls()
	if len(dockerCalls) != 2 {
		t.Fatalf("fake docker saw %d calls:\n%s", len(dockerCalls), dockerCallDump(dockerCalls))
	}
	wantBuild := []string{"build",
		"--platform", "linux/" + runtime.GOARCH,
		"--file", filepath.Join(worktree, "Dockerfile"),
		"--tag", "sbx-restricted-cli:latest",
		worktree,
	}
	if got := dockerCalls[0].Args; !equal(got, wantBuild) {
		t.Errorf("docker build argv mismatch:\n got: %q\nwant: %q", got, wantBuild)
	}
	archive := archiveFromSaveCall(t, dockerCalls[1])

	msbCalls := msbLog.Calls()
	if len(msbCalls) != 2 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(msbCalls), callDump(msbCalls))
	}
	if got := msbCalls[1].Args; !equal(got, []string{"load", "--input", archive}) {
		t.Errorf("msb load argv mismatch:\n got: %q\nwant the exported archive", got)
	}
}

// TestFixtureRestrictedPlanTranslation checks the Plan for the verbatim
// fixture: both binds, both Project volumes with their namespaced names,
// the allowlist with DNS, and the redacted secret map.
func TestFixtureRestrictedPlanTranslation(t *testing.T) {
	worktree := fixtureRepoFrom(t, fixtureRestrictedDir)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr.String())
	}
	rendered := stdout.String()
	for _, want := range []string{
		filepath.Join(worktree, "host-notes.txt") + " -> /mnt/host-notes.txt (read-only)",
		filepath.Join(worktree, "host-state") + " -> /mnt/host-state",
		"--mount-named",
		"-go_build:/root/.cache/go-build",
		"-go_mod:/go/pkg/mod",
		"allow@proxy.golang.org",
		"allow@sum.golang.org",
		"allow@example.com",
		"--dns-nameserver 1.1.1.1",
		"TEST_TOKEN: host variable SBX_FIXTURE_TOKEN, sent only to example.com",
		`value: "${SBX_FIXTURE_TOKEN}"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
}

// TestFixtureRestrictedRunTranslation checks the full disposable-run argv
// for the fixture's executable configuration: resources, both binds, the
// allowlist with DNS and TLS interception, and the secret map — with the
// guest shell used when no command is given.
func TestFixtureRestrictedRunTranslation(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("SBX_FIXTURE_TOKEN", "throwaway-fixture-token")
	worktree := fixtureRepoFrom(t, fixtureRestrictedDir)
	// The executable variant is the fixture minus the Project volumes,
	// whose compatibility checks do not exist yet; derived from the fixture
	// file so the two cannot drift. [build] stays: `sbx run` inspects the
	// image and runs without building anything.
	variant := testsupport.FixtureTOML(t,
		filepath.Join(fixtureRestrictedDir, "sbx.toml"),
		"[volumes.go_mod]", "[volumes.go_build]")
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(variant), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("run failed: %s", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	runCall := calls[2].Args
	want := []string{"run", "sbx-restricted-cli:latest",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktree + ":/workspace",
		"--mount-file", worktree + "/host-notes.txt:/mnt/host-notes.txt:ro",
		"--mount-dir", worktree + "/host-state:/mnt/host-state",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktree,
		"--net-rule", "allow@proxy.golang.org",
		"--net-rule", "allow@sum.golang.org",
		"--net-rule", "allow@example.com",
		"--tls-intercept",
		"--dns-nameserver", "1.1.1.1",
		"--dns-nameserver", "8.8.8.8",
		"--secret-conf", "<path>",
		"--workdir", "/workspace",
		"--no-tty",
		"--", "/bin/bash",
	}
	if !equal(spliceGenerated(runCall), want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", runCall, want)
	}
	conf := fake.SecretConf(t, 2)
	if !strings.Contains(conf, `value: "${SBX_FIXTURE_TOKEN}"`) {
		t.Errorf("secret map is missing the source reference:\n%s", conf)
	}
	if strings.Contains(conf, "throwaway-fixture-token") {
		t.Errorf("secret map contains the host secret value:\n%s", conf)
	}
}
