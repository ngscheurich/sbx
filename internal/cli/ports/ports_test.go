// Port-reservation tests through the real CLI paths: reservation and
// reuse across worktrees, unmanaged backends and occupied loopbacks,
// stale registry records and their correction, exhausted ranges,
// backend-inspection failures, prune, and the read-only reports.
package ports

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// portsTOML is a valid configuration declaring one named port.
const portsTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"

[ports.web]
guest = 4000
`

// registryPathOf returns the registry file's path under the test's host
// state directory.
func registryPathOf(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "ports", "registry.json")
}

// registryRecords loads the registry through the state package, for tests
// that assert on its content.
func registryRecords(t *testing.T) []state.PortReservation {
	t.Helper()
	records, err := state.LoadPortReservations()
	if err != nil {
		t.Fatalf("reading the registry: %v", err)
	}
	return records
}

// registrySandboxPort returns the recorded host port for one sandbox and
// port name, failing when absent.
func registrySandboxPort(t *testing.T, sandbox, name string) int {
	t.Helper()
	for _, r := range registryRecords(t) {
		if r.Sandbox == sandbox && r.Name == name {
			return r.Port
		}
	}
	t.Fatalf("the registry holds no reservation for %s's %s", sandbox, name)
	return 0
}

// seedRegistry writes reservations directly, simulating records another
// worktree or an earlier run left behind.
func seedRegistry(t *testing.T, records ...state.PortReservation) {
	t.Helper()
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return records, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestUpReservesAndPublishesPorts checks the creation path: the create argv
// carries the --port flags, the registry records the reservation, and
// the output reports the endpoint.
func TestUpReservesAndPublishesPorts(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, portsTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	code, stdout, stderr := harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "127.0.0.1:4001 -> guest 4000") {
		t.Errorf("up does not report the endpoint:\n%s", stdout)
	}
	if got := registrySandboxPort(t, id, "web"); got != 4001 {
		t.Errorf("registry port = %d, want 4001", got)
	}

	var creates []testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" {
			creates = append(creates, c)
		}
	}
	if len(creates) != 1 {
		t.Fatalf("fake msb saw %d create calls", len(creates))
	}
	joined := strings.Join(creates[0].Args, " ")
	if !strings.Contains(joined, "--port 127.0.0.1:4001:4000") {
		t.Errorf("create argv lacks the publish flag: %q", joined)
	}

	// A reuse run neither re-picks nor rewrites: the sandbox's ports change
	// only by recreating it.
	code, stdout, _ = harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("second up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "already running") || !strings.Contains(stdout, "127.0.0.1:4001") {
		t.Errorf("second up does not report reuse with the endpoint:\n%s", stdout)
	}
	if got := registrySandboxPort(t, id, "web"); got != 4001 {
		t.Errorf("registry port after reuse = %d, want 4001", got)
	}
}

// TestPortsDistinctAcrossWorktrees checks two worktrees of one project:
// distinct identities, distinct loopback ports, and each reuse keeping its
// own port.
func TestPortsDistinctAcrossWorktrees(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree1, _ := harness.FixtureRepo(t, portsTOML)
	worktree2, _ := harness.FixtureRepo(t, portsTOML)

	for i, wt := range []string{worktree1, worktree2} {
		if code, _, stderr := harness.SbxUp(t, wt); code != 0 {
			t.Fatalf("up in worktree %d failed: %s", i+1, stderr)
		}
	}
	port1 := registrySandboxPort(t, harness.SandboxIdentityOf(t, worktree1), "web")
	port2 := registrySandboxPort(t, harness.SandboxIdentityOf(t, worktree2), "web")
	if port1 == port2 {
		t.Errorf("both worktrees received port %d", port1)
	}
	// Both creates published, and each on its own port.
	var published []string
	for _, c := range fake.Calls() {
		if c.Args[0] != "create" {
			continue
		}
		for i, a := range c.Args {
			if a == "--port" {
				published = append(published, c.Args[i+1])
			}
		}
	}
	if len(published) != 2 || published[0] == published[1] {
		t.Errorf("creates published %v, want two distinct bindings", published)
	}
}

// TestPortsAvoidUnmanagedSandboxCandidates checks that an unmanaged msb
// sandbox occupying a candidate makes it ineligible — sbx checks the
// backend's published ports, not only its registry, because msb accepts a
// duplicate published port even while the first guest serves it.
func TestPortsAvoidUnmanagedSandboxCandidates(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)

	// An unmanaged sandbox already publishes 4001.
	fake.SeedSandbox(t, "unmanaged-11111111", "nginx:latest", "running", nil)
	fake.SeedPublishedPort(t, "unmanaged-11111111", 4001, 80)

	code, _, stderr := harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	id := harness.SandboxIdentityOf(t, worktree)
	if got := registrySandboxPort(t, id, "web"); got != 4002 {
		t.Errorf("registry port = %d, want 4002 (4001 is published by an unmanaged sandbox)", got)
	}
}

// TestPortsAvoidOccupiedLoopback checks the bind probe: a port occupied on
// the host loopback — by a process no registry or backend knows — is
// skipped.
func TestPortsAvoidOccupiedLoopback(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)

	listener, err := net.Listen("tcp", "127.0.0.1:4001")
	if err != nil {
		t.Fatalf("binding 4001 to simulate occupation: %v", err)
	}
	defer listener.Close()

	code, _, stderr := harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if got := registrySandboxPort(t, harness.SandboxIdentityOf(t, worktree), "web"); got != 4002 {
		t.Errorf("registry port = %d, want 4002 (4001 is occupied on the loopback)", got)
	}
}

// TestUpCorrectsStaleRegistryRecord checks that a mutating command replaces
// a stale registry entry with what inspect reports.
func TestUpCorrectsStaleRegistryRecord(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	// Corrupt the record: the registry now disagrees with the backend.
	seedRegistry(t, state.PortReservation{Sandbox: id, Name: "web", Guest: 4000, Port: 4005})

	code, _, stderr := harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up with a stale registry record failed: %s", stderr)
	}
	if got := registrySandboxPort(t, id, "web"); got != 4001 {
		t.Errorf("registry port after correction = %d, want the reported 4001", got)
	}
	// The correction must not have recreated the sandbox.
	removes := 0
	for _, c := range fake.Calls() {
		if c.Args[0] == "remove" {
			removes++
		}
	}
	if removes != 0 {
		t.Errorf("correction removed the sandbox %d time(s); sbx never recreates to fix a port", removes)
	}
}

// TestUpFailsRatherThanTakesAnotherReservation checks the conflict rule:
// a correction that would take a port another reservation holds fails,
// keeps the registry, and leaves the sandbox in place — never removed to
// retry the port.
func TestUpFailsRatherThanTakesAnotherReservation(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)

	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	// The registry loses our record and holds a conflicting one for the
	// port the sandbox actually publishes.
	seedRegistry(t, state.PortReservation{Sandbox: "other-wt2-22222222", Name: "web", Guest: 4000, Port: 4001})

	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up took a port another reservation holds")
	}
	if !strings.Contains(stderr, "4001") {
		t.Errorf("stderr does not name the conflicting port:\n%s", stderr)
	}
	if got := registrySandboxPort(t, "other-wt2-22222222", "web"); got != 4001 {
		t.Errorf("the other reservation was changed: %d", got)
	}
	// The sandbox itself was never removed: sbx never removes a partially
	// created or working sandbox to retry a port.
	for _, c := range fake.Calls() {
		if c.Args[0] == "remove" {
			t.Errorf("remove was called: %v", c.Args)
		}
	}
}

// TestUpFailsOnExhaustedRange checks that an exhausted range fails before
// anything is created.
func TestUpFailsOnExhaustedRange(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)

	var records []state.PortReservation
	for port := 4001; port <= 4099; port++ {
		records = append(records, state.PortReservation{
			Sandbox: fmt.Sprintf("filler-%d-00000000", port), Name: "web", Guest: 4000, Port: port,
		})
	}
	seedRegistry(t, records...)

	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up succeeded with an exhausted port range")
	}
	if !strings.Contains(stderr, "no host port is available") {
		t.Errorf("stderr does not name the exhausted range:\n%s", stderr)
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" {
			t.Errorf("create was called despite the exhausted range: %v", c.Args)
		}
	}
}

// TestUpFailsClosedOnBackendInspectionFailure checks that when the
// backend's published ports cannot be inspected, up fails before any
// resource changes; an uninspectable backend is never read as an empty one.
func TestUpFailsClosedOnBackendInspectionFailure(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)
	// An existing sandbox's inspection is what fails; the listing itself
	// works, so the failure is the port path's, not the find step's.
	fake.SeedSandbox(t, "unmanaged-11111111", "nginx:latest", "running", nil)
	t.Setenv("FAKE_MSB_INSPECT_FAIL", "1")

	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up succeeded without a backend inspection")
	}
	if !strings.Contains(stderr, "published ports") {
		t.Errorf("stderr does not name the failed inspection:\n%s", stderr)
	}
	if records := registryRecords(t); len(records) != 0 {
		t.Errorf("a failed inspection reserved %v anyway", records)
	}
}

// TestStatusReportsPortsReadOnly checks status: the published mappings and
// any registry discrepancy are reported, without correcting or reserving.
func TestStatusReportsPortsReadOnly(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	// Corrupt the record so status has a discrepancy to report.
	seedRegistry(t, state.PortReservation{Sandbox: id, Name: "web", Guest: 4000, Port: 4005})

	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	if !strings.Contains(stdout, "127.0.0.1:4001 -> guest 4000") {
		t.Errorf("status does not report the authoritative mapping:\n%s", stdout)
	}
	if !strings.Contains(stdout, "the registry reserves 4005") {
		t.Errorf("status does not report the registry discrepancy:\n%s", stdout)
	}
	// Read-only: the registry is untouched.
	if got := registrySandboxPort(t, id, "web"); got != 4005 {
		t.Errorf("status corrected the registry to %d; read-only commands report without correcting", got)
	}
}

// TestDisposableRunPublishesNoPorts checks that a disposable run carries
// no --port even when the configuration declares ports.
func TestDisposableRunPublishesNoPorts(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)

	var stdout, stderr strings.Builder
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "sh"}, nil, &stdout, &stderr)
	})
	_ = stdout
	if code != 0 {
		t.Fatalf("run failed: %s", stderr.String())
	}
	for _, c := range fake.Calls() {
		if c.Args[0] != "run" {
			continue
		}
		for _, a := range c.Args {
			if a == "--port" {
				t.Errorf("disposable run published ports: %v", c.Args)
			}
		}
	}
	// And reserved nothing.
	if records := registryRecords(t); len(records) != 0 {
		t.Errorf("a disposable run reserved %v; disposable runs publish no ports", records)
	}
}

// TestPortPruneKeepsStoppedAndRemovesGone checks prune's rules: stopped
// sandboxes keep their reservations, gone ones lose them, and `rm` alone
// never releases a reservation.
func TestPortPruneKeepsStoppedAndRemovesGone(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if code, _, stderr := harness.SbxRun(t, worktree, []string{"stop"}, nil); code != 0 {
		t.Fatalf("stop failed: %s", stderr)
	}
	// A reservation for a sandbox that no longer exists.
	seedRegistry(t,
		state.PortReservation{Sandbox: id, Name: "web", Guest: 4000, Port: 4001},
		state.PortReservation{Sandbox: "gone-wt9-99999999", Name: "web", Guest: 4000, Port: 4002},
	)

	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"port", "prune"}, nil)
	if code != 0 {
		t.Fatalf("port prune failed: %s", stderr)
	}
	if !strings.Contains(stdout, "gone-wt9-99999999") {
		t.Errorf("prune does not report the removed reservation:\n%s", stdout)
	}
	if got := registrySandboxPort(t, id, "web"); got != 4001 {
		t.Errorf("the stopped sandbox's reservation was pruned (registry says %d); stopped sandboxes keep theirs", got)
	}

	// rm keeps its reservation too: recreation reuses the same port.
	if code, _, stderr := harness.SbxRun(t, worktree, []string{"rm", "--yes"}, nil); code != 0 {
		t.Fatalf("rm failed: %s", stderr)
	}
	if got := registrySandboxPort(t, id, "web"); got != 4001 {
		t.Errorf("rm released the reservation (registry says %d); reservations survive rm until prune", got)
	}

	// Now the sandbox is gone and prune releases the port.
	code, stdout, _ = harness.SbxRun(t, worktree, []string{"port", "prune"}, nil)
	if code != 0 {
		t.Fatalf("second prune failed")
	}
	if !strings.Contains(stdout, id) || !strings.Contains(stdout, "4001") {
		t.Errorf("prune does not report releasing the removed sandbox's port:\n%s", stdout)
	}
	if records := registryRecords(t); len(records) != 0 {
		t.Errorf("registry after pruning everything = %v, want empty", records)
	}

	// Prune with nothing to do says so and succeeds.
	code, stdout, _ = harness.SbxRun(t, worktree, []string{"port", "prune"}, nil)
	if code != 0 || !strings.Contains(stdout, "nothing to prune") {
		t.Errorf("prune with an empty registry = %d:\n%s", code, stdout)
	}
}

// TestPortPruneFailsSafelyWhenInspectionFails checks that a failed backend
// inspection refuses to prune and changes nothing.
func TestPortPruneFailsSafelyWhenInspectionFails(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_LS_FAIL", "1")

	code, _, stderr := harness.SbxRun(t, worktree, []string{"port", "prune"}, nil)
	if code == 0 {
		t.Fatal("prune succeeded without a backend inspection")
	}
	if !strings.Contains(stderr, "inspecting the backend") {
		t.Errorf("stderr does not name the failed inspection:\n%s", stderr)
	}
	if got := registrySandboxPort(t, id, "web"); got != 4001 {
		t.Errorf("a failed inspection pruned anyway (registry says %d)", got)
	}
}

// TestPlanReportsPortsTentatively checks the read-only Plan: declared
// ports appear with their reservation, unreserved ones marked as chosen at
// creation, and no registry is written.
func TestPlanReportsPortsTentatively(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, portsTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	seedRegistry(t, state.PortReservation{Sandbox: id, Name: "web", Guest: 4000, Port: 4007})

	var stdout, stderr strings.Builder
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr.String())
	}
	rendered := stdout.String()
	if !strings.Contains(rendered, "web: guest 4000 -> 127.0.0.1:4007 (reserved)") {
		t.Errorf("plan does not report the reserved port:\n%s", rendered)
	}
	// Read-only: nothing new was reserved.
	if got := registrySandboxPort(t, id, "web"); got != 4007 {
		t.Errorf("plan rewrote the reservation to %d", got)
	}

	// An unreserved port is reported as chosen at creation.
	seedRegistry(t)
	var stdout2, stderr2 strings.Builder
	code = harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout2, &stderr2)
	})
	if code != 0 {
		t.Fatalf("second plan failed: %s", stderr2.String())
	}
	if !strings.Contains(stdout2.String(), "host port chosen at creation") {
		t.Errorf("plan does not mark the unreserved port tentative:\n%s", stdout2.String())
	}
	if records := registryRecords(t); len(records) != 0 {
		t.Errorf("plan reserved %v; read-only commands never reserve", records)
	}
}
