package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/harness"
)

func TestRunHostPorts(t *testing.T) {
	for _, policy := range []string{"public", "allowlist"} {
		t.Run(policy, func(t *testing.T) {
			toml := strings.Replace(harness.DisposableTOML, `policy = "public"`, `policy = "`+policy+`"`, 1)
			toml += "\nhost_ports = [3000]\n"
			baseline := "allow@public"
			if policy == "allowlist" {
				toml += "allow = [\"example.com\"]\n"
				baseline = "allow@example.com"
			}
			code, _, stderr, fake := harness.RunFixture(t, toml, "--", "curl", "http://host.microsandbox.internal:3000")
			if code != 0 {
				t.Fatalf("run failed: %s", stderr)
			}
			calls := fake.Calls()
			run := calls[len(calls)-1].Args
			if run[0] != "run" {
				t.Fatalf("last backend call = %v, want run", run)
			}
			var rules []string
			for i, arg := range run {
				if arg == "--net-rule" {
					rules = append(rules, run[i+1])
				}
			}
			if want := []string{baseline, "allow@dns", "allow@host:tcp:3000"}; !slices.Equal(rules, want) {
				t.Errorf("run rules = %v, want %v", rules, want)
			}
			if slices.Contains(run, "--port") {
				t.Errorf("host access published a guest port: %v", run)
			}
		})
	}
}

func TestRunRejectsInvalidHostPortsBeforeBackend(t *testing.T) {
	for _, network := range []string{
		"policy = \"none\"\nhost_ports = [3000]",
		"policy = \"public\"\nhost_ports = [0]",
		"policy = \"public\"\nhost_ports = [65536]",
		"policy = \"public\"\nhost_ports = [3000, 3000]",
	} {
		t.Run(network, func(t *testing.T) {
			toml := strings.Replace(harness.DisposableTOML, `policy = "public"`, network, 1)
			code, _, stderr, fake := harness.RunFixture(t, toml, "--", "true")
			if code == 0 || !strings.Contains(stderr.String(), "host_ports") {
				t.Fatalf("exit = %d, stderr = %s; want host_ports validation failure", code, stderr)
			}
			if calls := fake.Calls(); len(calls) > 0 {
				t.Errorf("invalid host ports reached backend: %v", calls)
			}
		})
	}
}
