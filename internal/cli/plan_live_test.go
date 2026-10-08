// Plan's live-sandbox report: for a worktree whose persistent sandbox
// already exists, plan describes what the backend holds — ownership,
// Creation drift, Bootstrap completion, and the observed published
// endpoints — without changing anything, exactly as status reports them.
package cli

import (
	"strings"
	"testing"
)

// liveTOML is a configuration with a port and a bootstrap, so the live
// report has endpoints and a completion marker to show.
const liveTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"

[ports.web]
guest = 4000

[bootstrap]
run = "echo bootstrapped"
`

// TestPlanReportsExistingSandboxState checks that plan describes an
// existing owned sandbox's live state: its backend state, un drifted
// snapshot match, completed Bootstrap, and the endpoints inspection is
// authoritative for.
func TestPlanReportsExistingSandboxState(t *testing.T) {
	worktree, _ := persistentFixture(t, liveTOML)
	id := sandboxIdentityOf(t, worktree)

	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	for _, want := range []string{
		"existing sandbox:",
		"state: Running",
		"creation drift: none",
		"bootstrap: complete",
		"127.0.0.1:",
		"guest 4000",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan output is missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("plan output does not name the sandbox identity %q:\n%s", id, stdout)
	}
	if strings.Contains(stdout, "added by later releases") {
		t.Errorf("plan still disclaims the live report it now carries:\n%s", stdout)
	}

	// The same report works for a stopped sandbox, whose labels, image
	// digest, and declared ports survive in the recorded configuration.
	if code, _, stderr := sbxRun(t, worktree, []string{"stop"}, nil); code != 0 {
		t.Fatalf("stop failed: %s", stderr)
	}
	code, stdout, stderr = sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed for a stopped sandbox: %s", stderr)
	}
	for _, want := range []string{"state: Stopped", "creation drift: none", "bootstrap: complete"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan output for a stopped sandbox is missing %q:\n%s", want, stdout)
		}
	}
}

// TestLogsForwardsBackendFailures checks that a failing backend surfaces
// through logs as a failure, never as an empty success.
func TestLogsForwardsBackendFailures(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_LS_FAIL", "1")

	code, _, stderr := sbxRun(t, worktree, []string{"logs"}, nil)
	if code == 0 {
		t.Fatal("logs succeeded while the backend's listing failed")
	}
	if !strings.Contains(stderr, "listing the backend’s sandboxes") {
		t.Errorf("stderr does not carry the backend's failure:\n%s", stderr)
	}
}

// TestPlanReportsDriftOnExistingSandbox checks that plan stays usable and
// honest when the configuration has drifted from the existing sandbox: the
// drift lines are reported, nothing is refused and nothing changes.
func TestPlanReportsDriftOnExistingSandbox(t *testing.T) {
	worktree, _ := persistentFixture(t, liveTOML)

	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	writeFile(t, worktree+"/sbx.toml", strings.Replace(liveTOML, `memory = "2G"`, `memory = "4G"`, 1))

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed on a drifted sandbox: %s", stderr)
	}
	for _, want := range []string{"creation drift", "memory"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan output is missing %q for the drifted sandbox:\n%s", want, stdout)
		}
	}
}

// TestPlanReportsUnownedExistingSandbox checks that plan says so when a
// same-named sandbox exists that sbx did not create: the intended creation
// would refuse, and the plan reports that instead of pretending the
// sandbox would be created.
func TestPlanReportsUnownedExistingSandbox(t *testing.T) {
	worktree, fake := persistentFixture(t, liveTOML)
	// A sandbox that sbx did not create: recorded directly in the fake's
	// store, carrying no sbx.managed label.
	fake.SeedSandbox(t, sandboxIdentityOf(t, worktree), "alpine:3.20", "running", map[string]string{"team": "ops"})

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	if !strings.Contains(stdout, "sbx did not create") {
		t.Errorf("plan output does not report the unowned sandbox:\n%s", stdout)
	}
}

// TestPlanReportsUninspectableSandbox checks that an inspection failure is
// reported as an unknown state, never read as an unowned sandbox: the two
// mean opposite things — one is a backend problem, the other a refusal.
func TestPlanReportsUninspectableSandbox(t *testing.T) {
	worktree, _ := persistentFixture(t, liveTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_INSPECT_FAIL", "1")

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	if strings.Contains(stdout, "sbx did not create") {
		t.Errorf("plan read an uninspectable sandbox as unowned:\n%s", stdout)
	}
	if !strings.Contains(stdout, "unknown") {
		t.Errorf("plan output does not report the unknown state:\n%s", stdout)
	}
}

// TestPlanWithoutExistingSandboxHasNoLiveReport checks that the live
// section appears only when the backend holds a sandbox under the
// identity: a fresh worktree's plan is purely prospective.
func TestPlanWithoutExistingSandboxHasNoLiveReport(t *testing.T) {
	worktree, _ := persistentFixture(t, liveTOML)

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	if strings.Contains(stdout, "existing sandbox:") {
		t.Errorf("a fresh worktree's plan reports an existing sandbox:\n%s", stdout)
	}
	if code, _, stderr := sbxRun(t, worktree, []string{"plan"}, nil); code != 0 {
		t.Fatalf("second plan failed: %s", stderr)
	}
}
