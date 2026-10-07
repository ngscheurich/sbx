package translate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/volumes"
)

// restrictedTOML is the restricted CLI fixture's configuration, copied from
// fixtures/restricted-cli/sbx.toml.
const restrictedTOML = `
image = "sbx-restricted-cli:latest"
cpus = 2
memory = "2G"
shell = "/bin/bash"

[[mounts]]
type = "bind"
source = "./host-notes.txt"
target = "/mnt/host-notes.txt"
read_only = true

[[mounts]]
type = "bind"
source = "./host-state"
target = "/mnt/host-state"

[volumes.go_mod]
target = "/go/pkg/mod"
scope = "project"

[volumes.go_build]
target = "/root/.cache/go-build"
scope = "project"

[network]
egress = "allowlist"
allow = ["proxy.golang.org", "sum.golang.org", "example.com"]
dns_nameservers = ["1.1.1.1", "8.8.8.8"]

[secrets.TEST_TOKEN]
from_env = "SBX_FIXTURE_TOKEN"
allow = ["example.com"]

`

func load(t *testing.T, toml string) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sbx.toml")
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func info(root string) gitx.Info {
	return gitx.Info{WorktreeRoot: root, CommonDir: filepath.Join(filepath.Dir(root), ".git")}
}

// TestSandboxTranslatesRestrictedFixture pins the translation of the
// restricted CLI fixture: mounts, Project volume names, network policy, and
// the secret map, with the Workspace first among the binds.
func TestSandboxTranslatesRestrictedFixture(t *testing.T) {
	// The bind sources must exist, so the fixture's files are created in a
	// temporary worktree just as a checkout would hold them.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "host-notes.txt"), []byte("read-only fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "host-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := load(t, restrictedTOML)
	tr, err := Sandbox(info(root), cfg, "disposable")
	if err != nil {
		t.Fatalf("Sandbox: %v", err)
	}

	if tr.Workspace != "/workspace" {
		t.Errorf("Workspace = %q", tr.Workspace)
	}
	o := tr.Options
	if len(o.Mounts) != 3 {
		t.Fatalf("Mounts = %+v, want workspace plus 2 binds", o.Mounts)
	}
	if o.Mounts[0].Source != root || o.Mounts[0].Target != "/workspace" || o.Mounts[0].ReadOnly {
		t.Errorf("workspace mount = %+v", o.Mounts[0])
	}
	if o.Mounts[1].Source != root+"/host-notes.txt" || o.Mounts[1].Target != "/mnt/host-notes.txt" || !o.Mounts[1].ReadOnly {
		t.Errorf("notes bind = %+v", o.Mounts[1])
	}
	if !o.Mounts[1].IsFile {
		t.Errorf("the notes file source should select --mount-file: %+v", o.Mounts[1])
	}
	if o.Mounts[2].Source != root+"/host-state" || o.Mounts[2].ReadOnly || o.Mounts[2].IsFile {
		t.Errorf("state bind = %+v", o.Mounts[2])
	}
	if len(o.Named) != 2 {
		t.Fatalf("Named = %+v, want the 2 project volumes", o.Named)
	}
	ns := identity.VolumeNamespace(filepath.Join(filepath.Dir(root), ".git"))
	// Project volume names pair the namespace hash with the logical name;
	// sorted by logical name for determinism.
	if o.Named[0].Name != ns+"-go_build" || o.Named[0].Target != "/root/.cache/go-build" {
		t.Errorf("go_build mount = %+v (namespace %s)", o.Named[0], ns)
	}
	if o.Named[1].Name != ns+"-go_mod" || o.Named[1].Target != "/go/pkg/mod" {
		t.Errorf("go_mod mount = %+v (namespace %s)", o.Named[1], ns)
	}
	if !o.TLSIntercept {
		t.Error("allowlist egress must turn on TLS interception")
	}
	if o.NoNet {
		t.Error("allowlist egress must not disable the network")
	}
	wantRules := []string{"allow@proxy.golang.org", "allow@sum.golang.org", "allow@example.com"}
	if len(o.NetRules) != len(wantRules) {
		t.Fatalf("NetRules = %v", o.NetRules)
	}
	for i, r := range wantRules {
		if o.NetRules[i] != r {
			t.Errorf("NetRules[%d] = %q, want %q", i, o.NetRules[i], r)
		}
	}
	if got := o.DNSNameservers; len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8" {
		t.Errorf("DNSNameservers = %v", got)
	}
	if len(o.Env) != 0 {
		t.Errorf("Env = %v, want none", o.Env)
	}
	for _, l := range o.Labels {
		if l.Key == "sbx.mode" && l.Value != "disposable" {
			t.Errorf("sbx.mode = %q", l.Value)
		}
	}

	if tr.SecretConfYAML == "" {
		t.Fatal("SecretConfYAML is empty")
	}
	want := "TEST_TOKEN:\n  value: \"${SBX_FIXTURE_TOKEN}\"\n  allow: [example.com]\n"
	if tr.SecretConfYAML != want {
		t.Errorf("SecretConfYAML =\n%s\nwant:\n%s", tr.SecretConfYAML, want)
	}
	if strings.Contains(tr.SecretConfYAML, "secret-value") {
		t.Error("secret map contains a value")
	}
}

