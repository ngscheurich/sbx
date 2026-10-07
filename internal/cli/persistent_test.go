// Persistent-sandbox command tests: creation, reuse, the stopped-to-running
// transition, ownership, drift in each of its forms, read-only commands, and
// removal with its confirmation rules — all against the stateful fake msb.
package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// persistentTOML is a valid configuration for the persistent-sandbox tests.
const persistentTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
egress = "public"
`

// secretTOML declares one destination-scoped secret.
const secretTOML = `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[secrets.TEST_TOKEN]
from_env = "SBX_TEST_TOKEN"
allow = ["example.com"]

[network]
egress = "public"
`

// persistentFixture prepares a fake msb, a private host state directory, and
// a worktree with the given sbx.toml.
func persistentFixture(t *testing.T, toml string) (string, testsupport.Log) {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, toml)
	return worktree, fake
}

// sbxRun runs one sbx command line inside dir and captures its streams.
func sbxRun(t *testing.T, dir string, args []string, stdin io.Reader) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := chdir(t, dir, func() int {
		return Run(context.Background(), args, stdin, &stdout, &stderr)
	})
	return code, stdout.String(), stderr.String()
}

// sandboxIdentityOf derives the worktree's Sandbox identity the way the CLI
// does, for tests that need the name the backend will see.
func sandboxIdentityOf(t *testing.T, worktree string) string {
	t.Helper()
	info, err := gitx.Discover(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	return identity.Derive(info.CommonDir, info.WorktreeRoot).Sandbox
}

// snapshotPathOf returns the worktree sandbox's snapshot file path.
func snapshotPathOf(t *testing.T, worktree string) string {
	t.Helper()
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "snapshots", sandboxIdentityOf(t, worktree)+".json")
}

// sbxUp drives `sbx up` inside the worktree.
func sbxUp(t *testing.T, worktree string, flags ...string) (int, string, string) {
	t.Helper()
	return sbxRun(t, worktree, append([]string{"up"}, flags...), nil)
}

// TestUpCreatesSandboxAndSnapshot checks the creation path: the exact call
// sequence, the exact create argv, and the creation snapshot written under
// the Sandbox identity with the image's manifest digest.
func TestUpCreatesSandboxAndSnapshot(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)

	code, stdout, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "persistent sandbox: "+id) || !strings.Contains(stdout, "created") {
		t.Errorf("up output lacks the identity or creation report:\n%s", stdout)
	}

	calls := fake.Calls()
	if len(calls) != 4 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if got := calls[0].Args; !equal(got, []string{"context", "--format", "json"}) {
		t.Errorf("first call mismatch: %q", got)
	}
	if got := calls[1].Args; !equal(got, []string{"ls", "--format", "json"}) {
		t.Errorf("second call mismatch: %q", got)
	}
	if got := calls[2].Args; !equal(got, []string{"image", "inspect", "alpine:3.20", "--format", "json"}) {
		t.Errorf("third call mismatch: %q", got)
	}
	wantCreate := []string{"create", "--name", id, "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktree + ":/workspace",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=persistent",
		"--label", "sbx.worktree=" + worktree,
	}
	if got := calls[3].Args; !equal(got, wantCreate) {
		t.Errorf("create argv mismatch:\n got: %q\nwant: %q", got, wantCreate)
	}

	snap, err := os.ReadFile(snapshotPathOf(t, worktree))
	if err != nil {
		t.Fatalf("no snapshot was written: %v", err)
	}
	for _, want := range []string{"sha256:fake", "sbx.mode", "persistent", worktree} {
		if !strings.Contains(string(snap), want) {
			t.Errorf("snapshot is missing %q:\n%s", want, snap)
		}
	}
}

// TestUpIsIdempotentOnRunningSandbox checks the reuse path: the second up
// inspects and confirms, but never creates, starts, or removes anything.
func TestUpIsIdempotentOnRunningSandbox(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	before, err := os.ReadFile(snapshotPathOf(t, worktree))
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("second up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "already running") {
		t.Errorf("second up did not report reuse:\n%s", stdout)
	}
	calls := fake.Calls()
	if len(calls) != 8 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	for _, c := range calls[4:] {
		switch c.Args[0] {
		case "create", "start", "stop", "remove":
			t.Errorf("reuse path mutated the backend: %q", c.Args)
		}
	}
	after, err := os.ReadFile(snapshotPathOf(t, worktree))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("reuse rewrote the snapshot:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestUpStartsStoppedSandbox checks the stopped-to-running transition: the
// existing sandbox is started, never recreated, and its snapshot survives.
func TestUpStartsStoppedSandbox(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	if code, _, stderr := sbxRun(t, worktree, []string{"stop"}, nil); code != 0 {
		t.Fatalf("stop failed: %s", stderr)
	}

	code, stdout, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("second up failed: %s", stderr)
	}
	if !strings.Contains(stdout, "started") {
		t.Errorf("second up did not report a start:\n%s", stdout)
	}
	last := fake.Calls()[len(fake.Calls())-1]
	if !equal(last.Args, []string{"start", id}) {
		t.Errorf("last call mismatch: %q", last.Args)
	}
	if _, err := os.Stat(snapshotPathOf(t, worktree)); err != nil {
		t.Errorf("starting removed the snapshot: %v", err)
	}
	creates := 0
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("starting a stopped sandbox recreated it: %d create calls", creates)
	}
}

// TestExecRunsGuestCommandAndLeavesSandboxRunning checks exec's argv, stream
// forwarding, exit-status propagation, and that the sandbox keeps running.
func TestExecRunsGuestCommandAndLeavesSandboxRunning(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_EXIT", "7")

	code, stdout, stderr := sbxRun(t, worktree, []string{"exec", "--", "echo", "hello"}, strings.NewReader("guest-input\n"))
	if code != 7 {
		t.Errorf("exec exit code = %d, want the guest's 7", code)
	}
	if !strings.Contains(stdout, "guest-stdout") || !strings.Contains(stderr, "guest-stderr") {
		t.Errorf("guest streams did not reach sbx:\nstdout: %q\nstderr: %q", stdout, stderr)
	}

	var execCall *testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" {
			call := c
			execCall = &call
		}
	}
	if execCall == nil {
		t.Fatalf("no exec call recorded:\n%s", callDump(fake.Calls()))
	}
	want := []string{"exec", id, "--workdir", "/workspace", "--stream", "--", "echo", "hello"}
	if !equal(execCall.Args, want) {
		t.Errorf("exec argv mismatch:\n got: %q\nwant: %q", execCall.Args, want)
	}
	if got := fake.Stdin(t); got != "guest-input\n" {
		t.Errorf("guest stdin mismatch: %q", got)
	}

	// exec leaves the sandbox running.
	code, stdout, stderr = sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	if !strings.Contains(stdout, "status: Running") {
		t.Errorf("sandbox did not stay running:\n%s", stdout)
	}
}

// TestExecSurvivesListingStatusMismatch checks that a live sandbox is used,
// never started, even when the backend's listing reports its status under a
// word other than "running" (observed on a real msb 0.7.6 host: `msb ls`
// reported a live sandbox and `msb start` refused it as already running).
// msb's own refusal is authoritative: the sandbox is up, so exec proceeds.
func TestExecSurvivesListingStatusMismatch(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_STATUS_OVERRIDE", "created")

	code, stdout, stderr := sbxRun(t, worktree, []string{"exec", "--", "echo", "hello"}, nil)
	if code != 0 {
		t.Fatalf("exec failed on a live sandbox the listing misreported: %s", stderr)
	}
	if !strings.Contains(stdout, "guest-stdout") {
		t.Errorf("guest output missing:\n%s", stdout)
	}
	// The stale listing sent exec down the start path, where msb refused it
	// as already running; exec treated that as the sandbox being up.
	starts := 0
	for _, c := range fake.Calls() {
		if c.Args[0] == "start" {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("exec recorded %d start calls, want the one refused attempt", starts)
	}
	if strings.Contains(stderr, "sbx:") {
		t.Errorf("exec surfaced an error for a live sandbox:\n%s", stderr)
	}
}

// TestExecImpliesUp checks that exec creates the persistent sandbox first
// when none exists yet.
func TestExecImpliesUp(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)

	code, stdout, stderr := sbxRun(t, worktree, []string{"exec", "--", "true"}, nil)
	if code != 0 {
		t.Fatalf("exec failed: %s\n%s", stderr, stdout)
	}
	sawCreate, sawExec := false, false
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" && contains(c.Args, "--name") && contains(c.Args, id) {
			sawCreate = true
		}
		if c.Args[0] == "exec" {
			sawExec = true
		}
	}
	if !sawCreate || !sawExec {
		t.Errorf("exec without an existing sandbox did not create first (create=%v, exec=%v):\n%s", sawCreate, sawExec, callDump(fake.Calls()))
	}
	if _, err := os.Stat(snapshotPathOf(t, worktree)); err != nil {
		t.Errorf("implicit up wrote no snapshot: %v", err)
	}
}

// TestExecWithoutArgvRunsConfiguredShell checks that a bare exec runs the
// configured shell, directly and without a host shell.
func TestExecWithoutArgvRunsConfiguredShell(t *testing.T) {
	worktree, fake := persistentFixture(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"
shell = "/bin/bash"

[network]
egress = "public"
`)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if code, _, stderr := sbxRun(t, worktree, []string{"exec"}, nil); code != 0 {
		t.Fatalf("exec failed: %s", stderr)
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "exec" && !equal(c.Args[len(c.Args)-2:], []string{"--", "/bin/bash"}) {
			t.Errorf("bare exec did not run the configured shell: %q", c.Args)
		}
	}
}

