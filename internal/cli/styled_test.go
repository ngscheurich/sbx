package cli_test

// Styled- and Plain-output tests: the regression pair that keeps color
// from ever carrying meaning alone — ANSI appears on the styled path, and
// the severity words survive on both paths.

import (
	"bytes"
	"context"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// untranslatableTOML declares a bind whose source does not exist, so the
// plan reports `translation failed`.
const untranslatableTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[[mounts]]
type = "bind"
source = "./absent.txt"
target = "/absent"

[network]
policy = "public"
`

// styledScenarios drives the three surfaces the acceptance names, through
// cli.Run and its writer seam: a translation-failed plan (the warning
// heading), a status report for a seeded running sandbox (state names),
// and a usage error (the `sbx:` prefix).
func styledScenarios(t *testing.T) (planOut, statusOut, errOut []byte) {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	worktree, _ := harness.FixtureRepo(t, untranslatableTOML)
	var stdout, stderr bytes.Buffer
	if code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	}); code != 0 {
		t.Fatalf("plan on the untranslatable fixture failed: %s", stderr.String())
	}
	planOut = stdout.Bytes()

	statusTree, _ := harness.FixtureRepo(t, validTOML)
	id := harness.SandboxIdentityOf(t, statusTree)
	fake.SeedSandbox(t, id, "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	stdout.Reset()
	stderr.Reset()
	if code := harness.Chdir(t, statusTree, func() int {
		return cli.Run(context.Background(), []string{"status"}, nil, &stdout, &stderr)
	}); code != 0 {
		t.Fatalf("status on the seeded sandbox failed: %s", stderr.String())
	}
	statusOut = stdout.Bytes()

	stdout.Reset()
	stderr.Reset()
	cli.Run(context.Background(), []string{"plan", "--verbose"}, nil, &stdout, &stderr)
	errOut = stderr.Bytes()
	return
}

// severityWords are the words that carry each scenario's meaning on their
// own; they must survive on both the styled and the plain path. They are
// fragments, not whole lines: styling may sit between them (the error
// prefix is styled, the message stays default).
var severityWords = map[string][][]byte{
	"plan report":      {[]byte("translation failed, so the sandbox cannot be planned")},
	"status report":    {[]byte("Running")},
	"usage error line": {[]byte("sbx:"), []byte("plan takes only --json")},
}

// TestStyledOutputColorsTheWordsItKeeps forces color through the seam with
// CLICOLOR_FORCE and asserts ANSI bytes appear alongside the words.
func TestStyledOutputColorsTheWordsItKeeps(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "") // no-color.org: an empty value counts as unset
	planOut, statusOut, errOut := styledScenarios(t)
	for name, got := range map[string][]byte{
		"plan report":      planOut,
		"status report":    statusOut,
		"usage error line": errOut,
	} {
		if !bytes.Contains(got, []byte("\x1b[")) {
			t.Errorf("%s carries no ANSI escapes:\n%s", name, got)
		}
		for _, want := range severityWords[name] {
			if !bytes.Contains(got, want) {
				t.Errorf("%s lost its severity words:\n%s", name, got)
			}
		}
	}
}

// TestPlainOutputCarriesTheWordsWithoutColor runs the same scenarios with
// color detection left alone: a pipe-like buffer receives zero escape
// bytes, and the severity words survive alone.
func TestPlainOutputCarriesTheWordsWithoutColor(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "0")
	planOut, statusOut, errOut := styledScenarios(t)
	for name, got := range map[string][]byte{
		"plan report":      planOut,
		"status report":    statusOut,
		"usage error line": errOut,
	} {
		if bytes.ContainsRune(got, 0x1b) {
			t.Errorf("%s carries escape bytes:\n%q", name, got)
		}
		for _, want := range severityWords[name] {
			if !bytes.Contains(got, want) {
				t.Errorf("%s lost its severity words:\n%s", name, got)
			}
		}
	}
}
