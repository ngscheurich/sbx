// Persistent-sandbox image-check gating tests: the creation path checks
// the image before creating, an existing sandbox's own image is checked
// even with --allow-stale, and an unverifiable sandbox image fails closed
// without touching the sandbox or its data (ADR-0004).
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// checkTOML is the persistent configuration with a declared image check.
const checkTOML = `
image = "alpine:3.20"
cpus = 2
memory = "2G"
image_check = "image-check.sh"

[network]
egress = "public"
`

// checkFixture prepares a persistent fixture with an image-check script.
func checkFixture(t *testing.T, toml, script string) (string, testsupport.Log) {
	t.Helper()
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	worktree, _ := fixtureRepo(t, toml)
	writeImageCheckScript(t, worktree, script)
	return worktree, fake
}

// TestUpCreationChecksImageBeforeCreating checks that a new persistent
// sandbox is created only after the check sandbox ran and was removed, and
// that the recorded pass lets the next up skip the check entirely.
func TestUpCreationChecksImageBeforeCreating(t *testing.T) {
	worktree, fake := checkFixture(t, checkTOML, checkScript)
	id := sandboxIdentityOf(t, worktree)

	code, _, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	calls := fake.Calls()
	// context, ls, image inspect (ensureImage), image inspect (gate),
	// check create, check exec, image inspect (confirm before the pass is
	// recorded), check remove, image inspect (confirm before creation),
	// persistent create.
	if len(calls) != 10 {
		t.Fatalf("fake msb saw %d calls:\n%s", len(calls), callDump(calls))
	}
	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
		t.Fatalf("check sandbox calls = %d/%d/%d:\n%s", len(creates), len(execs), len(removes), callDump(calls))
	}
	// The persistent create comes after the check sandbox is removed.
	if calls[9].Args[0] != "create" || !contains(calls[9].Args, id) {
		t.Errorf("the persistent create did not follow the check:\n%s", callDump(calls))
	}
	if !hasSuccess(t, checkScript) {
		t.Errorf("no image-check success was recorded for the passing check")
	}

	// The second up reuses the recorded pass: no new check sandbox.
	code, _, stderr = sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("second up failed: %s", stderr)
	}
	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 1 || len(execs) != 1 || len(removes) != 1 {
		t.Errorf("the second up checked the image again: %d creates, %d execs, %d removes",
			len(creates), len(execs), len(removes))
	}
}

// TestUpExistingSandboxRechecksChangedScript checks that a changed script
// forces a new check against the existing sandbox's image — the sandbox's
// contents have a pass only for the script that ran.
func TestUpExistingSandboxRechecksChangedScript(t *testing.T) {
	worktree, fake := checkFixture(t, checkTOML, checkScript)

	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}

	// The sandbox keeps running; only the script changes.
	changed := strings.ReplaceAll(checkScript, "command -v sh", "command -v sh # changed")
	writeImageCheckScript(t, worktree, changed)

	code, _, stderr := sbxUp(t, worktree)
	if code != 0 {
		t.Fatalf("up after the script change failed: %s", stderr)
	}
	creates, execs, removes := checkSandboxCalls(fake)
	if len(creates) != 2 || len(execs) != 2 || len(removes) != 2 {
		t.Errorf("the changed script did not force a new check: %d creates, %d execs, %d removes",
			len(creates), len(execs), len(removes))
	}
	if got := fake.StdinAt(t, execs[1].Index); got != changed {
		t.Errorf("the re-check did not run the changed script:\n got: %q\nwant: %q", got, changed)
	}
	// The new pass is recorded under the changed script's hash.
	if !hasSuccess(t, changed) {
		t.Errorf("no image-check success was recorded for the changed script")
	}
}

