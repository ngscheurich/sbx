// Package gitx discovers the Git locations sbx derives identity from: the
// worktree root and the repository's common Git directory.
package gitx

import (
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
// a Git worktree.
func Discover(dir string) (Info, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Info{}, fmt.Errorf("resolving %s: %w", dir, err)
	}

	toplevel, err := gitIn(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}, fmt.Errorf("sbx must run inside a Git worktree, but %s is not inside one (%v)", abs, err)
	}

	commonDir, err := gitIn(abs, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Info{}, fmt.Errorf("finding the common Git directory for %s: %w", abs, err)
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

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
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
