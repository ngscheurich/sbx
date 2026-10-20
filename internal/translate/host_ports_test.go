package translate

import (
	"slices"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/msb"
)

func TestSandboxHostPorts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		policy string
		ports  []int
		want   []string
	}{
		{name: "public unchanged", policy: "public"},
		{name: "allowlist unchanged", policy: "allowlist", want: []string{"allow@example.com"}},
		{
			name: "public with host", policy: "public", ports: []int{8080, 3000},
			want: []string{"allow@public", "allow@dns", "allow@host:tcp:3000", "allow@host:tcp:8080"},
		},
		{
			name: "allowlist with host", policy: "allowlist", ports: []int{8080, 3000},
			want: []string{"allow@example.com", "allow@dns", "allow@host:tcp:3000", "allow@host:tcp:8080"},
		},
	} {
		for _, mode := range []string{"persistent", "disposable"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				cfg := load(t, "image = \"alpine\"\ncpus = 1\nmemory = \"1G\"\n[network]\npolicy = \"public\"\n")
				cfg.Network = config.NetworkConfig{Policy: tc.policy, HostPorts: tc.ports}
				if tc.policy == "allowlist" {
					cfg.Network.Allow = []string{"example.com"}
				}
				declaredPorts := slices.Clone(cfg.Network.HostPorts)
				tr, err := Sandbox(info(t.TempDir()), cfg, mode)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(cfg.Network.HostPorts, declaredPorts) {
					t.Errorf("translation mutated host ports: got %v, want %v", cfg.Network.HostPorts, declaredPorts)
				}
				if !slices.Equal(tr.Options.NetRules, tc.want) {
					t.Errorf("NetRules = %v, want %v", tr.Options.NetRules, tc.want)
				}
				if tr.Options.NoNet || tr.Options.TLSIntercept != (tc.policy == "allowlist") {
					t.Errorf("unexpected network options: %+v", tr.Options)
				}
				args := msb.CreateArgs(tr.Options)
				var rules []string
				for i, arg := range args {
					if arg == "--net-rule" {
						rules = append(rules, args[i+1])
					}
				}
				if !slices.Equal(rules, tc.want) {
					t.Errorf("backend rules = %v, want %v", rules, tc.want)
				}
			})
		}
	}
}
