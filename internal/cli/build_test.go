package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// buildTOML is a valid configuration with a [build] recipe.
const buildTOML = `
image = "sbx-app:latest"
cpus = 2
memory = "2G"

[network]
policy = "public"

[build]
context = "."
dockerfile = "Dockerfile"
`

// buildCheckTOML is buildTOML with a declared image check.
const buildCheckTOML = `
image = "sbx-app:latest"
cpus = 2
memory = "2G"
image_check = "image-check.sh"

[network]
policy = "public"

[build]
context = "."
dockerfile = "Dockerfile"
`

// buildFixture prepares a fake msb and docker, a private host state
// directory, a private TMPDIR the build archive lands in, and a worktree
// holding the given sbx.toml plus a Dockerfile.
func buildFixture(t *testing.T, toml string) (worktree string, msbLog testsupport.Log, dockerLog testsupport.DockerLog) {
	t.Helper()
	msbLog = testsupport.FakeMSB(t)
	dockerLog = testsupport.FakeDocker(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	worktree, _ = fixtureRepo(t, toml)
	if err := os.WriteFile(filepath.Join(worktree, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return worktree, msbLog, dockerLog
}

// sbxBuild drives `sbx build` inside the worktree.
func sbxBuild(t *testing.T, worktree string, flags ...string) (int, string, string) {
	t.Helper()
	return sbxRun(t, worktree, append([]string{"build"}, flags...), nil)
}

// archiveFromSaveCall recovers the archive path from a recorded docker save
// call.
func archiveFromSaveCall(t *testing.T, call testsupport.Call) string {
	t.Helper()
	for i, a := range call.Args {
		if a == "--output" && i+1 < len(call.Args) {
			return call.Args[i+1]
		}
	}
	t.Fatalf("no --output in save call: %q", call.Args)
	return ""
}

// tmpdirEntries lists the private TMPDIR's entries.
func tmpdirEntries(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(os.Getenv("TMPDIR"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestBuildImportsImageAndCleansArchive pins the whole `sbx build` sequence:
// docker build with the recipe's settings, docker save to a temporary
// archive outside the repository, msb load --input of that archive — in
// that order — and the archive's removal afterward.
func TestBuildImportsImageAndCleansArchive(t *testing.T) {
	worktree, msbLog, dockerLog := buildFixture(t, buildTOML)
	// The import must follow the export; the fake msb refuses a load whose
	// required marker the fake docker touches only in "save".
	t.Setenv("FAKE_MSB_LOAD_REQUIRES", dockerLog.SavedMarker())

	code, stdout, stderr := sbxBuild(t, worktree)
	if code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	if !strings.Contains(stdout, "sbx-app:latest") {
		t.Errorf("build output does not name the image:\n%s", stdout)
	}
	if !strings.Contains(stdout, "fake docker build output") {
		t.Errorf("docker's build output was not streamed through:\n%s", stdout)
	}

	dockerCalls := dockerLog.Calls()
	if len(dockerCalls) != 2 {
		t.Fatalf("fake docker saw %d calls:\n%s", len(dockerCalls), dockerCallDump(dockerCalls))
	}
	wantBuild := []string{"build",
		"--platform", "linux/" + runtime.GOARCH,
		"--file", filepath.Join(worktree, "Dockerfile"),
		"--tag", "sbx-app:latest",
		worktree,
	}
	if got := dockerCalls[0].Args; !equal(got, wantBuild) {
		t.Errorf("docker build argv mismatch:\n got: %q\nwant: %q", got, wantBuild)
	}
	save := dockerCalls[1].Args
	if len(save) < 2 || save[0] != "save" {
		t.Fatalf("second docker call is not a save: %q", save)
	}
	archive := archiveFromSaveCall(t, dockerCalls[1])
	if filepath.Dir(archive) != os.Getenv("TMPDIR") {
		t.Errorf("archive %q is not in the temporary directory", archive)
	}
	if strings.HasPrefix(archive, worktree) {
		t.Errorf("archive %q is inside the repository", archive)
	}
	if wantSave := []string{"save", "--output", archive, "sbx-app:latest"}; !equal(save, wantSave) {
		t.Errorf("docker save argv mismatch:\n got: %q\nwant: %q", save, wantSave)
	}

	msbCalls := msbLog.Calls()
	if len(msbCalls) != 2 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(msbCalls), callDump(msbCalls))
	}
	if got := msbCalls[0].Args; !equal(got, []string{"context", "--format", "json"}) {
		t.Errorf("first msb call mismatch: %q", got)
	}
	if got := msbCalls[1].Args; !equal(got, []string{"load", "--input", archive}) {
		t.Errorf("msb load argv mismatch:\n got: %q\nwant: %q", got, msbCalls[1].Args)
	}
	if got := msbLog.Loaded(t, 1); got != archive {
		t.Errorf("msb loaded %q, want the exported archive %q", got, archive)
	}

	if entries := tmpdirEntries(t); len(entries) != 0 {
		t.Errorf("temporary archive was not cleaned up: %v", entries)
	}
}

// TestBuildPassesTargetAndExplicitPlatform pins the build argv when the
// recipe declares a target stage and a non-default platform.
func TestBuildPassesTargetAndExplicitPlatform(t *testing.T) {
	toml := `
image = "sbx-app:v2"
cpus = 2
memory = "2G"

[network]
policy = "public"

[build]
context = "./docker"
dockerfile = "docker/Containerfile"
target = "runtime"
platform = "linux/amd64"
`
	worktree, _, dockerLog := buildFixture(t, toml)
	dir := filepath.Join(worktree, "docker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := sbxBuild(t, worktree)
	if code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	want := []string{"build",
		"--platform", "linux/amd64",
		"--file", filepath.Join(worktree, "docker", "Containerfile"),
		"--target", "runtime",
		"--tag", "sbx-app:v2",
		filepath.Join(worktree, "docker"),
	}
	if got := dockerLog.Calls()[0].Args; !equal(got, want) {
		t.Errorf("docker build argv mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestBuildWithoutRecipeFails checks the no-build-recipe error: a prebuilt
// image is never built, and neither tool is invoked.
func TestBuildWithoutRecipeFails(t *testing.T) {
	worktree, msbLog, dockerLog := buildFixture(t, disposableTOML)

	code, _, stderr := sbxBuild(t, worktree)
	if code == 0 {
		t.Fatal("build succeeded without a [build] recipe")
	}
	for _, want := range []string{"[build]", "alpine:3.20", "prebuilt"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	if got := dockerLog.Calls(); len(got) != 0 {
		t.Errorf("docker was called without a recipe: %v", got)
	}
	if got := msbLog.Calls(); len(got) != 0 {
		t.Errorf("msb was called without a recipe: %v", got)
	}
}

// TestBuildCleansArchiveOnFailure checks the archive is removed however the
// build sequence fails: during the build, during the export, or during the
// import.
func TestBuildCleansArchiveOnFailure(t *testing.T) {
	cases := map[string]struct {
		env            map[string]string
		wantDockerCall int // number of docker calls the failure allows
		wantMsbCalls   int
	}{
		"build fails": {env: map[string]string{"FAKE_DOCKER_BUILD_FAIL": "1"}, wantDockerCall: 1, wantMsbCalls: 1},
		"save fails":  {env: map[string]string{"FAKE_DOCKER_SAVE_FAIL": "1"}, wantDockerCall: 2, wantMsbCalls: 1},
		"load fails":  {env: map[string]string{"FAKE_MSB_LOAD_FAIL": "1"}, wantDockerCall: 2, wantMsbCalls: 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			worktree, msbLog, dockerLog := buildFixture(t, buildTOML)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			code, _, stderr := sbxBuild(t, worktree)
			if code == 0 {
				t.Fatal("build succeeded despite the injected failure")
			}
			if !strings.Contains(stderr, "failed") {
				t.Errorf("stderr does not carry the failure:\n%s", stderr)
			}
			if got := len(dockerLog.Calls()); got != tc.wantDockerCall {
				t.Errorf("fake docker saw %d calls, want %d", got, tc.wantDockerCall)
			}
			if got := len(msbLog.Calls()); got != tc.wantMsbCalls {
				t.Errorf("fake msb saw %d calls, want %d", got, tc.wantMsbCalls)
			}
			if entries := tmpdirEntries(t); len(entries) != 0 {
				t.Errorf("temporary archive was not cleaned up after failure: %v", entries)
			}
		})
	}
}

// TestBuildCleansArchiveOnCancellation checks that interrupting the build
// still removes the temporary archive.
func TestBuildCleansArchiveOnCancellation(t *testing.T) {
	worktree, _, dockerLog := buildFixture(t, buildTOML)
	t.Setenv("FAKE_DOCKER_BUILD_SLEEP", "30")

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- chdir(t, worktree, func() int {
			return Run(ctx, []string{"build"}, nil, &stdout, &stderr)
		})
	}()
	// Wait until docker is mid-build, then cancel.
	dockerLog.Wait(t, 1)
	cancel()
	<-done

	if entries := tmpdirEntries(t); len(entries) != 0 {
		t.Errorf("temporary archive was not cleaned up after cancellation: %v", entries)
	}
}

// TestBuildKeepArchive checks that --keep-archive retains the archive and
// names it, for debugging.
func TestBuildKeepArchive(t *testing.T) {
	worktree, msbLog, _ := buildFixture(t, buildTOML)

	code, stdout, stderr := sbxBuild(t, worktree, "--keep-archive")
	if code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	entries := tmpdirEntries(t)
	if len(entries) != 1 {
		t.Fatalf("TMPDIR holds %v, want the one kept archive", entries)
	}
	archive := filepath.Join(os.Getenv("TMPDIR"), entries[0])
	if !strings.Contains(stdout, archive) {
		t.Errorf("build output does not name the kept archive %q:\n%s", archive, stdout)
	}
	if got := msbLog.Loaded(t, 1); got != archive {
		t.Errorf("msb loaded %q, want the kept archive %q", got, archive)
	}
}

// TestBuildRunsImageCheckAfterImport checks that a build with a declared
// image check runs it in an isolated sandbox after the import: a passing
// check is recorded for the imported image's contents, and a failing
// check fails the build — although the imported image stays cached.
func TestBuildRunsImageCheckAfterImport(t *testing.T) {
	t.Run("passing check is recorded", func(t *testing.T) {
		worktree, msbLog, _ := buildFixture(t, buildCheckTOML)
		writeImageCheckScript(t, worktree, checkScript)

		code, _, stderr := sbxBuild(t, worktree)
		if code != 0 {
			t.Fatalf("build failed: %s", stderr)
		}
		creates, execs, removes := checkSandboxCalls(msbLog)
		if len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
			t.Fatalf("check sandbox calls = %d/%d/%d, want one of each:\n", len(creates), len(execs), len(removes))
		}
		if !hasSuccess(t, checkScript) {
			t.Errorf("no image-check success was recorded for the built image")
		}
	})

	t.Run("failing check fails the build", func(t *testing.T) {
		worktree, msbLog, dockerLog := buildFixture(t, buildCheckTOML)
		writeImageCheckScript(t, worktree, checkScript)
		t.Setenv("FAKE_MSB_CHECK_EXIT", "37")

		code, _, stderr := sbxBuild(t, worktree)
		if code == 0 {
			t.Fatal("build succeeded although the image check failed")
		}
		if !strings.Contains(stderr, "image check") {
			t.Errorf("stderr does not name the image check:\n%s", stderr)
		}
		// The image was still built, imported, and checked — and the
		// imported image stays cached despite the failure.
		if got := dockerLog.Calls(); len(got) != 2 {
			t.Errorf("docker saw %d calls, want build and save", len(got))
		}
		if creates, execs, removes := checkSandboxCalls(msbLog); len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
			t.Errorf("check sandbox calls = %d/%d/%d, want one of each", len(creates), len(execs), len(removes))
		}
		if hasSuccess(t, checkScript) {
			t.Errorf("a failed check was recorded as a success")
		}
	})

	t.Run("uninspectable image fails closed", func(t *testing.T) {
		worktree, msbLog, dockerLog := buildFixture(t, buildCheckTOML)
		writeImageCheckScript(t, worktree, checkScript)
		// The import happened, but the gate cannot resolve the image's
		// contents: an inspection failure is not a pass.
		t.Setenv("FAKE_MSB_IMAGE_MISSING", "sbx-app:latest")

		code, _, _ := sbxBuild(t, worktree)
		if code == 0 {
			t.Fatal("build succeeded although the image cannot be inspected for its check")
		}
		if got := dockerLog.Calls(); len(got) != 2 {
			t.Errorf("docker saw %d calls, want build and save before the failed inspection", len(got))
		}
		if creates, _, _ := checkSandboxCalls(msbLog); len(creates) != 0 {
			t.Errorf("a check sandbox was created although the image cannot be inspected")
		}
		if hasSuccess(t, checkScript) {
			t.Errorf("an uninspectable image was recorded as a pass")
		}
	})
}

// TestBuildWritesNothingToTheRepository checks that building leaves the
// worktree untouched: the archive lives in the temporary directory and sbx
// never writes a generated sandbox YAML anywhere.
func TestBuildWritesNothingToTheRepository(t *testing.T) {
	worktree, _, _ := buildFixture(t, buildTOML)
	before := dirSnapshot(t, worktree)

	code, _, stderr := sbxBuild(t, worktree)
	if code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	if after := dirSnapshot(t, worktree); after != before {
		t.Errorf("build changed the worktree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestBuildRejectsUnknownFlags keeps the build command line strict.
func TestBuildRejectsUnknownFlags(t *testing.T) {
	worktree, _, _ := buildFixture(t, buildTOML)
	code, _, stderr := sbxBuild(t, worktree, "--verbose")
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "--verbose") {
		t.Errorf("stderr does not name the offending flag:\n%s", stderr)
	}
}

// TestBuildRefusesCloudContext checks the backend confirmation happens
// before Docker runs: a cloud selection stops the build.
func TestBuildRefusesCloudContext(t *testing.T) {
	worktree, msbLog, dockerLog := buildFixture(t, buildTOML)
	t.Setenv("FAKE_MSB_CONTEXT_BACKEND", "cloud")

	code, _, stderr := sbxBuild(t, worktree)
	if code == 0 {
		t.Fatal("build proceeded with a cloud backend")
	}
	if !strings.Contains(stderr, "cloud") {
		t.Errorf("stderr does not name the refused backend:\n%s", stderr)
	}
	if got := dockerLog.Calls(); len(got) != 0 {
		t.Errorf("docker ran despite the refused backend: %v", got)
	}
	for _, call := range msbLog.Calls() {
		if call.Args[0] != "context" {
			t.Errorf("build called msb %v after refusing the backend", call.Args)
		}
	}
}

// scrubPATH replaces PATH with a fresh directory holding symlinks to the
// named real executables only, so missing-tool behavior is testable
// hermetically. LookPath resolution happens before the switch.
func scrubPATH(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("cannot scrub PATH: %s not found on the test host", tool)
		}
		if err := os.Symlink(real, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// TestBuildMissingDockerIsActionable checks that a missing docker executable
// fails with installation guidance, before anything runs.
func TestBuildMissingDockerIsActionable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, buildTOML)
	if err := os.WriteFile(filepath.Join(worktree, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scrubPATH(t, "git")

	code, _, stderr := sbxBuild(t, worktree)
	if code == 0 {
		t.Fatal("build succeeded without docker")
	}
	if !strings.Contains(stderr, "docker") || !strings.Contains(stderr, "not found on PATH") {
		t.Errorf("stderr is not an actionable missing-Docker error:\n%s", stderr)
	}
}

// TestBuildMissingMsbIsActionable checks that a missing msb executable fails
// with installation guidance. The fake docker is installed into the
// scrubbed PATH (its common paths use shell builtins only), so the build
// itself would work; only msb is absent.
func TestBuildMissingMsbIsActionable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, buildTOML)
	if err := os.WriteFile(filepath.Join(worktree, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := scrubPATH(t, "git")
	dockerLog := testsupport.WriteFakeDocker(t, dir)

	code, _, stderr := sbxBuild(t, worktree)
	if code == 0 {
		t.Fatal("build succeeded without msb")
	}
	if !strings.Contains(stderr, "msb") || !strings.Contains(stderr, "not found on PATH") {
		t.Errorf("stderr is not an actionable missing-msb error:\n%s", stderr)
	}
	if got := dockerLog.Calls(); len(got) != 0 {
		t.Errorf("docker ran before the missing msb was reported: %v", got)
	}
}

// TestRunWithBuildRecipeInstructsSbxBuild checks that a missing locally
// built image is never pulled: the run fails with an instruction to build.
func TestRunWithBuildRecipeInstructsSbxBuild(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	dockerLog := testsupport.FakeDocker(t)
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "sbx-app:latest")
	worktree, _ := fixtureRepo(t, buildTOML)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run proceeded without the locally built image")
	}
	if !strings.Contains(stderr.String(), "sbx build") {
		t.Errorf("stderr does not instruct `sbx build`:\n%s", stderr.String())
	}
	for _, call := range fake.Calls() {
		if call.Args[0] == "image" && len(call.Args) > 1 && call.Args[1] == "pull" {
			t.Errorf("sbx pulled a locally built image: %q", call.Args)
		}
		if call.Args[0] == "run" {
			t.Errorf("run proceeded without the image: %q", call.Args)
		}
	}
	if got := dockerLog.Calls(); len(got) != 0 {
		t.Errorf("run invoked docker; nothing but `sbx build` builds images: %v", got)
	}
}

// TestUpWithBuildRecipeInstructsSbxBuild checks the persistent path: a
// missing locally built image fails creation with the build instruction and
// no pull.
func TestUpWithBuildRecipeInstructsSbxBuild(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "sbx-app:latest")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, buildTOML)

	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up proceeded without the locally built image")
	}
	if !strings.Contains(stderr, "sbx build") {
		t.Errorf("stderr does not instruct `sbx build`:\n%s", stderr)
	}
	for _, call := range fake.Calls() {
		if call.Args[0] == "image" && len(call.Args) > 1 && call.Args[1] == "pull" {
			t.Errorf("sbx pulled a locally built image: %q", call.Args)
		}
		if call.Args[0] == "create" {
			t.Errorf("creation was attempted without the image: %q", call.Args)
		}
	}
}

// TestRebuildBehindSameTagDriftsPersistentSandbox covers the rebuild
// lifecycle: after `sbx build` re-imports new contents behind the same tag,
// the existing persistent sandbox is reported as drifted and never
// replaced, --allow-stale keeps it usable, and disposable runs may use the
// new image. Nothing but `sbx build` ever invokes docker.
func TestRebuildBehindSameTagDriftsPersistentSandbox(t *testing.T) {
	worktree, msbLog, dockerLog := buildFixture(t, buildTOML)
	t.Setenv("FAKE_MSB_IMAGE_DIGEST", "sha256:one")

	if code, _, stderr := sbxBuild(t, worktree); code != 0 {
		t.Fatalf("first build failed: %s", stderr)
	}
	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up after build failed: %s", stderr)
	}

	// Rebuild: the same tag now points at new contents. The fake's digest
	// knob stands in for the new import changing what msb image inspect
	// reports.
	t.Setenv("FAKE_MSB_IMAGE_DIGEST", "sha256:two")
	if code, _, stderr := sbxBuild(t, worktree); code != 0 {
		t.Fatalf("rebuild failed: %s", stderr)
	}
	dockerCallsAfterRebuild := len(dockerLog.Calls())

	// The existing persistent sandbox is now drifted — content digests,
	// not tags, decide that — and sbx never replaces it.
	code, _, stderr := sbxUp(t, worktree)
	if code == 0 {
		t.Fatal("up reused a sandbox whose image tag now resolves to new contents")
	}
	for _, want := range []string{"drift", "sha256:one", "sha256:two"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	creates, removes := 0, 0
	for _, call := range msbLog.Calls() {
		switch call.Args[0] {
		case "create":
			creates++
		case "remove":
			removes++
		}
	}
	if removes != 0 {
		t.Error("sbx removed the drifted sandbox; it must never destroy private data")
	}
	if creates != 1 {
		t.Errorf("fake msb saw %d creates, want exactly the first one", creates)
	}

	// exec refuses the drifted sandbox as well.
	if code, _, _ := sbxRun(t, worktree, []string{"exec", "--", "true"}, nil); code == 0 {
		t.Error("exec used the drifted sandbox")
	}

	// --allow-stale keeps the drifted sandbox usable.
	code, _, stderr = sbxUp(t, worktree, "--allow-stale")
	if code != 0 {
		t.Fatalf("up --allow-stale failed: %s", stderr)
	}
	if !strings.Contains(stderr, "despite drift") {
		t.Errorf("--allow-stale did not warn about the drift:\n%s", stderr)
	}

	// A disposable run is not gated by the persistent sandbox's drift and
	// may use the new image.
	if code, _, stderr := sbxRun(t, worktree, []string{"run", "--", "true"}, nil); code != 0 {
		t.Errorf("disposable run failed after the rebuild: %s", stderr)
	}

	// No implicit build occurs on up, exec, or run.
	if got := len(dockerLog.Calls()); got != dockerCallsAfterRebuild {
		t.Errorf("docker saw %d calls beyond the explicit builds; nothing but `sbx build` builds images", got-dockerCallsAfterRebuild)
	}
}

// dockerCallDump renders recorded docker calls for failure messages.
func dockerCallDump(calls []testsupport.Call) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString("  " + strings.Join(c.Args, " ") + "\n")
	}
	return b.String()
}
