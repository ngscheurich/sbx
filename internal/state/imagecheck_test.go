// Image-check success records: host-state records that an image's contents
// passed the project's image-check script. Successes are keyed by the
// image's manifest digest and the script's hash — never by tag — so a
// repointed tag or an edited script is never covered by an old pass.
package state

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestImageCheckScriptHashDistinquishesScripts checks that the script hash
// identifies the script's exact contents, like the Bootstrap definition
// hash does.
func TestImageCheckScriptHashDistinquishesScripts(t *testing.T) {
	a := ImageCheckScriptHash("#!/bin/sh\nexit 0\n")
	b := ImageCheckScriptHash("#!/bin/sh\nexit 1\n")
	if a == b {
		t.Errorf("different scripts hash alike: %q", a)
	}
	if again := ImageCheckScriptHash("#!/bin/sh\nexit 0\n"); again != a {
		t.Errorf("the same script hashes differently: %q then %q", a, again)
	}
	if want := "sha256:"; len(a) < len(want) || a[:len(want)] != want {
		t.Errorf("script hash %q does not carry the sha256 prefix", a)
	}
}

// TestImageCheckSuccessRoundTrip checks that a recorded success is found
// again for the same digest and script, and never for other contents.
func TestImageCheckSuccessRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	digest := "sha256:abc123"
	script := ImageCheckScriptHash("#!/bin/sh\ntrue\n")
	if err := SaveImageCheckSuccess(digest, script); err != nil {
		t.Fatalf("saving the success: %v", err)
	}

	ok, err := HasImageCheckSuccess(digest, script)
	if err != nil {
		t.Fatalf("checking the recorded success: %v", err)
	}
	if !ok {
		t.Errorf("no success found for the saved digest and script")
	}

	// A different digest (a repointed tag's new contents) or a different
	// script is not covered by the record.
	for _, tc := range [][2]string{
		{"sha256:other", script},
		{digest, ImageCheckScriptHash("#!/bin/sh\nfalse\n")},
	} {
		ok, err := HasImageCheckSuccess(tc[0], tc[1])
		if err != nil {
			t.Fatalf("checking an unrecorded combination: %v", err)
		}
		if ok {
			t.Errorf("digest %s with script %s is covered by a record for other contents", tc[0], tc[1])
		}
	}
}

// TestHasImageCheckSuccessMissingRecord checks that no record is reported
// as no success — never as an error and never as a pass.
func TestHasImageCheckSuccessMissingRecord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ok, err := HasImageCheckSuccess("sha256:none", ImageCheckScriptHash("#!/bin/sh\ntrue\n"))
	if err != nil {
		t.Fatalf("checking without any record: %v", err)
	}
	if ok {
		t.Errorf("an unrecorded image check counts as passed")
	}
}

// TestHasImageCheckSuccessCorruptRecordIsError checks that a corrupt record
// fails closed: an unreadable success is never treated as a pass.
func TestHasImageCheckSuccessCorruptRecordIsError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	digest := "sha256:abc123"
	script := ImageCheckScriptHash("#!/bin/sh\ntrue\n")
	if err := SaveImageCheckSuccess(digest, script); err != nil {
		t.Fatalf("saving the success: %v", err)
	}
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "imagecheck")
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no image-check record directory under %s (err %v)", path, err)
	}
	if err := os.WriteFile(filepath.Join(path, entries[0].Name()), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HasImageCheckSuccess(digest, script); err == nil {
		t.Errorf("a corrupt record was treated as a pass")
	} else if !errors.Is(err, ErrCorruptImageCheckRecord) {
		t.Errorf("error = %v, want it to wrap ErrCorruptImageCheckRecord", err)
	}
}

// TestImageCheckRecordNeverHoldsScriptContent checks that the record file
// names the image contents and script hash only — never the script's
// contents or path beyond its hash.
func TestImageCheckRecordNeverHoldsScriptContent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	script := "#!/bin/sh\necho a-very-distinctive-string\n"
	if err := SaveImageCheckSuccess("sha256:abc123", ImageCheckScriptHash(script)); err != nil {
		t.Fatalf("saving the success: %v", err)
	}
	dir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "imagecheck")
	entries, _ := os.ReadDir(dir)
	var found []byte
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, data...)
	}
	if len(found) == 0 {
		t.Fatalf("no image-check record was written under %s", dir)
	}
	if bytes.Contains(found, []byte("a-very-distinctive-string")) {
		t.Errorf("the record file holds the script's contents:\n%s", found)
	}
}
