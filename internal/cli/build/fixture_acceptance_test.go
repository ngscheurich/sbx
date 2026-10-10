package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

func fixtureCommand(t *testing.T, worktree string, args ...string) string {
	t.Helper()
	code, out, errOut := harness.SbxRun(t, worktree, args, nil)
	if code != 0 {
		t.Fatalf("sbx %s: exit %d\n%s\n%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func fixtureBuild(t *testing.T, worktree string) {
	t.Helper()
	testsupport.FakeDocker(t)
	fixtureCommand(t, worktree, "build")
}

func TestFixtureRestrictedPersistentTranslationAndSecretSafety(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("SBX_FIXTURE_TOKEN", "throwaway-fixture-token")
	worktree := harness.FixtureWorktrees(t, fixtureRestrictedDir, 1)[0]
	fixtureBuild(t, worktree)
	fixtureCommand(t, worktree, "up")
	id := harness.SandboxIdentityOf(t, worktree)
	ns := fixtureVolumeNamespace(t, worktree)
	want := []string{"create", "--name", id, "sbx-restricted-cli:latest",
		"--cpus", "2", "--memory", "2G",
		"--mount-dir", worktree + ":/workspace",
		"--mount-file", filepath.Join(worktree, "host-notes.txt") + ":/mnt/host-notes.txt:ro",
		"--mount-dir", filepath.Join(worktree, "host-state") + ":/mnt/host-state",
		"--mount-named", ns + "-go_build:/root/.cache/go-build",
		"--mount-named", ns + "-go_mod:/go/pkg/mod",
		"--label", "sbx.managed=1", "--label", "sbx.mode=persistent",
		"--label", "sbx.worktree=" + worktree,
		"--net-rule", "allow@proxy.golang.org", "--net-rule", "allow@sum.golang.org",
		"--net-rule", "allow@example.com", "--tls-intercept",
		"--dns-nameserver", "1.1.1.1", "--dns-nameserver", "8.8.8.8",
		"--secret-conf", "<path>",
	}
	var creations int
	for _, call := range fake.Calls() {
		if call.Args[0] != "create" {
			continue
		}
		creations++
		if got := harness.SpliceGenerated(call.Args); !slices.Equal(got, want) {
			t.Errorf("persistent translation:\n got: %q\nwant: %q", got, want)
		}
		if got := fake.SecretConf(t, call.Index); got != "TEST_TOKEN:\n  value: \"${SBX_FIXTURE_TOKEN}\"\n  allow: [\"example.com\"]\n" {
			t.Errorf("secret configuration: %q", got)
		}
	}
	if creations != 1 {
		t.Fatalf("persistent creation count: %d, want 1", creations)
	}
	fixtureCommand(t, worktree, "exec", "--", "go", "version")
	fixtureCommand(t, worktree, "stop")
	t.Setenv("FAKE_MSB_START_REQUIRES_ENV", "SBX_FIXTURE_TOKEN")
	fixtureCommand(t, worktree, "up")
	for _, args := range [][]string{{"plan"}, {"status"}, {"plan", "--json"}, {"status", "--json"}} {
		if out := fixtureCommand(t, worktree, args...); strings.Contains(out, "throwaway-fixture-token") {
			t.Fatalf("secret value leaked into %s", args)
		}
	}
	for _, call := range fake.Calls() {
		if strings.Contains(strings.Join(call.Args, " "), "throwaway-fixture-token") {
			t.Fatal("secret value leaked into backend arguments")
		}
	}
	if entries := tmpdirEntries(t); len(entries) != 0 {
		t.Errorf("generated files survived: %v", entries)
	}
	fixtureCommand(t, worktree, "rm", "--yes")
	if got := fake.VolumeNames(); !slices.Equal(got, []string{ns + "-go_build", ns + "-go_mod"}) {
		t.Errorf("Project volumes after rm: %v", got)
	}
}

func TestFixtureRestrictedFailsClosedInBothModes(t *testing.T) {
	for _, mode := range []string{"up", "run"} {
		for _, failure := range []string{"missing secret", "cloud", "volume conflict"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				fake := testsupport.FakeMSB(t)
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				t.Setenv("SBX_FIXTURE_TOKEN", "throwaway")
				worktree := harness.FixtureWorktrees(t, fixtureRestrictedDir, 1)[0]
				want := ""
				switch failure {
				case "missing secret":
					if err := os.Unsetenv("SBX_FIXTURE_TOKEN"); err != nil {
						t.Fatal(err)
					}
					want = "SBX_FIXTURE_TOKEN"
				case "cloud":
					t.Setenv("FAKE_MSB_CONTEXT_BACKEND", "cloud")
					want = "cloud"
				case "volume conflict":
					fake.SeedVolume(t, harness.ProjectVolumeName(t, worktree, "go_mod"), "disk", testsupport.Int64(1<<30), nil)
					want = "conflict"
				}
				code, _, errOut := harness.SbxRun(t, worktree, []string{mode}, nil)
				if code == 0 || !strings.Contains(errOut, want) {
					t.Fatalf("exit %d, stderr: %s", code, errOut)
				}
				for _, call := range fake.Calls() {
					switch call.Args[0] {
					case "context", "ls", "inspect", "volumes":
					default:
						t.Errorf("backend mutation/image use before rejection: %q", call.Args)
					}
				}
			})
		}
	}
}