// TestSandboxProjectVolumesCarryDefinitions checks that Project volumes
// keep their full definitions — kind, size, quota — both in the named
// mounts msb receives and in the declared list the compatibility check
// compares against the backend's listing.
func TestSandboxProjectVolumesCarryDefinitions(t *testing.T) {
	root := t.TempDir()
	cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.data]
target = "/data"
scope = "project"
kind = "disk"
size = "8G"

[volumes.capped]
target = "/capped"
scope = "project"
quota = "4G"

[network]
egress = "public"
`)
	tr, err := Sandbox(info(root), cfg, "disposable")
	if err != nil {
		t.Fatalf("Sandbox: %v", err)
	}
	ns := identity.VolumeNamespace(filepath.Join(filepath.Dir(root), ".git"))

	if len(tr.Options.Named) != 2 {
		t.Fatalf("Named = %+v, want the 2 project volumes", tr.Options.Named)
	}
	wantMounts := []msb.NamedMount{
		{Name: ns + "-capped", Target: "/capped", Kind: "dir", Quota: "4G"},
		{Name: ns + "-data", Target: "/data", Kind: "disk", Size: "8G"},
	}
	for i, want := range wantMounts {
		if tr.Options.Named[i] != want {
			t.Errorf("Named[%d] = %+v, want %+v", i, tr.Options.Named[i], want)
		}
	}

	if len(tr.ProjectVolumes) != 2 {
		t.Fatalf("ProjectVolumes = %+v, want the 2 declared volumes", tr.ProjectVolumes)
	}
	wantDeclared := []volumes.Declared{
		{Logical: "capped", Backend: ns + "-capped", Target: "/capped", Kind: "dir", Quota: "4G"},
		{Logical: "data", Backend: ns + "-data", Target: "/data", Kind: "disk", Size: "8G"},
	}
	for i, want := range wantDeclared {
		if tr.ProjectVolumes[i] != want {
			t.Errorf("ProjectVolumes[%d] = %+v, want %+v", i, tr.ProjectVolumes[i], want)
		}
	}
}

// TestSandboxResolvesBindSourceShapes covers absolute, "~"-prefixed, and
// relative bind sources, plus the missing-source error.
func TestSandboxResolvesBindSourceShapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "dotfile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rel.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "abs.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "bind"
source = "`+filepath.Join(root, "abs.txt")+`"
target = "/abs"

[[mounts]]
type = "bind"
source = "~/dotfile"
target = "/home"

[[mounts]]
type = "bind"
source = "./rel.txt"
target = "/rel"

[network]
egress = "public"
`)
	tr, err := Sandbox(info(root), cfg, "disposable")
	if err != nil {
		t.Fatalf("Sandbox: %v", err)
	}
	sources := []string{}
	for _, m := range tr.Options.Mounts[1:] {
		sources = append(sources, m.Source)
	}
	want := []string{filepath.Join(root, "abs.txt"), filepath.Join(home, "dotfile"), filepath.Join(root, "rel.txt")}
	if len(sources) != len(want) {
		t.Fatalf("sources = %v", sources)
	}
	for i := range want {
		if sources[i] != want[i] {
			t.Errorf("source[%d] = %q, want %q", i, sources[i], want[i])
		}
	}
}

func TestSandboxMissingBindSourceFails(t *testing.T) {
	root := t.TempDir()
	cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "bind"
source = "./absent.txt"
target = "/absent"

[network]
egress = "public"
`)
	_, err := Sandbox(info(root), cfg, "disposable")
	if err == nil {
		t.Fatal("Sandbox succeeded with a missing bind source")
	}
	if !strings.Contains(err.Error(), "absent.txt") {
		t.Errorf("error does not name the missing source: %v", err)
	}
}

// TestSandboxRejectsInterpolatedBindSources pins the fail-closed rule: sbx
// has no interpolation language, so "$" references and "~user" forms in a
// bind source are errors, never silent expansions.
func TestSandboxRejectsInterpolatedBindSources(t *testing.T) {
	t.Setenv("OUTSIDE", "/etc")
	for _, source := range []string{
		"${OUTSIDE}",
		"$OUTSIDE",
		"./dir${UNSET_VAR}",
		"./cach$1e",
		"~bob/notes",
	} {
		root := t.TempDir()
		cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "bind"
source = "`+source+`"
target = "/mnt"

[network]
egress = "public"
`)
		if _, err := Sandbox(info(root), cfg, "disposable"); err == nil {
			t.Errorf("Sandbox accepted bind source %q", source)
		} else if !strings.Contains(err.Error(), source) {
			t.Errorf("error for %q does not name the source: %v", source, err)
		}
	}
}

