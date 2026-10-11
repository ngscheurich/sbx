package cli_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
)

func TestGlobalFlagsPreserveGuestArguments(t *testing.T) {
	config := validTOML + `
[aliases]
ls = "exec -- echo"
ll = "ls"
`
	want := []string{"echo", "--plain", "--no-color", "-p", "--json", "--", "--plain", "", "two words"}
	cases := [][]string{
		{"exec", "--", "echo"},
		{"--plain", "exec", "--", "echo"},
		{"exec", "--no-color", "-p", "--", "echo"},
		{"run", "--", "echo"},
		{"--plain", "run", "--", "echo"},
		{"run", "--plain", "--", "echo"},
		{"ls"},
		{"ll"},
		{"--plain", "ls"},
		{"--no-color", "ll"},
	}
	for _, prefix := range cases {
		t.Run(strings.Join(prefix, " "), func(t *testing.T) {
			worktree, fake := harness.PersistentFixture(t, config)
			t.Setenv("FAKE_MSB_EXIT", "7")
			args := append(slices.Clone(prefix), want[1:]...)
			code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
			if code != 7 || stdout != "guest-stdout\n" || stderr != "guest-stderr\n" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			found := false
			for _, call := range fake.Calls() {
				if call.Args[0] != "exec" && call.Args[0] != "run" {
					continue
				}
				found = true
				i := slices.Index(call.Args, "--")
				if i < 0 || !slices.Equal(call.Args[i+1:], want) {
					t.Errorf("guest arguments changed: call=%q, want guest argv=%q", call.Args, want)
				}
			}
			if !found {
				t.Fatal("no guest command reached the backend")
			}
		})
	}
}

func TestGlobalFlagsStopAtSeparator(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("PATH", t.TempDir())
	for _, flag := range []string{"--plain", "--no-color", "-p"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(t.Context(), []string{"list", "--", flag}, nil, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || !bytes.ContainsRune(stderr.Bytes(), 0x1b) {
				t.Fatalf("code=%d stdout=%q stderr=%q; flag after -- must not select Plain output", code, &stdout, &stderr)
			}
		})
	}
}

func TestGlobalFlagsKeepCommandFlagsScoped(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"list", "--plain", "--allow-stale"},
		{"up", "--plain", "--keep-archive"},
		{"plan", "--no-color", "--yes"},
		{"run", "--plain", "--allow-stale"},
		{"exec", "-p", "--retry-bootstrap"},
		{"port", "--plain", "prune", "--yes"},
		{"list", "--plain=false"},
		{"list", "--no-color=true"},
		{"list", "-pp"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(t.Context(), args, nil, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), args[len(args)-1]) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		})
	}
}

func TestGlobalFlagsForRootHelp(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"--plain"},
		{"-p", "--no-color"},
		{"help", "--plain"},
		{"--help", "--no-color"},
		{"-h", "-p"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(t.Context(), args, nil, &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 || bytes.ContainsRune(stdout.Bytes(), 0x1b) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("root help missing: %q", &stdout)
			}
		})
	}
}

func TestGlobalFlagsForNestedCommand(t *testing.T) {
	for _, args := range [][]string{
		{"--plain", "port", "prune"},
		{"port", "--plain", "prune"},
		{"port", "prune", "--no-color", "-p"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			worktree, _ := harness.PersistentFixture(t, validTOML)
			code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			if !strings.Contains(stdout, "nothing to prune") {
				t.Errorf("port prune did not run: %q", stdout)
			}
		})
	}
}
