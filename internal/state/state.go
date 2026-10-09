// Package state holds sbx's host state: the machine-wide host state
// directory with the creation-time snapshots of persistent sandboxes,
// Bootstrap completion markers (bootstrap.go), the machine-wide port
// registry (ports.go), image-check success records (imagecheck.go), and
// per-sandbox cross-process locks (lock.go). Snapshots capture what a
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
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/translate"
	"github.com/ngscheurich/sbx/internal/ui"
)

// ErrNoSnapshot reports that no creation-time snapshot exists for a
// sandbox. An owned sandbox without a snapshot counts as drifted.
var ErrNoSnapshot = errors.New("no creation-time snapshot")

// Dir returns sbx's host state directory:
// ${XDG_STATE_HOME:-~/.local/state}/sbx. A relative XDG_STATE_HOME is
// treated as unset — resolving it against the current directory would
// scatter machine-wide state wherever the caller happened to stand.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "sbx"), nil
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

// DriftEntry is one difference between the snapshot and the current
// configuration: the entry's name with renderings of what it was at
// creation (Was) and what the configuration now declares (Now). An entry
// the configuration no longer declares renders Now as "removed"; one it
// newly declares renders Was as an em dash. List-valued aspects — the
// environment, volumes, ports — are diffed per entry, so one dropped
// variable is one row: environment.PHX_BIND_ALL, not the whole list.
type DriftEntry struct {
	Setting string
	Was     string
	Now     string
}

// Drift reports Creation drift: the differences between the definition a
// sandbox was created from and the current definition. currentDigest is
// the image's manifest digest as it resolves now; an empty one means the
// image could not be inspected, which fails closed as drift rather than
// passing as unchanged. The returned entries are unindented; callers
// format them, most often with RenderDrift.
func Drift(created Snapshot, current Definition, currentDigest string) []DriftEntry {
	var drift []DriftEntry
	old, now := created.Definition, current

	scalar := func(name, was, nowV string) {
		if was != nowV {
			drift = append(drift, DriftEntry{Setting: name, Was: was, Now: nowV})
		}
	}
	scalar("image", old.Options.Image, now.Options.Image)
	scalar("memory", old.Options.Memory, now.Options.Memory)
	scalar("cpus", msb.FormatCPUs(old.Options.CPUs), msb.FormatCPUs(now.Options.CPUs))
	drift = append(drift, diffKeyed("mounts", mountsKeyed(old.Options.Mounts), mountsKeyed(now.Options.Mounts))...)
	drift = append(drift, diffKeyed("project volumes", namedKeyed(old.Options.Named), namedKeyed(now.Options.Named))...)
	drift = append(drift, diffKeyed("sandbox volumes", ownedKeyed(old.Options.Owned), ownedKeyed(now.Options.Owned))...)
	drift = append(drift, diffKeyed("environment", envKeyed(old.Options.Env), envKeyed(now.Options.Env))...)
	drift = append(drift, diffKeyed("ports", portsKeyed(old.Ports), portsKeyed(now.Ports))...)
	scalar("network policy", renderNetwork(old.Options), renderNetwork(now.Options))
	scalar("secret map", old.SecretConfYAML, now.SecretConfYAML)
	scalar("filesystem configuration (tmpfs mounts)", old.FsConfYAML, now.FsConfYAML)

	switch {
	case currentDigest == "":
		was := created.ImageDigest
		if was == "" {
			was = "(not recorded)"
		}
		drift = append(drift, DriftEntry{Setting: "image contents", Was: was,
			Now: "could not be inspected (unconfirmed)"})
	case created.ImageDigest != "" && created.ImageDigest != currentDigest:
		drift = append(drift, DriftEntry{Setting: "image contents", Was: created.ImageDigest, Now: currentDigest})
	}
	return drift
}

// diffKeyed turns two keyed value maps into one drift row per key whose
// presence or value differs, in key order. A key absent from the current
// side is removed; one absent from the creation side is new.
func diffKeyed(name string, was, now map[string]string) []DriftEntry {
	keys := make([]string, 0, len(was)+len(now))
	for k := range was {
		keys = append(keys, k)
	}
	for k := range now {
		if _, ok := was[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []DriftEntry
	for _, k := range keys {
		w, wasOK := was[k]
		n, nowOK := now[k]
		if wasOK && nowOK && w == n {
			continue
		}
		wasCell, nowCell := w, n
		if !wasOK {
			wasCell = "—"
		}
		if !nowOK {
			nowCell = "removed"
		}
		out = append(out, DriftEntry{Setting: name + "." + k, Was: wasCell, Now: nowCell})
	}
	return out
}

// envKeyed keys KEY=VALUE environment entries by variable name.
func envKeyed(items []string) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		k, v, _ := strings.Cut(item, "=")
		out[k] = v
	}
	return out
}

