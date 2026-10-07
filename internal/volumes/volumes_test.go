package volumes

import (
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/msb"
)

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"1K", 1024},
		{"512M", 512 * 1048576},
		{"8G", 8589934592},
		{"1G", 1073741824},
	} {
		got, err := ParseSize(tc.in)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", tc.in, err)
		} else if got != tc.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "512", "512MB", "0G", "01G", "1g", "-1M", "1.5G"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) succeeded, want an error", bad)
		}
	}
}

func declared(logical, kind, size, quota string) Declared {
	return Declared{
		Logical: logical,
		Backend: "abc12345-" + logical,
		Target:  "/data",
		Kind:    kind,
		Size:    size,
		Quota:   quota,
	}
}

func i64(v int64) *int64 { return &v }

// TestCheckFreshVolume checks that a declared volume with no existing
// backend record is fresh, not a conflict.
func TestCheckFreshVolume(t *testing.T) {
	report, err := Check([]Declared{declared("cache", "dir", "", "")}, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Conflicts) != 0 || len(report.Reused) != 0 {
		t.Errorf("report = %+v, want everything fresh", report)
	}
	if len(report.Fresh) != 1 || report.Fresh[0] != "cache" {
		t.Errorf("Fresh = %v, want [cache]", report.Fresh)
	}
}

// TestCheckEqualDefinitionsReuse covers the equal-definition cases,
// including the non-null capacity and quota reports: a matching disk size
// or directory quota reuses the stored volume.
func TestCheckEqualDefinitionsReuse(t *testing.T) {
	existing := []msb.VolumeInfo{
		{Name: "abc12345-plain", Kind: "dir"},
		{Name: "abc12345-data", Kind: "disk", CapacityBytes: i64(8589934592)},
		{Name: "abc12345-capped", Kind: "dir", QuotaMiB: i64(4096)},
	}
	declared := []Declared{
		declared("plain", "dir", "", ""),
		declared("data", "disk", "8G", ""),
		declared("capped", "dir", "", "4G"),
	}
	report, err := Check(declared, existing)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("equal definitions conflicted: %+v", report.Conflicts)
	}
	if len(report.Reused) != 3 {
		t.Errorf("Reused = %v, want all three volumes", report.Reused)
	}
}

// TestCheckConflicts covers one mismatch per aspect: kind, disk size, and
// quota in both directions. Each conflict must name the volume, show both
// definitions, and suggest another logical name.
func TestCheckConflicts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared Declared
		existing msb.VolumeInfo
		want     []string
	}{
		{
			name:     "kind differs",
			declared: declared("data", "dir", "", ""),
			existing: msb.VolumeInfo{Name: "abc12345-data", Kind: "disk", CapacityBytes: i64(8589934592)},
			want:     []string{`kind: declared "dir", existing "disk"`},
		},
		{
			name:     "disk size differs",
			declared: declared("data", "disk", "4G", ""),
			existing: msb.VolumeInfo{Name: "abc12345-data", Kind: "disk", CapacityBytes: i64(8589934592)},
			want:     []string{"size: declared \"4G\" (4294967296 bytes), existing capacity 8589934592 bytes"},
		},
		{
			name:     "disk capacity unreported",
			declared: declared("data", "disk", "8G", ""),
			existing: msb.VolumeInfo{Name: "abc12345-data", Kind: "disk"},
			want:     []string{"reports no capacity"},
		},
		{
			name:     "quota differs",
			declared: declared("cache", "dir", "", "2G"),
			existing: msb.VolumeInfo{Name: "abc12345-cache", Kind: "dir", QuotaMiB: i64(4096)},
			want:     []string{"quota: declared \"2G\" (2147483648 bytes), existing quota 4096 MiB"},
		},
		{
			name:     "quota unreported",
			declared: declared("cache", "dir", "", "4G"),
			existing: msb.VolumeInfo{Name: "abc12345-cache", Kind: "dir"},
			want:     []string{"reports no quota"},
		},
		{
			name:     "quota declared nowhere but present",
			declared: declared("cache", "dir", "", ""),
			existing: msb.VolumeInfo{Name: "abc12345-cache", Kind: "dir", QuotaMiB: i64(100)},
			want:     []string{"quota: declared none, existing quota 100 MiB"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := Check([]Declared{tc.declared}, []msb.VolumeInfo{tc.existing})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if len(report.Conflicts) != 1 {
				t.Fatalf("report = %+v, want exactly one conflict", report)
			}
			c := report.Conflicts[0]
			for _, want := range tc.want {
				found := false
				for _, m := range c.Mismatches {
					if strings.Contains(m, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("mismatches %v lack %q", c.Mismatches, want)
				}
			}
			rendered := c.String()
			for _, want := range []string{
				`"` + tc.declared.Logical + `"`,
				`"` + tc.declared.Backend + `"`,
				"declared in sbx.toml:",
				"existing volume:",
				"another logical name",
				"never resizes, overwrites, or deletes",
			} {
				if !strings.Contains(rendered, want) {
					t.Errorf("conflict report is missing %q:\n%s", want, rendered)
				}
			}
		})
	}
}

// TestCheckUnknownKindFailsClosed checks that a kind sbx does not
// recognize is an inspection error, not a match and not a conflict a
// rename could fix.
func TestCheckUnknownKindFailsClosed(t *testing.T) {
	existing := []msb.VolumeInfo{{Name: "abc12345-data", Kind: "zfs"}}
	_, err := Check([]Declared{declared("data", "dir", "", "")}, existing)
	if err == nil {
		t.Fatal("Check accepted an unrecognized kind")
	}
	if !strings.Contains(err.Error(), "zfs") {
		t.Errorf("error does not name the unrecognized kind: %v", err)
	}
}

// TestCheckAcceptsReportedKindSpellings tolerates msb spelling a directory
// volume "directory" in its listing.
func TestCheckAcceptsReportedKindSpellings(t *testing.T) {
	existing := []msb.VolumeInfo{{Name: "abc12345-cache", Kind: "directory"}}
	report, err := Check([]Declared{declared("cache", "dir", "", "")}, existing)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Reused) != 1 {
		t.Errorf("report = %+v, want the volume reused", report)
	}
}

// TestCheckSubMiBQuotaNeverMatches documents the unit conversion edge: the
// listing reports whole MiB, so a declared 512K quota can only conflict.
func TestCheckSubMiBQuotaNeverMatches(t *testing.T) {
	existing := []msb.VolumeInfo{{Name: "abc12345-cache", Kind: "dir", QuotaMiB: i64(1)}}
	report, err := Check([]Declared{declared("cache", "dir", "", "512K")}, existing)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Conflicts) != 1 {
		t.Errorf("a 512K quota matched a 1 MiB report: %+v", report)
	}
}
