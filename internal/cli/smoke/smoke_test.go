package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

func TestMain(m *testing.M) { harness.Main(m) }

func TestRealHostSmoke(t *testing.T) {
	if os.Getenv("SBX_SMOKE") != "1" {
		t.Skip("not run: opt in with SBX_SMOKE=1; requires local msb, virtualization, and Docker/buildx")
	}
	if reasons := missingPrerequisites(t.Context()); len(reasons) != 0 {
		t.Skip("not run: " + strings.Join(reasons, "; "))
	}
	t.Log("real-host smoke: fixture Project volumes and built images are retained; no user volumes are deleted")
	exerciseFixtures(t, buildSbx(t))
	t.Log("unverified: live network allowlist enforcement, secret destination enforcement, volume deletion, HTTP behavior, signal forwarding")
}

func missingPrerequisites(ctx context.Context) []string {
	var missing []string
	for _, binary := range []string{"git", "msb", "docker"} {
		if _, err := exec.LookPath(binary); err != nil {
			missing = append(missing, binary+" is not on PATH")
		}
	}
	if _, err := exec.LookPath("docker"); err == nil {
		for _, args := range [][]string{{"info"}, {"buildx", "version"}} {
			probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			cmd := exec.CommandContext(probeCtx, "docker", args...)
			cmd.WaitDelay = time.Second
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				missing = append(missing, fmt.Sprintf("docker %s unavailable: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out))))
			}
		}
	}
	switch runtime.GOOS {
	case "linux":
		f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			missing = append(missing, "virtualization unavailable: /dev/kvm cannot be opened read-write: "+err.Error())
		} else if err := f.Close(); err != nil {
			missing = append(missing, "virtualization probe: "+err.Error())
		}
	case "darwin":
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(probeCtx, "sysctl", "-n", "kern.hv_support")
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != "1" {
			missing = append(missing, "virtualization unavailable: kern.hv_support is not 1")
		}
	default:
		missing = append(missing, "unsupported host: "+runtime.GOOS+" (requires macOS or Linux)")
	}
	return missing
}

func buildSbx(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "sbx")
	command(t.Context(), t, "../../..", "go", "build", "-o", binary, "./cmd/sbx")
	return binary
}

func command(ctx context.Context, t *testing.T, dir, binary string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s in %s: %v\nstdout:\n%s\nstderr:\n%s", filepath.Base(binary), strings.Join(args, " "), dir, err, out.String(), errOut.String())
	}
	return out.String()
}

type upResult struct {
	Sandbox   string `json:"sandbox"`
	Created   bool   `json:"created"`
	Bootstrap string `json:"bootstrap"`
	Published []struct {
		Name  string `json:"name"`
		Host  int    `json:"host"`
		Guest int    `json:"guest"`
	} `json:"published_ports"`
}

func exerciseFixtures(t *testing.T, binary string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SBX_FIXTURE_TOKEN", "sbx-smoke-throwaway-token")
	restricted := harness.FixtureWorktrees(t, "../../../fixtures/restricted-cli", 1)[0]
	web := harness.FixtureWorktrees(t, "../../../fixtures/stateful-web", 2)
	run := func(dir string, args ...string) string {
		t.Helper()
		return command(t.Context(), t, dir, binary, append([]string{"--plain"}, args...)...)
	}

	var cleanup []string
	// Register before up: Bootstrap failure can leave a newly created sandbox.
	t.Cleanup(func() {
		for _, dir := range cleanup {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			cmd := exec.CommandContext(ctx, binary, "--plain", "rm", "--yes")
			cmd.Dir = dir
			cmd.WaitDelay = 5 * time.Second
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Errorf("cleanup failed for smoke worktree %s: %v\n%s", dir, err, out)
			}
		}
	})
	for _, dir := range web {
		var status struct {
			Exists bool `json:"exists"`
		}
		if err := json.Unmarshal([]byte(run(dir, "status", "--json")), &status); err != nil {
			t.Fatal(err)
		}
		if status.Exists {
			t.Fatalf("refusing smoke worktree %s: its sandbox already exists", dir)
		}
	}

	run(restricted, "build")
	t.Log("exercised: restricted CLI image build and import")
	run(restricted, "run", "--", "go", "version")
	t.Log("exercised: restricted CLI disposable go version with configured mounts, Project volumes, and throwaway secret")
	run(web[0], "build")
	t.Log("exercised: stateful web image build, import, and isolated Image check")
	up := func(dir string) upResult {
		t.Helper()
		var result upResult
		if err := json.Unmarshal([]byte(run(dir, "up", "--json")), &result); err != nil {
			t.Fatal(err)
		}
		if result.Sandbox == "" || result.Bootstrap != "complete" || len(result.Published) != 1 {
			t.Fatalf("unexpected up result: %+v", result)
		}
		p := result.Published[0]
		if p.Name != "web" || p.Guest != 4000 || p.Host < 4001 || p.Host > 4099 {
			t.Fatalf("unexpected loopback port reservation: %+v", p)
		}
		sandbox, err := (msb.CLI{}).Inspect(t.Context(), result.Sandbox)
		if err != nil {
			t.Fatal(err)
		}
		var bindings []struct {
			HostBind  string `json:"host_bind"`
			HostPort  int    `json:"host_port"`
			GuestPort int    `json:"guest_port"`
			Host      string `json:"host"`
			Guest     int    `json:"guest"`
		}
		if err := json.Unmarshal(sandbox.EffectiveConfig().Ports, &bindings); err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 1 {
			t.Fatalf("unexpected backend bindings: %+v", bindings)
		}
		binding := bindings[0]
		current := binding.HostBind == "127.0.0.1" && binding.HostPort == p.Host && binding.GuestPort == p.Guest
		legacy := binding.Host == fmt.Sprintf("127.0.0.1:%d", p.Host) && binding.Guest == p.Guest
		if !current && !legacy {
			t.Fatalf("backend did not report the expected loopback binding: %+v", binding)
		}
		return result
	}
	cleanup = append(cleanup, web[0])
	first := up(web[0])
	reused := up(web[0])
	cleanup = append(cleanup, web[1])
	second := up(web[1])
	if !first.Created || reused.Created || !second.Created || first.Sandbox != reused.Sandbox || first.Published[0].Host != reused.Published[0].Host || first.Sandbox == second.Sandbox || first.Published[0].Host == second.Published[0].Host {
		t.Fatalf("identity/port reuse or isolation failed: first=%+v reused=%+v second=%+v", first, reused, second)
	}
	t.Log("exercised: two-worktree persistent creation, Bootstrap completion, identity reuse, distinct identities and loopback port reservations")
	run(web[0], "exec", "--", "./scripts/test.sh")
	run(web[0], "run", "--", "./scripts/test.sh")
	t.Log("exercised: stateful web scripts in persistent and bootstrapped disposable sandboxes")
	run(web[0], "exec", "--", "sh", "-c", `echo a > "$SBX_DATA_DIR/marker"`)
	run(web[1], "exec", "--", "test", "!", "-e", "/var/lib/sbx-app/marker")
	t.Log("exercised: Sandbox data isolation between sibling worktrees")
	for _, dir := range web {
		run(dir, "rm", "--yes")
	}
	cleanup = nil
	t.Log("exercised: removal of the two persistent sandboxes created by this smoke test (volume deletion not asserted)")
}