// TestUpRefusesUnownedNameCollision checks the never-adopt rule: a same-named
// sandbox without the sbx.managed label stops up before any mutation, with
// the way out named.
func TestUpRefusesUnownedNameCollision(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)
	fake.SeedSandbox(t, id, "alpine:3.20", "running", map[string]string{"team": "ops"})

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up adopted a sandbox it did not create")
	}
	for _, want := range []string{"did not create", "msb remove " + id} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	for _, c := range fake.Calls() {
		switch c.Args[0] {
		case "create", "start", "stop", "remove":
			t.Errorf("refusal still mutated the backend: %q", c.Args)
		}
	}
}

// TestUpRefusesConfigurationDrift checks that a changed definition refuses
// reuse, names both values, and that --allow-stale uses the sandbox anyway
// with a warning.
func TestUpRefusesConfigurationDrift(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	changed := strings.Replace(persistentTOML, `memory = "2G"`, `memory = "4G"`, 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up reused a drifted sandbox without --allow-stale")
	}
	for _, want := range []string{"drifted", "memory", "2G", "4G", "--allow-stale"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}

	code, stdout, stderr := sbxUp(t, worktree, "--allow-stale")
	if code != 0 {
		t.Fatalf("up --allow-stale failed: %s", stderr)
	}
	if !strings.Contains(stdout, "already running") {
		t.Errorf("--allow-stale did not use the sandbox:\n%s", stdout)
	}
	if !strings.Contains(stderr, "despite drift") {
		t.Errorf("--allow-stale did not warn about the drift:\n%s", stderr)
	}
}

