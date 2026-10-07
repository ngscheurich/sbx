package msb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// VolumeInfo is one record of `msb volumes --format json`. msb 0.7.3–0.7.6
// report name, kind, capacity_bytes, and quota_mib (verified on 0.7.3 and
// 0.7.6): a disk volume shows a populated capacity_bytes — 1073741824 for
// 1G — with a null quota_mib, and a plain directory volume shows both null.
// A populated quota_mib has never been observed, because no msb CLI option
// sets a named volume's quota; the pointer keeps null distinguishable from
// zero so an unreported quota is never read as "no quota" or vice versa.
type VolumeInfo struct {
	// Name is the volume's literal backend name.
	Name string `json:"name"`
	// Kind is the volume kind msb reports, such as "dir" or "disk".
	Kind string `json:"kind"`
	// CapacityBytes is a disk volume's capacity in bytes, nil when msb
	// reports null.
	CapacityBytes *int64 `json:"capacity_bytes"`
	// QuotaMiB is a directory volume's quota in whole MiB, nil when msb
	// reports null.
	QuotaMiB *int64 `json:"quota_mib"`
}

// Volumes runs `msb volumes --format json` and returns every volume the
// backend knows. The Project volume compatibility check depends on this
// listing, so it fails closed: a failed command, unparseable output, or a
// record without a name or kind is an error, never an empty list — an
// unreadable backend must not look like one without conflicting volumes.
func (c CLI) Volumes(ctx context.Context) ([]VolumeInfo, error) {
	out, err := c.run(ctx, "volumes", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("listing the backend's volumes: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	var records []VolumeInfo
	if err := json.Unmarshal([]byte(trimmed), &records); err != nil {
		return nil, fmt.Errorf("parsing msb volumes output: %w", err)
	}
	for _, v := range records {
		if v.Name == "" || v.Kind == "" {
			return nil, fmt.Errorf("parsing msb volumes output: a record is missing its name or kind: %s", trimmed)
		}
	}
	return records, nil
}
