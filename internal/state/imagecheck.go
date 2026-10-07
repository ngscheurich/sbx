// Image-check success records: host-state records that one image's exact
// contents passed the project's image-check script. A record is keyed by
// the image's manifest digest and the script's content hash — never by tag
// alone — so a repointed tag or an edited script is never covered by an
// old pass (ADR-0004). The records are machine-scoped: they describe image
// contents, not a worktree or sandbox.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrCorruptImageCheckRecord reports an unreadable image-check record. A
// corrupt record fails closed: an unreadable success is never a pass.
var ErrCorruptImageCheckRecord = errors.New("corrupt image-check record")

// ImageCheckRecord records one successful image-check run.
type ImageCheckRecord struct {
	// ImageDigest is the manifest digest of the image contents that passed.
	ImageDigest string `json:"image_digest"`
	// ScriptHash is the hash of the exact script contents that passed.
	ScriptHash string `json:"script_hash"`
	// RecordedAt is when the check passed.
	RecordedAt string `json:"recorded_at"`
}

// ImageCheckScriptHash identifies an image-check script by its exact
// contents. Comparing hashes reports a changed script without storing the
// script itself.
func ImageCheckScriptHash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// imageCheckKey derives the record file's key from the image digest and
// script hash: the SHA-256 of both, so the key depends on the pair and the
// file name is filesystem-safe.
func imageCheckKey(digest, scriptHash string) string {
	sum := sha256.Sum256([]byte(digest + "\x00" + scriptHash))
	return hex.EncodeToString(sum[:])
}

func imageCheckPath(digest, scriptHash string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "imagecheck", imageCheckKey(digest, scriptHash)+".json"), nil
}

// SaveImageCheckSuccess atomically records that the image contents with the
// given manifest digest passed the script with the given hash. Callers
// write it only after the check actually passed; a failed or interrupted
// check records nothing.
func SaveImageCheckSuccess(digest, scriptHash string) error {
	path, err := imageCheckPath(digest, scriptHash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating the image-check record directory: %w", err)
	}
	record := ImageCheckRecord{
		ImageDigest: digest,
		ScriptHash:  scriptHash,
		RecordedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the image-check record: %w", err)
	}
	return writeFileAtomic(path, data)
}

// HasImageCheckSuccess reports whether a matching success is recorded for
// the image digest and script hash. A corrupt record is an error, never
// silently treated as a pass or an absence.
func HasImageCheckSuccess(digest, scriptHash string) (bool, error) {
	path, err := imageCheckPath(digest, scriptHash)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading the image-check record: %w", err)
	}
	var record ImageCheckRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return false, fmt.Errorf("%w: %v", ErrCorruptImageCheckRecord, err)
	}
	// The key is derived from the pair, but the record's own fields are the
	// readable truth: a record that does not name this pair proves nothing.
	return record.ImageDigest == digest && record.ScriptHash == scriptHash, nil
}
