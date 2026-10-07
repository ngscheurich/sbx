package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBootstrapMarkerRoundTrip checks that a marker survives saving and
// loading, that a missing one is ErrNoBootstrapMarker rather than an empty
// one, and that deletion is idempotent.
func TestBootstrapMarkerRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if _, err := LoadBootstrapMarker("app-main-12345678"); !errors.Is(err, ErrNoBootstrapMarker) {
		t.Errorf("load before save = %v, want ErrNoBootstrapMarker", err)
	}

	marker := BootstrapMarker{
		Sandbox:        "app-main-12345678",
		CreatedAt:      "2024-05-01T10:00:00Z",
		DefinitionHash: BootstrapHash("echo hi"),
	}
	if err := SaveBootstrapMarker("app-main-12345678", marker); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadBootstrapMarker("app-main-12345678")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded != marker {
		t.Errorf("round trip changed the marker:\n got %+v\nwant %+v", loaded, marker)
	}

	if err := DeleteBootstrapMarker("app-main-12345678"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := LoadBootstrapMarker("app-main-12345678"); !errors.Is(err, ErrNoBootstrapMarker) {
		t.Errorf("load after delete = %v, want ErrNoBootstrapMarker", err)
	}
	if err := DeleteBootstrapMarker("app-main-12345678"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

// TestBootstrapMarkerCorrupt checks that an unreadable marker is an error,
// never silently treated as completion or as absence.
func TestBootstrapMarkerCorrupt(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	dir := filepath.Join(base, "sbx", "bootstrap")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app-main-12345678.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadBootstrapMarker("app-main-12345678")
	if err == nil || errors.Is(err, ErrNoBootstrapMarker) {
		t.Errorf("load of a corrupt marker = %v, want an unreadable-marker error", err)
	}
}

// TestBootstrapHash pins the hash's shape and its sensitivity to the exact
// definition text.
func TestBootstrapHash(t *testing.T) {
	h := BootstrapHash("echo hi")
	if !strings.HasPrefix(h, "sha256:") {
		t.Errorf("BootstrapHash = %q, want a sha256: prefix", h)
	}
	if h != BootstrapHash("echo hi") {
		t.Error("BootstrapHash is not stable")
	}
	if h == BootstrapHash("echo hi ") || h == BootstrapHash("echo bye") {
		t.Error("BootstrapHash does not distinguish different definitions")
	}
}

// TestSandboxLockSerialization checks the cross-process lock's shape within
// one process: a second TrySandboxLock reports contention without blocking,
// and the lock is takeable again after Release.
func TestSandboxLockSerialization(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	first, held, err := TrySandboxLock("app-main-12345678")
	if err != nil || !held {
		t.Fatalf("first try: held=%v, err=%v", held, err)
	}
	if _, held, err := TrySandboxLock("app-main-12345678"); err != nil || held {
		t.Errorf("second try while held: held=%v, err=%v, want held=false without an error", held, err)
	}

	// A blocking acquisition waits for Release.
	acquired := make(chan *SandboxLock, 1)
	go func() {
		l, err := LockSandbox("app-main-12345678")
		if err != nil {
			t.Errorf("blocking lock: %v", err)
			return
		}
		acquired <- l
	}()
	select {
	case <-acquired:
		t.Fatal("the blocking lock was taken while still held")
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case l := <-acquired:
		if err := l.Release(); err != nil {
			t.Fatalf("second release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the blocking lock was not taken after Release")
	}

	// Distinct sandboxes do not contend.
	other, held, err := TrySandboxLock("app-main-87654321")
	if err != nil || !held {
		t.Fatalf("a different sandbox's lock: held=%v, err=%v", held, err)
	}
	_ = other.Release()
}
