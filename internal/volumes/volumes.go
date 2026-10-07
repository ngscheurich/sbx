// Package volumes decides whether the Project volumes a worktree declares
// are compatible with the volumes the backend already holds — before any
// sandbox mutation, and in the read-only Plan (ADR-0003). Sibling
// worktrees derive the same backend names from the common Git directory,
// so two branches can declare the same Project volume differently; sbx
// rejects such a conflict instead of splitting or resizing storage. sbx
// never resizes, overwrites, or deletes an existing volume.
//
// # Unit conversion
//
// TOML sizes and quotas use msb's format — an integer with a K, M, or G
// suffix — in binary units: 1K is 1024 bytes, 1M is 1048576 bytes, and 1G
// is 1073741824 bytes, matching msb, which reports a 1G disk volume as
// capacity_bytes 1073741824 (verified on msb 0.7.3 and 0.7.6). The
// machine-readable `msb volumes --format json` listing reports a disk's
// size as capacity_bytes and a directory's quota as quota_mib in whole
// MiB. A declared size therefore converts to bytes and compares with
// capacity_bytes directly; a declared quota converts to bytes and
// compares with quota_mib × 1048576, so a sub-MiB quota can never match
// an integer MiB report. Whether msb ever populates quota_mib is
// UNVERIFIED — no CLI option sets a named volume's quota — so a declared
// quota meets a null report as a conflict, never as a match.
package volumes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ngscheurich/sbx/internal/msb"
)

// Declared is one Project volume definition from a worktree's sbx.toml,
// with the backend name derived from the common Git directory.
type Declared struct {
	// Logical is the volume's name in sbx.toml.
	Logical string
	// Backend is the derived backend volume name.
	Backend string
	// Target is the absolute guest mount point.
	Target string
	// Kind is "dir" (default) or "disk".
	Kind string
	// Size is the declared disk size in msb's format, empty for
	// directories.
	Size string
	// Quota is the declared directory quota in msb's format, empty when
	// no quota is declared.
	Quota string
}

// Report is the outcome of checking declared Project volumes against the
// backend's existing volumes.
type Report struct {
	// Conflicts lists the declared volumes whose definitions clash with
	// the existing volume under the same backend name.
	Conflicts []Conflict
	// Reused lists the logical names whose definition matches the existing
	// volume, so creation reuses the stored data.
	Reused []string
	// Fresh lists the logical names no backend volume exists for yet, so
	// creation makes them.
	Fresh []string
}

// Conflict is one declared Project volume whose definition disagrees with
// the existing backend volume of the same derived name.
type Conflict struct {
	// Declared is the worktree's definition.
	Declared Declared
	// Existing is the backend's record.
	Existing msb.VolumeInfo
	// Mismatches are the disagreeing aspects: kind, size, or quota.
	Mismatches []string
}

// String renders the conflict with both definitions and the way out:
// choose another logical name. It never suggests deleting or resizing the
// existing volume — sbx never destroys shared data.
func (c Conflict) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "project volume %q (backend name %q):\n", c.Declared.Logical, c.Declared.Backend)
	fmt.Fprintf(&b, "  declared in sbx.toml: %s\n", describeDeclared(c.Declared))
	fmt.Fprintf(&b, "  existing volume:      %s\n", describeExisting(c.Existing))
	for _, m := range c.Mismatches {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	fmt.Fprintf(&b, "choose another logical name in sbx.toml, such as %q; sbx never resizes, overwrites, or deletes an existing volume",
		c.Declared.Logical+"2")
	return b.String()
}

// FormatConflicts renders every conflict for an error or report, one per
// paragraph.
func FormatConflicts(conflicts []Conflict) string {
	parts := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, "\n\n")
}

// Check compares every declared Project volume with the backend's existing
// volumes. A declared volume with no existing record is fresh; one whose
// kind, size, and quota all match is reused; anything else is a conflict.
// A record whose kind sbx does not recognize is malformed inspection, an
// error — never a silent match and never a fixable-looking conflict.
func Check(declared []Declared, existing []msb.VolumeInfo) (Report, error) {
	byName := make(map[string]msb.VolumeInfo, len(existing))
	for _, v := range existing {
		if _, err := normalizeKind(v.Kind); err != nil {
			return Report{}, fmt.Errorf("volume %q in the backend's listing has an unrecognized kind: %w", v.Name, err)
		}
		byName[v.Name] = v
	}
	var report Report
	for _, d := range declared {
		v, ok := byName[d.Backend]
		if !ok {
			report.Fresh = append(report.Fresh, d.Logical)
			continue
		}
		kind, _ := normalizeKind(v.Kind)
		mismatches, err := compare(d, v, kind)
		if err != nil {
			return Report{}, err
		}
		if len(mismatches) > 0 {
			report.Conflicts = append(report.Conflicts, Conflict{Declared: d, Existing: v, Mismatches: mismatches})
		} else {
			report.Reused = append(report.Reused, d.Logical)
		}
	}
	return report, nil
}

