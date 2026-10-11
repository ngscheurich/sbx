package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
)

func TestCommandHelpWithOtherArguments(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cases := [][]string{
		{"exec", "--allow-stale", "--help", "--", "echo", "hello"},
		{"run", "-h", "--", "echo", "hello"},
		{"up", "--allow-stale", "--retry-bootstrap", "-h"},
		{"build", "--keep-archive", "--help"},
		{"plan", "--json", "--help"},
		{"rm", "--yes", "--json", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stderr=%q", code, &stderr)
			}
			if !strings.Contains(stdout.String(), "sbx "+args[0]) || !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("expected command help, got %q", &stdout)
			}
		})
	}
}

func TestHelpDoesNotConsumeGuestArguments(t *testing.T) {
	for _, command := range []string{"exec", "run"} {
		t.Run(command, func(t *testing.T) {
			worktree, _ := harness.PersistentFixture(t, validTOML)
			t.Setenv("FAKE_MSB_EXIT", "7")
			for _, flag := range []string{"-h", "--help"} {
				t.Run(flag, func(t *testing.T) {
					var stdout, stderr bytes.Buffer
					code := harness.Chdir(t, worktree, func() int {
						return cli.Run(t.Context(), []string{command, "--", "echo", flag}, nil, &stdout, &stderr)
					})
					if code != 7 || stdout.String() != "guest-stdout\n" || stderr.String() != "guest-stderr\n" {
						t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
					}
				})
			}
		})
	}
}

func TestCommandHelpHonorsOutputMode(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")
	cases := []struct {
		name    string
		args    []string
		noColor string
		styled  bool
	}{
		{name: "styled", args: []string{"up", "--help"}, styled: true},
		{name: "plain", args: []string{"--plain", "help", "up"}},
		{name: "no color", args: []string{"up", "-h"}, noColor: "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColor)
			var stdout, stderr bytes.Buffer
			if code := cli.Run(t.Context(), tc.args, nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stderr=%q", code, &stderr)
			}
			if got := bytes.ContainsRune(stdout.Bytes(), 0x1b); got != tc.styled {
				t.Errorf("ANSI=%t, want %t: %q", got, tc.styled, &stdout)
			}
			for _, want := range []string{"Usage:", "--allow-stale", "--retry-bootstrap", "Creation drift", "incomplete"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help lost %q:\n%s", want, &stdout)
				}
			}
		})
	}
}

func TestHelpRejectsUnknownTopic(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"help", "extra"}, {"help", "port", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, &stdout, &stderr)
		}
		for _, want := range []string{"unknown help command", "extra", "sbx help"} {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("%v: stderr lacks %q: %s", args, want, &stderr)
			}
		}
	}
}

func TestUnknownFlagsPointToCommandHelp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	commands := [][]string{
		{"plan"}, {"build"}, {"run"}, {"up"}, {"exec"}, {"status"},
		{"list"}, {"logs"}, {"stop"}, {"rm"}, {"port"}, {"port", "prune"},
	}
	for _, command := range commands {
		name := strings.Join(command, " ")
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(command, "--bogus")
			if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 2 {
				t.Fatalf("code=%d, want usage error: %s", code, &stderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("usage error wrote to stdout: %q", &stdout)
			}
			for _, want := range []string{"sbx:", "--bogus", "sbx help " + name} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q: %s", want, &stderr)
				}
			}
		})
	}
}

func TestPortCommandHelp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cases := []struct {
		args []string
		want []string
	}{
		{args: []string{"port"}, want: []string{"sbx port <subcommand>", "Subcommands:", "prune", "sbx help port prune", "-h, --help"}},
		{args: []string{"port", "-h"}, want: []string{"sbx port <subcommand>", "Subcommands:", "-h, --help"}},
		{args: []string{"port", "--help"}, want: []string{"sbx port <subcommand>", "Subcommands:", "-h, --help"}},
		{args: []string{"help", "port"}, want: []string{"sbx port <subcommand>", "Subcommands:", "-h, --help"}},
		{args: []string{"port", "prune", "-h"}, want: []string{"sbx port prune [--json]", "stopped", "backend", "changing nothing", "-h, --help"}},
		{args: []string{"port", "prune", "--help"}, want: []string{"sbx port prune [--json]", "stopped", "backend", "changing nothing", "-h, --help"}},
		{args: []string{"help", "port", "prune"}, want: []string{"sbx port prune [--json]", "stopped", "backend", "changing nothing", "-h, --help"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := harness.Chdir(t, t.TempDir(), func() int {
				return cli.Run(t.Context(), tc.args, nil, &stdout, &stderr)
			})
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stderr=%q", code, &stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help lacks %q:\n%s", want, &stdout)
				}
			}
		})
	}
}

func TestCommandHelp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	cases := []struct {
		name string
		want []string
	}{
		{name: "plan", want: []string{"--json", "changing nothing", "sbx.toml", "Does not pull images"}},
		{name: "build", want: []string{"--keep-archive", "--json", "Requires a [build] section in sbx.toml", "Docker", "Image check"}},
		{name: "run", want: []string{"-- <argv...>", "sbx.toml", "configured shell", "Publishes no ports", "exit status", "--json is not supported"}},
		{name: "up", want: []string{"--allow-stale", "--retry-bootstrap", "--json", "Creation drift", "incomplete", "Image checks", "sbx.toml"}},
		{name: "exec", want: []string{"--allow-stale", "-- <argv...>", "sbx.toml", "configured shell", "exit status", "--json is not supported"}},
		{name: "status", want: []string{"--json", "changing nothing", "sbx.toml", "Creation drift"}},
		{name: "list", want: []string{"--json", "Read-only", "any directory", "without project configuration", "Owned sandboxes"}},
		{name: "logs", want: []string{"Read-only", "existing", "Git worktree", "--json is not supported"}},
		{name: "stop", want: []string{"--json", "existing", "Git worktree", "state and volumes", "Creation drift"}},
		{name: "rm", want: []string{"--yes", "--json", "confirmation", "noninteractive", "Sandbox volumes", "Project volumes and port reservations", "Git worktree"}},
		{name: "version", want: []string{"--json", "without project configuration"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var first string
			for _, args := range [][]string{{tc.name, "-h"}, {tc.name, "--help"}, {"help", tc.name}} {
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					var stdout, stderr bytes.Buffer
					code := harness.Chdir(t, t.TempDir(), func() int {
						return cli.Run(t.Context(), args, nil, &stdout, &stderr)
					})
					if code != 0 || stderr.Len() != 0 {
						t.Fatalf("code=%d stderr=%q", code, &stderr)
					}
					want := append([]string{"sbx " + tc.name, "Usage:", "Flags:", "-h, --help"}, tc.want...)
					for _, fragment := range want {
						if !strings.Contains(stdout.String(), fragment) {
							t.Errorf("help lacks %q:\n%s", fragment, &stdout)
						}
					}
					if first == "" {
						first = stdout.String()
					} else if stdout.String() != first {
						t.Errorf("help forms differ:\n%s\nwant:\n%s", &stdout, first)
					}
				})
			}
		})
	}
}
