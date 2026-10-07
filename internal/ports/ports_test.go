// Port-policy tests: reservation reuse and picking, collision rules,
// reconciliation, read-only discrepancy reports, and pruning — against a
// real registry in a temporary host state directory.
package ports

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
)

// alwaysFree is a probe that reports every candidate free, for tests that
// do not exercise the bind probe.
func alwaysFree(int) (bool, error) { return true, nil }

// noBackend reports no backend sandboxes and no failures.
func noBackend() ([]int, error) { return nil, nil }

// reservedOf reads the registry's reservations.
func reservedOf(t *testing.T) []state.PortReservation {
	t.Helper()
	records, err := state.LoadPortReservations()
	if err != nil {
		t.Fatalf("reading the registry: %v", err)
	}
	return records
}

func reservation(sandbox, name string, guest, port int) state.PortReservation {
	return state.PortReservation{Sandbox: sandbox, Name: name, Guest: guest, Port: port}
}

// TestReserveReusesAndPicks checks the two ways a port is chosen: a
// sandbox's own reservations are reused in preference (whatever the
// backend or the loopback now says), and new picks scan 4001 upward
// in order.
func TestReserveReusesAndPicks(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// First sandbox picks 4001.
	got, err := Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, noBackend, alwaysFree)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(got) != 1 || got[0].Host != 4001 {
		t.Errorf("first Reserve = %v, want port 4001", got)
	}

	// A second sandbox skips the held port.
	got, err = Reserve("app-wt2-22222222", []Declared{{Name: "web", Guest: 4000}}, noBackend, alwaysFree)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(got) != 1 || got[0].Host != 4002 {
		t.Errorf("second Reserve = %v, want port 4002", got)
	}

	// Re-running the first sandbox reuses its reservation.
	got, err = Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, noBackend, alwaysFree)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(got) != 1 || got[0].Host != 4001 {
		t.Errorf("reused Reserve = %v, want port 4001", got)
	}
	if records := reservedOf(t); len(records) != 2 {
		t.Errorf("registry holds %d records, want 2: %v", len(records), records)
	}
}

// TestReserveSkipsBackendPublishedPorts checks that a candidate the
// backend's existing sandboxes publish is ineligible even though no
// registry record holds it and the loopback probe would pass: msb accepts
// duplicate published ports, so sbx must not rely on a collision there.
func TestReserveSkipsBackendPublishedPorts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	backend := func() ([]int, error) { return []int{4001, 4002}, nil }

	got, err := Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, backend, alwaysFree)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(got) != 1 || got[0].Host != 4003 {
		t.Errorf("Reserve = %v, want port 4003 (4001 and 4002 are published by the backend)", got)
	}
}

// TestReserveSkipsProbeOccupiedPorts checks that a port occupied on the
// host loopback — a bind/probe race with a process outside sbx — is
// skipped, and that a probe failure aborts with nothing reserved.
func TestReserveSkipsProbeOccupiedPorts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Port 4001 is genuinely occupied on the loopback.
	listener, err := net.Listen("tcp", "127.0.0.1:4001")
	if err != nil {
		t.Fatalf("binding 4001 to simulate occupation: %v", err)
	}
	defer listener.Close()
	probe := func(port int) (bool, error) {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false, nil
		}
		l.Close()
		return true, nil
	}

	got, err := Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, noBackend, probe)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(got) != 1 || got[0].Host != 4002 {
		t.Errorf("Reserve = %v, want port 4002 (4001 is occupied)", got)
	}
}

// TestReserveFailsClosedOnBackendInspectionFailure checks that a failed
// backend inspection aborts the reservation before anything is written:
// an uninspectable backend is never read as an empty one.
func TestReserveFailsClosedOnBackendInspectionFailure(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	boom := errors.New("inspect failed")
	backend := func() ([]int, error) { return nil, boom }

	if _, err := Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, backend, alwaysFree); !errors.Is(err, boom) {
		t.Fatalf("Reserve error = %v, want the inspection failure", err)
	}
	if records := reservedOf(t); len(records) != 0 {
		t.Errorf("a failed inspection reserved %v anyway", records)
	}
}

// TestReserveProbeFailureAborts checks that a probe error — not merely an
// occupied port — fails the reservation.
func TestReserveProbeFailureAborts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	boom := errors.New("probe failed")
	if _, err := Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, noBackend, func(int) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Fatalf("Reserve error = %v, want the probe failure", err)
	}
}

// TestReserveExhaustedRange checks the error when every port in the range
// is spoken for.
func TestReserveExhaustedRange(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Fill the registry with reservations for the whole range.
	records := make([]state.PortReservation, 0, LastPort-FirstPort+1)
	for port := FirstPort; port <= LastPort; port++ {
		records = append(records, reservation(fmt.Sprintf("filler-%d", port), "web", 4000, port))
	}
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return records, nil
	}); err != nil {
		t.Fatalf("seeding the registry: %v", err)
	}

	_, err := Reserve("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, noBackend, alwaysFree)
	if err == nil {
		t.Fatal("Reserve succeeded with an exhausted range")
	}
	for _, want := range []string{"no host port is available", "4001", "4099"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
}

// TestReserveRejectsDuplicateRegistryRecords checks the corrupt-registry
// guard: two records holding one port make every later decision unsound.
func TestReserveRejectsDuplicateRegistryRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{
			reservation("a-1-11111111", "web", 4000, 4001),
			reservation("b-1-22222222", "web", 4000, 4001),
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Reserve("c-1-33333333", []Declared{{Name: "web", Guest: 4000}}, noBackend, alwaysFree); err == nil {
		t.Fatal("Reserve succeeded on a corrupt registry")
	}
}

// TestReserveConcurrentCallersGetDisjointPorts checks the machine-wide
// guarantee two concurrent creators rely on: neither receives a port the
// other reserved.
func TestReserveConcurrentCallersGetDisjointPorts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	declared := []Declared{{Name: "web", Guest: 4000}, {Name: "metrics", Guest: 9090}}

	var wg sync.WaitGroup
	results := make(chan []Assignment, 3)
	errs := make(chan error, 3)
	for i := range 3 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := Reserve(fmt.Sprintf("app-wt%d-%08d", i, i), declared, noBackend, alwaysFree)
			if err != nil {
				errs <- err
				return
			}
			results <- got
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Reserve: %v", err)
	}
	close(results)
	seen := map[int]bool{}
	for got := range results {
		for _, a := range got {
			if seen[a.Host] {
				t.Errorf("two concurrent callers received port %d", a.Host)
			}
			seen[a.Host] = true
		}
	}
	if len(seen) != 6 {
		t.Errorf("concurrent callers received %d distinct ports, want 6", len(seen))
	}
}

