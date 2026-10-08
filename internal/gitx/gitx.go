// Package gitx discovers the Git locations sbx derives identity from: the
// worktree root and the repository's common Git directory.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Info holds the absolute, symlink-free Git paths sbx derives names from.
type Info struct {
	// WorktreeRoot is the worktree the invocation directory belongs to.
	WorktreeRoot string
	// CommonDir is the repository's common Git directory, shared by every
	// worktree of the project.
	CommonDir string
}

// Discover resolves the worktree root and common Git directory for the
// directory at dir. It fails with an actionable error when dir is not inside
// a Git worktree, and with a distinct error when git itself cannot be run.
func Discover(ctx context.Context, dir string) (Info, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Info{}, fmt.Errorf("resolving %s: %w", dir, err)
	}

	// One rev-parse answers both questions, printing in argument order,
	// one per line. Every sbx invocation pays this spawn, which macOS
	// prices an order of magnitude above Linux, so the two queries
	// deliberately share a subprocess.
	out, err := gitIn(ctx, abs, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Info{}, err
		}
		return Info{}, fmt.Errorf("sbx must run inside a Git worktree, but %s is not inside one (%v)", abs, err)
	}
	toplevel, commonDir, found := strings.Cut(out, "\n")
	if !found || toplevel == "" || commonDir == "" {
		return Info{}, fmt.Errorf("git rev-parse in %s answered %q, want the worktree root and the common Git directory", abs, out)
	}

	info := Info{WorktreeRoot: toplevel, CommonDir: commonDir}
	for _, p := range []*string{&info.WorktreeRoot, &info.CommonDir} {
		resolved, err := filepath.EvalSymlinks(*p)
		if err != nil {
			return Info{}, fmt.Errorf("resolving %s: %w", *p, err)
		}
		abs2, err := filepath.Abs(resolved)
		if err != nil {
			return Info{}, fmt.Errorf("resolving %s: %w", *p, err)
		}
		*p = abs2
	}
	return info, nil
}

// gitIn runs one git command in dir and returns its standard output. The
// streams are kept separate: the parsed answer comes from stdout alone, so a
// warning git prints to stderr (ownership advisories, config warnings) can
// never be mistaken for the answer, and stderr's first line serves the error
// path. A missing git executable is reported as such, not mistaken for a
// failed rev-parse.
func gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("git is not installed or not on PATH: %w", err)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// firstLine returns the first non-empty line of a command's output.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "git failed without output"
}