// mountsKeyed keys bind mounts by target.
func mountsKeyed(mounts []msb.Mount) map[string]string {
	out := make(map[string]string, len(mounts))
	for _, m := range mounts {
		out[m.Target] = renderMount(m)
	}
	return out
}

// namedKeyed keys Project volumes by logical name.
func namedKeyed(named []msb.NamedMount) map[string]string {
	out := make(map[string]string, len(named))
	for _, n := range named {
		out[n.Name] = renderNamedMount(n)
	}
	return out
}

// ownedKeyed keys Sandbox volumes by target.
func ownedKeyed(owned []msb.OwnedMount) map[string]string {
	out := make(map[string]string, len(owned))
	for _, o := range owned {
		out[o.Target] = renderOwnedMount(o)
	}
	return out
}

// portsKeyed keys declared guest ports by name.
func portsKeyed(ports []translate.DeclaredPort) map[string]string {
	out := make(map[string]string, len(ports))
	for _, p := range ports {
		out[p.Name] = fmt.Sprintf("guest %d", p.Guest)
	}
	return out
}

// RenderDrift renders drift entries as a bordered table: the entry name,
// then the creation-time value, then what the configuration now declares.
// A value wider than driftValueCap wraps within its cell, so a long
// environment list never widens the table past readability. Each rendered
// line is prefixed with indent. It returns the empty string when there is
// nothing to report. A plain table (the --plain lever) drops the borders
// and keeps every word.
func RenderDrift(entries []DriftEntry, indent string, plain bool) string {
	if len(entries) == 0 {
		return ""
	}
	headers := []string{"setting", "at creation", "current configuration"}
	// Each value column wraps to its widest content, capped; the name
	// column is uncapped because names are short and are the row's key.
	widths := [3]int{utf8.RuneCountInString(headers[0]), utf8.RuneCountInString(headers[1]), utf8.RuneCountInString(headers[2])}
	for _, e := range entries {
		for i, cell := range [...]string{e.Setting, e.Was, e.Now} {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	for i := 1; i < len(widths); i++ {
		widths[i] = min(widths[i], driftValueCap)
	}
	cell := func(s string, w int) string {
		return strings.Join(wrapCell(s, w), "\n")
	}
	rows := make([][]string, len(entries))
	for i, e := range entries {
		rows[i] = []string{e.Setting, cell(e.Was, widths[1]), cell(e.Now, widths[2])}
	}
	lines := strings.Split(ui.Table(plain, headers, rows), "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

// driftValueCap bounds each value column: wider content wraps onto
// continuation lines rather than widening the table past readability.
const driftValueCap = 44

// wrapCell wraps s to w display columns, breaking on spaces where possible
// and hard-breaking any token wider than w. Existing newlines are honored.
func wrapCell(s string, w int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		var line string
		flush := func() {
			lines = append(lines, line)
			line = ""
		}
		accept := func(part string) {
			if line == "" {
				line = part
				return
			}
			if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(part) <= w {
				line += " " + part
				return
			}
			flush()
			line = part
		}
		for _, word := range strings.Split(para, " ") {
			for utf8.RuneCountInString(word) > w {
				r := []rune(word)
				if line != "" {
					flush()
				}
				lines = append(lines, string(r[:w]))
				word = string(r[w:])
			}
			accept(word)
		}
		flush()
	}
	return lines
}

// renderMount renders one bind mount for a drift-table cell.
func renderMount(m msb.Mount) string {
	spec := "bind " + m.Source + " -> " + m.Target
	if m.ReadOnly {
		spec += " (read-only)"
	}
	if m.IsFile {
		spec += " (file)"
	}
	return spec
}

// renderNamedMount renders one mounted Project volume for a drift-table
// cell.
func renderNamedMount(n msb.NamedMount) string {
	spec := n.Name + " at " + n.Target
	if n.Kind == "disk" {
		spec += " (disk, " + n.Size + ")"
	} else if n.Quota != "" {
		spec += " (directory, quota " + n.Quota + ")"
	}
	return spec
}

// renderOwnedMount renders one private Sandbox volume for a drift-table
// cell.
func renderOwnedMount(o msb.OwnedMount) string {
	spec := o.Target
	if o.Kind == "disk" {
		spec += " (disk, " + o.Size + ")"
	}
	return spec
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
