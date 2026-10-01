package msb

import (
	"bytes"
	"context"
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
		NetConf: "/tmp/sbx-net.yaml",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("fake msb saw %d calls, want 1", len(calls))
	}
	want := []string{
		"create", "app-main-12345678-run-abc123",
		"--image", "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--mount", "/repo/wt:/workspace",
		"--mount", "/repo/ro:/mnt/ro:ro",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--net-conf", "/tmp/sbx-net.yaml",
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
	for _, banned := range []string{"--net-conf", "--mount", "--label"} {
		if contains(got, banned) {
			t.Errorf("create argv contains %q with no mounts, labels, or policy: %q", banned, got)
		}
	}
	if !equal(got, []string{"create", "box", "--image", "alpine:3.20", "--cpus", "1.5", "--memory", "512M"}) {
		t.Errorf("create argv mismatch: %q", got)
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
	want := []string{"exec", "box-run-abc", "--workdir", "/workspace", "--", "go", "version"}
	if got := calls[0].Args; !equal(got, want) {
		t.Errorf("exec argv mismatch:\n got: %q\nwant: %q", got, want)
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

// TestRemoveExactArgv pins `msb rm <name>`.
func TestRemoveExactArgv(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	box := CLI{}
	if err := box.Remove(context.Background(), "box-run-abc"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	want := []string{"rm", "box-run-abc"}
	if got := fake.Calls()[0].Args; !equal(got, want) {
		t.Errorf("rm argv mismatch: %q", got)
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
		{"top level", `{"backend":"local"}`, "local", false},
		{"nested", `{"name":"default","context":{"backend":"local"}}`, "local", false},
		{"case insensitive", `{"Backend":"local"}`, "local", false},
		{"backend object", `{"backend":{"type":"local","name":"d"}}`, "local", false},
		{"cloud refused", `{"backend":"cloud"}`, "cloud", false},
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
