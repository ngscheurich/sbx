package config

import (
	"slices"
	"strings"
	"testing"
)

func TestLoadHostPorts(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"public", "allowlist"} {
		t.Run(policy, func(t *testing.T) {
			network := `policy = "` + policy + `"` + "\nhost_ports = [3000, 1, 65535]\n"
			if policy == "allowlist" {
				network += "allow = [\"example.com\"]\n"
			}
			cfg, err := Load(write(t, strings.Replace(minimal, `policy = "public"`, network, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if want := []int{3000, 1, 65535}; !slices.Equal(cfg.Network.HostPorts, want) {
				t.Errorf("HostPorts = %v, want %v", cfg.Network.HostPorts, want)
			}
		})
	}
}

func TestLoadRejectsInvalidHostPorts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		policy string
		ports  string
		want   string
	}{
		{name: "zero", policy: "public", ports: "[0]", want: "between 1 and 65535"},
		{name: "negative", policy: "public", ports: "[-1]", want: "between 1 and 65535"},
		{name: "too large", policy: "public", ports: "[65536]", want: "between 1 and 65535"},
		{name: "duplicate", policy: "public", ports: "[3000, 3000]", want: "duplicate"},
		{name: "network disabled", policy: "none", ports: "[3000]", want: `policy = "none"`},
		{name: "string", policy: "public", ports: `["3000"]`, want: "host_ports"},
		{name: "fractional", policy: "public", ports: "[3000.5]", want: "host_ports"},
		{name: "not an array", policy: "public", ports: "3000", want: "host_ports"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			network := `policy = "` + tc.policy + `"` + "\nhost_ports = " + tc.ports
			_, err := Load(write(t, strings.Replace(minimal, `policy = "public"`, network, 1)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadEmptyHostPorts(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"public", "none"} {
		network := `policy = "` + policy + `"` + "\nhost_ports = []"
		cfg, err := Load(write(t, strings.Replace(minimal, `policy = "public"`, network, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Network.HostPorts) != 0 {
			t.Errorf("HostPorts = %v, want empty", cfg.Network.HostPorts)
		}
	}
}