// TestUpRefusesImageDigestDrift checks that a tag repointed at new contents
// is drift even though sbx.toml is unchanged.
func TestUpRefusesImageDigestDrift(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_IMAGE_DIGEST", "sha256:rebuilt")

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up reused a sandbox whose image tag now resolves to new contents")
	}
	for _, want := range []string{"sha256:fake", "sha256:rebuilt"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
}

// TestUpRefusesUnresolvableImage checks that an image msb cannot inspect
// fails closed as drift, not as proof of being unchanged.
func TestUpRefusesUnresolvableImage(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "alpine:3.20")

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up treated an unresolvable image as unchanged")
	}
	if !strings.Contains(stderr, "could not be confirmed") {
		t.Errorf("stderr does not report the unconfirmed image:\n%s", stderr)
	}
}

// TestUpRefusesMissingSnapshot checks that an owned sandbox without a
// snapshot counts as drifted.
func TestUpRefusesMissingSnapshot(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	if err := os.Remove(snapshotPathOf(t, worktree)); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up reused a sandbox without a creation snapshot")
	}
	if !strings.Contains(stderr, "no creation-time snapshot") {
		t.Errorf("stderr does not report the missing snapshot:\n%s", stderr)
	}
}

// TestUpMissingImageCreationFailure checks the creation path's image
// handling: both the inspect and pull failures surface, with the concrete
// next step, before any create.
func TestUpMissingImageCreationFailure(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "alpine:3.20")
	t.Setenv("FAKE_MSB_PULL_FAIL", "1")

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up created a sandbox from an unavailable image")
	}
	for _, want := range []string{"not available to msb", "inspect", "pull", "sbx build"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" {
			t.Errorf("creation was attempted anyway: %q", c.Args)
		}
	}
}