// compare lists the aspects on which a declared Project volume and the
// existing backend volume disagree. Null capacity or quota reports where
// the declaration needs a value are mismatches, not matches: an
// unreported property proves nothing.
func compare(d Declared, v msb.VolumeInfo, existingKind string) ([]string, error) {
	var mismatches []string
	if existingKind != d.Kind {
		mismatches = append(mismatches, fmt.Sprintf("kind: declared %q, existing %q", d.Kind, v.Kind))
	}
	if d.Kind == "disk" {
		want, err := ParseSize(d.Size)
		if err != nil {
			return nil, fmt.Errorf("project volume %q: %w", d.Logical, err)
		}
		switch {
		case v.CapacityBytes == nil:
			mismatches = append(mismatches, fmt.Sprintf("size: declared %q (%d bytes), but the existing volume reports no capacity", d.Size, want))
		case *v.CapacityBytes != want:
			mismatches = append(mismatches, fmt.Sprintf("size: declared %q (%d bytes), existing capacity %d bytes", d.Size, want, *v.CapacityBytes))
		}
		return mismatches, nil
	}
	// Directory volumes carry a quota, never a size. msb reports the quota
	// as whole MiB; the declared value converts to bytes and compares
	// against quota_mib × 1048576.
	switch {
	case d.Quota == "":
		if v.QuotaMiB != nil && *v.QuotaMiB != 0 {
			mismatches = append(mismatches, fmt.Sprintf("quota: declared none, existing quota %d MiB", *v.QuotaMiB))
		}
	default:
		want, err := ParseSize(d.Quota)
		if err != nil {
			return nil, fmt.Errorf("project volume %q: %w", d.Logical, err)
		}
		switch {
		case v.QuotaMiB == nil:
			mismatches = append(mismatches, fmt.Sprintf("quota: declared %q (%d bytes), but the existing volume reports no quota", d.Quota, want))
		case *v.QuotaMiB*1048576 != want:
			mismatches = append(mismatches, fmt.Sprintf("quota: declared %q (%d bytes), existing quota %d MiB", d.Quota, want, *v.QuotaMiB))
		}
	}
	return mismatches, nil
}

// normalizeKind maps the kind strings msb reports onto sbx's two volume
// kinds. Anything else is unrecognized: the check fails closed rather than
// guess at compatibility.
func normalizeKind(kind string) (string, error) {
	switch strings.ToLower(kind) {
	case "dir", "directory":
		return "dir", nil
	case "disk":
		return "disk", nil
	default:
		return "", fmt.Errorf("kind %q is neither \"dir\" nor \"disk\"", kind)
	}
}

// describeDeclared renders a declared definition for a conflict report.
func describeDeclared(d Declared) string {
	if d.Kind == "disk" {
		return fmt.Sprintf("disk volume, size %q", d.Size)
	}
	if d.Quota != "" {
		return fmt.Sprintf("directory volume, quota %q", d.Quota)
	}
	return "directory volume"
}

// describeExisting renders the backend's record for a conflict report,
// keeping null reports visible as such rather than as zeroes.
func describeExisting(v msb.VolumeInfo) string {
	kind, err := normalizeKind(v.Kind)
	if err != nil {
		kind = fmt.Sprintf("kind %q", v.Kind)
	}
	var b strings.Builder
	if kind == "disk" {
		b.WriteString("disk volume")
	} else {
		b.WriteString("directory volume")
	}
	if v.CapacityBytes != nil {
		fmt.Fprintf(&b, ", capacity %d bytes", *v.CapacityBytes)
	}
	if v.QuotaMiB != nil {
		fmt.Fprintf(&b, ", quota %d MiB", *v.QuotaMiB)
	}
	return b.String()
}

// sizePattern is msb's size format: a nonzero integer without leading
// zeros, with a K, M, or G suffix. Strict parsing matches configuration
// validation, so a size that passed sbx.toml always parses here.
var sizePattern = regexp.MustCompile(`^([1-9][0-9]*)([KMG])$`)

// ParseSize converts a size in msb's TOML format to bytes, in binary
// units: 1K is 1024 bytes, 1M is 1048576 bytes, 1G is 1073741824 bytes.
// The result compares with the JSON listing's capacity_bytes directly,
// and with quota_mib × 1048576 for quotas.
func ParseSize(s string) (int64, error) {
	m := sizePattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("size %q is not an integer with a K, M, or G suffix, such as 512M", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q overflows: %w", s, err)
	}
	var unit int64
	switch m[2] {
	case "K":
		unit = 1 << 10
	case "M":
		unit = 1 << 20
	case "G":
		unit = 1 << 30
	}
	return n * unit, nil
}
