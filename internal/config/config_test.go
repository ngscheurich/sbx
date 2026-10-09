package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sbx.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimal = `
image = "alpine:3.20"
cpus = 2
memory = "2G"

[network]
policy = "public"
`

func TestLoadMinimal(t *testing.T) {
	cfg, err := Load(write(t, minimal))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Image != "alpine:3.20" {
		t.Errorf("Image = %q", cfg.Image)
	}
	if cfg.CPUs != 2 {
		t.Errorf("CPUs = %v", cfg.CPUs)
	}
	if cfg.Memory != "2G" {
		t.Errorf("Memory = %q", cfg.Memory)
	}
	if cfg.Shell != "/bin/sh" {
		t.Errorf("Shell = %q, want default /bin/sh", cfg.Shell)
	}
	if cfg.Network.Policy != "public" {
		t.Errorf("Policy = %q", cfg.Network.Policy)
	}
}

func TestLoadAllowlist(t *testing.T) {
	content := `
image = "alpine"
cpus = 1
memory = "1G"

[network]
policy = "allowlist"
allow = ["proxy.golang.org", "*.example.com"]
`
	cfg, err := Load(write(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Network.Allow) != 2 {
		t.Errorf("Allow = %v", cfg.Network.Allow)
	}
}

func TestLoadValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"missing image", `
cpus = 1
memory = "1G"
[network]
policy = "public"
`, `image`},
		{"blank image", `
image = ""
cpus = 1
memory = "1G"
[network]
policy = "public"
`, `image`},
		{"missing cpus", `
image = "alpine"
memory = "1G"
[network]
policy = "public"
`, `cpus`},
		{"zero cpus", `
image = "alpine"
cpus = 0
memory = "1G"
[network]
policy = "public"
`, `cpus`},
		{"negative cpus", `
image = "alpine"
cpus = -1
memory = "1G"
[network]
policy = "public"
`, `cpus`},
		{"nan cpus", `
image = "alpine"
cpus = nan
memory = "1G"
[network]
policy = "public"
`, `cpus`},
		{"infinite cpus", `
image = "alpine"
cpus = inf
memory = "1G"
[network]
policy = "public"
`, `cpus`},
		{"zero memory", `
image = "alpine"
cpus = 1
memory = "0G"
[network]
policy = "public"
`, `memory`},
		{"duplicate volume targets", `
image = "alpine"
cpus = 1
memory = "1G"
[volumes.a]
target = "/data"
scope = "sandbox"
[volumes.b]
target = "/data"
scope = "sandbox"
[network]
policy = "public"
`, `duplicate mount target`},
		{"volume target collides with workspace", `
image = "alpine"
cpus = 1
memory = "1G"
[volumes.data]
target = "/workspace"
scope = "sandbox"
[network]
policy = "public"
`, `duplicate mount target`},
		{"volume target collides with a mount", `
image = "alpine"
cpus = 1
memory = "1G"
[[mounts]]
type = "bind"
source = "./x"
target = "/mnt/x"
[volumes.data]
target = "/mnt/x"
scope = "sandbox"
[network]
policy = "public"
`, `duplicate mount target`},
		{"env reference in bind target", `
image = "alpine"
cpus = 1
memory = "1G"
[[mounts]]
type = "bind"
source = "./x"
target = "/mnt/x${y}"
[network]
policy = "public"
`, `environment reference`},
		{"env reference in volume target", `
image = "alpine"
cpus = 1
memory = "1G"
[volumes.data]
target = "/var/lib/${x}"
scope = "sandbox"
[network]
policy = "public"
`, `environment reference`},
		{"secret name collides with env", `
image = "alpine"
cpus = 1
memory = "1G"
[env]
FOO = "bar"
[secrets.FOO]
from_env = "HOST_FOO"
allow = ["example.com"]
[network]
policy = "public"
`, `conflicts`},
		{"missing memory", `
image = "alpine"
cpus = 1
[network]
policy = "public"
`, `memory`},
		{"invalid memory format", `
image = "alpine"
cpus = 1
memory = "512MB"
[network]
policy = "public"
`, `memory`},
		{"memory lowercase suffix", `
image = "alpine"
cpus = 1
memory = "512m"
[network]
policy = "public"
`, `memory`},
		{"missing network", `
image = "alpine"
cpus = 1
memory = "1G"
`, `network`},
		{"invalid policy", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "bridged"
`, `policy`},
		{"missing policy", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
`, `policy`},
		{"allowlist without allow", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "allowlist"
`, `allow`},
		{"empty allow", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "allowlist"
allow = []
`, `allow`},
		{"allow outside allowlist", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "public"
allow = ["example.com"]
`, `allow`},
		{"single-label domain", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "allowlist"
allow = ["localhost"]
`, `allow`},
		{"suffix with one label", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "allowlist"
allow = ["*.com"]
`, `allow`},
		{"dns_nameservers not an address with allowlist", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "allowlist"
allow = ["example.com"]
dns_nameservers = ["dns.example.com"]
`, `dns_nameservers`},
		{"unknown key", `
image = "alpine"
cpus = 1
memory = "1G"
unknown = true
[network]
policy = "public"
`, `unknown`},
		{"wrong type for cpus", `
image = "alpine"
cpus = "two"
memory = "1G"
[network]
policy = "public"
`, `cpus`},
		{"wrong type for memory", `
image = "alpine"
cpus = 1
memory = 512
[network]
policy = "public"
`, `memory`},
		{"wrong type for allow", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "allowlist"
allow = "example.com"
`, `allow`},
		{"shell table instead of string", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "public"
[shell]
`, `shell`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.content))
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadBootstrap(t *testing.T) {
	cfg, err := Load(write(t, minimal+`
[bootstrap]
run = "echo bootstrapped > /workspace/.bootstrapped"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.BootstrapDeclared() {
		t.Error("a loaded [bootstrap] is not reported as declared")
	}
	if want := "echo bootstrapped > /workspace/.bootstrapped"; cfg.Bootstrap.Run != want {
		t.Errorf("Bootstrap.Run = %q, want %q", cfg.Bootstrap.Run, want)
	}

	bare, err := Load(write(t, minimal))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if bare.BootstrapDeclared() {
		t.Error("a configuration without [bootstrap] is reported as declaring one")
	}
}

func TestLoadRejectsEmptyBootstrap(t *testing.T) {
	for _, content := range []string{
		minimal + "\n[bootstrap]\n",
		minimal + "\n[bootstrap]\nrun = \"  \"\n",
	} {
		_, err := Load(write(t, content))
		if err == nil {
			t.Fatalf("Load succeeded with an empty bootstrap:\n%s", content)
		}
		if !strings.Contains(err.Error(), "bootstrap.run") {
			t.Errorf("error = %q, want it to name bootstrap.run", err.Error())
		}
	}
}

// TestLoadImageCheck checks that image_check parses as the project-owned
// guest script's path, and that declaring it empty is rejected: an empty
// declaration would silently mean "no check".
func TestLoadImageCheck(t *testing.T) {
	base := "image = \"alpine:3.20\"\ncpus = 1\nmemory = \"1G\"\nimage_check = \"image-check.sh\"\n\n[network]\npolicy = \"public\"\n"
	cfg, err := Load(write(t, base))
	if err != nil {
		t.Fatalf("Load with image_check: %v", err)
	}
	if cfg.ImageCheck != "image-check.sh" {
		t.Errorf("ImageCheck = %q, want %q", cfg.ImageCheck, "image-check.sh")
	}

	_, err = Load(write(t, "image = \"alpine:3.20\"\ncpus = 1\nmemory = \"1G\"\nimage_check = \"\"\n\n[network]\npolicy = \"public\"\n"))
	if err == nil || !strings.Contains(err.Error(), "image_check") {
		t.Errorf("empty image_check error = %v, want it to name image_check", err)
	}
}

func TestLoadPorts(t *testing.T) {
	cfg, err := Load(write(t, minimal+`
[ports.web]
guest = 4000

[ports.metrics]
guest = 9090
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("Ports = %v", cfg.Ports)
	}
	if got := cfg.Ports["web"].Guest; got != 4000 {
		t.Errorf("ports.web.guest = %d, want 4000", got)
	}
	if got := cfg.Ports["metrics"].Guest; got != 9090 {
		t.Errorf("ports.metrics.guest = %d, want 9090", got)
	}
}

func TestLoadPortValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"missing guest", minimal + "\n[ports.web]\n", "ports.web: guest is required"},
		{"guest too low", minimal + "\n[ports.web]\nguest = 0", "ports.web: guest is required"},
		{"guest too high", minimal + "\n[ports.web]\nguest = 65536", "ports.web: guest is required"},
		{"bad name", minimal + "\n[ports.Web]\nguest = 4000", `ports: name "Web"`},
		{"duplicate guest", minimal + "\n[ports.web]\nguest = 8080\n\n[ports.metrics]\nguest = 8080", "ports.web: guest 8080 is already declared as ports.metrics"},
		{"policy none", `
image = "alpine"
cpus = 1
memory = "1G"

[network]
policy = "none"

[ports.web]
guest = 4000
`, `ports: declared ports are invalid with policy = "none"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.content))
			if err == nil {
				t.Fatalf("Load succeeded for invalid ports:\n%s", tt.content)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadBuild(t *testing.T) {
	cfg, err := Load(write(t, minimal+`
[build]
context = "./docker"
dockerfile = "Containerfile"
target = "runtime"
platform = "linux/amd64"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Build == nil {
		t.Fatal("Build is nil, want the declared recipe")
	}
	if cfg.Build.Context != "./docker" {
		t.Errorf("Build.Context = %q", cfg.Build.Context)
	}
	if cfg.Build.Dockerfile != "Containerfile" {
		t.Errorf("Build.Dockerfile = %q", cfg.Build.Dockerfile)
	}
	if cfg.Build.Target != "runtime" {
		t.Errorf("Build.Target = %q", cfg.Build.Target)
	}
	if cfg.Build.Platform != "linux/amd64" {
		t.Errorf("Build.Platform = %q", cfg.Build.Platform)
	}
}