// TestUpCreatesWithProjectVolumes checks the creation path with declared
// Project volumes: the compatibility listing runs before any mutation,
// and the create argv carries each volume's definition — the disk's kind
// and size, the directory's quota.
func TestUpCreatesWithProjectVolumes(t *testing.T) {
	worktree, fake := persistentFixture(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.capped]
target = "/capped"
scope = "project"
quota = "4G"

[volumes.data]
target = "/data"
scope = "project"
kind = "disk"
size = "8G"

[network]
egress = "public"
`)
	id := sandboxIdentityOf(t, worktree)
	code, _, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up with declared Project volumes failed: %s", stderr)
	}
	calls := fake.Calls()
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	// context, sandbox listing, volume listing, image inspect, create —
	// the compatibility check runs before the image or sandbox is touched.
	if got := calls[2].Args; !equal(got, []string{"volumes", "--format", "json"}) {
		t.Errorf("third call mismatch: %q", got)
	}
	create := calls[4].Args
	capped := projectVolumeName(t, worktree, "capped") + ":/capped:quota=4G"
	data := projectVolumeName(t, worktree, "data") + ":/data:kind=disk,size=8G"
	for i, a := range create {
		if a == "--mount-named" && i+1 < len(create) {
			switch create[i+1] {
			case capped:
				capped = ""
			case data:
				data = ""
			}
		}
	}
	if capped != "" || data != "" {
		t.Errorf("create argv is missing project volumes %q and %q: %q", capped, data, create)
	}
	if _, err := os.Stat(snapshotPathOf(t, worktree)); err != nil {
		t.Errorf("creation wrote no snapshot: %v", err)
	}
	if !strings.Contains(strings.Join(create, " "), id) {
		t.Errorf("create argv lacks the sandbox identity %q: %q", id, create)
	}
}

// TestUpRetryBootstrapRequiresADeclaration checks that --retry-bootstrap
// without a [bootstrap] table is an explicit error, before any backend
// call: silently treating it as a plain up would hide an unrun step.
func TestUpRetryBootstrapRequiresADeclaration(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	code, _, stderr := sbxUp(t, worktree, "--retry-bootstrap")
	if code != exitFailure {
		t.Errorf("exit code = %d, want a failure", code)
	}
	for _, want := range []string{"--retry-bootstrap", "[bootstrap]"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	if got := fake.Calls(); len(got) != 0 {
		t.Errorf("msb was called before the rejection: %v", got)
	}
}

// TestSecretBearingRestartSuppliesHostValues checks that creating a
// sandbox with a secret works and that starting it again after a stop
// succeeds with the secret's host value supplied again: msb re-resolves
// each secret from its own environment on every start (observed on a real
// host, ticket 01), and sbx's subprocess environment carries the host
// value through. No sbx output or host state may contain the value.
func TestSecretBearingRestartSuppliesHostValues(t *testing.T) {
	worktree, fake := persistentFixture(t, secretTOML)
	t.Setenv("SBX_TEST_TOKEN", "throwaway-test-value")
	// The fake refuses a start whose environment lacks the variable, the
	// way the real msb fails a restart whose secret value is missing.
	t.Setenv("FAKE_MSB_START_REQUIRES_ENV", "SBX_TEST_TOKEN")
	id := sandboxIdentityOf(t, worktree)

	code, _, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("creating a sandbox with a secret failed: %s", stderr)
	}
	var createIdx int
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" {
			createIdx = c.Index
			if !contains(c.Args, "--secret-conf") {
				t.Errorf("create did not pass the secret map: %q", c.Args)
			}
		}
	}
	conf := fake.SecretConf(t, createIdx)
	if !strings.Contains(conf, `value: "${SBX_TEST_TOKEN}"`) {
		t.Errorf("secret map is missing the source reference:\n%s", conf)
	}
	if strings.Contains(conf, "throwaway-test-value") {
		t.Errorf("secret map contains the host value:\n%s", conf)
	}

	if code, _, stderr := sbxRun(t, worktree, []string{"stop"}, nil); code != 0 {
		t.Fatalf("stop failed: %s", stderr)
	}

	code, stdout, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed to restart the stopped secret-bearing sandbox: %s", stderr)
	}
	started := false
	for _, c := range fake.Calls() {
		if equal(c.Args, []string{"start", id}) {
			started = true
		}
	}
	if !started {
		t.Errorf("the restart never reached the backend as a start:\n%s", callDump(fake.Calls()))
	}
	if !strings.Contains(stdout, "started") {
		t.Errorf("the restart report is missing from up's output:\n%s", stdout)
	}

	// Neither host state nor sbx's own reports may carry the value.
	snap, err := os.ReadFile(snapshotPathOf(t, worktree))
	if err != nil {
		t.Fatalf("reading the creation snapshot: %v", err)
	}
	if strings.Contains(string(snap), "throwaway-test-value") {
		t.Errorf("the creation snapshot contains the host secret value")
	}
	if strings.Contains(stdout+stderr, "throwaway-test-value") {
		t.Errorf("up's output contains the host secret value")
	}
}

// TestSecretBearingRestartFailsClosedOnMissingSecret checks that a stopped
// secret-bearing sandbox is not started when its host variable is absent:
// msb would fail the start, and sbx refuses before any resource changes
// instead, naming the variable.
func TestSecretBearingRestartFailsClosedOnMissingSecret(t *testing.T) {
	worktree, fake := persistentFixture(t, secretTOML)
	id := sandboxIdentityOf(t, worktree)
	t.Setenv("SBX_TEST_TOKEN", "throwaway-test-value")

	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("creating a sandbox with a secret failed: %s", stderr)
	}
	if code, _, stderr := sbxRun(t, worktree, []string{"stop"}, nil); code != 0 {
		t.Fatalf("stop failed: %s", stderr)
	}
	if err := os.Unsetenv("SBX_TEST_TOKEN"); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up started a secret-bearing sandbox with its host variable missing")
	}
	if !strings.Contains(stderr, "SBX_TEST_TOKEN") {
		t.Errorf("stderr does not name the missing variable:\n%s", stderr)
	}
	for _, c := range fake.Calls() {
		if equal(c.Args, []string{"start", id}) {
			t.Errorf("a restart without the secret's host value reached the backend: %q", c.Args)
		}
	}
}

// TestExecRefusesArgvWithoutSeparator checks the argv-parsing rule shared
// with run.
func TestExecRefusesArgvWithoutSeparator(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	code, _, stderr := sbxRun(t, worktree, []string{"exec", "echo", "hello"}, nil)
	if code != exitUsage {
		t.Errorf("exit code = %d, want a usage error", code)
	}
	if !strings.Contains(stderr, "--") {
		t.Errorf("stderr does not name the separator:\n%s", stderr)
	}
}

// TestStopKeepsState checks that stopping is a state-keeping transition and
// that status reads the recorded configuration of a stopped sandbox.
func TestStopKeepsState(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	code, stdout, stderr := sbxRun(t, worktree, []string{"stop"}, nil)
	if code != 0 {
		t.Fatalf("stop failed: %s", stderr)
	}
	if !strings.Contains(stdout, "state and volumes are kept") {
		t.Errorf("stop output does not say the state is kept:\n%s", stdout)
	}
	last := fake.Calls()[len(fake.Calls())-1]
	if !equal(last.Args, []string{"stop", id}) {
		t.Errorf("stop call mismatch: %q", last.Args)
	}
	if _, err := os.Stat(snapshotPathOf(t, worktree)); err != nil {
		t.Errorf("stop removed the snapshot: %v", err)
	}

	// Status of a stopped sandbox reads the recorded configuration layer.
	code, stdout, stderr = sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	for _, want := range []string{"status: Stopped", "drift: none"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status of the stopped sandbox is missing %q:\n%s", want, stdout)
		}
	}

	// Stopping again is reported, not re-issued.
	code, stdout, _ = sbxRun(t, worktree, []string{"stop"}, nil)
	if code != 0 {
		t.Fatalf("second stop failed")
	}
	if !strings.Contains(stdout, "already stopped") {
		t.Errorf("second stop did not report idempotence:\n%s", stdout)
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "stop" && c.Index > last.Index {
			t.Errorf("a second stop reached the backend: %q", c.Args)
		}
	}
}

// TestStopAndLogsRequireExistingSandbox checks the missing-sandbox errors.
func TestStopAndLogsRequireExistingSandbox(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	for _, cmd := range []string{"stop", "logs", "rm"} {
		code, _, stderr := sbxRun(t, worktree, []string{cmd}, nil)
		if code == 0 {
			t.Errorf("%s succeeded without a sandbox", cmd)
		}
		if !strings.Contains(stderr, "no persistent sandbox") {
			t.Errorf("%s stderr does not explain the absence:\n%s", cmd, stderr)
		}
	}
}

// TestLogsShowsBackendLogs checks the read-only logs pass-through.
func TestLogsShowsBackendLogs(t *testing.T) {
	worktree, _ := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	code, stdout, stderr := sbxRun(t, worktree, []string{"logs"}, nil)
	if code != 0 {
		t.Fatalf("logs failed: %s", stderr)
	}
	for _, want := range []string{"fake log line 1 for " + id, "fake log line 2 for " + id} {
		if !strings.Contains(stdout, want) {
			t.Errorf("logs output is missing %q:\n%s", want, stdout)
		}
	}
}

// TestStatusIsReadOnly checks that status reports without writing sbx state
// or mutating the backend, and that it stays usable when the sandbox has
// drifted — exactly when up and exec would refuse.
func TestStatusIsReadOnly(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	stateDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx")
	before := dirSnapshot(t, stateDir)
	callsBefore := len(fake.Calls())

	code, stdout, stderr := sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	for _, want := range []string{"persistent sandbox: " + id, "status: Running", "drift: none"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output is missing %q:\n%s", want, stdout)
		}
	}
	if after := dirSnapshot(t, stateDir); after != before {
		t.Errorf("status wrote sbx state:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	for _, c := range fake.Calls()[callsBefore:] {
		switch c.Args[0] {
		case "create", "start", "stop", "remove":
			t.Errorf("status mutated the backend: %q", c.Args)
		case "image":
			// Inspecting an image is a read; pulling one is not.
			if len(c.Args) > 1 && c.Args[1] == "pull" {
				t.Errorf("status pulled an image: %q", c.Args)
			}
		}
	}

	// A drifted sandbox is reported, not refused.
	changed := strings.Replace(persistentTOML, `memory = "2G"`, `memory = "4G"`, 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status refused a drifted sandbox: %s", stderr)
	}
	if !strings.Contains(stdout, "4G") || !strings.Contains(stdout, "refuse") {
		t.Errorf("status does not report the drift and its effect:\n%s", stdout)
	}
}

// TestStatusOnMissingAndUnownedSandboxes checks status's reports for a
// worktree without a sandbox and for a same-named unowned one.
func TestStatusOnMissingAndUnownedSandboxes(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	id := sandboxIdentityOf(t, worktree)

	code, stdout, stderr := sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed without a sandbox: %s", stderr)
	}
	if !strings.Contains(stdout, "not created") {
		t.Errorf("status does not report the missing sandbox:\n%s", stdout)
	}

	// A same-named unowned sandbox is reported as such, never adopted.
	fake.SeedSandbox(t, id, "alpine:3.20", "running", map[string]string{"team": "ops"})
	code, stdout, stderr = sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed on an unowned sandbox: %s", stderr)
	}
	if !strings.Contains(stdout, "sbx did not create it") {
		t.Errorf("status does not report the unowned sandbox:\n%s", stdout)
	}
}

// TestRmNeedsConfirmationNoninteractively checks that rm without --yes
// fails closed when stdin is not a terminal, touching nothing.
func TestRmNeedsConfirmationNoninteractively(t *testing.T) {
	worktree, fake := persistentFixture(t, persistentTOML)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	code, _, stderr := sbxRun(t, worktree, []string{"rm"}, nil)
	if code == 0 {
		t.Fatal("rm removed a sandbox without confirmation")
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr does not name the noninteractive escape hatch:\n%s", stderr)
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "remove" {
			t.Errorf("removal reached the backend without confirmation: %q", c.Args)
		}
	}
	if _, err := os.Stat(snapshotPathOf(t, worktree)); err != nil {
		t.Errorf("rm kept nothing: the snapshot is gone: %v", err)
	}
}

// TestRmYesRemovesSandboxAndVolumesReport checks the confirmed removal: the
// backend call, the snapshot cleanup, the report of Sandbox volumes lost,
// and that nothing ever touches volumes or port reservations.
func TestRmYesRemovesSandboxAndVolumesReport(t *testing.T) {
	worktree, fake := persistentFixture(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.cache]
target = "/cache"
scope = "sandbox"

[volumes.data]
target = "/var/lib/app"
scope = "sandbox"
kind = "disk"
size = "8G"

[network]
egress = "public"
`)
	id := sandboxIdentityOf(t, worktree)
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	code, stdout, stderr := sbxRun(t, worktree, []string{"rm", "--yes"}, nil)
	if code != 0 {
		t.Fatalf("rm --yes failed: %s", stderr)
	}
	var rmCall *testsupport.Call
	for _, c := range fake.Calls() {
		if c.Args[0] == "remove" {
			call := c
			rmCall = &call
		}
	}
	if rmCall == nil || !equal(rmCall.Args, []string{"remove", "--force", id}) {
		t.Errorf("remove call mismatch: %+v", rmCall)
	}
	for _, want := range []string{
		"removed",
		"/cache (directory)",
		"/var/lib/app (disk, 8G)",
		"Project volumes and port reservations were kept",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("rm output is missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(snapshotPathOf(t, worktree)); !os.IsNotExist(err) {
		t.Errorf("the snapshot survived removal: %v", err)
	}
	for _, c := range fake.Calls() {
		if contains(c.Args, "volume") || contains(c.Args, "volumes") {
			t.Errorf("rm touched a volume command: %q", c.Args)
		}
	}

	// The sandbox is gone from the backend's view.
	code, stdout, stderr = sbxRun(t, worktree, []string{"status"}, nil)
	if code != 0 {
		t.Fatalf("status failed after rm: %s", stderr)
	}
	if !strings.Contains(stdout, "not created") {
		t.Errorf("status does not report the removed sandbox:\n%s", stdout)
	}
}

// TestConfirmRemovalAnswers checks the interactive confirmation's accepted
// and declined answers, including end of input.
func TestConfirmRemovalAnswers(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{" y \n", true},
		{"n\n", false},
		{"no\n", false},
		{"", false},
		{"\n", false},
	}
	for _, tc := range cases {
		var stderr bytes.Buffer
		got := confirmRemoval("app-wt1-1234abcd", strings.NewReader(tc.answer), &stderr)
		if got != tc.want {
			t.Errorf("confirmRemoval(%q) = %v, want %v", tc.answer, got, tc.want)
		}
		if !strings.Contains(stderr.String(), "Remove the persistent sandbox") {
			t.Errorf("the prompt is missing from stderr:\n%s", stderr.String())
		}
	}
}
