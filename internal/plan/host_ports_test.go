package plan

import (
	"slices"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
)

func TestPlanHostPorts(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"public", "allowlist"} {
		t.Run(policy, func(t *testing.T) {
			cfg := config.Config{
				Image: "alpine", CPUs: 1, Memory: "1G", Shell: "/bin/sh",
				Workspace: config.WorkspaceConfig{Target: "/workspace"},
				Network: config.NetworkConfig{
					Policy: policy, HostPorts: []int{3000, 8080},
				},
			}
			if policy == "allowlist" {
				cfg.Network.Allow = []string{"example.com"}
			}
			p := Compose(gitx.Info{WorktreeRoot: "/src/app/wt1", CommonDir: "/src/app/.git"}, cfg)
			if p.TranslateErr != nil {
				t.Fatal(p.TranslateErr)
			}
			plain := renderPlain(t, p)
			for _, want := range []string{
				"policy " + policy,
				"host TCP ports 3000, 8080 via host.microsandbox.internal",
				"gateway DNS enabled",
				"--net-rule allow@host:tcp:3000",
				"--net-rule allow@host:tcp:8080",
			} {
				if !strings.Contains(plain, want) {
					t.Errorf("plan missing %q:\n%s", want, plain)
				}
			}
			report := p.JSON()
			if !slices.Equal(report.Translation.Network.HostPorts, []int{3000, 8080}) {
				t.Errorf("JSON host ports = %v", report.Translation.Network.HostPorts)
			}
			for _, rule := range []string{"allow@dns", "allow@host:tcp:3000", "allow@host:tcp:8080"} {
				if !slices.Contains(report.CreateArgv, rule) {
					t.Errorf("JSON creation argv missing %q: %v", rule, report.CreateArgv)
				}
			}
		})
	}
}

func TestJSONEmptyHostPorts(t *testing.T) {
	t.Parallel()
	if got := testPlan(t).JSON().Translation.Network.HostPorts; got == nil || len(got) != 0 {
		t.Errorf("JSON host ports = %v, want an empty array", got)
	}
}
