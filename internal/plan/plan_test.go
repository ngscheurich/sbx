package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
)

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
	// The Plan shows secret names and host variable names, never values. A
	// value in the environment must not leak into the rendered plan even if
	// a host variable happened to share a name's spelling.
	t.Setenv("SBX_PLAN_TOKEN", "throwaway-plan-value-41f9")
	rendered := testPlan(t).Render()
	if strings.Contains(rendered, "throwaway-plan-value") {
		t.Errorf("rendered plan contains a host environment value:\n%s", rendered)
	}
}

func TestRenderShowsTranslation(t *testing.T) {
	rendered := testPlan(t).Render()
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
	rendered := p.Render()
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
egress = "public"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rendered := Compose(info, cfg).Render()
	if strings.Contains(rendered, "--secret-conf") {
		t.Errorf("secretless plan renders --secret-conf, which msb create would never receive:\n%s", rendered)
	}
	if strings.Contains(rendered, "secret map") {
		t.Errorf("secretless plan renders the secret map section:\n%s", rendered)
	}
}

func TestRenderNotesProjectVolumeGate(t *testing.T) {
	rendered := testPlan(t).Render()
	if !strings.Contains(rendered, "compatibility checks are pending") {
		t.Errorf("plan with project volumes does not note the pending gate:\n%s", rendered)
	}
}

func TestRenderNeverContainsSecretValues(t *testing.T) {
	rendered := testPlan(t).Render()
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
egress = "public"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := Compose(info, cfg)
	if p.TranslateErr == nil {
		t.Fatal("Compose succeeded with a missing bind source")
	}
	if !strings.Contains(p.Render(), "absent.txt") {
		t.Errorf("rendered plan does not name the missing source:\n%s", p.Render())
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
egress = "public"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := Compose(gitx.Info{WorktreeRoot: root, CommonDir: filepath.Join(filepath.Dir(root), ".git")}, cfg)
	rendered := p.Render()
	if !strings.Contains(rendered, "bind "+filepath.Join(root, "notes.txt")+" -> /mnt/notes.txt (read-only)") {
		t.Errorf("rendered plan does not show the resolved bind:\n%s", rendered)
	}
}
