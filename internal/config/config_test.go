package config

import (
	"os"
	"path/filepath"
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
egress = "public"
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
	if cfg.Network.Egress != "public" {
		t.Errorf("Egress = %q", cfg.Network.Egress)
	}
}

func TestLoadAllowlist(t *testing.T) {
	content := `
image = "alpine"
cpus = 1
memory = "1G"

[network]
egress = "allowlist"
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
egress = "public"
`, `image`},
		{"blank image", `
image = ""
cpus = 1
memory = "1G"
[network]
egress = "public"
`, `image`},
		{"missing cpus", `
image = "alpine"
memory = "1G"
[network]
egress = "public"
`, `cpus`},
		{"zero cpus", `
image = "alpine"
cpus = 0
memory = "1G"
[network]
egress = "public"
`, `cpus`},
		{"negative cpus", `
image = "alpine"
cpus = -1
memory = "1G"
[network]
egress = "public"
`, `cpus`},
		{"missing memory", `
image = "alpine"
cpus = 1
[network]
egress = "public"
`, `memory`},
		{"invalid memory format", `
image = "alpine"
cpus = 1
memory = "512MB"
[network]
egress = "public"
`, `memory`},
		{"memory lowercase suffix", `
image = "alpine"
cpus = 1
memory = "512m"
[network]
egress = "public"
`, `memory`},
		{"missing network", `
image = "alpine"
cpus = 1
memory = "1G"
`, `network`},
		{"invalid egress", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "bridged"
`, `egress`},
		{"missing egress", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
`, `egress`},
		{"allowlist without allow", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "allowlist"
`, `allow`},
		{"empty allow", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "allowlist"
allow = []
`, `allow`},
		{"allow outside allowlist", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "public"
allow = ["example.com"]
`, `allow`},
		{"single-label domain", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "allowlist"
allow = ["localhost"]
`, `allow`},
		{"suffix with one label", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "allowlist"
allow = ["*.com"]
`, `allow`},
		{"dns_nameservers under public egress", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "public"
dns_nameservers = ["1.1.1.1"]
`, `not supported yet`},
		{"unknown key", `
image = "alpine"
cpus = 1
memory = "1G"
unknown = true
[network]
egress = "public"
`, `unknown`},
		{"wrong type for cpus", `
image = "alpine"
cpus = "two"
memory = "1G"
[network]
egress = "public"
`, `cpus`},
		{"wrong type for memory", `
image = "alpine"
cpus = 1
memory = 512
[network]
egress = "public"
`, `memory`},
		{"wrong type for allow", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "allowlist"
allow = "example.com"
`, `allow`},
		{"shell table instead of string", `
image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "public"
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

func TestLoadRejectsNotYetSupportedFields(t *testing.T) {
	tests := map[string]string{
		"workspace":     "[workspace]\ntarget = \"/work\"",
		"mounts":        "[[mounts]]\ntype = \"bind\"\nsource = \"./x\"\ntarget = \"/x\"",
		"volumes":       "[volumes.cache]\ntarget = \"/cache\"\nscope = \"project\"",
		"secrets":       "[secrets.TOKEN]\nfrom_env = \"TOKEN\"\nallow = [\"example.com\"]",
		"ports":         "[ports.web]\nguest = 4000",
		"env":           "[env]\nFOO = \"bar\"",
		"build":         "[build]\ncontext = \".\"",
		"bootstrap":     "[bootstrap]\nrun = \"echo hi\"",
		"image_check":   "image_check = \"check.sh\"",
		"dns_nameservers": `image = "alpine"
cpus = 1
memory = "1G"
[network]
egress = "public"
dns_nameservers = ["1.1.1.1"]`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, content + "\n"))
			if err == nil {
				t.Fatalf("Load succeeded for unsupported field %q", name)
			}
			if !strings.Contains(err.Error(), "not supported yet") {
				t.Errorf("error = %q, want it to say the field is not supported yet", err.Error())
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error = %q, want it to name %q", err.Error(), name)
			}
		})
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