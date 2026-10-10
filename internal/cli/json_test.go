package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

func jsonGolden(t *testing.T, name, got string) {
	t.Helper()
	if !json.Valid([]byte(got)) {
		t.Fatalf("stdout is not JSON: %q", got)
	}
	testsupport.Golden(t, filepath.Join("testdata", name+".golden"), []byte(got))
}

func TestJSONListGolden(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "proj-b", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	fake.SeedSandbox(t, "proj-a", "debian:12", "stopped", map[string]string{"sbx.managed": "1"})
	fake.SeedSandbox(t, "unowned", "alpine:3.20", "running", nil)
	for _, args := range [][]string{{"list", "--json"}, {"--plain", "list", "--json"}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("%v: code %d: %s", args, code, &stderr)
		}
		jsonGolden(t, "list-json", stdout.String())
	}
}

func TestJSONListEmptyGolden(t *testing.T) {
	testsupport.FakeMSB(t)
	var stdout, stderr bytes.Buffer
	if code := cli.Run(t.Context(), []string{"list", "--json"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, &stderr)
	}
	jsonGolden(t, "list-empty-json", stdout.String())
}

func TestJSONVersionGolden(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := cli.Run(t.Context(), []string{"version", "--json"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, &stderr)
	}
	jsonGolden(t, "version-json", stdout.String())
}

func TestJSONRejectedCommands(t *testing.T) {
	for _, args := range [][]string{{"run", "--json"}, {"exec", "--json"}, {"logs", "--json"}, {"help", "--json"}, {"--help", "--json"}, {"--json", "list"}, {"list", "--plain", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 2 {
				t.Fatalf("code %d, want usage error: %s", code, &stderr)
			}
			if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "sbx:") {
				t.Fatalf("stdout=%q stderr=%q", &stdout, &stderr)
			}
		})
	}
}

func TestJSONUpGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML+"\n[bootstrap]\nrun = 'echo ready'\n")
	jsonGolden(t, "up-json", jsonCommand(t, worktree, id, "up", "--json"))
	jsonGolden(t, "up-running-json", jsonCommand(t, worktree, id, "up", "--json"))
}

func TestJSONUpRetryAndDriftGolden(t *testing.T) {
	config := validTOML + "\n[bootstrap]\nrun = 'echo ready'\n"
	worktree, id := jsonFixture(t, config)
	jsonCommand(t, worktree, id, "up", "--json")
	config = strings.Replace(config, "echo ready", "echo changed", 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonGolden(t, "up-retry-json", jsonCommand(t, worktree, id, "up", "--retry-bootstrap", "--json"))
	config = strings.Replace(config, "cpus = 2", "cpus = 4", 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonGolden(t, "up-drift-json", jsonCommand(t, worktree, id, "up", "--allow-stale", "--json"))
}

func TestHelpAndVersionKeepNonJSONBehavior(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help", "extra"}, {"--help", "extra"}, {"version", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, &stdout, &stderr)
		}
	}
}

func TestJSONUpPortsAndImageCheckGolden(t *testing.T) {
	worktree, id := jsonFixture(t, "image_check = 'image-check.sh'\n"+validTOML+"\n[ports.web]\nguest = 80\n")
	harness.WriteImageCheckScript(t, worktree, harness.CheckScript)
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{{Sandbox: id, Name: "web", Port: 4001, Guest: 80}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	jsonGolden(t, "up-ports-json", jsonCommand(t, worktree, id, "up", "--json"))
}

func TestJSONStopGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML)
	jsonCommand(t, worktree, id, "up", "--json")
	jsonGolden(t, "stop-json", jsonCommand(t, worktree, id, "stop", "--json"))
	jsonGolden(t, "stop-stopped-json", jsonCommand(t, worktree, id, "stop", "--json"))
	jsonGolden(t, "up-started-json", jsonCommand(t, worktree, id, "up", "--json"))
}

func TestJSONRmGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML+"\n[volumes.data]\nscope = 'sandbox'\ntarget = '/data'\nkind = 'disk'\nsize = '2G'\n[volumes.logs]\nscope = 'sandbox'\ntarget = '/logs'\n")
	jsonCommand(t, worktree, id, "up", "--json")
	code, stdout, stderr := jsonRun(t, worktree, "rm", "--json")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "without confirmation") {
		t.Fatalf("rm auto-confirmed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	jsonGolden(t, "rm-json", jsonCommand(t, worktree, id, "rm", "--yes", "--json"))
}

func TestJSONRmUnknownVolumesGolden(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, validTOML)
	id := harness.SandboxIdentityOf(t, worktree)
	fake.SeedSandbox(t, id, "alpine:3.20", "stopped", map[string]string{"sbx.managed": "1"})
	jsonGolden(t, "rm-unknown-json", jsonCommand(t, worktree, id, "rm", "--yes", "--json"))
}

func TestJSONRmEmptyVolumesGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML)
	jsonCommand(t, worktree, id, "up", "--json")
	jsonGolden(t, "rm-empty-json", jsonCommand(t, worktree, id, "rm", "--yes", "--json"))
}

func TestJSONRmUnreadableVolumesGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML)
	jsonCommand(t, worktree, id, "up", "--json")
	dir, err := state.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshots", id+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	jsonGolden(t, "rm-unreadable-json", jsonCommand(t, worktree, id, "rm", "--yes", "--json"))
}

func TestJSONBuildGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML+"\n[build]\ncontext = '.'\nplatform = 'linux/amd64'\n")
	testsupport.FakeDocker(t)
	t.Setenv("TMPDIR", t.TempDir())
	jsonGolden(t, "build-json", jsonCommand(t, worktree, id, "build", "--json"))
	got := jsonCommand(t, worktree, id, "build", "--keep-archive", "--json")
	var result struct {
		Archive string `json:"archive"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Archive); err != nil {
		t.Fatalf("archive was not kept: %v", err)
	}
	jsonGolden(t, "build-archive-json", strings.ReplaceAll(got, result.Archive, "<archive>"))
}

func TestJSONPortPruneGolden(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "kept", "alpine:3.20", "stopped", map[string]string{"sbx.managed": "1"})
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := state.UpdatePortReservations(func([]state.PortReservation) ([]state.PortReservation, error) {
		return []state.PortReservation{
			{Sandbox: "gone", Name: "web", Port: 4001, Guest: 80},
			{Sandbox: "kept", Name: "web", Port: 4002, Guest: 80},
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"port-prune-json", "port-prune-empty-json"} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(t.Context(), []string{"port", "prune", "--json"}, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("code %d: %s", code, &stderr)
		}
		jsonGolden(t, name, stdout.String())
	}
}

func TestJSONStatusGolden(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, validTOML)
	id := harness.SandboxIdentityOf(t, worktree)
	jsonGolden(t, "status-absent-json", jsonCommand(t, worktree, id, "status", "--json"))
	fake.SeedSandbox(t, id, "alpine:3.20", "running", nil)
	jsonGolden(t, "status-unowned-json", jsonCommand(t, worktree, id, "status", "--json"))
	fake.SeedSandbox(t, id, "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	fake.SeedPublishedPort(t, id, 8080, 80)
	jsonGolden(t, "status-json", jsonCommand(t, worktree, id, "status", "--json"))
}

func TestJSONStatusStatesGolden(t *testing.T) {
	config := validTOML + "\n[bootstrap]\nrun = 'echo ready'\n"
	worktree, id := jsonFixture(t, config)
	jsonCommand(t, worktree, id, "up", "--json")
	for _, tc := range []struct{ name, toml string }{
		{name: "status-clean-json", toml: config},
		{name: "status-changed-json", toml: strings.Replace(config, "echo ready", "echo changed", 1)},
		{name: "status-drift-json", toml: strings.Replace(config, "cpus = 2", "cpus = 4", 1)},
		{name: "status-unknown-json", toml: config + "\n[[mounts]]\ntype = 'bind'\nsource = './missing'\ntarget = '/missing'\n"},
	} {
		if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(tc.toml), 0o644); err != nil {
			t.Fatal(err)
		}
		got := jsonCommand(t, worktree, id, "status", "--json")
		var report struct {
			CreatedAt string `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(got), &report); err != nil {
			t.Fatal(err)
		}
		got = strings.ReplaceAll(got, report.CreatedAt, "<created-at>")
		got = strings.ReplaceAll(got, worktree, "<worktree>")
		jsonGolden(t, tc.name, got)
	}
}

