// Fixture tests: the acceptance fixtures are copied into temporary Git
// repositories and exercised through the real CLI paths, keeping the
// fixtures in sync with the automated tests.
package build

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/cli"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/harness"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// fixtureRestrictedDir is the restricted CLI fixture's path relative to this
// package.
const fixtureRestrictedDir = "../../../fixtures/restricted-cli"

func TestFixtureRestrictedRejectsUnknownBuildField(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("SBX_FIXTURE_TOKEN", "throwaway-fixture-token")
	worktree := harness.FixtureWorktrees(t, fixtureRestrictedDir, 1)[0]
	variant := testsupport.FixtureTOML(t,
		filepath.Join(fixtureRestrictedDir, "sbx.toml")) + "\nunsupported = true\n"
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(variant), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run", "--", "go", "version"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded with an unknown build field")
	}
	if got := fake.Calls(); len(got) != 0 {
		t.Errorf("msb was called before rejection: %v", got)
	}
}

// TestFixtureRestrictedBuildVerbatim copies the complete fixture and checks
// that `sbx build` builds and imports its image: the build runs no Project
// volume compatibility check (it mutates no sandbox) and needs no secret's
// host variable, so the verbatim fixture builds as-is.
func TestFixtureRestrictedBuildVerbatim(t *testing.T) {
	msbLog := testsupport.FakeMSB(t)
	dockerLog := testsupport.FakeDocker(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("FAKE_MSB_LOAD_REQUIRES", dockerLog.SavedMarker())
	worktree := harness.FixtureWorktrees(t, fixtureRestrictedDir, 1)[0]

	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"build"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("build failed on the verbatim fixture: %s", stderr.String())
	}

	dockerCalls := dockerLog.Calls()
	if len(dockerCalls) != 2 {
		t.Fatalf("fake docker saw %d calls:\n%s", len(dockerCalls), dockerCallDump(dockerCalls))
	}
	wantBuild := []string{"build",
		"--platform", "linux/" + runtime.GOARCH,
		"--file", filepath.Join(worktree, "Dockerfile"),
		"--tag", "sbx-restricted-cli:latest",
		worktree,
	}
	if got := dockerCalls[0].Args; !harness.Equal(got, wantBuild) {
		t.Errorf("docker build argv mismatch:\n got: %q\nwant: %q", got, wantBuild)
	}
	archive := archiveFromSaveCall(t, dockerCalls[1])

	msbCalls := msbLog.Calls()
	if len(msbCalls) != 2 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(msbCalls), harness.CallDump(msbCalls))
	}
	if got := msbCalls[1].Args; !harness.Equal(got, []string{"load", "--input", archive}) {
		t.Errorf("msb load argv mismatch:\n got: %q\nwant the exported archive", got)
	}
}

