// Bootstrap completion markers: host-state records that a sandbox's
// Bootstrap definition ran to success. A marker binds the Sandbox identity
// and the sandbox's created_at — msb's record of which sandbox incarnation
// ran — to a hash of the definition, so a marker left by a removed and
// recreated sandbox is never reused and a changed definition is reported
// without touching Creation drift (ADR-0002). Markers are host state, not
// sandbox labels: msb requires a restart to change a running sandbox's
// label, and Bootstrap recording must not restart anything.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoBootstrapMarker reports that no Bootstrap completion marker exists
// for a sandbox: Bootstrap never completed for it, or its removal cleared
// the marker. A missing marker means the sandbox is incomplete.
var ErrNoBootstrapMarker = errors.New("no bootstrap completion marker")

// BootstrapMarker records one successful Bootstrap run.
type BootstrapMarker struct {
	// Sandbox is the Sandbox identity the marker belongs to.
	Sandbox string `json:"sandbox"`
	// CreatedAt is the sandbox's created_at as msb reported it when
	// Bootstrap ran. It ties the marker to one sandbox incarnation: a
	// removed and recreated sandbox has a new created_at, so the old
	// marker is stale and never reused.
	CreatedAt string `json:"created_at"`
	// DefinitionHash is the hash of the Bootstrap definition that ran.
	DefinitionHash string `json:"definition_hash"`
}

// BootstrapHash identifies a Bootstrap definition: the exact guest shell
// code. Comparing hashes reports a changed definition without storing the
// code itself.
func BootstrapHash(run string) string {
	sum := sha256.Sum256([]byte(run))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// bootstrapMarkerPath returns the marker file path for a Sandbox identity.
func bootstrapMarkerPath(sandbox string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bootstrap", sandbox+".json"), nil
}

// SaveBootstrapMarker atomically records a sandbox's Bootstrap completion,
// overwriting any marker from an earlier run. Callers write it only after
// the guest code succeeded; a failure here leaves the sandbox incomplete.
func SaveBootstrapMarker(sandbox string, marker BootstrapMarker) error {
	path, err := bootstrapMarkerPath(sandbox)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating the bootstrap marker directory: %w", err)
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the bootstrap marker for %s: %w", sandbox, err)
	}
	return writeFileAtomic(path, data)
}

// LoadBootstrapMarker reads a sandbox's Bootstrap completion marker. A
// missing marker is ErrNoBootstrapMarker; a corrupt one is an error, never
// silently treated as completion.
func LoadBootstrapMarker(sandbox string) (BootstrapMarker, error) {
	path, err := bootstrapMarkerPath(sandbox)
	if err != nil {
		return BootstrapMarker{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return BootstrapMarker{}, fmt.Errorf("%w for sandbox %s", ErrNoBootstrapMarker, sandbox)
	}
	if err != nil {
		return BootstrapMarker{}, fmt.Errorf("reading the bootstrap marker for %s: %w", sandbox, err)
	}
	var marker BootstrapMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return BootstrapMarker{}, fmt.Errorf("the bootstrap marker for %s is unreadable: %w", sandbox, err)
	}
	return marker, nil
}

// DeleteBootstrapMarker removes a sandbox's marker, for when the sandbox it
// belongs to is removed. A missing marker is not an error.
func DeleteBootstrapMarker(sandbox string) error {
	path, err := bootstrapMarkerPath(sandbox)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing the bootstrap marker for %s: %w", sandbox, err)
	}
	return nil
}
