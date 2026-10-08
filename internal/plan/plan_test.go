package plan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/ui"
	"github.com/ngscheurich/sbx/internal/volumes"
)

// renderPlain renders the plan the way every consumer receives it: one
// rendering path, styled, written through the ui writers, so the bytes
// degrade to Plain output before any comparison.
func renderPlain(t *testing.T, p Plan) string {
	t.Helper()
	var buf bytes.Buffer
	out, _, styles := ui.Writers(&buf, io.Discard, false)
	fmt.Fprint(out, p.Render(styles, false))
	return buf.String()
}

// writeConfig writes a configuration to a temporary file and returns its
// path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sbx.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

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
	rendered := renderPlain(t, testPlan(t))
	for _, want := range []string{
		"app-wt1-",
		"/src/app/wt1",
		"/src/app/.git",
		"alpine:3.20",
		"2G",
		"policy",
		"/bin/sh",
		"sbx changed nothing",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderMentionsVolumeNaming(t *testing.T) {
	ns := testPlan(t).VolumeNamespace
	rendered := renderPlain(t, testPlan(t))
	if !strings.Contains(rendered, ns) {
		t.Errorf("rendered plan does not show the volume namespace %q:\n%s", ns, rendered)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a := renderPlain(t, testPlan(t))
	b := renderPlain(t, testPlan(t))
	if a != b {
		t.Error("Render is not deterministic")
	}
}

func TestRenderNoHostEnvironmentValues(t *testing.T) {
	// The Plan shows secret names and host variable names, never values. A
	// value in the environment must not leak into the rendered plan even if
	// a host variable happened to share a name's spelling.
	t.Setenv("SBX_PLAN_TOKEN", "throwaway-plan-value-41f9")
	rendered := renderPlain(t, testPlan(t))
	if strings.Contains(rendered, "throwaway-plan-value") {
		t.Errorf("rendered plan contains a host environment value:\n%s", rendered)
	}
}

func TestRenderShowsTranslation(t *testing.T) {
	rendered := renderPlain(t, testPlan(t))
	for _, want := range []string{
		"tmpfs /tmp (512M)",
		"project volume " + testPlan(t).VolumeNamespace + "-cache at /var/cache",
		"MODE=test",
		"allow@example.com",
		"deny by default, TLS interception on",
		"dns 1.1.1.1",
		"TOKEN: host variable SBX_PLAN_TOKEN, sent only to example.com",
		`value: "${SBX_PLAN_TOKEN}"`,
		`--secret-conf "<secret map generated at creation>"`,
		"msb create",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderShowsSandboxNameInCreateArgs(t *testing.T) {
	p := testPlan(t)
	rendered := renderPlain(t, p)
	if !strings.Contains(rendered, "--name "+p.Sandbox) {
		t.Errorf("rendered msb create arguments lack --name <sandbox identity>:\n%s", rendered)
	}
}

func TestRenderSecretlessConfigOmitsSecretConf(t *testing.T) {
	root := t.TempDir()
	info := gitx.Info{WorktreeRoot: root, CommonDir: filepath.Join(filepath.Dir(root), ".git")}
	cfg, err := config.Load(writeConfig(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[network]
policy = "public"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rendered := renderPlain(t, Compose(info, cfg))
	if strings.Contains(rendered, "--secret-conf") {
		t.Errorf("secretless plan renders --secret-conf, which msb create would never receive:\n%s", rendered)
	}
	if strings.Contains(rendered, "secret map") {
		t.Errorf("secretless plan renders the secret map section:\n%s", rendered)
	}
}

// TestRenderNotesProjectVolumeCheck covers the pure rendering: before the
// caller runs the compatibility check against the backend, the plan notes
// that the check happens before any creation rather than claiming an
// outcome.
func TestRenderNotesProjectVolumeCheck(t *testing.T) {
	rendered := renderPlain(t, testPlan(t))
	if !strings.Contains(rendered, "compatibility with existing volumes is checked before any creation") {
		t.Errorf("plan with project volumes does not note the pre-creation check:\n%s", rendered)
	}
	if strings.Contains(rendered, "reuses the existing volume") || strings.Contains(rendered, "will be created") {
		t.Errorf("unchecked plan claims a compatibility outcome:\n%s", rendered)
	}
}

// TestRenderReportsVolumeReuseAndCreation checks the rendered per-volume
// outcomes once the check has run cleanly.
func TestRenderReportsVolumeReuseAndCreation(t *testing.T) {
	p := testPlan(t)
	p.VolumeReport = &volumes.Report{Reused: []string{"cache"}}
	rendered := renderPlain(t, p)
	if !strings.Contains(rendered, "project volume "+p.VolumeNamespace+"-cache at /var/cache (reuses the existing volume)") {
		t.Errorf("rendered plan does not report the reuse:\n%s", rendered)
	}

	p.VolumeReport = &volumes.Report{Fresh: []string{"cache"}}
	rendered = renderPlain(t, p)
	if !strings.Contains(rendered, "(will be created)") {
		t.Errorf("rendered plan does not report the creation:\n%s", rendered)
	}
}

// TestRenderReportsVolumeConflicts checks that a conflict renders both
// definitions and the suggestion, while the plan footer still promises
// nothing changed.
func TestRenderReportsVolumeConflicts(t *testing.T) {
	p := testPlan(t)
	declared := p.Translation.ProjectVolumes[0]
	p.VolumeReport = &volumes.Report{Conflicts: []volumes.Conflict{{
		Declared:   declared,
		Existing:   msb.VolumeInfo{Name: declared.Backend, Kind: "disk", CapacityBytes: int64Ptr(8589934592)},
		Mismatches: []string{`kind: declared "dir", existing "disk"`},
	}}}
	rendered := renderPlain(t, p)
	for _, want := range []string{
		"project volume conflicts",
		"CONFLICT",
		`"cache"`,
		"declared in sbx.toml:",
		"existing volume:",
		`kind: declared "dir", existing "disk"`,
		"another logical name",
		"sbx changed nothing",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
}

// TestRenderReportsVolumeCheckFailure checks that a failed or malformed
// inspection renders as unknown, never as an empty listing or a
// compatible definition.
func TestRenderReportsVolumeCheckFailure(t *testing.T) {
	p := testPlan(t)
	p.VolumeCheckErr = errors.New("listing the backend's volumes: msb exploded")
	rendered := renderPlain(t, p)
	if !strings.Contains(rendered, "compatibility could not be checked: listing the backend's volumes: msb exploded") {
		t.Errorf("rendered plan does not report the failed check:\n%s", rendered)
	}
	if strings.Contains(rendered, "reuses the existing volume") || strings.Contains(rendered, "will be created") {
		t.Errorf("failed check rendered as a compatibility outcome:\n%s", rendered)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestRenderNeverContainsSecretValues(t *testing.T) {
	rendered := renderPlain(t, testPlan(t))
	// The plan knows names and host variables, never values. The value is
	// not set in the test process at all; guard against future regressions
	// by checking the placeholder form is what appears.
	if strings.Count(rendered, "SBX_PLAN_TOKEN") != 2 {
		t.Errorf("the host variable name should appear exactly twice (secret line and map):\n%s", rendered)
	}
}

func TestComposeReportsMissingBindSource(t *testing.T) {
	root := t.TempDir()
	info := gitx.Info{WorktreeRoot: root, CommonDir: filepath.Join(filepath.Dir(root), ".git")}
	cfg, err := config.Load(writeConfig(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "bind"
source = "./absent.txt"
target = "/absent"

[network]
policy = "public"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := Compose(info, cfg)
	if p.TranslateErr == nil {
		t.Fatal("Compose succeeded with a missing bind source")
	}
	if !strings.Contains(renderPlain(t, p), "absent.txt") {
		t.Errorf("rendered plan does not name the missing source:\n%s", renderPlain(t, p))
	}
}

func TestRenderShowsResolvedBind(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(writeConfig(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "bind"
source = "./notes.txt"
target = "/mnt/notes.txt"
read_only = true

[network]
policy = "public"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := Compose(gitx.Info{WorktreeRoot: root, CommonDir: filepath.Join(filepath.Dir(root), ".git")}, cfg)
	rendered := renderPlain(t, p)
	if !strings.Contains(rendered, "bind "+filepath.Join(root, "notes.txt")+" -> /mnt/notes.txt (read-only)") {
		t.Errorf("rendered plan does not show the resolved bind:\n%s", rendered)
	}
}
