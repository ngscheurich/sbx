// Bootstrap tests: one-time initialization after each new sandbox, the
// completion marker's rules (identity, created_at, definition hash),
// explicit retry, repair through exec, stale markers, disposable-run
// wrapping, and the per-sandbox creation lock — all against the stateful
// fake msb.
package up

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// bootstrapTOML declares a Bootstrap definition.
const bootstrapTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[bootstrap]
run = "echo bootstrapped > /workspace/.bootstrapped"

[network]
policy = "public"
`

// bootstrapRun is bootstrapTOML's definition, for hash comparisons.
const bootstrapRun = "echo bootstrapped > /workspace/.bootstrapped"

// execCalls returns the recorded exec invocations, which include both
// bootstrap runs and user commands.
func execCalls(fake testsupport.Log) []testsupport.Call {
	var out []testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" {
			out = append(out, c)
		}
	}
	return out
}

// TestUpRunsBootstrapOnceAndRecordsCompletion checks the creation path with
// a declared Bootstrap: it runs through the configured shell in the
// Workspace after creation, completion is recorded bound to the sandbox's
// created_at and the definition hash, and the next up does not rerun it.
func TestUpRunsBootstrapOnceAndRecordsCompletion(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	code, stdout, stderr := harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "bootstrapped") {
		t.Errorf("up output does not report the bootstrap:\n%s", stdout)
	}
	if !strings.Contains(stderr, "running bootstrap") {
		t.Errorf("up did not announce the bootstrap on stderr:\n%s", stderr)
	}

	calls := fake.Calls()
	if len(calls) != 6 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	for i, want := range [][]string{
		{"context", "--format", "json"},
		{"ls", "--format", "json"},
		{"image", "inspect", "alpine:3.20", "--format", "json"},
	} {
		if got := calls[i].Args; !harness.Equal(got, want) {
			t.Errorf("call %d mismatch:\n got: %q\nwant: %q", i, got, want)
		}
	}
	if got := calls[3].Args; got[0] != "create" {
		t.Errorf("fourth call is not the create: %q", got)
	}
	if got := calls[4].Args; !harness.Equal(got, []string{"inspect", id, "--format", "json"}) {
		t.Errorf("the new sandbox was not inspected for its created_at: %q", got)
	}
	wantBootstrap := []string{"exec", id, "--workdir", "/workspace", "--stream", "--",
		"/bin/sh", "-c", bootstrapRun}
	if got := calls[5].Args; !harness.Equal(got, wantBootstrap) {
		t.Errorf("bootstrap exec mismatch:\n got: %q\nwant: %q", got, wantBootstrap)
	}

	marker, err := state.LoadBootstrapMarker(id)
	if err != nil {
		t.Fatalf("no completion marker was recorded: %v", err)
	}
	if marker.Sandbox != id {
		t.Errorf("marker sandbox = %q, want %q", marker.Sandbox, id)
	}
	if marker.CreatedAt == "" {
		t.Error("marker is not bound to the sandbox's created_at")
	}
	if want := state.BootstrapHash(bootstrapRun); marker.DefinitionHash != want {
		t.Errorf("marker hash = %q, want %q", marker.DefinitionHash, want)
	}

	// The next up reuses the sandbox and does not rerun Bootstrap.
	code, stdout, stderr = harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("second up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "already running") {
		t.Errorf("second up did not report reuse:\n%s", stdout)
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Errorf("bootstrap ran again on a recorded sandbox: %d exec calls:\n%s", len(got), harness.CallDump(fake.Calls()))
	}

	code, stdout, stderr = harness.SbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	if !strings.Contains(stdout, "bootstrap: complete") {
		t.Errorf("status does not report the completed bootstrap:\n%s", stdout)
	}
}

// TestUpBootstrapFailureStaysIncomplete checks that a failed Bootstrap is
// never retried by plain up — with or without --allow-stale — and that
// --retry-bootstrap is the explicit way through.
func TestUpBootstrapFailureStaysIncomplete(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	id := harness.SandboxIdentityOf(t, worktree)
	t.Setenv("FAKE_MSB_EXIT", "9")

	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up succeeded with a failing bootstrap")
	}
	for _, want := range []string{"bootstrap failed with exit status 9", "--retry-bootstrap", "sbx exec"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	if _, err := state.LoadBootstrapMarker(id); !errors.Is(err, state.ErrNoBootstrapMarker) {
		t.Errorf("a failed bootstrap was recorded: %v", err)
	}
	// The sandbox stays, incomplete but running, for repair.
	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	for _, want := range []string{"status: Running", "bootstrap: incomplete"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status is missing %q:\n%s", want, stdout)
		}
	}

	// Plain up reports the incomplete bootstrap instead of rerunning it.
	code, _, stderr = harness.SbxUp(t, worktree)
	if code == 0 {
		t.Fatal("plain up retried or ignored an incomplete bootstrap")
	}
	if !strings.Contains(stderr, "incomplete") {
		t.Errorf("stderr does not report the incomplete bootstrap:\n%s", stderr)
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Errorf("plain up reran bootstrap: %d exec calls", len(got))
	}

	// --allow-stale is about drift; it never marks Bootstrap complete.
	code, _, stderr = harness.SbxUp(t, worktree, "--allow-stale")
	if code == 0 {
		t.Fatal("--allow-stale completed an incomplete bootstrap")
	}
	if !strings.Contains(stderr, "incomplete") {
		t.Errorf("stderr does not report the incomplete bootstrap:\n%s", stderr)
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Errorf("--allow-stale reran bootstrap: %d exec calls", len(got))
	}

	// --retry-bootstrap runs the current definition and records success.
	t.Setenv("FAKE_MSB_EXIT", "0")
	code, stdout, stderr = harness.SbxUp(t, worktree, "--retry-bootstrap")
	if code != 0 {
		t.Fatalf("--retry-bootstrap failed: %s", stderr)
	}
	if !strings.Contains(stdout, "bootstrap ran again") {
		t.Errorf("--retry-bootstrap did not report the rerun:\n%s", stdout)
	}
	if got := execCalls(fake); len(got) != 2 {
		t.Errorf("--retry-bootstrap did not run exactly one bootstrap: %d exec calls", len(got))
	}
	if _, err := state.LoadBootstrapMarker(id); err != nil {
		t.Errorf("no marker after a successful retry: %v", err)
	}

	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up after the retry failed: %s", stderr)
	}
}

// TestBootstrapInterruptionLeavesIncomplete checks that interrupting sbx
// while Bootstrap runs leaves the sandbox incomplete: no marker is written
// and the next up reports it rather than rerunning.
func TestBootstrapInterruptionLeavesIncomplete(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	id := harness.SandboxIdentityOf(t, worktree)
	t.Setenv("FAKE_MSB_EXEC_SLEEP", "30")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- harness.Chdir(t, worktree, func() int {
			var stdout, stderr strings.Builder
			return cli.Run(ctx, []string{"up"}, nil, &stdout, &stderr)
		})
	}()
	// Wait for the bootstrap exec (the sixth call), then interrupt it.
	fake.Wait(t, 6)
	cancel()
	if code := <-done; code == 0 {
		t.Fatal("interrupted up succeeded")
	}

	if _, err := state.LoadBootstrapMarker(id); !errors.Is(err, state.ErrNoBootstrapMarker) {
		t.Errorf("an interrupted bootstrap was recorded: %v", err)
	}
	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 || !strings.Contains(stderr, "incomplete") {
		t.Errorf("the next up did not report the interrupted bootstrap: code=%d\n%s", code, stderr)
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Errorf("the interrupted bootstrap was retried by plain up: %d exec calls", len(got))
	}
}

// TestExecRepairsWithWarning checks that exec runs the guest command in an
// incompletely bootstrapped sandbox, with a warning, so the user can repair
// it — and that exec never marks Bootstrap complete itself.
func TestExecRepairsWithWarning(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	id := harness.SandboxIdentityOf(t, worktree)
	t.Setenv("FAKE_MSB_EXIT", "1")
	if code, _, _ := harness.SbxUp(t, worktree); code == 0 {
		t.Fatal("setup: up succeeded with a failing bootstrap")
	}
	t.Setenv("FAKE_MSB_EXIT", "0")

	code, _, stderr := harness.SbxRun(t, worktree, []string{"exec", "--", "true"}, nil)
	if code != 0 {
		t.Fatalf("exec failed: %s", stderr)
	}
	for _, want := range []string{"warning", "bootstrap", "incomplete", "--retry-bootstrap"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("exec warning is missing %q:\n%s", want, stderr)
		}
	}
	execs := execCalls(fake)
	if len(execs) != 2 {
		t.Fatalf("want the failed bootstrap plus the repair exec, got %d exec calls", len(execs))
	}
	if got := execs[1].Args[len(execs[1].Args)-2:]; !harness.Equal(got, []string{"--", "true"}) {
		t.Errorf("the repair command did not reach the sandbox: %q", execs[1].Args)
	}
	if _, err := state.LoadBootstrapMarker(id); !errors.Is(err, state.ErrNoBootstrapMarker) {
		t.Errorf("exec marked the bootstrap complete: %v", err)
	}
}

// TestExecRunsDespiteCreationBootstrapFailure checks that even when the
// implicit up inside exec creates the sandbox and its Bootstrap fails, the
// guest command still runs with a warning: repair is always possible.
func TestExecRunsDespiteCreationBootstrapFailure(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	t.Setenv("FAKE_MSB_EXIT", "1")

	code, _, stderr := harness.SbxRun(t, worktree, []string{"exec", "--", "true"}, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want the guest's 1", code)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "bootstrap") {
		t.Errorf("stderr lacks the bootstrap warning:\n%s", stderr)
	}
	execs := execCalls(fake)
	if len(execs) != 2 {
		t.Fatalf("want the failed bootstrap plus the repair exec, got %d exec calls", len(execs))
	}
	if got := execs[1].Args[len(execs[1].Args)-2:]; !harness.Equal(got, []string{"--", "true"}) {
		t.Errorf("the repair command did not reach the sandbox: %q", execs[1].Args)
	}
}

// TestStaleBootstrapMarkerNotReused checks that a marker recorded for an
// earlier incarnation of the sandbox (a different created_at) is stale:
// plain up reports it as incomplete instead of accepting it, a retry binds
// the marker to the current incarnation, and rm clears it.
func TestStaleBootstrapMarkerNotReused(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	id := harness.SandboxIdentityOf(t, worktree)

	// Bootstrap runs automatically only at creation; a sandbox that
	// predates the declaration is incomplete until --retry-bootstrap.
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("setup: first up failed: %s", stderr)
	}
	if code, _, stderr := harness.SbxUp(t, worktree, "--retry-bootstrap"); code != 0 {
		t.Fatalf("setup: retry failed: %s", stderr)
	}
	marker, err := state.LoadBootstrapMarker(id)
	if err != nil {
		t.Fatalf("setup: no marker: %v", err)
	}

	// A marker from an earlier incarnation (say the sandbox was removed
	// and recreated behind sbx's back) must not be reused.
	stale := marker
	stale.CreatedAt = "2000-01-01T00:00:00Z"
	if err := state.SaveBootstrapMarker(id, stale); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 {
		t.Fatal("plain up accepted a stale bootstrap marker")
	}
	if !strings.Contains(stderr, "incomplete") {
		t.Errorf("stderr does not report the stale marker as incomplete:\n%s", stderr)
	}
	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 || !strings.Contains(stdout, "bootstrap: incomplete") {
		t.Errorf("status accepted a stale marker: code=%d\n%s", code, stdout)
	}

	code, _, stderr = harness.SbxUp(t, worktree, "--retry-bootstrap")
	if code != 0 {
		t.Fatalf("--retry-bootstrap failed: %s", stderr)
	}
	fresh, err := state.LoadBootstrapMarker(id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.CreatedAt != marker.CreatedAt {
		t.Errorf("the retried marker is bound to created_at %q, want the current sandbox's %q", fresh.CreatedAt, marker.CreatedAt)
	}

	// rm clears the marker, so a later sandbox of the same identity can
	// never inherit this completion.
	if code, _, stderr := harness.SbxRun(t, worktree, []string{"rm", "--yes"}, nil); code != 0 {
		t.Fatalf("rm failed: %s", stderr)
	}
	if _, err := state.LoadBootstrapMarker(id); !errors.Is(err, state.ErrNoBootstrapMarker) {
		t.Errorf("rm kept the bootstrap marker: %v", err)
	}

	// Recreating the sandbox bootstraps it again: nothing was inherited.
	before := len(execCalls(fake))
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up after rm failed: %s", stderr)
	}
	if got := len(execCalls(fake)) - before; got != 1 {
		t.Errorf("the recreated sandbox ran bootstrap %d times, want 1", got)
	}
}

// TestChangedBootstrapReportedWithoutDrift checks that editing [bootstrap]
// is not Creation drift: use is not blocked, nothing reruns, and status and
// up report the change.
func TestChangedBootstrapReportedWithoutDrift(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	changed := strings.Replace(bootstrapTOML, "echo bootstrapped", "echo reconfigured", 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	for _, want := range []string{"drift: none", "definition has changed since it ran"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status is missing %q:\n%s", want, stdout)
		}
	}

	before := len(execCalls(fake))
	code, stdout, stderr = harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up refused a changed bootstrap: %s", stderr)
	}
	if !strings.Contains(stdout, "definition has changed since it ran") {
		t.Errorf("up does not report the changed bootstrap:\n%s", stdout)
	}
	if got := len(execCalls(fake)) - before; got != 0 {
		t.Errorf("a changed definition reran bootstrap: %d exec calls", got)
	}

	code, _, stderr = harness.SbxRun(t, worktree, []string{"exec", "--", "true"}, nil)
	if code != 0 {
		t.Fatalf("exec failed: %s", stderr)
	}
	if strings.Contains(stderr, "incomplete") {
		t.Errorf("exec warned about a merely changed bootstrap:\n%s", stderr)
	}
}

// TestRetryBootstrapRerunsChangedDefinition checks that --retry-bootstrap
// runs the current definition when the recorded one no longer matches —
// the flag is the only lever that escapes a changed Bootstrap without
// destroying the sandbox — and records completion for the new hash, so a
// later plain up reports a complete Bootstrap instead of a changed one.
func TestRetryBootstrapRerunsChangedDefinition(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	changed := strings.Replace(bootstrapTOML, "echo bootstrapped", "echo reconfigured", 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	before := len(execCalls(fake))
	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"up", "--retry-bootstrap"}, nil)
	if code != 0 {
		t.Fatalf("up --retry-bootstrap failed: %s", stderr)
	}
	if !strings.Contains(stdout, "bootstrap ran again and its completion was recorded") {
		t.Errorf("up does not report the retry:\n%s", stdout)
	}
	if got := len(execCalls(fake)) - before; got != 1 {
		t.Errorf("bootstrap exec calls during retry = %d, want 1", got)
	}

	code, stdout, stderr = harness.SbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("second up failed: %s", stderr)
	}
	if strings.Contains(stdout, "definition has changed") {
		t.Errorf("the retried bootstrap was not recorded:\n%s", stdout)
	}
}

// TestConcurrentUpSerializesCreationAndBootstrap checks the per-sandbox
// lock: a concurrent up waits with a message, and the second caller — once
// it holds the lock — sees the completed bootstrap and does not run it
// again.
func TestConcurrentUpSerializesCreationAndBootstrap(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, bootstrapTOML)
	t.Setenv("FAKE_MSB_EXEC_SLEEP", "1")

	type result struct {
		code   int
		stderr string
	}
	runUpAsync := func() chan result {
		ch := make(chan result, 1)
		go func() {
			var stdout, stderr strings.Builder
			code := harness.Chdir(t, worktree, func() int {
				return cli.Run(context.Background(), []string{"up"}, nil, &stdout, &stderr)
			})
			ch <- result{code, stderr.String()}
		}()
		return ch
	}

	first := runUpAsync()
	// The sixth call is the bootstrap exec, which sleeps: the first up now
	// holds the lock inside its bootstrap.
	fake.Wait(t, 6)
	second := runUpAsync()

	if r := <-first; r.code != 0 {
		t.Fatalf("first up failed: %s", r.stderr)
	}
	r := <-second
	if r.code != 0 {
		t.Fatalf("second up failed: %s", r.stderr)
	}
	if !strings.Contains(r.stderr, "waiting") {
		t.Errorf("the waiting caller was not told so:\n%s", r.stderr)
	}

	creates := 0
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("concurrent ups created %d sandboxes, want 1", creates)
	}
	if got := execCalls(fake); len(got) != 1 {
		t.Errorf("bootstrap ran %d times for one sandbox, want 1:\n%s", len(got), harness.CallDump(fake.Calls()))
	}
}

// TestRunBootstrapsDisposableBeforeGuestCommand checks that a disposable
// run with a declared Bootstrap wraps the guest command: Bootstrap runs
// through the configured shell in the Workspace, and the user's argv
// passes through positionally, uninterpreted by any shell.
func TestRunBootstrapsDisposableBeforeGuestCommand(t *testing.T) {
	code, _, stderr, fake := harness.RunFixture(t, bootstrapTOML, "--", "echo", "hello world")
	if code != 0 {
		t.Fatalf("run failed: %s", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	runCall := calls[2].Args
	if harness.Contains(runCall, "--name") {
		t.Errorf("sbx named a disposable run; msb keeps named sandboxes: %q", runCall)
	}
	sep := harness.IndexOf(runCall, "--")
	if sep < 0 {
		t.Fatalf("run call has no -- separator: %q", runCall)
	}
	got := runCall[sep+1:]
	if len(got) < 5 || got[0] != "/bin/sh" || got[1] != "-c" || got[3] != "sbx-bootstrap" {
		t.Fatalf("the guest command is not the bootstrap wrapper: %q", got)
	}
	script := got[2]
	for _, want := range []string{
		bootstrapRun,
		`sbx_bootstrap_status=$?`,
		`exec "$@"`,
		"bootstrap failed",
		"sandbox is removed",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("wrapper script is missing %q:\n%s", want, script)
		}
	}
	if !harness.Equal(got[4:], []string{"echo", "hello world"}) {
		t.Errorf("the user argv did not pass through positionally: %q", got[4:])
	}
}

// TestRunBootstrapFailureEndsDisposableRun checks that a failing disposable
// run — Bootstrap's failure included — propagates its exit status and
// leaves cleanup to msb run: sbx makes no remove call of its own, and the
// auto-named one-shot takes its Sandbox volumes with it.
func TestRunBootstrapFailureEndsDisposableRun(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXIT", "1")
	worktree, _ := harness.FixtureRepo(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.data]
target = "/var/lib/app"
scope = "sandbox"

[bootstrap]
run = "exit 1"

[network]
policy = "public"
`)
	var stdout, stderr strings.Builder
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 1 {
		t.Errorf("exit code = %d, want the run's 1 (stderr: %s)", code, stderr.String())
	}
	for _, c := range fake.Calls() {
		switch c.Args[0] {
		case "create", "remove", "inspect":
			t.Errorf("sbx managed the disposable sandbox's lifecycle itself: %q", c.Args)
		}
	}
	// The run call still mounts the Sandbox volume; msb removes it with
	// the one-shot sandbox when the command completes, failure included.
	var runCall []string
	for _, c := range fake.Calls() {
		if c.Args[0] == "run" {
			runCall = c.Args
		}
	}
	if !harness.Contains(runCall, "/var/lib/app") {
		t.Errorf("the disposable run lost its Sandbox volume mount: %q", runCall)
	}
}

// TestPlanReportsDeclaredBootstrap checks that the read-only plan mentions
// the declared Bootstrap without writing any sbx state.
func TestPlanReportsDeclaredBootstrap(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, bootstrapTOML)

	code, stdout, stderr := harness.SbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	for _, want := range []string{"bootstrap:", "/bin/sh -c"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan is missing %q:\n%s", want, stdout)
		}
	}
	// The live-sandbox report lists the backend read-only; nothing that
	// changes a sandbox or pulls an image is allowed.
	for _, c := range fake.Calls() {
		switch c.Args[0] {
		case "ls":
		default:
			t.Errorf("plan called msb %q beyond the read-only listing: %v", c.Args, fake.Calls())
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx")); !os.IsNotExist(err) {
		t.Error("plan wrote sbx state")
	}
}
