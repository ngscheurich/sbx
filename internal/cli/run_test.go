package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// runFixture sets up a worktree with sbx.toml, a fake msb, and captured
// streams, then runs `sbx run` from inside the worktree.
func runFixture(t *testing.T, toml string, argv ...string) (int, *bytes.Buffer, *bytes.Buffer, testsupport.Log) {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, toml)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), append([]string{"run"}, argv...), strings.NewReader("guest-input\n"), &stdout, &stderr)
	})
	return code, &stdout, &stderr, fake
}

// disposableTOML is a valid configuration for the disposable-run tests.
const disposableTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
egress = "public"
`

// TestRunSequencePinsBackendCalls checks the exact call sequence and the
// translated create/exec argv for a disposable run.
func TestRunSequencePinsBackendCalls(t *testing.T) {
	code, stdout, stderr, fake := runFixture(t, disposableTOML, "--", "echo", "hello")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "guest-stdout") {
		t.Errorf("guest stdout did not reach sbx stdout: %q", stdout.String())
	}

	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}

	// context, image inspect, run — in that order; the sandbox lifecycle
	// is msb run's own behavior, so sbx makes no cleanup calls.
	if got := calls[0].Args; !equal(got, []string{"context", "--format", "json"}) {
		t.Errorf("first call mismatch: %q", got)
	}
	if got := calls[1].Args; !equal(got, []string{"image", "inspect", "alpine:3.20", "--format", "json"}) {
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
	if !equal(runCall, want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", runCall, want)
	}
}

// worktreeOf recovers the worktree root from the fixture: the fake's create
// call carries it as the --mount source and sbx.worktree label. It reads the
// sbx.worktree label value from the recorded create call.
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
	_, stdout, stderr, fake := runFixture(t, disposableTOML, "--", "true")
	if !strings.Contains(stderr.String(), "guest-stderr") {
		t.Errorf("guest stderr did not reach sbx stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "guest-stdout") {
		t.Errorf("guest stdout did not reach sbx stdout: %q", stdout.String())
	}
	if calls := fake.Calls(); len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
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
egress = "none"
`
	_, _, stderr, fake := runFixture(t, toml)
	if !strings.Contains(stderr.String(), "guest-stderr") {
		t.Errorf("guest stderr did not reach sbx stderr: %q", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
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
	if got := calls[2].Args; !equal(got, want) {
		t.Errorf("shell-mode run argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestRunRejectsArgvWithoutSeparator keeps `sbx run foo` a usage error so
// future flags cannot be mistaken for a guest command.
func TestRunRejectsArgvWithoutSeparator(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "echo", "hi"}, nil, &stdout, &stderr)
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
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "false"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 37 {
		t.Errorf("exit code = %d, want the guest's 37 (stderr: %s)", code, stderr.String())
	}
	if calls := fake.Calls(); len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
}

// TestRunPropagatesCommandFailure checks that a failing msb run surfaces
// the backend's message and exit status without any cleanup calls.
func TestRunPropagatesCommandFailure(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXEC_FAIL_START", "1")
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
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
	worktree, _ := fixtureRepo(t, disposableTOML)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- chdir(t, worktree, func() int {
			return Run(ctx, []string{"run", "--", "sleep", "30"}, nil, &stdout, &stderr)
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
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
}

// TestRunRefusesCloudContext checks that a cloud selection from msb context
// stops the run before anything is created.
func TestRunRefusesCloudContext(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_CONTEXT_BACKEND", "cloud")
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
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
	_, _, _, fake := runFixture(t, disposableTOML, "--", "true")
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
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 4 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if !equal(calls[2].Args, []string{"image", "pull", "alpine:3.20"}) {
		t.Errorf("expected the pull as the third call, got %q", calls[2].Args)
	}
}

// TestRunFailsClosedOnProjectVolumes checks that a declared Project volume
// stops the run before any msb call: its compatibility checks are not
// implemented yet, so the volume must not be mounted unprotected.
func TestRunFailsClosedOnProjectVolumes(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.cache]
target = "/cache"
scope = "project"

[network]
egress = "public"
`)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded with a declared Project volume")
	}
	if !strings.Contains(stderr.String(), "project volumes") {
		t.Errorf("stderr does not name the project volumes: %s", stderr.String())
	}
	if got := fake.Calls(); len(got) != 0 {
		t.Errorf("msb was called before rejection: %v", got)
	}
}

// TestRunTranslatesMountsEnvAndAllowlist checks the full msb run argv for
// bind and tmpfs mounts, environment, sandbox volumes, and allowlist
// egress with DNS, resolved against the worktree root.
func TestRunTranslatesMountsEnvAndAllowlist(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("SBX_TEST_TOKEN", "throwaway-value-7b2f")
	worktree, _ := fixtureRepo(t, `
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
egress = "allowlist"
allow = ["example.com", "*.example.org"]
dns_nameservers = ["1.1.1.1", "8.8.8.8"]
`)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	want := []string{"run", "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktree + ":/work",
		"--mount-file", worktree + "/seed.txt:/mnt/seed.txt:ro",
		"--tmpfs", "/tmp:512M:noexec",
		"--mount-owned", "/scratch",
		"--env", "MODE=test",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktree,
		"--net-rule", "allow@example.com",
		"--net-rule", "allow@*.example.org",
		"--net-default-egress", "deny",
		"--tls-intercept",
		"--dns-nameserver", "1.1.1.1",
		"--dns-nameserver", "8.8.8.8",
		"--secret-conf", "<path>",
		"--workdir", "/work",
		"--no-tty",
		"--", "true",
	}
	// The --secret-conf value is a temporary path; the splice above marks
	// it so the rest of the argv compares exactly.
	runCall := calls[2].Args
	if !equal(spliceSecretConf(runCall), want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", runCall, want)
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

// spliceSecretConf replaces the value after --secret-conf with a
// placeholder so the argv comparison stays exact elsewhere.
func spliceSecretConf(args []string) []string {
	i := indexOf(args, "--secret-conf")
	if i < 0 {
		return args
	}
	out := append([]string{}, args[:i+1]...)
	out = append(out, "<path>")
	return append(out, args[i+2:]...)
}

// indexOf returns the index of the first occurrence of token, or -1.
func indexOf(args []string, token string) int {
	for i, a := range args {
		if a == token {
			return i
		}
	}
	return -1
}

// TestRunMissingSecretEnvFailsBeforeAnything checks that a declared secret
// whose host variable is unset stops the run before any msb call.
func TestRunMissingSecretEnvFailsBeforeAnything(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[secrets.TOKEN]
from_env = "SBX_DEFINITELY_UNSET_VARIABLE"
allow = ["example.com"]

[network]
egress = "public"
`)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
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
	worktree, _ := fixtureRepo(t, disposableTOML)
	sub := filepath.Join(worktree, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := chdir(t, sub, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !contains(callsOf(fake), "--mount-dir") {
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
	worktree, _ := fixtureRepo(t, disposableTOML)
	runOnce := func() {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := chdir(t, worktree, func() int {
			return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
		})
		if code != 0 {
			t.Fatalf("run failed: %s", stderr.String())
		}
	}

	runOnce()
	runOnce()
	for _, c := range fake.Calls() {
		if c.Args[0] == "run" && contains(c.Args, "--name") {
			t.Errorf("sbx named a disposable run; msb keeps named sandboxes: %v", c.Args)
		}
	}
}

// callDump renders recorded calls for failure messages.
func callDump(calls []testsupport.Call) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString("  " + strings.Join(c.Args, " ") + "\n")
	}
	return b.String()
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// contains reports whether the argument list includes the exact token.
func contains(args []string, token string) bool {
	for _, a := range args {
		if a == token {
			return true
		}
	}
	return false
}
