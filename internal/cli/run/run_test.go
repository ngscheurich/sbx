package run

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// TestRunSequencePinsBackendCalls checks the exact call sequence and the
// translated create/exec argv for a disposable run.
func TestRunSequencePinsBackendCalls(t *testing.T) {
	code, stdout, stderr, fake := harness.RunFixture(t, harness.DisposableTOML, "--", "echo", "hello")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "guest-stdout") {
		t.Errorf("guest stdout did not reach sbx stdout: %q", stdout.String())
	}

	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}

	// context, image inspect, run — in that order; the sandbox lifecycle
	// is msb run's own behavior, so sbx makes no cleanup calls.
	if got := calls[0].Args; !harness.Equal(got, []string{"context", "--format", "json"}) {
		t.Errorf("first call mismatch: %q", got)
	}
	if got := calls[1].Args; !harness.Equal(got, []string{"image", "inspect", "alpine:3.20", "--format", "json"}) {
		t.Errorf("second call mismatch: %q", got)
	}
	runCall := calls[2].Args
	if len(runCall) < 2 {
		t.Fatalf("run call truncated: %q", runCall)
	}
	want := []string{"run", "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktreeOf(t, fake) + ":/workspace",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktreeOf(t, fake),
		"--workdir", "/workspace",
		"--no-tty",
		"--", "echo", "hello",
	}
	// The full run argv, image positional included, is compared against
	// the translation.
	if !harness.Equal(runCall, want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", runCall, want)
	}
}

// worktreeOf recovers the worktree root from the fixture: disposable-run
// tests record no create call, so it reads the sbx.worktree label from any
// recorded call (the run call carries it).
func worktreeOf(t *testing.T, fake testsupport.Log) string {
	t.Helper()
	for _, call := range fake.Calls() {
		for i, a := range call.Args {
			if a == "--label" && strings.HasPrefix(call.Args[i+1], "sbx.worktree=") {
				return strings.TrimPrefix(call.Args[i+1], "sbx.worktree=")
			}
		}
	}
	t.Fatal("no sbx.worktree label recorded")
	return ""
}

// TestRunForwardsGuestStreams checks that guest output reaches sbx's own
// streams on a successful run.
func TestRunForwardsGuestStreams(t *testing.T) {
	_, stdout, stderr, fake := harness.RunFixture(t, harness.DisposableTOML, "--", "true")
	if !strings.Contains(stderr.String(), "guest-stderr") {
		t.Errorf("guest stderr did not reach sbx stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "guest-stdout") {
		t.Errorf("guest stdout did not reach sbx stdout: %q", stdout.String())
	}
	if calls := fake.Calls(); len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
}

// TestRunUsesConfiguredShellWithoutArgv checks that `sbx run` with no guest
// command runs the configured shell, directly and without a host shell.
func TestRunUsesConfiguredShellWithoutArgv(t *testing.T) {
	toml := `
image = "alpine:3.20"
cpus = 1
memory = "1G"
shell = "/bin/bash"

[network]
policy = "none"
`
	_, _, stderr, fake := harness.RunFixture(t, toml)
	if !strings.Contains(stderr.String(), "guest-stderr") {
		t.Errorf("guest stderr did not reach sbx stderr: %q", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	want := []string{"run", "alpine:3.20",
		"--cpus", "1",
		"--memory", "1G",
		"--mount-dir", worktreeOf(t, fake) + ":/workspace",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktreeOf(t, fake),
		"--no-net",
		"--workdir", "/workspace",
		"--no-tty",
		"--", "/bin/bash",
	}
	if got := calls[2].Args; !harness.Equal(got, want) {
		t.Errorf("shell-mode run argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestRunRejectsArgvWithoutSeparator keeps `sbx run foo` a usage error so
// future flags cannot be mistaken for a guest command.
func TestRunRejectsArgvWithoutSeparator(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "echo", "hi"}, nil, &stdout, &stderr)
	})
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr.String(), "--") {
		t.Errorf("stderr does not explain the -- separator: %s", stderr.String())
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("msb was called before argument validation: %v", fake.Calls())
	}
}

// TestRunPropagatesGuestExitStatus checks that the guest's exit status
// becomes sbx's exit status.
func TestRunPropagatesGuestExitStatus(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXIT", "37")
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "false"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 37 {
		t.Errorf("exit code = %d, want the guest's 37 (stderr: %s)", code, stderr.String())
	}
	if calls := fake.Calls(); len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
}

// TestRunPropagatesCommandFailure checks that a failing msb run surfaces
// the backend's message and exit status without any cleanup calls.
func TestRunPropagatesCommandFailure(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXEC_FAIL_START", "1")
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 127 {
		t.Errorf("exit code = %d, want 127 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot start the guest") {
		t.Errorf("stderr does not carry the backend message: %s", stderr.String())
	}
	for _, call := range fake.Calls() {
		if call.Args[0] == "remove" || call.Args[0] == "inspect" {
			t.Errorf("unexpected lifecycle call after a failed run: %v", call.Args)
		}
	}
}

// TestRunCancellationPropagatesStatus checks that canceling sbx while the
// guest runs propagates the interruption status; the sandbox lifecycle is
// msb run's own behavior.
func TestRunCancellationPropagatesStatus(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXEC_SLEEP", "30")
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- harness.Chdir(t, worktree, func() int {
			return cli.Run(ctx, []string{"run", "--", "sleep", "30"}, nil, &stdout, &stderr)
		})
	}()
	// Wait until the guest is running, then cancel.
	fake.Wait(t, 3)
	cancel()
	code := <-done

	if code != 143 { // the fake traps SIGTERM and exits 143
		t.Errorf("exit code = %d, want 143 from the interrupted guest (stderr: %s)", code, stderr.String())
	}
	if calls := fake.Calls(); len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
}

// TestRunRefusesCloudContext checks that a cloud selection from msb context
// stops the run before anything is created.
func TestRunRefusesCloudContext(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_CONTEXT_BACKEND", "cloud")
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run proceeded with a cloud backend")
	}
	if !strings.Contains(stderr.String(), "cloud") {
		t.Errorf("stderr does not name the refused backend: %s", stderr.String())
	}
	for _, call := range fake.Calls() {
		if call.Args[0] != "context" {
			t.Errorf("run called msb %v after refusing the backend", call.Args)
		}
	}
}

