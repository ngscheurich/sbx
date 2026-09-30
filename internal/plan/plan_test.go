package plan

import (
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
)

func testPlan(t *testing.T) Plan {
	t.Helper()
	info := gitx.Info{
		WorktreeRoot: "/src/app/wt1",
		CommonDir:    "/src/app/.git",
	}
	cfg, err := config.Load("testdata/sbx.toml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return Compose(info, cfg)
}

func TestComposeUsesOnlyGitPaths(t *testing.T) {
	p := testPlan(t)
	if p.Project != "app" {
		t.Errorf("Project = %q", p.Project)
	}
	if p.Sandbox != "app-wt1-"+identity.HashPath("/src/app/wt1") {
		t.Errorf("Sandbox = %q, want app-wt1-<hash of worktree path>", p.Sandbox)
	}
	if p.VolumeNamespace != identity.VolumeNamespace("/src/app/.git") {
		t.Errorf("VolumeNamespace = %q, want <hash of common git dir>", p.VolumeNamespace)
	}
}

func TestRenderShowsIdentityAndConfiguration(t *testing.T) {
	rendered := testPlan(t).Render()
	for _, want := range []string{
		"app-wt1-",
		"/src/app/wt1",
		"/src/app/.git",
		"alpine:3.20",
		"2G",
		"egress",
		"/bin/sh",
		"partial",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderMentionsVolumeNaming(t *testing.T) {
	ns := testPlan(t).VolumeNamespace
	rendered := testPlan(t).Render()
	if !strings.Contains(rendered, ns) {
		t.Errorf("rendered plan does not show the volume namespace %q:\n%s", ns, rendered)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a := testPlan(t).Render()
	b := testPlan(t).Render()
	if a != b {
		t.Error("Render is not deterministic")
	}
}

func TestRenderNoHostEnvironmentValues(t *testing.T) {
	// This build's configuration surface has no secrets yet; the Plan must
	// never grow host-environment leakage silently.
	rendered := testPlan(t).Render()
	if strings.Contains(rendered, "SBX_") {
		t.Errorf("rendered plan mentions host environment names:\n%s", rendered)
	}
}