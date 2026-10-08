// Package state holds sbx's host state: the machine-wide host state
// directory with the creation-time snapshots of persistent sandboxes,
// Bootstrap completion markers (bootstrap.go), the machine-wide port
// registry (ports.go), and per-sandbox cross-process locks (lock.go).
// Image-check successes join in a later release. Snapshots capture what a
// sandbox was
// created from so sbx can report Creation drift without ever recreating a
// sandbox that holds private data; markers record a Bootstrap success bound
// to the sandbox incarnation that ran it (ADR-0002).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/translate"
)

// ErrNoSnapshot reports that no creation-time snapshot exists for a
// sandbox. An owned sandbox without a snapshot counts as drifted.
var ErrNoSnapshot = errors.New("no creation-time snapshot")

// Dir returns sbx's host state directory:
// ${XDG_STATE_HOME:-~/.local/state}/sbx.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		abs, err := filepath.Abs(xdg)
		if err != nil {
			return "", fmt.Errorf("resolving XDG_STATE_HOME %s: %w", xdg, err)
		}
		return filepath.Join(abs, "sbx"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the home directory for the host state: %w", err)
	}
	return filepath.Join(home, ".local", "state", "sbx"), nil
}

// Definition is one rendering of a persistent sandbox's creation-time
// settings. The generated secret and filesystem configurations travel by
// content, not path, because their files are per-creation temporaries.
type Definition struct {
	// Options is the msb creation settings, normalized so the
	// generated-file paths never participate in comparisons.
	Options msb.CreateOptions `json:"options"`
	// Ports are the declared named guest ports, in name order. The host
	// loopback bindings live in the port registry, not here: they can be
	// corrected without recreation, so they are not creation-time settings.
	Ports []translate.DeclaredPort `json:"ports,omitempty"`
	// SecretConfYAML is the generated secret-name map (names and host
	// variable references only, never values).
	SecretConfYAML string `json:"secret_conf_yaml,omitempty"`
	// FsConfYAML is the generated filesystem configuration (tmpfs mounts).
	FsConfYAML string `json:"fs_conf_yaml,omitempty"`
}

// DefinitionOf captures the creation-relevant parts of a translation.
func DefinitionOf(tr translate.Translation) Definition {
	return Definition{
		Options:        normalizeOptions(tr.Options),
		Ports:          tr.Ports,
		SecretConfYAML: tr.SecretConfYAML,
		FsConfYAML:     tr.FsConfYAML,
	}
}

// normalizeOptions strips the per-creation generated-file paths from a
// creation-options value so two identical creations compare equal.
func normalizeOptions(o msb.CreateOptions) msb.CreateOptions {
	o.SecretConf = ""
	o.FsConf = ""
	// The host loopback bindings are registry data, not creation settings:
	// they are chosen at creation and can be corrected later, so they
	// never participate in drift. Which guest ports are declared does.
	o.Publish = nil
	return o
}

// Snapshot is the captured Creation-time settings of one persistent
// sandbox, stored under its Sandbox identity.
type Snapshot struct {
	// ImageDigest is the manifest digest of the image the sandbox was
	// created from, so a tag repointed at new contents is drift even when
	// sbx.toml is unchanged.
	ImageDigest string `json:"image_digest"`
	// Definition is what the sandbox was created from.
	Definition Definition `json:"definition"`
}

// snapshotPath returns the snapshot file path for a Sandbox identity.
func snapshotPath(sandbox string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshots", sandbox+".json"), nil
}

// SaveSnapshot atomically records a sandbox's creation-time settings,
// overwriting any snapshot from an earlier creation of the same identity.
func SaveSnapshot(sandbox string, snap Snapshot) error {
	path, err := snapshotPath(sandbox)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating the snapshot directory: %w", err)
	}
	snap.Definition.Options = normalizeOptions(snap.Definition.Options)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the snapshot for %s: %w", sandbox, err)
	}
	return writeFileAtomic(path, data)
}

// LoadSnapshot reads a sandbox's creation-time snapshot. A missing
// snapshot is ErrNoSnapshot; a corrupt one is an error, never silently
// treated as a match.
func LoadSnapshot(sandbox string) (Snapshot, error) {
	path, err := snapshotPath(sandbox)
	if err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Snapshot{}, fmt.Errorf("%w for sandbox %s", ErrNoSnapshot, sandbox)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading the snapshot for %s: %w", sandbox, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("the snapshot for %s is unreadable: %w", sandbox, err)
	}
	return snap, nil
}

