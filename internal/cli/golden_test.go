package cli

// Plain-output goldens: what a pipe or NO_COLOR receives is byte-identical
// to sbx's pre-styling output, pinned per surface — help, port help, one
// representative error line, and the status report. The goldens were
// captured from the pre-styling code; the writer seam keeps them true by
// construction. -update rewrites them, safely too: the goldens are always
// captured through the seam, so they can hold only plain bytes.

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

func TestPlainHelpGolden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	testsupport.Golden(t, "testdata/help.golden", stdout.Bytes())
}

func TestPlainPortHelpGolden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"port"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	testsupport.Golden(t, "testdata/port-help.golden", stdout.Bytes())
}

// TestPlainErrorLineGolden pins one representative `sbx: …` error line:
// the styled prefix must strip back to exactly today's bytes.
func TestPlainErrorLineGolden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	Run(context.Background(), []string{"plan", "--verbose"}, nil, &stdout, &stderr)
	testsupport.Golden(t, "testdata/error-line.golden", stderr.Bytes())
}

// seededStatusOutput drives `sbx status` against a seeded fake backend: a
// running, owned sandbox with one published port, no snapshot (drift), and
// a registry with no reservation for the published port. Everything in the
// report is deterministic except the Sandbox identity, which hashes the
// worktree path; the golden pins it as a token.
func seededStatusOutput(t *testing.T) []byte {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, validTOML)
	id := sandboxIdentityOf(t, worktree)
	fake.SeedSandbox(t, id, "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	fake.SeedPublishedPort(t, id, 8080, 80)

	var stdout, stderr bytes.Buffer
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"status"}, nil, &stdout, &stderr)
	}); code != 0 {
		t.Fatalf("status on the seeded sandbox failed: %s", stderr.String())
	}
	// chdir restores the working directory only at cleanup; step back
	// before the caller reads and writes package testdata.
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(stdout.String(), id, "<sandbox-id>"))
}

func TestPlainStatusGolden(t *testing.T) {
	testsupport.Golden(t, "testdata/status.golden", seededStatusOutput(t))
}
