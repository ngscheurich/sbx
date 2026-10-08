package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// seededListing drives `sbx list` against a seeded fake backend. list
// reads no project configuration, so the test never chdirs into a repo:
// running from the package directory already proves the command works
// outside a Git worktree (ADR-0008). The fake stamps every seeded sandbox
// with the same created_at, and TZ pins the local formatting, so the
// Created column is deterministic.
func seededListing(t *testing.T, seed func(testsupport.Log)) ([]byte, *bytes.Buffer) {
	t.Helper()
	t.Setenv("TZ", "UTC")
	fake := testsupport.FakeMSB(t)
	seed(fake)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"list"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	return stdout.Bytes(), &stderr
}

// TestPlainListGolden pins the table: two owned sandboxes (one running,
// one stopped) sorted by name, and an unowned backend sandbox that must
// never appear.
func TestPlainListGolden(t *testing.T) {
	out, _ := seededListing(t, func(fake testsupport.Log) {
		fake.SeedSandbox(t, "proj-b-feat-x-ab12cd", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
		fake.SeedSandbox(t, "proj-a-main-ef34ab", "debian:12", "stopped", map[string]string{"sbx.managed": "1"})
		fake.SeedSandbox(t, "someone-elses", "alpine:3.20", "running", nil)
	})
	testsupport.Golden(t, "testdata/list.golden", out)
}

// TestPlainListEmptyGolden pins the empty result: no owned sandboxes —
// here only an unowned one the backend holds — is one plain line, exit 0.
func TestPlainListEmptyGolden(t *testing.T) {
	out, _ := seededListing(t, func(fake testsupport.Log) {
		fake.SeedSandbox(t, "someone-elses", "alpine:3.20", "running", nil)
	})
	testsupport.Golden(t, "testdata/list-empty.golden", out)
}

// TestPlainListPlainGolden pins the plain lever end to end: `sbx --plain
// list` keeps every word, drops the border glyphs, and strips the forced
// color too.
func TestPlainListPlainGolden(t *testing.T) {
	t.Setenv("TZ", "UTC")
	t.Setenv("CLICOLOR_FORCE", "1")
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "proj-b-feat-x-ab12cd", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--plain", "list"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if strings.ContainsAny(out, "┌┬┐│├┼┤└┴┘─") || strings.Contains(out, "\x1b[") {
		t.Errorf("--plain list kept decoration:\n%q", out)
	}
	testsupport.Golden(t, "testdata/list-plain.golden", stdout.Bytes())
}

// TestPlainListAlias pins the documented alias and the short form:
// --no-color and -p are the same lever as --plain, byte-for-byte.
func TestPlainListAlias(t *testing.T) {
	t.Setenv("TZ", "UTC")
	t.Setenv("CLICOLOR_FORCE", "1")
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "proj-b-feat-x-ab12cd", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	want, err := os.ReadFile("testdata/list-plain.golden")
	for _, flag := range []string{"--no-color", "-p"} {
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), []string{flag, "list"}, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit code = %d (stderr: %s)", flag, code, stderr.String())
		}
		if err != nil || !bytes.Equal(stdout.Bytes(), want) {
			t.Errorf("%s output differs from --plain (err: %v)\ngot:\n%swant:\n%s", flag, err, stdout.String(), want)
		}
	}
}

// A --plain token after the command name belongs to that command, which
// rejects it: the global lever leads the command line.
func TestPlainAfterCommandIsRejected(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	_ = fake
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"list", "--plain"}, nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
}

// A failed backend listing fails closed: exit 1 and one error line,
// never an empty table.
func TestListFailsClosedOnListingFailure(t *testing.T) {
	t.Setenv("FAKE_MSB_LS_FAIL", "1")
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "proj-a-main-ef34ab", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"list"}, nil, &stdout, &stderr); code != exitFailure {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitFailure, stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "sbx:") {
		t.Fatalf("stderr = %q, want an `sbx: …` error line", stderr.String())
	}
}

// A failed inspection fails closed the same way, even when every other
// sandbox inspects cleanly.
func TestListFailsClosedOnInspectFailure(t *testing.T) {
	t.Setenv("FAKE_MSB_INSPECT_FAIL", "1")
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "proj-a-main-ef34ab", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"list"}, nil, &stdout, &stderr); code != exitFailure {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitFailure, stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "sbx:") {
		t.Fatalf("stderr = %q, want an `sbx: …` error line", stderr.String())
	}
}

// list takes no arguments or flags, the way plan does.
func TestListRejectsArguments(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	_ = fake
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"list", "--all"}, nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
}