// TestRunForcesLocalBackendInSubprocessEnv checks the environment boundary:
// even a hostile host MSB_BACKEND never reaches the msb subprocess.
func TestRunForcesLocalBackendInSubprocessEnv(t *testing.T) {
	t.Setenv("MSB_BACKEND", "cloud")
	_, _, _, fake := harness.RunFixture(t, harness.DisposableTOML, "--", "true")
	for i, call := range fake.Calls() {
		if call.Backend != "local" {
			t.Errorf("call %d (%v) ran with MSB_BACKEND=%q, want local", i, call.Args, call.Backend)
		}
	}
}

// TestRunPullsMissingPrebuiltImage checks the inspect-then-pull step before
// creation for a missing image.
func TestRunPullsMissingPrebuiltImage(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "alpine:3.20")
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 4 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	if !harness.Equal(calls[2].Args, []string{"image", "pull", "alpine:3.20"}) {
		t.Errorf("expected the pull as the third call, got %q", calls[2].Args)
	}
}

// TestRunMountsProjectVolume checks that a declared Project volume passes
// the compatibility check — nothing exists under its name yet — and
// reaches msb as a --mount-named with its derived name.
func TestRunMountsProjectVolume(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := harness.FixtureRepo(t, projectVolumeTOML)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("run with a declared Project volume failed: %s", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 4 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	// context, volume listing, image inspect, run — in that order: the
	// compatibility check runs before the image or sandbox is touched.
	if got := calls[1].Args; !harness.Equal(got, []string{"volumes", "--format", "json"}) {
		t.Errorf("second call mismatch: %q", got)
	}
	runCall := calls[3].Args
	want := harness.ProjectVolumeName(t, worktree, "cache") + ":/cache"
	found := false
	for i, a := range runCall {
		if a == "--mount-named" && i+1 < len(runCall) && runCall[i+1] == want {
			found = true
		}
	}
	if !found {
		t.Errorf("run argv lacks --mount-named %s: %q", want, runCall)
	}
}

// TestRunTranslatesMountsEnvAndAllowlist checks the full msb run argv for
// bind and tmpfs mounts, environment, sandbox volumes, and allowlist
// policy with DNS, resolved against the worktree root.
func TestRunTranslatesMountsEnvAndAllowlist(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("SBX_TEST_TOKEN", "throwaway-value-7b2f")
	worktree, _ := harness.FixtureRepo(t, `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[workspace]
target = "/work"

[[mounts]]
type = "bind"
source = "./seed.txt"
target = "/mnt/seed.txt"
read_only = true

[[mounts]]
type = "tmpfs"
target = "/tmp"
size = "512M"
noexec = true

[volumes.scratch]
target = "/scratch"
scope = "sandbox"

[env]
MODE = "test"

[secrets.TEST_TOKEN]
from_env = "SBX_TEST_TOKEN"
allow = ["example.com"]

[network]
policy = "allowlist"
allow = ["example.com", "*.example.org"]
dns_nameservers = ["1.1.1.1", "8.8.8.8"]
`)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	want := []string{"run", "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktree + ":/work",
		"--mount-file", worktree + "/seed.txt:/mnt/seed.txt:ro",
		"--mount-owned", "/scratch",
		"--env", "MODE=test",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktree,
		"--net-rule", "allow@example.com",
		"--net-rule", "allow@*.example.org",
		"--tls-intercept",
		"--dns-nameserver", "1.1.1.1",
		"--dns-nameserver", "8.8.8.8",
		"--secret-conf", "<path>",
		"--fs-conf", "<path>",
		"--workdir", "/work",
		"--no-tty",
		"--", "true",
	}
	// The --secret-conf value is a temporary path; the splice above marks
	// it so the rest of the argv compares exactly.
	runCall := calls[2].Args
	if !harness.Equal(harness.SpliceGenerated(runCall), want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", runCall, want)
	}

	// The filesystem configuration carries the tmpfs with noexec.
	fsConf := fake.FsConf(t, 2)
	wantFS := "mounts:\n  - tmpfs: { size: \"512M\" }\n    target: \"/tmp\"\n    noexec: true\n"
	if fsConf != wantFS {
		t.Errorf("fs-conf mismatch:\n got: %q\nwant: %q", fsConf, wantFS)
	}

	// The secret map holds names and source references, never the value.
	conf := fake.SecretConf(t, 2)
	if !strings.Contains(conf, "TEST_TOKEN:") ||
		!strings.Contains(conf, "${SBX_TEST_TOKEN}") ||
		!strings.Contains(conf, "example.com") {
		t.Errorf("secret map is missing the expected redacted content:\n%s", conf)
	}
	if strings.Contains(conf, "throwaway-value") {
		t.Errorf("secret map contains the host secret value:\n%s", conf)
	}
	for _, call := range calls {
		for _, a := range call.Args {
			if strings.Contains(a, "throwaway-value") {
				t.Errorf("host secret value leaked into msb arguments: %q", a)
			}
		}
	}
}

// TestRunMissingSecretEnvFailsBeforeAnything checks that a declared secret
// whose host variable is unset stops the run before any msb call.
func TestRunMissingSecretEnvFailsBeforeAnything(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := harness.FixtureRepo(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[secrets.TOKEN]
from_env = "SBX_DEFINITELY_UNSET_VARIABLE"
allow = ["example.com"]

[network]
policy = "public"
`)
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded with a missing secret host variable")
	}
	if !strings.Contains(stderr.String(), "SBX_DEFINITELY_UNSET_VARIABLE") {
		t.Errorf("stderr does not name the missing variable: %s", stderr.String())
	}
	if got := fake.Calls(); len(got) != 0 {
		t.Errorf("msb was called before rejection: %v", got)
	}
}

// TestRunMountsWorktreeRootFromSubdirectory checks that a run invoked below
// the worktree root still mounts the root, not the invocation directory.
func TestRunMountsWorktreeRootFromSubdirectory(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	sub := filepath.Join(worktree, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, sub, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !harness.Contains(callsOf(fake), "--mount-dir") {
		t.Fatal("no mount recorded")
	}
	for _, call := range fake.Calls() {
		for i, a := range call.Args {
			if a == "--mount-dir" && !strings.HasPrefix(call.Args[i+1], worktree+":") {
				t.Errorf("mount source %q is not the worktree root %q", call.Args[i+1], worktree)
			}
		}
	}
}

// callsOf flattens every recorded call's arguments.
func callsOf(fake testsupport.Log) []string {
	var out []string
	for _, c := range fake.Calls() {
		out = append(out, c.Args...)
	}
	return out
}

// TestRunLeavesNamingToMsb checks that sbx passes no --name for disposable
// runs: msb run removes an auto-named one-shot when the command completes,
// and keeps one it did not name, so sbx must not name them.
func TestRunLeavesNamingToMsb(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := harness.FixtureRepo(t, harness.DisposableTOML)
	runOnce := func() {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := harness.Chdir(t, worktree, func() int {
			return cli.Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
		})
		if code != 0 {
			t.Fatalf("run failed: %s", stderr.String())
		}
	}

	runOnce()
	runOnce()
	for _, c := range fake.Calls() {
		if c.Args[0] == "run" && harness.Contains(c.Args, "--name") {
			t.Errorf("sbx named a disposable run; msb keeps named sandboxes: %v", c.Args)
		}
	}
}