func TestSmokeMissingPrerequisitesReportsNotRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRealHostSmoke$", "-test.v", "-test.timeout=1m")
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "SBX_SMOKE=1", "PATH="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prerequisite probe: %v\n%s", err, out)
	}
	for _, want := range []string{"not run:", "msb is not on PATH", "docker is not on PATH", "--- SKIP: TestRealHostSmoke"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("prerequisite report omitted %q: %s", want, out)
		}
	}
}

func TestSmokeCleanupAfterBootstrapFailure(t *testing.T) {
	if binary := os.Getenv("SBX_SMOKE_FAILURE_BINARY"); binary != "" {
		exerciseFixtures(t, binary)
		return
	}
	fake := testsupport.FakeMSB(t)
	testsupport.FakeDocker(t)
	fake.SeedSandbox(t, "unrelated-user-sandbox", "alpine", "running", nil)
	binary := buildSbx(t)
	wrapper := filepath.Join(t.TempDir(), "sbx")
	script := "#!/bin/sh\nif [ \"$2\" = up ]; then export FAKE_MSB_EXIT=23; fi\nexec \"$SBX_SMOKE_CLI\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSmokeCleanupAfterBootstrapFailure$", "-test.v", "-test.timeout=1m")
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "SBX_SMOKE_FAILURE_BINARY="+wrapper, "SBX_SMOKE_CLI="+binary)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "bootstrap failed") {
		t.Fatalf("expected Bootstrap failure: %v\n%s", err, out)
	}
	for _, call := range fake.Calls() {
		if call.Args[0] == "create" && fake.SandboxExists(t, call.Args[2]) {
			t.Errorf("smoke sandbox survived failure: %s", call.Args[2])
		}
		if call.Args[0] == "remove" && call.Args[2] == "unrelated-user-sandbox" {
			t.Fatal("smoke attempted to remove an unrelated sandbox")
		}
	}
	if !fake.SandboxExists(t, "unrelated-user-sandbox") {
		t.Fatal("smoke removed an unrelated sandbox")
	}
}

func TestSmokeFlowWithFakeExecutables(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	testsupport.FakeDocker(t)
	exerciseFixtures(t, buildSbx(t))
	var removed, created []string
	for _, call := range fake.Calls() {
		if call.Args[0] == "create" {
			created = append(created, call.Args[2])
		}
		if call.Args[0] == "remove" {
			removed = append(removed, call.Args[2])
		}
		if call.Args[0] == "volume" {
			t.Errorf("smoke modified volumes directly: %q", call.Args)
		}
	}
	slices.Sort(created)
	slices.Sort(removed)
	if len(created) != 3 || !slices.Equal(created, removed) {
		t.Errorf("smoke cleanup did not match its creations: created %v, removed %v", created, removed)
	}
	t.Log("runner checked with fake executables only; no real-host behavior verified")
}
