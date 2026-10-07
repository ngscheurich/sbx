// Plan-side image-check tests: a declared image check's status appears in
// the read-only plan as known, pending, or unresolvable — and planning
// never launches a check sandbox or writes host state.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// TestPlanReportsImageCheckStatus checks the three statuses and the plan's
// purity while a check is declared.
func TestPlanReportsImageCheckStatus(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		fake := testsupport.FakeMSB(t)
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		worktree, _ := fixtureRepo(t, imageCheckTOML)
		writeImageCheckScript(t, worktree, checkScript)
		before := countImageCheckRecords(t)

		rendered := planRender(t, worktree)
		if !strings.Contains(rendered, "image check: image-check.sh") ||
			!strings.Contains(rendered, "pending") {
			t.Errorf("the plan does not report the pending image check:\n%s", rendered)
		}
		assertPlanCheckPurity(t, fake, before)
	})

	t.Run("known", func(t *testing.T) {
		fake := testsupport.FakeMSB(t)
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		worktree, _ := fixtureRepo(t, imageCheckTOML)
		writeImageCheckScript(t, worktree, checkScript)
		// A recorded pass for the image's contents and this script.
		if err := state.SaveImageCheckSuccess(fakeDigest, state.ImageCheckScriptHash(checkScript)); err != nil {
			t.Fatal(err)
		}
		before := countImageCheckRecords(t)

		rendered := planRender(t, worktree)
		if !strings.Contains(rendered, "image check: image-check.sh") ||
			!strings.Contains(rendered, "passed") {
			t.Errorf("the plan does not report the known image check:\n%s", rendered)
		}
		assertPlanCheckPurity(t, fake, before)
	})

	t.Run("unresolvable when the image cannot be inspected", func(t *testing.T) {
		fake := testsupport.FakeMSB(t)
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		t.Setenv("FAKE_MSB_IMAGE_MISSING", "alpine:3.20")
		worktree, _ := fixtureRepo(t, imageCheckTOML)
		writeImageCheckScript(t, worktree, checkScript)
		before := countImageCheckRecords(t)

		rendered := planRender(t, worktree)
		if !strings.Contains(rendered, "image check: image-check.sh") ||
			!strings.Contains(rendered, "cannot be determined") {
			t.Errorf("the plan does not report the unresolvable image check:\n%s", rendered)
		}
		assertPlanCheckPurity(t, fake, before)
	})
}

// countImageCheckRecords returns how many image-check record files exist
// in the private host state.
func countImageCheckRecords(t *testing.T) int {
	t.Helper()
	recordDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "imagecheck")
	entries, err := os.ReadDir(recordDir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading the image-check record directory: %v", err)
	}
	return len(entries)
}

// planRender runs `sbx plan` in the worktree and returns the rendered text.
func planRender(t *testing.T, worktree string) string {
	t.Helper()
	var stdout, stderr strings.Builder
	code := chdir(t, worktree, func() int {
		return Run(context.Background(), []string{"plan"}, nil, &stdout, &stderr)
	})
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr.String())
	}
	return stdout.String()
}

// assertPlanCheckPurity checks that planning launched no check sandbox,
// mutated nothing in the backend, and wrote no new host state while an
// image check is declared. beforeRecords is the set of image-check record
// files that existed before the plan ran (records the test itself may have
// seeded); no new file may appear.
func assertPlanCheckPurity(t *testing.T, fake testsupport.Log, beforeRecords int) {
	t.Helper()
	if creates, execs, _ := checkSandboxCalls(fake); len(creates) != 0 || len(execs) != 0 {
		t.Errorf("the plan launched a check sandbox:\n%s", callDump(fake.Calls()))
	}
	for _, c := range fake.Calls() {
		if c.Args[0] == "create" || c.Args[0] == "run" || c.Args[0] == "pull" {
			t.Errorf("the plan mutated the backend: %q", c.Args)
		}
	}
	recordDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sbx", "imagecheck")
	entries, err := os.ReadDir(recordDir)
	if os.IsNotExist(err) {
		entries = nil
	} else if err != nil {
		t.Fatalf("reading the image-check record directory: %v", err)
	}
	if len(entries) != beforeRecords {
		t.Errorf("the plan wrote image-check records (%d before, %d after)", beforeRecords, len(entries))
	}
}
