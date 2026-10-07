package msb

import (
	"context"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// TestVolumesParsesListing checks the machine-readable volume listing,
// including the non-null capacity and quota reports the compatibility
// check compares against.
func TestVolumesParsesListing(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}

	fake.SeedVolume(t, "plain-cache", "dir", nil, nil)
	fake.SeedVolume(t, "data-disk", "disk", testsupport.Int64(8589934592), nil)
	fake.SeedVolume(t, "capped", "dir", nil, testsupport.Int64(4096))

	vols, err := box.Volumes(context.Background())
	if err != nil {
		t.Fatalf("Volumes: %v", err)
	}
	if len(vols) != 3 {
		t.Fatalf("Volumes returned %d records, want 3: %+v", len(vols), vols)
	}
	byName := map[string]VolumeInfo{}
	for _, v := range vols {
		byName[v.Name] = v
	}
	plain := byName["plain-cache"]
	if plain.Kind != "dir" || plain.CapacityBytes != nil || plain.QuotaMiB != nil {
		t.Errorf("plain directory volume = %+v, want null capacity and quota", plain)
	}
	disk := byName["data-disk"]
	if disk.Kind != "disk" || disk.CapacityBytes == nil || *disk.CapacityBytes != 8589934592 {
		t.Errorf("disk volume = %+v, want capacity 8589934592", disk)
	}
	if disk.QuotaMiB != nil {
		t.Errorf("disk volume quota = %v, want null", *disk.QuotaMiB)
	}
	capped := byName["capped"]
	if capped.QuotaMiB == nil || *capped.QuotaMiB != 4096 {
		t.Errorf("quota-limited volume = %+v, want quota 4096 MiB", capped)
	}
}

// TestVolumesEmptyListing checks that a backend without volumes yields an
// empty list, not an error.
func TestVolumesEmptyListing(t *testing.T) {
	testsupport.FakeMSB(t)
	vols, err := CLI{}.Volumes(context.Background())
	if err != nil {
		t.Fatalf("Volumes: %v", err)
	}
	if len(vols) != 0 {
		t.Errorf("Volumes = %+v, want empty", vols)
	}
}

// TestVolumesFailureIsNotEmpty checks that a failed listing is an error,
// never read as a backend without volumes.
func TestVolumesFailureIsNotEmpty(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_VOLUMES_FAIL", "1")
	_, err := CLI{}.Volumes(context.Background())
	if err == nil {
		t.Fatal("Volumes succeeded when the backend failed")
	}
	if !strings.Contains(err.Error(), "volumes") {
		t.Errorf("error does not name the volumes listing: %v", err)
	}
}

// TestVolumesMalformedIsNotEmpty checks that unparseable output is an
// error, never read as a backend without volumes.
func TestVolumesMalformedIsNotEmpty(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_VOLUMES_MALFORMED", "1")
	if _, err := (CLI{}).Volumes(context.Background()); err == nil {
		t.Fatal("Volumes accepted malformed output")
	}
}

// TestVolumesRejectsNamelessRecord checks that a record missing its name
// or kind is malformed output, not a volume that never matches.
func TestVolumesRejectsNamelessRecord(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	fake.SeedVolume(t, "broken", "", nil, nil)
	_, err := CLI{}.Volumes(context.Background())
	if err == nil {
		t.Fatal("Volumes accepted a record without a kind")
	}
	if !strings.Contains(err.Error(), "missing its name or kind") {
		t.Errorf("error does not describe the malformed record: %v", err)
	}
}
