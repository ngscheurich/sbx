// Port-registry tests: the records' round trip, the atomic update under
// the cross-process lock, and concurrent callers never receiving the same
// candidate.
package state

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestPortRegistryRoundTrip checks that reservations survive saving and
// loading, and that a missing registry reads as no reservations.
func TestPortRegistryRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	records, err := LoadPortReservations()
	if err != nil {
		t.Fatalf("load with no registry: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("missing registry read as %v, want none", records)
	}

	want := []PortReservation{
		{Sandbox: "app-wt1-12345678", Name: "web", Guest: 4000, Port: 4001},
		{Sandbox: "app-wt2-9abcdef0", Name: "web", Guest: 4000, Port: 4002},
	}
	if err := UpdatePortReservations(func(records []PortReservation) ([]PortReservation, error) {
		return want, nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	records, err = LoadPortReservations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 2 || records[0] != want[0] || records[1] != want[1] {
		t.Errorf("registry = %v, want %v", records, want)
	}
}

// TestPortRegistryUpdateAbortsOnError checks that a mutate error writes
// nothing.
func TestPortRegistryUpdateAbortsOnError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	boom := errors.New("boom")
	if err := UpdatePortReservations(func(records []PortReservation) ([]PortReservation, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("update error = %v, want boom", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "ports", "registry.json")); !os.IsNotExist(err) {
		t.Errorf("a failed update wrote a registry file: %v", err)
	}
}

// TestPortRegistryConcurrentUpdates checks that concurrent read-modify-write
// cycles — different processes, or different locks in one — both land: the
// lock serializes them, so neither update is lost.
func TestPortRegistryConcurrentUpdates(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, sandbox := range []string{"app-wt1-12345678", "app-wt2-9abcdef0"} {
		wg.Add(1)
		go func(i int, sandbox string) {
			defer wg.Done()
			errs <- UpdatePortReservations(func(records []PortReservation) ([]PortReservation, error) {
				return append(records, PortReservation{
					Sandbox: sandbox, Name: "web", Guest: 4000, Port: 4001 + i,
				}), nil
			})
		}(i, sandbox)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}
	records, err := LoadPortReservations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("registry has %d records after concurrent updates, want both: %v", len(records), records)
	}
}

// TestPortRegistryCorruptIsAnError checks that a corrupt registry file is
// an error, never silently empty.
func TestPortRegistryCorruptIsAnError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "ports", "registry.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPortReservations(); err == nil {
		t.Error("loading a corrupt registry succeeded, want an error")
	}
}
