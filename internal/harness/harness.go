// Package harness drives the sbx CLI in-process for the command-layer test
// packages. The tests only use the exported Run entry point, so they split
// across several packages under internal/cli; the helpers they share live
// here rather than being copied per package.
//
// Each test package's TestMain must call Main.
package harness

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// Main pins the process-local state every command test package needs and
// should be called from the package's TestMain. It pins the local zone
// before any test runs — t.Setenv inside a test is too late, because the
// time package reads TZ exactly once, the first time anything formats a
// local time, and macOS can initialize the local zone before TestMain even
// runs. Assigning the Local pointer directly cannot be undone by init
// order; the env var keeps subprocesses (the fakes) consistent with us.
// List goldens pin UTC formatting, which only holds if UTC is the process
// zone throughout. Main also removes the seed repository template on exit.
func Main(m *testing.M) {
	os.Setenv("TZ", "UTC")
	time.Local = time.UTC
	code := m.Run()
	if seedTemplateDir != "" {
		os.RemoveAll(seedTemplateDir)
	}
	os.Exit(code)
}

// Chdir runs fn with the process working directory at dir, restoring the
// previous directory when the test finishes. Only for use when the caller
// has no way to pass the directory in: Run derives everything from the
// working directory.
func Chdir(t *testing.T, dir string, fn func() int) int {
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

// SbxRun drives `sbx` with args inside dir, feeding stdin, and returns the
// exit code with both captured streams.
func SbxRun(t *testing.T, dir string, args []string, stdin io.Reader) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Chdir(t, dir, func() int {
		return cli.Run(context.Background(), args, stdin, &stdout, &stderr)
	})
	return code, stdout.String(), stderr.String()
}

// SbxUp drives `sbx up` inside the worktree.
func SbxUp(t *testing.T, worktree string, flags ...string) (int, string, string) {
	t.Helper()
	return SbxRun(t, worktree, append([]string{"up"}, flags...), nil)
}

// RunFixture drives `sbx run` inside a fresh fixture worktree, feeding the
// guest stdin the run tests expect, and returns the exit code with both
// captured streams and the fake msb's log.
func RunFixture(t *testing.T, toml string, argv ...string) (int, *bytes.Buffer, *bytes.Buffer, testsupport.Log) {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	worktree, _ := FixtureRepo(t, toml)
	var stdout, stderr bytes.Buffer
	code := Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), append([]string{"run"}, argv...), strings.NewReader("guest-input\n"), &stdout, &stderr)
	})
	return code, &stdout, &stderr, fake
}

// PlanIdentity runs sbx plan and extracts the sandbox identity from output.
func PlanIdentity(t *testing.T, dir string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Chdir(t, dir, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
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

// PersistentFixture prepares a fake msb, a private host state directory,
// and a fixture worktree for the persistent-sandbox tests.
func PersistentFixture(t *testing.T, toml string) (string, testsupport.Log) {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := FixtureRepo(t, toml)
	return worktree, fake
}

// SandboxIdentityOf derives the worktree's Sandbox identity the way the CLI
// does, for tests that need the name the backend will see.
func SandboxIdentityOf(t *testing.T, worktree string) string {
	t.Helper()
	info, err := gitx.Discover(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	return identity.Derive(info.CommonDir, info.WorktreeRoot).Sandbox
}

// DirSnapshot renders dir's recursive entry list, one path per line, for
// tests that pin directory contents. The .git directory is skipped.
func DirSnapshot(t *testing.T, dir string) string {
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

// CallDump renders recorded calls for failure messages.
func CallDump(calls []testsupport.Call) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString("  " + strings.Join(c.Args, " ") + "\n")
	}
	return b.String()
}

// Equal reports whether the argument lists match element for element.
func Equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Contains reports whether the argument list includes the exact token.
func Contains(args []string, token string) bool {
	for _, a := range args {
		if a == token {
			return true
		}
	}
	return false
}

// IndexOf returns the index of the first occurrence of token, or -1.
func IndexOf(args []string, token string) int {
	for i, a := range args {
		if a == token {
			return i
		}
	}
	return -1
}

// SpliceGenerated replaces the volatile argument of every generated-file
// flag with a stable placeholder, so expectations can pin the rest of the
// argv.
func SpliceGenerated(args []string) []string {
	for _, flag := range []string{"--secret-conf", "--fs-conf"} {
		i := IndexOf(args, flag)
		if i >= 0 {
			tail := append([]string{}, args[i+2:]...)
			args = append(append(args[:i+1:i+1], "<path>"), tail...)
		}
	}
	return args
}

// WriteFile writes content at path with the default test file mode.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ProjectVolumeName derives the backend volume name for a project volume
// the way the CLI does.
func ProjectVolumeName(t *testing.T, worktree, logical string) string {
	t.Helper()
	info, err := gitx.Discover(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	return identity.VolumeName(info.CommonDir, logical)
}

// FakeDigest is the manifest digest the fake msb reports by default.
const FakeDigest = "sha256:fake"

// CheckScript is a minimal image-check script that succeeds when run.
const CheckScript = "#!/bin/sh\ncommand -v sh >/dev/null\n"

// WriteImageCheckScript writes the check script into the worktree.
func WriteImageCheckScript(t *testing.T, worktree, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, "image-check.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// CheckSandboxCalls splits the fake msb's recorded calls into the image
// check's create, exec, and remove calls.
func CheckSandboxCalls(fake testsupport.Log) (creates, execs, removes []testsupport.Call) {
	for _, c := range fake.Calls() {
		isCheck := false
		for _, a := range c.Args {
			if strings.Contains(a, "-imgchk-") {
				isCheck = true
			}
		}
		if !isCheck {
			continue
		}
		switch c.Args[0] {
		case "create":
			creates = append(creates, c)
		case "exec":
			execs = append(execs, c)
		case "remove":
			removes = append(removes, c)
		}
	}
	return creates, execs, removes
}

// HasSuccess reports whether a matching image-check success is recorded for
// the default fake digest and the given script.
func HasSuccess(t *testing.T, script string) bool {
	t.Helper()
	ok, err := state.HasImageCheckSuccess(FakeDigest, state.ImageCheckScriptHash(script))
	if err != nil {
		t.Fatalf("checking the recorded success: %v", err)
	}
	return ok
}