// TestFixtureRestrictedPlanTranslation checks the Plan for the verbatim
// fixture: both binds, both Project volumes with their namespaced names,
// the allowlist with DNS, and the redacted secret map.
func TestFixtureRestrictedPlanTranslation(t *testing.T) {
	testsupport.FakeMSB(t)
	worktree := harness.FixtureWorktrees(t, fixtureRestrictedDir, 1)[0]

	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr.String())
	}
	rendered := stdout.String()
	for _, want := range []string{
		filepath.Join(worktree, "host-notes.txt") + " -> /mnt/host-notes.txt (read-only)",
		filepath.Join(worktree, "host-state") + " -> /mnt/host-state",
		"--mount-named",
		"go_build at /root/.cache/go-build (will be created)",
		"go_mod at /go/pkg/mod (will be created)",
		"allow@proxy.golang.org",
		"allow@sum.golang.org",
		"allow@example.com",
		"--dns-nameserver 1.1.1.1",
		"TEST_TOKEN: host variable SBX_FIXTURE_TOKEN, sent only to example.com",
		`value: "${SBX_FIXTURE_TOKEN}"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
}

// TestFixtureRestrictedRunTranslation checks the full disposable-run argv
// for the fixture's executable configuration: resources, both binds, both
// Project volumes, the allowlist with DNS and TLS interception, and the
// secret map — with the guest shell used when no command is given.
func TestFixtureRestrictedRunTranslation(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("SBX_FIXTURE_TOKEN", "throwaway-fixture-token")
	worktree := harness.FixtureWorktrees(t, fixtureRestrictedDir, 1)[0]
	var stdout, stderr bytes.Buffer
	code := harness.Chdir(t, worktree, func() int {
		return cli.Run(context.Background(), []string{"run"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("run failed: %s", stderr.String())
	}
	calls := fake.Calls()
	if len(calls) != 4 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), harness.CallDump(calls))
	}
	// The Project volume compatibility check runs between the context
	// confirmation and the image inspection, before any mutation.
	if got := calls[1].Args; !harness.Equal(got, []string{"volumes", "--format", "json"}) {
		t.Errorf("second call mismatch: %q", got)
	}
	ns := fixtureVolumeNamespace(t, worktree)
	runCall := calls[3].Args
	want := []string{"run", "sbx-restricted-cli:latest",
		"--cpus", "2",
		"--memory", "2G",
		"--mount-dir", worktree + ":/workspace",
		"--mount-file", worktree + "/host-notes.txt:/mnt/host-notes.txt:ro",
		"--mount-dir", worktree + "/host-state:/mnt/host-state",
		"--mount-named", ns + "-go_build:/root/.cache/go-build",
		"--mount-named", ns + "-go_mod:/go/pkg/mod",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=disposable",
		"--label", "sbx.worktree=" + worktree,
		"--net-rule", "allow@proxy.golang.org",
		"--net-rule", "allow@sum.golang.org",
		"--net-rule", "allow@example.com",
		"--tls-intercept",
		"--dns-nameserver", "1.1.1.1",
		"--dns-nameserver", "8.8.8.8",
		"--secret-conf", "<path>",
		"--workdir", "/workspace",
		"--no-tty",
		"--", "/bin/bash",
	}
	if !harness.Equal(harness.SpliceGenerated(runCall), want) {
		t.Errorf("run argv mismatch:\n got: %q\nwant: %q", runCall, want)
	}
	conf := fake.SecretConf(t, 3)
	if !strings.Contains(conf, `value: "${SBX_FIXTURE_TOKEN}"`) {
		t.Errorf("secret map is missing the source reference:\n%s", conf)
	}
	if strings.Contains(conf, "throwaway-fixture-token") {
		t.Errorf("secret map contains the host secret value:\n%s", conf)
	}
}

// fixtureVolumeNamespace derives the Project volume namespace for the
// worktree, the way the CLI does.
func fixtureVolumeNamespace(t *testing.T, worktree string) string {
	t.Helper()
	info, err := gitx.Discover(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	return identity.VolumeNamespace(info.CommonDir)
}

// fixtureStatefulDir is the stateful web fixture's path relative to this
// package.
const fixtureStatefulDir = "../../../fixtures/stateful-web"

// TestFixtureStatefulWebUpAndPorts copies the complete fixture into two
// worktrees of one repository and checks `sbx up` across them: identity
// reuse within a worktree, distinct identities and loopback ports across
// worktrees, and each create's --port carrying the reserved port.
func TestFixtureStatefulWebUpAndPorts(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	worktrees := harness.FixtureWorktrees(t, fixtureStatefulDir, 2)

	ids := make([]string, 2)
	ports := make([]string, 2)
	for i, wt := range worktrees {
		// The first up creates; the second reuses the same identity.
		for round := 0; round < 2; round++ {
			code, stdout, stderr := harness.SbxUp(t, wt)
			if code != 0 {
				t.Fatalf("up in worktree %d (round %d) failed: %s", i+1, round+1, stderr)
			}
			id := harness.SandboxIdentityOf(t, wt)
			if !strings.Contains(stdout, "persistent sandbox: "+id) {
				t.Errorf("up in worktree %d does not report the identity:\n%s", i+1, stdout)
			}
			if round == 0 && !strings.Contains(stdout, "created") {
				t.Errorf("first up in worktree %d does not report creation:\n%s", i+1, stdout)
			}
			if round == 1 && !strings.Contains(stdout, "already running") {
				t.Errorf("second up in worktree %d does not report reuse:\n%s", i+1, stdout)
			}
			// The published endpoint appears in both creation and reuse
			// reports; the port is stable per worktree.
			port := ""
			for _, line := range strings.Split(stdout, "\n") {
				if after, ok := strings.CutPrefix(line, "web: 127.0.0.1:"); ok {
					port = strings.SplitN(after, " ", 2)[0]
				}
			}
			if port == "" {
				t.Errorf("up in worktree %d (round %d) reports no web endpoint:\n%s", i+1, round+1, stdout)
			}
			ports[i] = port
			ids[i] = id
		}
	}
	if ids[0] == ids[1] {
		t.Errorf("both worktrees derived the identity %s", ids[0])
	}
	if ports[0] == "" || ports[0] == ports[1] {
		t.Errorf("worktree ports = %v, want distinct loopback ports", ports)
	}

	// Every create published its port on the loopback.
	var published []string
	for _, c := range fake.Calls() {
		if c.Args[0] != "create" {
			continue
		}
		for i, a := range c.Args {
			if a == "--port" {
				published = append(published, c.Args[i+1])
			}
		}
	}
	if len(published) != 2 {
		t.Fatalf("fake msb saw %d --port flags across creates, want 2", len(published))
	}
	for i, spec := range published {
		if !strings.HasPrefix(spec, "127.0.0.1:") || !strings.HasSuffix(spec, ":4000") {
			t.Errorf("create %d published %q, want 127.0.0.1:<port>:4000", i+1, spec)
		}
	}
	if published[0] == published[1] {
		t.Errorf("both creates published %q", published[0])
	}
}
