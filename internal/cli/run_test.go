package cli

import (
	"bytes"
	"context"
	"regexp"
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
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}

	var name string
	// context, image inspect, create, exec, rm — in that order.
	if got := calls[0].Args; !equal(got, []string{"context", "--format", "json"}) {
		t.Errorf("first call mismatch: %q", got)
	}
	if got := calls[1].Args; !equal(got, []string{"image", "inspect", "alpine:3.20", "--format", "json"}) {
		t.Errorf("second call mismatch: %q", got)
	}
	create := calls[2].Args
	if len(create) < 2 {
		t.Fatalf("create call truncated: %q", create)
	}
	name = create[2]
	if !regexp.MustCompile(`^[a-z0-9-]+-run-[0-9a-f]{6}$`).MatchString(name) {
		t.Errorf("create name %q is not a unique disposable-run name", name)
	}
	want := []string{"create", "--name", name, "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktreeOf(t, fake) + ":/workspace",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktreeOf(t, fake),
	}
	// The full create argv, image positional included, is compared
	// against the translation.
	if !equal(create, want) {
		t.Errorf("create argv mismatch:\n got: %q\nwant: %q", create, want)
	}
	if got := calls[3].Args; !equal(got, []string{"exec", name, "--workdir", "/workspace", "--stream", "--", "echo", "hello"}) {
		t.Errorf("exec argv mismatch: %q", got)
	}
	if got := calls[4].Args; !equal(got, []string{"remove", "--force", name}) {
		t.Errorf("rm argv mismatch: %q", got)
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

// TestRunRemovesSandboxOnSuccess checks the final rm call names the created
// sandbox.
func TestRunRemovesSandboxOnSuccess(t *testing.T) {
	_, stdout, stderr, fake := runFixture(t, disposableTOML, "--", "true")
	if !strings.Contains(stderr.String(), "guest-stderr") {
		t.Errorf("guest stderr did not reach sbx stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "guest-stdout") {
		t.Errorf("guest stdout did not reach sbx stdout: %q", stdout.String())
	}
	calls := fake.Calls()
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	createName := calls[2].Args[2]
	if got := calls[4].Args; !equal(got, []string{"remove", "--force", createName}) {
		t.Errorf("rm argv mismatch: %q, want rm of %q", got, createName)
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
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if got := calls[3].Args; !equal(got, []string{"exec", calls[2].Args[2], "--workdir", "/workspace", "--stream", "--", "/bin/bash"}) {
		t.Errorf("shell-mode exec argv mismatch: %q", got)
	}

	// egress = "none" must reach create as msb's --no-net.
	if !contains(calls[2].Args, "--no-net") {
		t.Errorf("create argv lacks --no-net with egress none: %q", calls[2].Args)
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
// becomes sbx's exit status and that the sandbox is still removed.
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
	calls := fake.Calls()
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if got := calls[4].Args; !equal(got, []string{"remove", "--force", calls[2].Args[2]}) {
		t.Errorf("sandbox was not removed after guest failure: %q", got)
	}
}

// TestRunCreateFailureDoesNotRemoveAnything checks that a failed creation
// never calls rm — an existing sandbox with the same name must survive.
func TestRunCreateFailureDoesNotRemoveAnything(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	fake.FailCreate(t, "-run-")
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded despite a failing create")
	}
	if !strings.Contains(stderr.String(), "create") {
		t.Errorf("stderr does not mention the creation failure: %s", stderr.String())
	}
	for _, call := range fake.Calls() {
		if call.Args[0] == "remove" {
			t.Errorf("rm was called after a creation failure: %v", call.Args)
		}
	}
}

// TestRunCleansUpWhenExecFailsToStart checks cleanup after an exec that
// never reaches the guest.
func TestRunCleansUpWhenExecFailsToStart(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXEC_FAIL_START", "1")
	worktree, _ := fixtureRepo(t, disposableTOML)
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded despite an exec failure")
	}
	calls := fake.Calls()
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if got := calls[4].Args; !equal(got, []string{"remove", "--force", calls[2].Args[2]}) {
		t.Errorf("sandbox was not removed after exec failure: %q", got)
	}
}

// TestRunCleansUpOnCancellation checks that canceling sbx while the guest
// runs still removes the sandbox and propagates the interruption status.
func TestRunCleansUpOnCancellation(t *testing.T) {
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
	fake.Wait(t, 4)
	cancel()
	code := <-done

	if code != 143 { // the fake traps SIGTERM and exits 143
		t.Errorf("exit code = %d, want 143 from the interrupted guest (stderr: %s)", code, stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 5 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if got := calls[4].Args; !equal(got, []string{"remove", "--force", calls[2].Args[2]}) {
		t.Errorf("sandbox was not removed after cancellation: %q", got)
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
	if len(calls) != 6 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if !equal(calls[2].Args, []string{"image", "pull", "alpine:3.20"}) {
		t.Errorf("expected the pull as the third call, got %q", calls[2].Args)
	}
}

// TestRunRejectsUntranslatedSettingsBeforeAnything checks that settings
// this build does not translate stop the run before any msb call.
func TestRunRejectsUntranslatedSettingsBeforeAnything(t *testing.T) {
	cases := map[string]string{
		"mounts": `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "bind"
source = "data"
target = "/data"

[network]
egress = "public"
`,
		"allowlist egress": `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[network]
egress = "allowlist"
allow = ["example.com"]
`,
	}
	for name, toml := range cases {
		t.Run(name, func(t *testing.T) {
			fake := testsupport.FakeMSB(t)
			worktree, _ := fixtureRepo(t, toml)
			var stdout, stderr bytes.Buffer
			code := chdir(t, worktree, func() int {
				return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
			})
			if code == 0 {
				t.Fatalf("%s: run succeeded with untranslated settings", name)
			}
			if got := fake.Calls(); len(got) != 0 {
				t.Errorf("%s: msb was called before rejection: %v", name, got)
			}
		})
	}
}

// TestRunUniqueNames checks that two disposable runs never share a name.
func TestRunUniqueNames(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, disposableTOML)
	runOnce := func() string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := chdir(t, worktree, func() int {
			return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
		})
		if code != 0 {
			t.Fatalf("run failed: %s", stderr.String())
		}
		calls := fake.Calls()
		var creates []string
		for _, c := range calls {
			if c.Args[0] == "create" {
				creates = append(creates, c.Args[2])
			}
		}
		if len(creates) == 0 {
			t.Fatal("no create call recorded")
		}
		return creates[len(creates)-1]
	}

	first := runOnce()
	second := runOnce()
	if first == second {
		t.Errorf("two disposable runs shared the name %q", first)
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