// TestReconcileCorrectsStaleRecords checks that a mutating correction
// replaces a stale registry entry with what the backend reports.
func TestReconcileCorrectsStaleRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// The registry says 4005; the sandbox actually publishes 4001.
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{reservation("app-wt1-11111111", "web", 4000, 4005)}, nil
	}); err != nil {
		t.Fatal(err)
	}

	err := Reconcile("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}},
		[]msb.PublishedPort{{HostPort: 4001, GuestPort: 4000}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	records := reservedOf(t)
	if len(records) != 1 || records[0].Port != 4001 {
		t.Errorf("registry after reconcile = %v, want port 4001", records)
	}
}

// TestReconcileFailsRatherThanTakesAnotherReservation checks the conflict
// rule: a correction that would take a port another reservation holds
// fails and leaves the registry unchanged.
func TestReconcileFailsRatherThanTakesAnotherReservation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{reservation("other-wt2-22222222", "web", 4000, 4001)}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// The sandbox reports 4001, which belongs to another reservation.
	err := Reconcile("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}},
		[]msb.PublishedPort{{HostPort: 4001, GuestPort: 4000}})
	if err == nil {
		t.Fatal("Reconcile took another reservation's port")
	}
	if records := reservedOf(t); len(records) != 1 || records[0].Sandbox != "other-wt2-22222222" {
		t.Errorf("registry changed on a refused reconcile: %v", records)
	}
}

// TestReconcileAcceptsMatchingRecords checks that a matching report leaves
// the registry alone.
func TestReconcileAcceptsMatchingRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{reservation("app-wt1-11111111", "web", 4000, 4001)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}},
		[]msb.PublishedPort{{HostPort: 4001, GuestPort: 4000}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// TestReconcileFailsOnUnreportedGuest checks that a sandbox not reporting a
// declared guest port cannot be reconciled silently.
func TestReconcileFailsOnUnreportedGuest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := Reconcile("app-wt1-11111111", []Declared{{Name: "web", Guest: 4000}}, nil); err == nil {
		t.Fatal("Reconcile succeeded with no reported port for the declared guest")
	}
}

// TestDiscrepancies checks the read-only report: matching state reports
// nothing, and each mismatch is named.
func TestDiscrepancies(t *testing.T) {
	t.Run("agreement", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
			return []state.PortReservation{reservation("app-wt1-11111111", "web", 4000, 4001)}, nil
		}); err != nil {
			t.Fatal(err)
		}
		lines, err := Discrepancies("app-wt1-11111111", []msb.PublishedPort{{HostPort: 4001, GuestPort: 4000}})
		if err != nil {
			t.Fatalf("Discrepancies: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("Discrepancies = %v, want none", lines)
		}
	})
	t.Run("stale reservation", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
			return []state.PortReservation{reservation("app-wt1-11111111", "web", 4000, 4005)}, nil
		}); err != nil {
			t.Fatal(err)
		}
		lines, err := Discrepancies("app-wt1-11111111", []msb.PublishedPort{{HostPort: 4001, GuestPort: 4000}})
		if err != nil {
			t.Fatalf("Discrepancies: %v", err)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "the registry reserves 4005") || !strings.Contains(lines[0], "reports 4001") {
			t.Errorf("Discrepancies = %v, want the stale reservation named", lines)
		}
	})
	t.Run("unregistered publication", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		lines, err := Discrepancies("app-wt1-11111111", []msb.PublishedPort{{HostPort: 4001, GuestPort: 4000}})
		if err != nil {
			t.Fatalf("Discrepancies: %v", err)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "holds no reservation") {
			t.Errorf("Discrepancies = %v, want the missing reservation named", lines)
		}
	})
}

// TestPruneKeepsStoppedAndRemovesGone checks pruning against a caller's
// existence report: stopped sandboxes are kept, gone ones are removed.
func TestPruneKeepsStoppedAndRemovesGone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{
			reservation("app-running-11111111", "web", 4000, 4001),
			reservation("app-stopped-22222222", "web", 4000, 4002),
			reservation("app-gone-33333333", "web", 4000, 4003),
		}, nil
	}); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(func(sandbox string) bool {
		return sandbox == "app-running-11111111" || sandbox == "app-stopped-22222222"
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 1 || removed[0].Sandbox != "app-gone-33333333" {
		t.Errorf("removed = %v, want only app-gone-33333333", removed)
	}
	records := reservedOf(t)
	if len(records) != 2 {
		t.Errorf("registry after prune = %v, want the running and stopped sandboxes kept", records)
	}
}