func TestSandboxTmpfsAndOwnedVolumes(t *testing.T) {
	root := t.TempDir()
	cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[[mounts]]
type = "tmpfs"
target = "/tmp"
size = "512M"

[[mounts]]
type = "tmpfs"
target = "/run"
size = "64M"

[volumes.data]
target = "/var/lib/data"
scope = "sandbox"
kind = "disk"
size = "2G"

[network]
egress = "none"
`)
	tr, err := Sandbox(info(root), cfg, "persistent")
	if err != nil {
		t.Fatalf("Sandbox: %v", err)
	}
	o := tr.Options
	if len(tr.Tmpfs) != 2 || tr.Tmpfs[0].Target != "/tmp" || tr.Tmpfs[0].Size != "512M" {
		t.Errorf("Tmpfs = %+v", tr.Tmpfs)
	}
	if tr.FsConfYAML == "" {
		t.Fatal("FsConfYAML is empty")
	}
	want := "mounts:\n  - tmpfs: { size: \"512M\" }\n    target: \"/tmp\"\n  - tmpfs: { size: \"64M\" }\n    target: \"/run\"\n"
	if tr.FsConfYAML != want {
		t.Errorf("FsConfYAML =\n%s\nwant:\n%s", tr.FsConfYAML, want)
	}
	if len(o.Owned) != 1 || o.Owned[0].Kind != "disk" || o.Owned[0].Size != "2G" || o.Owned[0].Target != "/var/lib/data" {
		t.Errorf("Owned = %+v", o.Owned)
	}
	if !o.NoNet {
		t.Error("egress none must set NoNet")
	}
	if o.TLSIntercept || len(o.NetRules) > 0 {
		t.Errorf("egress none must not carry allowlist policy: rules=%v tls=%v", o.NetRules, o.TLSIntercept)
	}
	for _, l := range o.Labels {
		if l.Key == "sbx.mode" && l.Value != "persistent" {
			t.Errorf("sbx.mode = %q", l.Value)
		}
	}
}

func TestSandboxEnvSortedAndWorkspaceDefault(t *testing.T) {
	root := t.TempDir()
	cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[env]
B = "2"
A = "1"

[network]
egress = "public"
`)
	tr, err := Sandbox(info(root), cfg, "disposable")
	if err != nil {
		t.Fatalf("Sandbox: %v", err)
	}
	if got := tr.Options.Env; len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Errorf("Env = %v, want sorted A=1, B=2", got)
	}
	if tr.Workspace != "/workspace" {
		t.Errorf("default Workspace = %q", tr.Workspace)
	}
}

func TestFsConfYAMLRejectsEnvReferencePatterns(t *testing.T) {
	// msb expands ${NAME} in YAML files and rejects unknown variables, so a
	// mount string containing the pattern must fail translation.
	_, err := FsConfYAML([]Tmpfs{{Target: "/a${b}", Size: "64M"}})
	if err == nil {
		t.Fatal("FsConfYAML accepted a ${...} pattern")
	}
	if !strings.Contains(err.Error(), "environment reference") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

func TestCheckSecretEnv(t *testing.T) {
	cfg := load(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[secrets.A]
from_env = "HOST_A"
allow = ["a.example.com"]

[secrets.B]
from_env = "HOST_B"
allow = ["b.example.com"]

[network]
egress = "public"
`)
	lookup := func(key string) (string, bool) {
		if key == "HOST_A" {
			return "x", true
		}
		return "", false
	}
	err := CheckSecretEnv(cfg, lookup)
	if err == nil {
		t.Fatal("CheckSecretEnv succeeded with a missing variable")
	}
	if !strings.Contains(err.Error(), "HOST_B") || strings.Contains(err.Error(), "HOST_A") {
		t.Errorf("error names the wrong variables: %v", err)
	}
	if err := CheckSecretEnv(cfg, func(string) (string, bool) { return "x", true }); err != nil {
		t.Errorf("CheckSecretEnv failed with all variables present: %v", err)
	}
}

func TestSecretConfYAMLMultipleAllows(t *testing.T) {
	secrets := map[string]config.SecretConfig{
		"TWO": {FromEnv: "HOST_TWO", Allow: []string{"one.com", "two.com"}},
	}
	got := SecretConfYAML(secrets, []string{"TWO"})
	want := "TWO:\n  value: \"${HOST_TWO}\"\n  allow:\n    - one.com\n    - two.com\n"
	if got != want {
		t.Errorf("SecretConfYAML =\n%s\nwant:\n%s", got, want)
	}
}