// TestExecAllowStaleStillChecksSandboxImage checks that --allow-stale
// bypasses Creation drift but never the image check: an existing sandbox
// whose image has no recorded pass for the current script is checked
// before use.
func TestExecAllowStaleStillChecksSandboxImage(t *testing.T) {
	worktree, fake := checkFixture(t, checkTOML, checkScript)

	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	// Lose the recorded pass: the sandbox's image must be re-checked even
	// though the sandbox itself is unchanged and --allow-stale is given.
	recordDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "imagecheck")
	entries, err := os.ReadDir(recordDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no image-check records under %s (err %v)", recordDir, err)
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(recordDir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}

	// Drift the configuration too, so --allow-stale is what gets past the
	// drift refusal; the image check must still run.
	drifted := strings.Replace(checkTOML, "cpus = 2", "cpus = 3", 1)
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"exec", "--allow-stale", "--", "true"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("exec with --allow-stale failed: %s", stderr.String())
	}
	if creates, execs, removes := checkSandboxCalls(fake); len(creates) != 2 || len(execs) != 2 || len(removes) != 2 {
		t.Errorf("--allow-stale did not re-check the sandbox's image: %d creates, %d execs, %d removes",
			len(creates), len(execs), len(removes))
	}
	if !hasSuccess(t, checkScript) {
		t.Errorf("no image-check success was recorded for the re-checked image")
	}
}

// TestExecFailsClosedWhenSandboxImageUnverifiable checks the fail-closed
// rule: an existing sandbox created from contents the image reference no
// longer resolves to cannot be re-checked, so sbx refuses to use it —
// without creating a check sandbox or touching the sandbox.
func TestExecFailsClosedWhenSandboxImageUnverifiable(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_MSB_IMAGE_DIGEST", "sha256:elsewhere")
	worktree, _ := fixtureRepo(t, checkTOML)
	writeImageCheckScript(t, worktree, checkScript)
	id := sandboxIdentityOf(t, worktree)
	// A sandbox sbx owns, created from contents (sha256:seeded) the image
	// reference no longer resolves to (sha256:elsewhere).
	fake.SeedSandbox(t, id, "alpine:3.20", "stopped", map[string]string{
		"sbx.managed":  "1",
		"sbx.mode":     "persistent",
		"sbx.worktree": worktree,
	})

	var stdout, stderr strings.Builder
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"exec", "--allow-stale", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("exec used a sandbox whose image cannot be verified against the image check")
	}
	if !strings.Contains(stderr.String(), "image contents") {
		t.Errorf("stderr does not explain the unverifiable image contents:\n%s", stderr.String())
	}
	if creates, _, _ := checkSandboxCalls(fake); len(creates) != 0 {
		t.Errorf("a check sandbox was created for unverifiable contents:\n%s", callDump(fake.Calls()))
	}
	if !fake.SandboxExists(t, id) {
		t.Errorf("the refused sandbox was touched")
	}
}

// TestExecFailsClosedWhenImageInspectionFails checks the same rule when
// the image reference cannot be inspected at all: an inspection failure is
// not a pass.
func TestExecFailsClosedWhenImageInspectionFails(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_MSB_IMAGE_MISSING", "alpine:3.20")
	worktree, _ := fixtureRepo(t, checkTOML)
	writeImageCheckScript(t, worktree, checkScript)
	id := sandboxIdentityOf(t, worktree)
	fake.SeedSandbox(t, id, "alpine:3.20", "stopped", map[string]string{
		"sbx.managed":  "1",
		"sbx.mode":     "persistent",
		"sbx.worktree": worktree,
	})

	var stdout, stderr strings.Builder
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"exec", "--allow-stale", "--", "true"}, nil, &stdout, &stderr)
	})
	if code == 0 {
		t.Fatal("exec used a sandbox whose image cannot be inspected for the image check")
	}
	if creates, _, _ := checkSandboxCalls(fake); len(creates) != 0 {
		t.Errorf("a check sandbox was created although the image cannot be inspected:\n%s", callDump(fake.Calls()))
	}
	if !fake.SandboxExists(t, id) {
		t.Errorf("the refused sandbox was touched")
	}
}