func TestJSONPlanGolden(t *testing.T) {
	worktree, id := jsonFixture(t, validTOML)
	got := jsonCommand(t, worktree, id, "plan", "--json")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sandbox", "config", "translation", "create_argv"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("plan lacks %s", key)
		}
	}
	if strings.Contains(got, "sbx changed nothing") {
		t.Fatalf("JSON plan contains human report: %q", got)
	}
	jsonGolden(t, "plan-cli-json", normalizePlan(t, got))
}

func TestJSONPlanLiveGolden(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := harness.FixtureRepo(t, validTOML)
	id := harness.SandboxIdentityOf(t, worktree)
	fake.SeedSandbox(t, id, "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	fake.SeedPublishedPort(t, id, 4001, 80)
	got := jsonCommand(t, worktree, id, "plan", "--json")
	jsonGolden(t, "plan-cli-live-json", normalizePlan(t, got))
}

func normalizePlan(t *testing.T, got string) string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"common_git_dir", "worktree_root", "volume_namespace", "project"} {
		var value string
		if err := json.Unmarshal(doc[key], &value); err != nil {
			t.Fatal(err)
		}
		got = strings.ReplaceAll(got, value, "<"+key+">")
	}
	return got
}

func TestJSONFailuresKeepStdoutEmpty(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		env   string
		value string
		extra string
	}{
		{name: "listing", args: []string{"list", "--json"}, env: "FAKE_MSB_LS_FAIL", value: "1"},
		{name: "status", args: []string{"status", "--json"}, env: "FAKE_MSB_LS_FAIL", value: "1"},
		{name: "bootstrap", args: []string{"up", "--json"}, env: "FAKE_MSB_EXIT", value: "37", extra: "\n[bootstrap]\nrun = 'false'\n"},
		{name: "docker", args: []string{"build", "--json"}, env: "FAKE_DOCKER_BUILD_FAIL", value: "1", extra: "\n[build]\ncontext = '.'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worktree, _ := jsonFixture(t, validTOML+tc.extra)
			testsupport.FakeDocker(t)
			t.Setenv(tc.env, tc.value)
			code, stdout, stderr := harness.SbxRun(t, worktree, tc.args, nil)
			if code != 1 || stdout != "" || !strings.Contains(stderr, "sbx:") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestJSONGuestArgumentsPassThrough(t *testing.T) {
	for _, cmd := range []string{"run", "exec"} {
		t.Run(cmd, func(t *testing.T) {
			worktree, _ := jsonFixture(t, validTOML)
			code, stdout, stderr := harness.SbxRun(t, worktree, []string{cmd, "--", "echo", "--json"}, nil)
			if code != 0 || stdout != "guest-stdout\n" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

type closedWriter struct{}

// Write reports a closed output stream.
func (closedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestJSONWriteFailure(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := cli.Run(t.Context(), []string{"version", "--json"}, nil, closedWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "write JSON output") {
		t.Fatalf("code=%d stderr=%q", code, &stderr)
	}
}

func TestJSONIgnoresStyling(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("CLICOLOR_FORCE", "1")
	for _, args := range [][]string{{"list", "--json"}, {"--plain", "list", "--json"}, {"version", "--json"}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(t.Context(), args, nil, &stdout, &stderr); code != 0 || !json.Valid(stdout.Bytes()) || bytes.ContainsRune(stdout.Bytes(), '\x1b') {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, &stdout, &stderr)
		}
	}
}

func jsonFixture(t *testing.T, toml string) (string, string) {
	t.Helper()
	worktree, _ := harness.PersistentFixture(t, toml)
	return worktree, harness.SandboxIdentityOf(t, worktree)
}

func jsonCommand(t *testing.T, worktree, id string, args ...string) string {
	t.Helper()
	code, stdout, stderr := jsonRun(t, worktree, args...)
	if code != 0 {
		t.Fatalf("%v: code %d: %s", args, code, stderr)
	}
	return strings.ReplaceAll(stdout, id, "<sandbox-id>")
}

func jsonRun(t *testing.T, worktree string, args ...string) (int, string, string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := harness.SbxRun(t, worktree, args, nil)
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	return code, stdout, stderr
}