func TestLoadBuildDefaults(t *testing.T) {
	cfg, err := Load(write(t, minimal+`
[build]
context = "."
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Build == nil {
		t.Fatal("Build is nil, want the declared recipe")
	}
	if want := "linux/" + runtime.GOARCH; cfg.Build.Platform != want {
		t.Errorf("Build.Platform = %q, want the host default %q", cfg.Build.Platform, want)
	}
	if cfg.Build.Dockerfile != "" || cfg.Build.Target != "" {
		t.Errorf("Build = %+v, want no dockerfile or target", cfg.Build)
	}
}

func TestLoadNoBuildLeavesImagePrebuilt(t *testing.T) {
	cfg, err := Load(write(t, minimal))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Build != nil {
		t.Errorf("Build = %+v, want nil: without [build] the image is prebuilt", cfg.Build)
	}
}

func TestLoadBuildRequiresContext(t *testing.T) {
	_, err := Load(write(t, minimal+`
[build]
dockerfile = "Dockerfile"
`))
	if err == nil {
		t.Fatal("Load succeeded with a [build] table without a context")
	}
	if !strings.Contains(err.Error(), "build") || !strings.Contains(err.Error(), "context") {
		t.Errorf("error = %q, want it to name build and context", err.Error())
	}
}

func TestLoadBuildRejectsUnknownKeys(t *testing.T) {
	_, err := Load(write(t, minimal+`
[build]
context = "."
args = ["--debug"]
`))
	if err == nil {
		t.Fatal("Load succeeded with an unknown [build] key")
	}
	if !strings.Contains(err.Error(), "build.args") {
		t.Errorf("error = %q, want it to name build.args", err.Error())
	}
}

func TestLoadBuildValidatesPlatform(t *testing.T) {
	for _, platform := range []string{"linux", "linux/", "/amd64", "linux/amd64/extra/deep", "Linux/AMD64"} {
		_, err := Load(write(t, minimal+`
[build]
context = "."
platform = "`+platform+`"
`))
		if err == nil {
			t.Errorf("Load succeeded with platform %q", platform)
		} else if !strings.Contains(err.Error(), "platform") {
			t.Errorf("error = %q, want it to mention platform", err.Error())
		}
	}
	for _, platform := range []string{"linux/arm64", "linux/arm/v7"} {
		if _, err := Load(write(t, minimal+`
[build]
context = "."
platform = "`+platform+`"
`)); err != nil {
			t.Errorf("Load rejected platform %q: %v", platform, err)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err == nil {
		t.Fatal("Load succeeded for a missing file")
	}
	if !strings.Contains(err.Error(), "sbx.toml") {
		t.Errorf("error = %q, want it to mention sbx.toml", err.Error())
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	_, err := Load(write(t, "image = [unclosed\n"))
	if err == nil {
		t.Fatal("Load succeeded for malformed TOML")
	}
}

func TestLoadWorkspaceAndMounts(t *testing.T) {
	content := `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[workspace]
target = "/work"

[[mounts]]
type = "bind"
source = "./notes.txt"
target = "/mnt/notes.txt"
read_only = true

[[mounts]]
type = "bind"
source = "/abs/state"
target = "/mnt/state"

[[mounts]]
type = "tmpfs"
target = "/tmp"
size = "512M"
noexec = true

[network]
policy = "public"
`
	cfg, err := Load(write(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workspace.Target != "/work" {
		t.Errorf("Workspace.Target = %q", cfg.Workspace.Target)
	}
	if len(cfg.Mounts) != 3 {
		t.Fatalf("Mounts = %+v, want 3 in declaration order", cfg.Mounts)
	}
	if cfg.Mounts[0].Type != "bind" || cfg.Mounts[0].Source != "./notes.txt" ||
		cfg.Mounts[0].Target != "/mnt/notes.txt" || !cfg.Mounts[0].ReadOnly {
		t.Errorf("bind mount = %+v", cfg.Mounts[0])
	}
	if cfg.Mounts[1].ReadOnly {
		t.Errorf("bind mount 2 should default to read-write: %+v", cfg.Mounts[1])
	}
	if cfg.Mounts[2].Type != "tmpfs" || cfg.Mounts[2].Size != "512M" || !cfg.Mounts[2].NoExec {
		t.Errorf("tmpfs mount = %+v", cfg.Mounts[2])
	}
}

func TestLoadVolumesEnvSecretsDNS(t *testing.T) {
	content := `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.go_mod]
target = "/go/pkg/mod"
scope = "project"

[volumes.artifacts]
target = "/artifacts"
scope = "sandbox"
kind = "disk"
size = "8G"

[volumes.quota_dir]
target = "/quota"
scope = "project"
quota = "4G"

[env]
SBX_DATA_DIR = "/var/lib/app"
MODE = "dev"

[secrets.TEST_TOKEN]
from_env = "SBX_FIXTURE_TOKEN"
allow = ["example.com"]

[network]
policy = "allowlist"
allow = ["example.com"]
dns_nameservers = ["1.1.1.1", "8.8.8.8"]
`
	cfg, err := Load(write(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v := cfg.Volumes["go_mod"]; v.Scope != "project" || v.Target != "/go/pkg/mod" || v.Kind != "dir" {
		t.Errorf("go_mod volume = %+v", v)
	}
	if v := cfg.Volumes["artifacts"]; v.Kind != "disk" || v.Size != "8G" {
		t.Errorf("artifacts volume = %+v", v)
	}
	if v := cfg.Volumes["quota_dir"]; v.Quota != "4G" {
		t.Errorf("quota_dir volume = %+v", v)
	}
	if cfg.Env["SBX_DATA_DIR"] != "/var/lib/app" {
		t.Errorf("Env = %v", cfg.Env)
	}
	s := cfg.Secrets["TEST_TOKEN"]
	if s.FromEnv != "SBX_FIXTURE_TOKEN" || len(s.Allow) != 1 || s.Allow[0] != "example.com" {
		t.Errorf("secret = %+v", s)
	}
	if len(cfg.Network.DNSNameservers) != 2 {
		t.Errorf("DNSNameservers = %v", cfg.Network.DNSNameservers)
	}
}

func TestLoadValidationFailuresExtended(t *testing.T) {
	base := `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[network]
policy = "public"
`
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"workspace target relative", `
image = "alpine"
cpus = 1
memory = "1G"
[workspace]
target = "work"
[network]
policy = "public"
`, `workspace`},
		{"mount without type", base + `
[[mounts]]
source = "./x"
target = "/x"
`, `type`},
		{"mount unknown type", base + `
[[mounts]]
type = "nfs"
source = "./x"
target = "/x"
`, `type`},
		{"bind without source", base + `
[[mounts]]
type = "bind"
target = "/x"
`, `source`},
		{"mount target not absolute", base + `
[[mounts]]
type = "bind"
source = "./x"
target = "x"
`, `target`},
		{"duplicate mount targets", base + `
[[mounts]]
type = "bind"
source = "./a"
target = "/x"

[[mounts]]
type = "bind"
source = "./b"
target = "/x"
`, `duplicate`},
		{"mount target collides with workspace", base + `
[workspace]
target = "/work"

[[mounts]]
type = "bind"
source = "./a"
target = "/work"
`, `duplicate`},
		{"tmpfs without size", base + `
[[mounts]]
type = "tmpfs"
target = "/tmp"
`, `size`},
		{"tmpfs with invalid size", base + `
[[mounts]]
type = "tmpfs"
target = "/tmp"
size = "512MB"
`, `size`},
		{"bind with tmpfs size", base + `
[[mounts]]
type = "bind"
source = "./x"
target = "/x"
size = "1G"
`, `size`},
		{"volume name invalid", base + `
[volumes.GoMod]
target = "/go"
scope = "project"
`, `name`},
		{"volume name with hyphen", base + `
[volumes.go-mod]
target = "/go"
scope = "project"
`, `name`},
		{"volume without scope", base + `
[volumes.cache]
target = "/cache"
`, `scope`},
		{"volume invalid scope", base + `
[volumes.cache]
target = "/cache"
scope = "shared"
`, `scope`},
		{"volume target not absolute", base + `
[volumes.cache]
target = "cache"
scope = "project"
`, `target`},
		{"disk without size", base + `
[volumes.data]
target = "/data"
scope = "sandbox"
kind = "disk"
`, `size`},
		{"dir volume with size", base + `
[volumes.data]
target = "/data"
scope = "sandbox"
size = "2G"
`, `size`},
		{"quota on disk", base + `
[volumes.data]
target = "/data"
scope = "project"
kind = "disk"
size = "8G"
quota = "4G"
`, `quota`},
		{"quota on sandbox volume", base + `
[volumes.data]
target = "/data"
scope = "sandbox"
quota = "4G"
`, `quota`},
		{"quota invalid size", base + `
[volumes.data]
target = "/data"
scope = "project"
quota = "4X"
`, `quota`},
		{"env name invalid", base + `
[env]
"lower-case" = "x"
`, `name`},
		{"env name starting with digit", base + `
[env]
"1PASS" = "x"
`, `name`},
		{"secret name invalid", base + `
[secrets.lower-case]
from_env = "TOKEN"
allow = ["example.com"]
`, `name`},
		{"secret without from_env", base + `
[secrets.TOKEN]
allow = ["example.com"]
`, `from_env`},
		{"secret without allow", base + `
[secrets.TOKEN]
from_env = "TOKEN"
`, `allow`},
		{"secret with empty allow", base + `
[secrets.TOKEN]
from_env = "TOKEN"
allow = []
`, `allow`},
		{"secret with invalid allow entry", base + `
[secrets.TOKEN]
from_env = "TOKEN"
allow = ["*.com"]
`, `allow`},
		{"dns_nameservers with none", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "none"
dns_nameservers = ["1.1.1.1"]
`, `dns_nameservers`},
		{"dns_nameservers not an address", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
policy = "public"
dns_nameservers = ["dns.example.com"]
`, `dns_nameservers`},
		{"tmpfs with read_only", base + `
[[mounts]]
type = "tmpfs"
target = "/tmp"
size = "64M"
read_only = true
`, `read_only`},
		{"bind with noexec", base + `
[[mounts]]
type = "bind"
source = "./x"
target = "/x"
noexec = true
`, `noexec`},
		{"bind with unformatted size", base + `
[[mounts]]
type = "bind"
source = "./x"
target = "/x"
size = "garbage"
`, `size`},
		{"env value not a string", base + `
[env]
COUNT = 3
`, `env`},
		{"volume with unknown key", base + `
[volumes.cache]
target = "/cache"
scope = "project"
readonly = true
`, `unknown`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.content))
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestLoadAliases checks that [aliases] loads as a table of names to sbx
// command lines.
func TestLoadAliases(t *testing.T) {
	cfg, err := Load(write(t, minimal+`
[aliases]
ls = "exec -- ls"
ll = "ls"
`))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	want := map[string]string{"ls": "exec -- ls", "ll": "ls"}
	for name, value := range want {
		if got := cfg.Aliases[name]; got != value {
			t.Errorf("aliases[%q] = %q, want %q", name, got, value)
		}
	}
	if len(cfg.Aliases) != len(want) {
		t.Errorf("aliases = %v, want %v", cfg.Aliases, want)
	}
}

// TestLoadRejectsUninvocableAliasNames pins the name rules: a name that can
// never be invoked — empty, whitespace, or flag-like — is a load error, not
// dead configuration.
func TestLoadRejectsUninvocableAliasNames(t *testing.T) {
	tests := map[string]string{
		"leading dash": minimal + "\n[aliases]\n\"-x\" = \"exec -- ls\"\n",
		"whitespace":   minimal + "\n[aliases]\n\"my ls\" = \"exec -- ls\"\n",
		"empty":        minimal + "\n[aliases]\n\"\" = \"exec -- ls\"\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, content))
			if err == nil {
				t.Fatalf("Load succeeded with an uninvocable alias name:\n%s", content)
			}
			if !strings.Contains(err.Error(), "can never be invoked") {
				t.Errorf("error = %q, want it to say the name can never be invoked", err.Error())
			}
		})
	}
}

// TestLoadRejectsEmptyAliasValue: a value that expands to nothing could
// never dispatch, so declaring one is a load error.
func TestLoadRejectsEmptyAliasValue(t *testing.T) {
	for _, content := range []string{
		minimal + "\n[aliases]\nls = \"\"\n",
		minimal + "\n[aliases]\nls = \"   \"\n",
	} {
		_, err := Load(write(t, content))
		if err == nil {
			t.Fatalf("Load succeeded with an empty alias value:\n%s", content)
		}
		if !strings.Contains(err.Error(), "aliases.ls") {
			t.Errorf("error = %q, want it to name aliases.ls", err.Error())
		}
	}
}

// TestLoadRejectsNonStringAliasValue: an alias value is a command line, so
// a number or table is a type error like any other malformed declaration.
func TestLoadRejectsNonStringAliasValue(t *testing.T) {
	_, err := Load(write(t, minimal+"\n[aliases]\nls = 3\n"))
	if err == nil {
		t.Fatal("Load succeeded with a numeric alias value")
	}
	if !strings.Contains(err.Error(), "sbx.toml") {
		t.Errorf("error = %q, want it to name sbx.toml", err.Error())
	}
}