func TestFixtureStatefulCompleteLifecycle(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	worktrees := harness.FixtureWorktrees(t, fixtureStatefulDir, 2)
	fixtureBuild(t, worktrees[0])
	creates, _, removes := harness.CheckSandboxCalls(fake)
	if len(creates) != 1 || len(removes) != 1 {
		t.Fatalf("build did not check and clean the image: creates %d, removes %d", len(creates), len(removes))
	}
	check := creates[0]
	wantCheck := []string{"create", "--name", check.Args[2], "sbx-stateful-web:latest",
		"--cpus", "2", "--memory", "4G", "--label", "sbx.managed=1",
		"--label", "sbx.mode=image-check", "--label", "sbx.worktree=" + worktrees[0]}
	if !slices.Equal(check.Args, wantCheck) {
		t.Errorf("isolated image check: %q", check.Args)
	}
	script, err := os.ReadFile(filepath.Join(worktrees[0], "image-check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.Stdin(t); got != string(script) {
		t.Errorf("check script was not copied verbatim: %q", got)
	}
	if fake.SandboxExists(t, check.Args[2]) {
		t.Fatal("image-check sandbox survived build")
	}

	ports := make([]int, 2)
	for i, worktree := range worktrees {
		out := fixtureCommand(t, worktree, "up", "--json")
		var result struct {
			Sandbox   string `json:"sandbox"`
			Created   bool   `json:"created"`
			Bootstrap string `json:"bootstrap"`
			Published []struct {
				Host  int `json:"host"`
				Guest int `json:"guest"`
			} `json:"published_ports"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if !result.Created || result.Bootstrap != "complete" || len(result.Published) != 1 || result.Published[0].Guest != 4000 {
			t.Fatalf("up result: %s", out)
		}
		ports[i] = result.Published[0].Host
		if ports[i] < 4001 || ports[i] > 4099 {
			t.Fatalf("port outside reservation range: %d", ports[i])
		}
		fixtureCommand(t, worktree, "up")
		id := result.Sandbox
		want := []string{"create", "--name", id, "sbx-stateful-web:latest",
			"--cpus", "2", "--memory", "4G", "--mount-dir", worktree + ":/workspace",
			"--mount-named", harness.ProjectVolumeName(t, worktree, "shared_cache") + ":/opt/shared-cache",
			"--mount-owned", "/vm/artifacts:kind=disk,size=8G", "--mount-owned", "/var/lib/sbx-app:kind=disk,size=2G",
			"--env", "SBX_DATA_DIR=/var/lib/sbx-app", "--label", "sbx.managed=1",
			"--label", "sbx.mode=persistent", "--label", "sbx.worktree=" + worktree,
			"--port", fmt.Sprintf("127.0.0.1:%d:4000", ports[i]), "--fs-conf", "<path>"}
		var creations, bootstraps int
		for _, call := range fake.Calls() {
			if len(call.Args) > 2 && call.Args[0] == "create" && call.Args[2] == id {
				creations++
				if got := harness.SpliceGenerated(call.Args); !slices.Equal(got, want) {
					t.Errorf("persistent translation:\n got: %q\nwant: %q", got, want)
				}
				assertFixtureTmpfs(t, fake, call)
			}
			if len(call.Args) > 1 && call.Args[0] == "exec" && call.Args[1] == id && slices.Contains(call.Args, `printf "bootstrapped\n" > "$SBX_DATA_DIR/bootstrapped"`) {
				bootstraps++
			}
		}
		if creations != 1 || bootstraps != 1 {
			t.Errorf("idempotent up: %d creations, %d Bootstraps", creations, bootstraps)
		}
		fixtureCommand(t, worktree, "exec", "--", "./scripts/test.sh")
	}
	if ports[0] == ports[1] {
		t.Fatal("sibling worktrees share a host port")
	}
	if harness.ProjectVolumeName(t, worktrees[0], "shared_cache") != harness.ProjectVolumeName(t, worktrees[1], "shared_cache") {
		t.Fatal("sibling worktrees do not share the Project volume")
	}

	for _, status := range []string{"0", "37"} {
		t.Setenv("FAKE_MSB_EXIT", status)
		code, out, errOut := harness.SbxRun(t, worktrees[0], []string{"run", "--", "./scripts/test.sh"}, nil)
		if fmt.Sprint(code) != status || !strings.Contains(out, "guest-stdout") || !strings.Contains(errOut, "guest-stderr") {
			t.Fatalf("run exit/output propagation: %d %q %q", code, out, errOut)
		}
		call := fake.Calls()[len(fake.Calls())-1]
		args := call.Args
		sep := slices.Index(args, "--")
		want := []string{"run", "sbx-stateful-web:latest", "--cpus", "2", "--memory", "4G",
			"--mount-dir", worktrees[0] + ":/workspace",
			"--mount-named", harness.ProjectVolumeName(t, worktrees[0], "shared_cache") + ":/opt/shared-cache",
			"--mount-owned", "/vm/artifacts:kind=disk,size=8G", "--mount-owned", "/var/lib/sbx-app:kind=disk,size=2G",
			"--env", "SBX_DATA_DIR=/var/lib/sbx-app", "--label", "sbx.managed=1",
			"--label", "sbx.mode=disposable", "--label", "sbx.worktree=" + worktrees[0],
			"--fs-conf", "<path>", "--workdir", "/workspace", "--no-tty"}
		if sep < 0 || !slices.Equal(harness.SpliceGenerated(args[:sep]), want) {
			t.Fatalf("disposable translation: %q", args)
		}
		guest := args[sep+1:]
		if len(guest) != 5 || guest[0] != "/bin/sh" || guest[1] != "-c" || guest[3] != "sbx-bootstrap" || guest[4] != "./scripts/test.sh" || !strings.Contains(guest[2], `printf "bootstrapped\n" > "$SBX_DATA_DIR/bootstrapped"`) || !strings.Contains(guest[2], `exec "$@"`) {
			t.Errorf("Bootstrap/argv wrapper: %q", guest)
		}
		assertFixtureTmpfs(t, fake, call)
		for _, name := range fake.VolumeNames() {
			if strings.HasPrefix(name, "owned-run-") {
				t.Errorf("disposable Sandbox volume survived: %s", name)
			}
		}
	}
	t.Setenv("FAKE_MSB_EXIT", "0")
	beforeStop := fake.VolumeNames()
	fixtureCommand(t, worktrees[0], "stop")
	fixtureCommand(t, worktrees[0], "up")
	if !slices.Equal(beforeStop, fake.VolumeNames()) {
		t.Fatal("stop/start replaced Sandbox volumes")
	}
	fixtureCommand(t, worktrees[0], "rm", "--yes")
	pruned := fixtureCommand(t, worktrees[0], "port", "prune", "--json")
	if !strings.Contains(pruned, harness.SandboxIdentityOf(t, worktrees[0])) || strings.Contains(pruned, harness.SandboxIdentityOf(t, worktrees[1])) {
		t.Errorf("prune removed the wrong reservations: %s", pruned)
	}
	fixtureCommand(t, worktrees[1], "rm", "--yes")
	if want := []string{harness.ProjectVolumeName(t, worktrees[0], "shared_cache")}; !slices.Equal(fake.VolumeNames(), want) {
		t.Errorf("volumes after both sandboxes removed: %v, want %v", fake.VolumeNames(), want)
	}
	if entries := tmpdirEntries(t); len(entries) != 0 {
		t.Errorf("temporary archives/configuration survived: %v", entries)
	}
}

func TestFixtureStatefulFailedDisposableCleansPrivateStorage(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree := harness.FixtureWorktrees(t, fixtureStatefulDir, 1)[0]
	fixtureBuild(t, worktree)
	t.Setenv("FAKE_MSB_EXEC_FAIL_START", "1")
	code, _, errOut := harness.SbxRun(t, worktree, []string{"run", "--", "./scripts/test.sh"}, nil)
	if code != 127 {
		t.Fatalf("startup failure exit %d, want 127: %s", code, errOut)
	}
	want := []string{harness.ProjectVolumeName(t, worktree, "shared_cache")}
	if got := fake.VolumeNames(); !slices.Equal(got, want) {
		t.Errorf("failed disposable run left private storage: got %v, want %v", got, want)
	}
}

func TestFixtureStatefulImageCheckFailureBlocksBothModes(t *testing.T) {
	for _, mode := range []string{"up", "run"} {
		t.Run(mode, func(t *testing.T) {
			fake := testsupport.FakeMSB(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			worktree := harness.FixtureWorktrees(t, fixtureStatefulDir, 1)[0]
			fixtureBuild(t, worktree)
			t.Setenv("FAKE_MSB_IMAGE_DIGEST", "sha256:changed-image")
			t.Setenv("FAKE_MSB_CHECK_EXIT", "19")
			start := len(fake.Calls())
			code, _, errOut := harness.SbxRun(t, worktree, []string{mode}, nil)
			if code == 0 || !strings.Contains(errOut, "image check failed with exit status 19") {
				t.Fatalf("unchecked image used: exit %d, %s", code, errOut)
			}
			for _, call := range fake.Calls()[start:] {
				if call.Args[0] == "run" || (call.Args[0] == "create" && !strings.Contains(call.Args[2], "-imgchk-")) {
					t.Errorf("worktree sandbox used after failed check: %q", call.Args)
				}
			}
			creates, _, removes := harness.CheckSandboxCalls(fake)
			if len(creates) != 2 || len(removes) != 2 {
				t.Errorf("failed check was not cleaned: %d creates, %d removes", len(creates), len(removes))
			}
		})
	}
}

func TestFixtureStatefulBootstrapRecoveryAndDrift(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree := harness.FixtureWorktrees(t, fixtureStatefulDir, 1)[0]
	fixtureBuild(t, worktree)
	t.Setenv("FAKE_MSB_EXIT", "23")
	code, _, errOut := harness.SbxUp(t, worktree)
	if code == 0 || !strings.Contains(errOut, "bootstrap") {
		t.Fatalf("Bootstrap failure was not reported: exit %d, %s", code, errOut)
	}
	t.Setenv("FAKE_MSB_EXIT", "0")
	code, _, errOut = harness.SbxUp(t, worktree)
	if code == 0 || !strings.Contains(errOut, "incomplete") {
		t.Fatalf("partial Bootstrap silently retried: exit %d, %s", code, errOut)
	}
	fixtureCommand(t, worktree, "up", "--retry-bootstrap")
	if out := fixtureCommand(t, worktree, "status"); !strings.Contains(out, "bootstrap: complete") {
		t.Fatalf("Bootstrap retry was not recorded: %s", out)
	}

	path := filepath.Join(worktree, "sbx.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	harness.WriteFile(t, path, strings.Replace(string(data), `memory = "4G"`, `memory = "5G"`, 1))
	start := len(fake.Calls())
	code, _, errOut = harness.SbxUp(t, worktree)
	if code == 0 || !strings.Contains(errOut, "drift") {
		t.Fatalf("Creation drift did not block up: exit %d, %s", code, errOut)
	}
	for _, call := range fake.Calls()[start:] {
		if call.Args[0] == "create" || call.Args[0] == "start" || call.Args[0] == "remove" {
			t.Errorf("drifted sandbox mutated: %q", call.Args)
		}
	}
	fixtureCommand(t, worktree, "run", "--", "./scripts/test.sh")
	fixtureCommand(t, worktree, "exec", "--allow-stale", "--", "./scripts/test.sh")
	fixtureCommand(t, worktree, "rm", "--yes")
}

func assertFixtureTmpfs(t *testing.T, fake testsupport.Log, call testsupport.Call) {
	t.Helper()
	if got := fake.FsConf(t, call.Index); got != "mounts:\n  - tmpfs: { size: \"512M\" }\n    target: \"/tmp\"\n" {
		t.Errorf("tmpfs translation: %q", got)
	}
}

func TestFixtureStatefulGuestScriptsAreExecutable(t *testing.T) {
	worktree := harness.FixtureWorktrees(t, fixtureStatefulDir, 1)[0]
	for _, script := range []string{"image-check.sh", "scripts/setup.sh", "scripts/test.sh", "scripts/server.sh"} {
		info, err := os.Stat(filepath.Join(worktree, script))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s is not executable in the copied Workspace", script)
		}
	}
}
