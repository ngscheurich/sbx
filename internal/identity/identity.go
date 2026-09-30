// Package identity derives names that depend only on Git locations, never on
// editable configuration (ADR-0005) and never on project configuration values.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

// maxComponent is the maximum length of a sanitized name component in a
// Sandbox identity, per the spec: "cut to 32 characters".
const maxComponent = 32

// ProjectBasename derives the project name from the repository's common Git
// directory. When that directory is named ".git", the parent directory's name
// is used; otherwise the directory's own name is used without a trailing
// ".git". "/src/app/.git" and "/src/app.git" both yield "app".
func ProjectBasename(commonDir string) string {
	base := path.Base(strings.TrimRight(commonDir, "/"))
	if base == ".git" {
		base = path.Base(path.Dir(strings.TrimRight(commonDir, "/")))
	}
	return strings.TrimSuffix(base, ".git")
}

// Sanitize lowercases a name component, replaces every character outside
// [a-z0-9] with "-", and cuts the result to 32 characters.
func Sanitize(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) > maxComponent {
		s = s[:maxComponent]
	}
	return s
}

// HashPath returns the first 8 hex characters of the SHA-256 of the given
// path. Paths must already be absolute and symlink-free when hashed.
func HashPath(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])[:8]
}

// VolumeNamespace derives the Project volume namespace from the common Git
// directory. Sibling worktrees of one project share it; unrelated clones do
// not (ADR-0003).
func VolumeNamespace(commonDir string) string {
	return HashPath(commonDir)
}

// VolumeName derives the literal backend name of a Project volume from the
// common Git directory and the logical volume name declared in sbx.toml.
func VolumeName(commonDir, logical string) string {
	return VolumeNamespace(commonDir) + "-" + logical
}

// Identity is the configuration-independent identity of a worktree's
// persistent sandbox and its project's volume namespace.
type Identity struct {
	// Project is the sanitized project basename.
	Project string
	// Worktree is the sanitized worktree basename.
	Worktree string
	// Hash is the 8-character hash of the worktree path.
	Hash string
	// Sandbox is the full Sandbox identity: project, worktree, and hash.
	Sandbox string
}

// Derive computes the Sandbox identity from the common Git directory and the
// worktree root. Both paths must already be absolute and symlink-free.
func Derive(commonDir, worktreeRoot string) Identity {
	project := Sanitize(ProjectBasename(commonDir))
	worktree := Sanitize(path.Base(worktreeRoot))
	hash := HashPath(worktreeRoot)
	return Identity{
		Project:  project,
		Worktree: worktree,
		Hash:     hash,
		Sandbox:  project + "-" + worktree + "-" + hash,
	}
}
