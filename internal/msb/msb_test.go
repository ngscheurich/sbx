package msb

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// TestCreateExactArgv pins the full argv of `msb create`, in order: the
// sandbox name, base resources, mounts, labels, and an optional network
// policy file.
func TestCreateExactArgv(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}

	err := box.Create(context.Background(), CreateOptions{
		Name:   "app-main-12345678-run-abc123",
		Image:  "alpine:3.20",
		CPUs:   2,
		Memory: "2G",
		Mounts: []Mount{
			{Source: "/repo/wt", Target: "/workspace"},
			{Source: "/repo/ro", Target: "/mnt/ro", ReadOnly: true},
		},
		Labels: []Label{
			{Key: "sbx.managed", Value: "1"},
			{Key: "sbx.mode", Value: "disposable"},
		},
		NoNet: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("fake msb saw %d calls, want 1", len(calls))
	}
	want := []string{
		"create", "--name", "app-main-12345678-run-abc123", "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", "/repo/wt:/workspace",
		"--mount-dir", "/repo/ro:/mnt/ro:ro",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--no-net",
	}
	if got := calls[0].Args; !equal(got, want) {
		t.Errorf("create argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestCreateWithoutNetConfAndFractionalCPUs checks the argv when no network
// policy is needed and cpus is not an integer.
func TestCreateWithoutNetConfAndFractionalCPUs(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}

	err := box.Create(context.Background(), CreateOptions{
		Name:   "box",
		Image:  "alpine:3.20",
		CPUs:   1.5,
		Memory: "512M",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got := fake.Calls()[0].Args
	for _, banned := range []string{"--no-net", "--mount-dir", "--label"} {
		if contains(got, banned) {
			t.Errorf("create argv contains %q with no mounts, labels, or policy: %q", banned, got)
		}
	}
	if !equal(got, []string{"create", "--name", "box", "alpine:3.20", "--cpus", "1.5", "--memory", "512M"}) {
		t.Errorf("create argv mismatch: %q", got)
	}
}

// TestRunExactArgv pins msb run, the native one-shot: create-time settings,
// the stream mode, and the guest command after --. msb run replaces the
// image CMD while preserving its effective entrypoint, and sbx matches that
// native behavior.
func TestRunExactArgv(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}

	var stdout, stderr bytes.Buffer
	code, err := box.Run(context.Background(), RunOptions{
		CreateOptions: CreateOptions{
			Name:   "app-main-12345678-run-abc123",
			Image:  "alpine:3.20",
			CPUs:   2,
			Memory: "2G",
			Mounts: []Mount{{Source: "/repo/wt", Target: "/workspace"}},
			Labels: []Label{{Key: "sbx.managed", Value: "1"}},
			NoNet:  true,
		},
		Workdir: "/workspace",
		Argv:    []string{"echo", "hi"},
	}, strings.NewReader("payload\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	want := []string{
		"run", "--name", "app-main-12345678-run-abc123", "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", "/repo/wt:/workspace",
		"--label", "sbx.managed=1",
		"--no-net",
		"--workdir", "/workspace",
		"--no-tty",
		"--", "echo", "hi",
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("fake msb saw %d calls, want 1", len(calls))
	}
	if got := calls[0].Args; !equal(got, want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestCreateFailureSurfacesMessage checks that a failing create reports the
// backend's own message, not a swallowed error.
func TestCreateFailureSurfacesMessage(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	fake.FailCreate(t, "box")
	box := CLI{}

	err := box.Create(context.Background(), CreateOptions{Name: "box-run-abc", Image: "alpine:3.20", Memory: "1G", CPUs: 1})
	if err == nil {
		t.Fatal("create succeeded against a failing fake")
	}
	if !strings.Contains(err.Error(), "create failed for box-run-abc") {
		t.Errorf("error does not carry the backend message: %v", err)
	}
}

// TestExecArgvAndStreams pins the `msb exec` argv and the standard-stream
// and exit-status propagation through the subprocess.
func TestExecArgvAndStreams(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXIT", "37")

	box := CLI{}
	var stdout, stderr bytes.Buffer
	code, err := box.Exec(context.Background(), "box-run-abc", "/workspace",
		[]string{"go", "version"}, strings.NewReader("payload\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if code != 37 {
		t.Errorf("exit code = %d, want 37", code)
	}
	if stdout.String() != "guest-stdout\n" {
		t.Errorf("stdout = %q, want guest output", stdout.String())
	}
	if stderr.String() != "guest-stderr\n" {
		t.Errorf("stderr = %q, want guest output", stderr.String())
	}
	if got := fake.Stdin(t); got != "payload\n" {
		t.Errorf("stdin forwarded = %q, want %q", got, "payload\n")
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("fake msb saw %d calls, want 1", len(calls))
	}
	want := []string{"exec", "box-run-abc", "--workdir", "/workspace", "--stream", "--", "go", "version"}
	if got := calls[0].Args; !equal(got, want) {
		t.Errorf("exec argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestExecUsesTtyOnTerminalStdin checks that an interactive terminal gets
// --tty instead of --stream, per msb 0.7.3's exclusivity rule.
func TestExecUsesTtyOnTerminalStdin(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXEC_NO_STDIN_READ", "1") // a PTY master never EOFs
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { ptmx.Close() })

	box := CLI{}
	code, err := box.Exec(context.Background(), "box", "/workspace", []string{"ls"}, ptmx, nil, nil)
	if err != nil || code != 0 {
		t.Fatalf("exec: code=%d err=%v", code, err)
	}
	got := fake.Calls()[0].Args
	if !contains(got, "--tty") || contains(got, "--stream") {
		t.Errorf("terminal stdin should select --tty, got %q", got)
	}
}

// TestExecTtyCancellation checks that canceling a --tty exec terminates the
// child (signaled directly, since it shares sbx's foreground group) and
// still returns the trap exit status.
func TestExecTtyCancellation(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_EXEC_SLEEP", "30")
	t.Setenv("FAKE_MSB_EXEC_NO_STDIN_READ", "1") // a PTY master never EOFs
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { ptmx.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	box := CLI{}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := box.Exec(ctx, "box", "", []string{"sleep", "30"}, ptmx, nil, nil)
		done <- result{code, err}
	}()
	fake.Wait(t, 1)
	cancel()
	res := <-done
	if res.err != nil {
		t.Fatalf("exec: %v", res.err)
	}
	if res.code != 143 {
		t.Errorf("exit code = %d, want 143 from the interrupted guest", res.code)
	}
}

// TestExecWithoutWorkdir omits the --workdir flag when no working directory
// is requested.
func TestExecWithoutWorkdir(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}
	code, err := box.Exec(context.Background(), "box", "", []string{"ls"}, nil, nil, nil)
	if err != nil || code != 0 {
		t.Fatalf("exec: code=%d err=%v", code, err)
	}
	got := fake.Calls()[0].Args
	if contains(got, "--workdir") {
		t.Errorf("exec argv contains --workdir with no workdir set: %q", got)
	}
}

// TestLocalContextParsesAndRejectsCloud checks both the happy parse of
// `msb context --format json` and the refusal of a non-local backend.
func TestLocalContextParsesAndRejectsCloud(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}

	cx, err := box.LocalContext(context.Background())
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if cx.Backend != "local" {
		t.Errorf("backend = %q, want local", cx.Backend)
	}
	want := []string{"context", "--format", "json"}
	if got := fake.Calls()[0].Args; !equal(got, want) {
		t.Errorf("context argv mismatch: %q", got)
	}

	t.Setenv("FAKE_MSB_CONTEXT_BACKEND", "cloud")
	_, err = box.LocalContext(context.Background())
	if err == nil {
		t.Fatal("context accepted a cloud backend")
	}
	if !strings.Contains(err.Error(), "cloud") || !strings.Contains(err.Error(), "local") {
		t.Errorf("refusal does not name both backends: %v", err)
	}
}

// TestSubprocessEnvForcesLocalBackend pins the environment boundary: every
// msb subprocess sees MSB_BACKEND=local even when the host environment
// selects another backend.
func TestSubprocessEnvForcesLocalBackend(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("MSB_BACKEND", "cloud")

	box := CLI{}
	if _, err := box.LocalContext(context.Background()); err != nil {
		t.Fatalf("context: %v", err)
	}
	if err := box.Create(context.Background(), CreateOptions{Name: "b", Image: "alpine:3.20", CPUs: 1, Memory: "1G"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := box.PullIfMissing(context.Background(), "alpine:3.20"); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := box.Remove(context.Background(), "b"); err != nil {
		t.Fatalf("rm: %v", err)
	}

	for i, call := range fake.Calls() {
		if call.Backend != "local" {
			t.Errorf("call %d (%v) ran with MSB_BACKEND=%q, want local", i, call.Args, call.Backend)
		}
	}
}

// TestPullIfMissing checks the inspect-then-pull sequence for a missing
// prebuilt image and the inspect-only path for a present one.
func TestPullIfMissing(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}

	if err := box.PullIfMissing(context.Background(), "alpine:3.20"); err != nil {
		t.Fatalf("pull of a present image: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("present image produced %d calls, want 1 (inspect only)", len(calls))
	}
	if !equal(calls[0].Args, []string{"image", "inspect", "alpine:3.20", "--format", "json"}) {
		t.Errorf("inspect argv mismatch: %q", calls[0].Args)
	}

	t.Setenv("FAKE_MSB_IMAGE_MISSING", "missing:latest")
	if err := box.PullIfMissing(context.Background(), "missing:latest"); err != nil {
		t.Fatalf("pull of a missing image: %v", err)
	}
	calls = fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("missing image produced %d calls, want 3", len(calls))
	}
	if !equal(calls[1].Args, []string{"image", "inspect", "missing:latest", "--format", "json"}) {
		t.Errorf("failed inspect argv mismatch: %q", calls[1].Args)
	}
	if !equal(calls[2].Args, []string{"image", "pull", "missing:latest"}) {
		t.Errorf("pull argv mismatch: %q", calls[2].Args)
	}
}

// TestPullFailureHintsAtImport checks that a failed pull surfaces both the
// inspect and pull errors plus the local-import next step, instead of the
// bare pull failure.
func TestPullFailureHintsAtImport(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "built-locally:latest")
	t.Setenv("FAKE_MSB_PULL_FAIL", "1")

	box := CLI{}
	err := box.PullIfMissing(context.Background(), "built-locally:latest")
	if err == nil {
		t.Fatal("pull of an unobtainable image succeeded")
	}
	for _, want := range []string{"built-locally:latest", "inspect", "pull failed", "msb load"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	// Exactly one inspect and one pull, no repeats.
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("fake msb saw %d calls, want 2:\n%s", len(calls), dumpCalls(calls))
	}
}

// TestRemoveExactArgv pins `msb remove --force <name>`: the sandbox is
// likely still running after exec, so removal stops it first.
func TestRemoveExactArgv(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}
	if err := box.Remove(context.Background(), "box-run-abc"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	want := []string{"remove", "--force", "box-run-abc"}
	if got := fake.Calls()[0].Args; !equal(got, want) {
		t.Errorf("remove argv mismatch: %q", got)
	}
}

// TestParseContextShapes covers the plausible `msb context --format json`
// shapes: top-level backend, one level down, case differences, a backend
// object with a type field, and output with no backend field at all.
func TestParseContextShapes(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		want    string
		wantErr bool
	}{
		{"msb 0.7.3 real output", "{\n  \"kind\": \"local\",\n  \"source\": \"MSB_BACKEND\"\n}", "local", false},
		{"msb 0.7.3 cloud", `{"kind":"cloud","source":"profile"}`, "cloud", false},
		{"top level", `{"backend":"local"}`, "local", false},
		{"nested", `{"name":"default","context":{"backend":"local"}}`, "local", false},
		{"case insensitive", `{"Backend":"local"}`, "local", false},
		{"backend object", `{"backend":{"type":"local","name":"d"}}`, "local", false},
		{"cloud refused", `{"backend":"cloud"}`, "cloud", false},
		{"backend-like key but not the backend", `{"kind_name":"default"}`, "", true},
		{"no backend field", `{"name":"default","url":"http://localhost:8338"}`, "", true},
		{"not json", `local`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx, err := parseContext(tc.out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseContext(%s) succeeded: %+v", tc.out, cx)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseContext(%s): %v", tc.out, err)
			}
			if cx.Backend != tc.want {
				t.Errorf("backend = %q, want %q", cx.Backend, tc.want)
			}
		})
	}

	// A missing field must surface the raw output so a real-host schema
	// mismatch is self-diagnosing.
	_, err := parseContext(`{"name":"default"}`)
	if err == nil || !strings.Contains(err.Error(), "no backend field found") {
		t.Errorf("error does not include the raw output: %v", err)
	}
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

func contains(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// dumpCalls renders recorded calls for failure messages.
func dumpCalls(calls []testsupport.Call) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString("  " + strings.Join(c.Args, " ") + "\n")
	}
	return b.String()
}

// TestCreateArgsFullSurface pins the create-style argument rendering for
// every translated setting: mounts, tmpfs, named and owned volumes,
// environment, network rules, DNS, TLS interception, and the secret map.
func TestCreateArgsFullSurface(t *testing.T) {
	args := CreateArgs(CreateOptions{
		Name:   "box",
		Image:  "alpine:3.20",
		CPUs:   2.5,
		Memory: "2G",
		Mounts: []Mount{
			{Source: "/src", Target: "/workspace"},
			{Source: "/notes", Target: "/mnt/notes", ReadOnly: true, IsFile: true},
		},
		Tmpfs: []Tmpfs{
			{Target: "/tmp", Size: "512M"},
			{Target: "/run", Size: "64M"},
		},
		Named: []NamedMount{{Name: "abc-cache", Target: "/cache"}},
		Owned: []OwnedMount{
			{Target: "/scratch"},
			{Target: "/data", Kind: "disk", Size: "8G"},
		},
		Env:            []string{"A=1", "B=2"},
		Labels:         []Label{{Key: "sbx.managed", Value: "1"}},
		NetRules:       []string{"allow@example.com", "allow@*.example.org"},
		TLSIntercept:   true,
		DnsNameservers: []string{"1.1.1.1", "8.8.8.8"},
		SecretConf:     "/tmp/secrets.yaml",
	})
	want := []string{
		"--name", "box",
		"alpine:3.20",
		"--cpus", "2.5",
		"--memory", "2G",
		"--mount-dir", "/src:/workspace",
		"--mount-file", "/notes:/mnt/notes:ro",
		"--tmpfs", "/tmp:512M",
		"--tmpfs", "/run:64M",
		"--mount-named", "abc-cache:/cache",
		"--mount-owned", "/scratch",
		"--mount-owned", "/data:kind=disk,size=8G",
		"--env", "A=1",
		"--env", "B=2",
		"--label", "sbx.managed=1",
		"--net-rule", "allow@example.com",
		"--net-rule", "allow@*.example.org",
		"--net-default-egress", "deny",
		"--tls-intercept",
		"--dns-nameserver", "1.1.1.1",
		"--dns-nameserver", "8.8.8.8",
		"--secret-conf", "/tmp/secrets.yaml",
	}
	if !equalStrings(args, want) {
		t.Errorf("CreateArgs mismatch:\n got: %q\nwant: %q", args, want)
	}
}

// TestCreateArgsMinimal pins the public-egress minimal form: no network
// flags at all, msb's default policy.
func TestCreateArgsMinimal(t *testing.T) {
	args := CreateArgs(CreateOptions{Image: "alpine:3.20", CPUs: 1, Memory: "1G"})
	want := []string{"alpine:3.20", "--cpus", "1", "--memory", "1G"}
	if !equalStrings(args, want) {
		t.Errorf("CreateArgs mismatch:\n got: %q\nwant: %q", args, want)
	}
}

func equalStrings(a, b []string) bool {
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
