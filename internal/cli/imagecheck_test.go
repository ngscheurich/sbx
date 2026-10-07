// Image-check gating tests: a declared image_check runs in an isolated
// check sandbox — created from the image alone, removed on every exit
// path — and no sandbox uses an image whose check has not passed for its
// current contents and the current script (ADR-0004). All against the
// stateful fake msb.
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// imageCheckTOML is a valid configuration declaring an image check on a
// prebuilt image.
const imageCheckTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"
image_check = "image-check.sh"

[network]
egress = "public"
`

// checkScript is the guest script the tests declare; its passing form
// exits 0.
const checkScript = "#!/bin/sh\ncommand -v sh >/dev/null\n"

// writeImageCheckScript writes the check script into the worktree.
func writeImageCheckScript(t *testing.T, worktree, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, "image-check.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// checkSandboxCalls returns the recorded create/exec/remove calls belonging
// to image-check sandboxes (whose names embed -imgchk-).
func checkSandboxCalls(fake testsupport.Log) (creates, execs, removes []testsupport.Call) {
	for _, c := range fake.Calls() {
		isCheck := false
		for _, a := range c.Args {
			if strings.Contains(a, "-imgchk-") {
				isCheck = true
			}
		}
		if !isCheck {
			continue
		}
		switch c.Args[0] {
		case "create":
			creates = append(creates, c)
		case "exec":
			execs = append(execs, c)
		case "remove":
			removes = append(removes, c)
		}
	}
	return creates, execs, removes
}

// fakeDigest is the manifest digest the fake msb reports by default.
const fakeDigest = "sha256:fake"

// hasSuccess reports whether a matching success is recorded for the
// default fake digest and the given script.
func hasSuccess(t *testing.T, script string) bool {
	t.Helper()
	ok, err := state.HasImageCheckSuccess(fakeDigest, state.ImageCheckScriptHash(script))
	if err != nil {
		t.Fatalf("checking the recorded success: %v", err)
	}
	return ok
}

// TestRunChecksImageBeforeDisposableUse checks the full disposable sequence
// with a declared image check: the check sandbox is created from the image
// alone, the script is copied in and run, the sandbox is removed, and the
// image contents are confirmed unchanged before `msb run` uses the image.
// A recorded success is reused: the second run checks nothing.
func TestRunChecksImageBeforeDisposableUse(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, imageCheckTOML)
	writeImageCheckScript(t, worktree, checkScript)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("run failed: %s", stderr.String())
	}

	calls := fake.Calls()
	// context, image inspect (prebuilt pull check), image inspect (gate),
	// check create, check exec, image inspect (confirm before the pass is
	// recorded), check remove, image inspect (confirm before the run),
	// msb run.
	if len(calls) != 9 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}

	// The check sandbox is created from the image alone: no workspace
	// mount, no volumes, no environment, no network policy — only the
	// project's resource limits.
	create := calls[3].Args
	if !equal(create, []string{
		"create", "--name", create[2], "alpine:3.20",
		"--cpus", "2",
		"--memory", "2G",
		"--label", "sbx.managed=1",
		"--label", "sbx.mode=image-check",
		"--label", "sbx.worktree=" + worktree,
	}) {
		t.Errorf("check create argv mismatch:\n got: %q", create)
	}
	if !strings.HasPrefix(create[2], sandboxIdentityOf(t, worktree)+"-imgchk-") {
		t.Errorf("check sandbox name %q does not extend the Sandbox identity", create[2])
	}

	// The script travels by content through the exec's standard input and
	// runs by its own shebang.
	exec := calls[4].Args
	if exec[0] != "exec" || exec[1] != create[2] {
		t.Errorf("check exec argv mismatch: %q", exec)
	}
	if !strings.Contains(strings.Join(exec, " "), "/tmp/sbx-image-check") {
		t.Errorf("check exec does not run the copied script: %q", exec)
	}
	if got := fake.StdinAt(t, 4); got != checkScript {
		t.Errorf("check script stdin mismatch:\n got: %q\nwant: %q", got, checkScript)
	}

	// The check sandbox is removed on success.
	if !equal(calls[6].Args, []string{"remove", "--force", create[2]}) {
		t.Errorf("check remove argv mismatch: %q", calls[6].Args)
	}
	if fake.SandboxExists(t, create[2]) {
		t.Errorf("the check sandbox %s was not removed", create[2])
	}

	// The pass is recorded for the image's contents and the script's hash.
	if !hasSuccess(t, checkScript) {
		t.Errorf("no image-check success was recorded for the passing check")
	}

	// The guest command still runs.
	if calls[8].Args[0] != "run" {
		t.Errorf("the disposable run did not happen; last call: %q", calls[8].Args)
	}

	// The second run reuses the recorded success: no check sandbox.
	var stdout2, stderr2 bytes.Buffer
	code = chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &stdout2, &stderr2)
	})
	if code != 0 {
		t.Fatalf("second run failed: %s", stderr2.String())
	}
	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
		t.Errorf("the second run checked the image again: %d creates, %d execs, %d removes",
			len(creates), len(execs), len(removes))
	}
}

// TestImageChangedMidCheckRecordsNothing checks the confirm-before-record
// rule: when the image tag repoints while the check runs, the pass is
// recorded for nothing — the check may have run against contents it does
// not attest — and the run fails before any sandbox uses the image.
func TestImageChangedMidCheckRecordsNothing(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// The run's inspects: pull check (1), gate (2), confirm before the
	// record (3), confirm before the run (4). The tag repoints at (3).
	t.Setenv("FAKE_MSB_IMAGE_DIGEST_FROM", "3:sha256:moved")
	worktree, _ := fixtureRepo(t, imageCheckTOML)
	writeImageCheckScript(t, worktree, checkScript)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("the run succeeded although the image changed during its check")
	}
	if !strings.Contains(stderr.String(), "nothing was recorded") {
		t.Errorf("stderr does not explain that nothing was recorded:\n%s", stderr.String())
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "run" {
			t.Errorf("the image was used although its contents moved mid-check: %q", c.Args)
		}
	}
	if hasSuccess(t, checkScript) {
		t.Errorf("a pass was recorded for contents the check may not have run against")
	}
}

// TestRunRechecksRepointedTag checks the changed-tag path for a prebuilt
// image: a tag repointed since the last pass has no matching record, so
// the check runs again for the new contents before the image is used.
func TestRunRechecksRepointedTag(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, imageCheckTOML)
	writeImageCheckScript(t, worktree, checkScript)

	// First run checks and records the pass for sha256:fake.
	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("first run failed: %s", stderr.String())
	}

	// The tag repoints before the second run's gate inspect (the fifth
	// image inspect of the test) and stays repointed.
	t.Setenv("FAKE_MSB_IMAGE_DIGEST_FROM", "6:sha256:repointed")
	code = chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("second run failed: %s", stderr.String())
	}

	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 2 || len(execs) != 2 || len(removes) != 2 {
		t.Errorf("the repointed tag was not re-checked: %d creates, %d execs, %d removes", len(creates), len(execs), len(removes))
	}
	ok, err := state.HasImageCheckSuccess("sha256:repointed", state.ImageCheckScriptHash(checkScript))
	if err != nil || !ok {
		t.Errorf("no pass was recorded for the repointed contents (ok %v, err %v)", ok, err)
	}
}

// TestImageCheckFailureBlocksDisposableUse checks that a failing check
// fails the run before the image is used, records nothing, and still
// removes the check sandbox.
func TestImageCheckFailureBlocksDisposableUse(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_MSB_CHECK_EXIT", "37")
	worktree, _ := fixtureRepo(t, imageCheckTOML)
	writeImageCheckScript(t, worktree, checkScript)

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("run succeeded although the image check failed")
	}
	if !strings.Contains(stderr.String(), "image check") {
		t.Errorf("stderr does not name the image check:\n%s", stderr.String())
	}

	calls := fake.Calls()
	for _, c := range calls {
		if c.Args[0] == "run" {
			t.Errorf("the image was used despite the failed check: %q", c.Args)
		}
	}
	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
		t.Errorf("check sandbox calls = %d/%d/%d, want one create, exec, and remove:\n%s",
			len(creates), len(execs), len(removes), callDump(calls))
	}
	if hasSuccess(t, checkScript) {
		t.Errorf("a failed check was recorded as a success")
	}
}

// TestImageCheckInterruptionRemovesCheckSandbox checks that canceling sbx
// while the check runs removes the check sandbox and records nothing.
func TestImageCheckInterruptionRemovesCheckSandbox(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_MSB_EXEC_SLEEP", "30")
	worktree, _ := fixtureRepo(t, imageCheckTOML)
	writeImageCheckScript(t, worktree, checkScript)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderr strings.Builder
	go func() {
		done <- chdir(t, worktree, func() int {
			return Run(ctx, []string{"run", "--", "echo", "hi"}, strings.NewReader(""), &strings.Builder{}, &stderr)
		})
	}()
	// Wait for the check exec to be running, then interrupt.
	fake.Wait(t, 5)
	cancel()
	if code := <-done; code == 0 {
		t.Fatal("the interrupted run succeeded")
	}

	// The deferred removal runs even though ctx is canceled.
	fake.Wait(t, 6)
	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
		t.Errorf("check sandbox calls = %d/%d/%d, want the sandbox removed despite cancellation:\n%s",
			len(creates), len(execs), len(removes), callDump(fake.Calls()))
	}
	if hasSuccess(t, checkScript) {
		t.Errorf("an interrupted check was recorded as a success")
	}
}

// TestFixtureStatefulWebImageCheckScriptCopiedVerbatim checks that the
// stateful web fixture's image-check.sh is copied into the check sandbox
// byte for byte — the script passes only when the Workspace is absent,
// which the real guest verifies; the fixture has no Workspace here.
func TestFixtureStatefulWebImageCheckScriptCopiedVerbatim(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, imageCheckTOML)
	scriptBytes, err := os.ReadFile("../../fixtures/stateful-web/image-check.sh")
	if err != nil {
		t.Fatalf("reading the fixture's image-check.sh: %v", err)
	}
	writeImageCheckScript(t, worktree, string(scriptBytes))

	var stdout, stderr bytes.Buffer
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"run", "--", "true"}, strings.NewReader(""), &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("run failed: %s", stderr.String())
	}
	if got := fake.StdinAt(t, 4); got != string(scriptBytes) {
		t.Errorf("the fixture's image-check.sh did not reach the check sandbox verbatim:\n got: %q\nwant: %q", got, string(scriptBytes))
	}
}