// DeleteSnapshot removes a sandbox's snapshot, for when the sandbox it
// described is removed. A missing snapshot is not an error.
func DeleteSnapshot(sandbox string) error {
	path, err := snapshotPath(sandbox)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing the snapshot for %s: %w", sandbox, err)
	}
	return nil
}

// writeFileAtomic writes data to path via a same-directory temporary and a
// rename, so readers never observe a partial file.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Drift reports Creation drift: the differences between the definition a
// sandbox was created from and the current definition. currentDigest is
// the image's manifest digest as it resolves now; an empty one means the
// image could not be inspected, which fails closed as drift rather than
// passing as unchanged. The returned lines are unindented; callers
// format them.
func Drift(created Snapshot, current Definition, currentDigest string) []string {
	var drift []string
	old, now := created.Definition, current

	aspects := []struct {
		name    string
		created string
		current string
	}{
		{"image", old.Options.Image, now.Options.Image},
		{"memory", old.Options.Memory, now.Options.Memory},
		{"cpus", msb.FormatCPUs(old.Options.CPUs), msb.FormatCPUs(now.Options.CPUs)},
		{"mounts", renderMounts(old.Options.Mounts), renderMounts(now.Options.Mounts)},
		{"project volumes", renderNamed(old.Options.Named), renderNamed(now.Options.Named)},
		{"sandbox volumes", renderOwned(old.Options.Owned), renderOwned(now.Options.Owned)},
		{"environment", renderList(old.Options.Env), renderList(now.Options.Env)},
		{"ports", renderPorts(old.Ports), renderPorts(now.Ports)},
		{"network policy", renderNetwork(old.Options), renderNetwork(now.Options)},
		{"secret map", old.SecretConfYAML, now.SecretConfYAML},
		{"filesystem configuration (tmpfs mounts)", old.FsConfYAML, now.FsConfYAML},
	}
	for _, a := range aspects {
		if a.created != a.current {
			drift = append(drift, fmt.Sprintf("%s: created with %s, current configuration declares %s",
				a.name, a.created, a.current))
		}
	}

	switch {
	case currentDigest == "":
		drift = append(drift, "image contents: could not be confirmed (msb cannot inspect the image); sbx cannot prove the image is unchanged")
	case created.ImageDigest != "" && created.ImageDigest != currentDigest:
		drift = append(drift, fmt.Sprintf("image contents: created from %s, the image tag now resolves to %s",
			created.ImageDigest, currentDigest))
	}
	return drift
}

// renderMounts renders the bind mounts (Workspace first) as one compact
// list. An absent list renders as "[]" so absence and emptiness differ
// from a non-empty list.
func renderMounts(mounts []msb.Mount) string {
	if len(mounts) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		spec := "bind " + m.Source + " -> " + m.Target
		if m.ReadOnly {
			spec += " (read-only)"
		}
		if m.IsFile {
			spec += " (file)"
		}
		parts = append(parts, spec)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// renderNamed renders the mounted Project volumes.
func renderNamed(named []msb.NamedMount) string {
	if len(named) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(named))
	for _, n := range named {
		spec := n.Name + " at " + n.Target
		if n.Kind == "disk" {
			spec += " (disk, " + n.Size + ")"
		} else if n.Quota != "" {
			spec += " (directory, quota " + n.Quota + ")"
		}
		parts = append(parts, spec)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// renderOwned renders the private Sandbox volumes.
func renderOwned(owned []msb.OwnedMount) string {
	if len(owned) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(owned))
	for _, o := range owned {
		spec := o.Target
		if o.Kind == "disk" {
			spec += " (disk, " + o.Size + ")"
		}
		parts = append(parts, spec)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// renderPorts renders the declared named guest ports. An absent list
// renders as "[]" so absence and emptiness differ from a non-empty list.
func renderPorts(ports []translate.DeclaredPort) string {
	if len(ports) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf("%s: guest %d", p.Name, p.Guest))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// renderList renders an ordered string list such as the environment.
func renderList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	return "[" + strings.Join(items, "; ") + "]"
}

// renderNetwork summarizes the network policy mode: allow rules,
// DNS, TLS interception, and total disconnection.
func renderNetwork(o msb.CreateOptions) string {
	var parts []string
	switch {
	case o.NoNet:
		parts = append(parts, "no network")
	case len(o.NetRules) > 0:
		parts = append(parts, "allowlist "+renderList(o.NetRules))
	default:
		parts = append(parts, "public policy")
	}
	if o.TLSIntercept {
		parts = append(parts, "TLS interception on")
	}
	if len(o.DNSNameservers) > 0 {
		parts = append(parts, "dns "+renderList(o.DNSNameservers))
	}
	return strings.Join(parts, ", ")
}
