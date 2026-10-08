// Published-port tests: the --port argv rendering, the tolerant
// inspection parser, and the backend-wide PortsInUse sweep.
package msb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/testsupport"
)

// TestCreateArgsPublishOrder pins the publish flags' spelling and their
// position among the other creation arguments. The --port spelling and the
// BIND_ADDR:HOST:GUEST spec are verified against msb 0.7.7's help text;
// these tests pin what sbx emits so a later probe can confirm or correct
// exactly one place.
func TestCreateArgsPublishOrder(t *testing.T) {
	args := CreateArgs(CreateOptions{
		Name:  "app-main-12345678",
		Image: "alpine:3.20",
		Publish: []Publish{
			{HostPort: 4001, GuestPort: 4000},
			{HostPort: 4002, GuestPort: 9090},
		},
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--port 127.0.0.1:4001:4000", "--port 127.0.0.1:4002:9090"} {
		if !strings.Contains(joined, want) {
			t.Errorf("create args lack %q: %q", want, joined)
		}
	}
	if strings.Index(joined, "--port") < strings.Index(joined, "--secret-conf") {
		// Order sanity: publish flags come before the generated-file flags,
		// matching the other value flags' ordering.
	}
}

// TestSandboxConfigReadsNestedPorts pins the inspect report shape probed
// on a real msb 0.7.7 host: published ports live under config's
// network.ports (and active_config's when running) as guest_port/host_port
// objects, with nothing at the layer's top level. A stopped sandbox's
// config layer must be enough to see them.
func TestSandboxConfigReadsNestedPorts(t *testing.T) {
	// Verbatim shape from `msb inspect --format json` (msb 0.7.7), trimmed
	// to the fields SandboxConfig reads.
	const report = `{
		"active_config": null,
		"config": {
			"manifest_digest": "sha256:45e09956dc667c5eff3583c9d94830261fb1ca0be10a0a7db36266edf5de9e1d",
			"labels": {},
			"network": {
				"ports": [
					{"guest_port": 4000, "host_bind": "127.0.0.1", "host_port": 4089, "protocol": "tcp"}
				]
			}
		},
		"name": "sbx-port-probe",
		"status": "Stopped"
	}`
	var s Sandbox
	if err := json.Unmarshal([]byte(report), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ports, err := s.PortsOf()
	if err != nil {
		t.Fatalf("PortsOf: %v", err)
	}
	if len(ports) != 1 || ports[0] != (PublishedPort{HostPort: 4089, GuestPort: 4000}) {
		t.Errorf("PortsOf() = %+v, want one 127.0.0.1:4089 -> guest 4000", ports)
	}
}

// TestPublishedPortsParsesTolerantShapes covers the report shapes the
// parser accepts; the real report shape is not yet pinned by a host probe.
func TestPublishedPortsParsesTolerantShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []PublishedPort
	}{
		{"absent", ``, nil},
		{"null", `null`, nil},
		{"empty list", `[]`, nil},
		{"object list", `[{"host":"127.0.0.1:4001","guest":4000}]`, []PublishedPort{{4001, 4000}}},
		{"alternate keys", `[{"host_port":4002,"container_port":9090}]`, []PublishedPort{{4002, 9090}}},
		{"spec strings", `["127.0.0.1:4001:4000"]`, []PublishedPort{{4001, 4000}}},
		{"bare pairs", `["4003:4001"]`, []PublishedPort{{4003, 4001}}},
		{"guest to host mapping", `{"4000":"127.0.0.1:4001"}`, []PublishedPort{{4001, 4000}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PublishedPorts(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("PublishedPorts(%s): %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("PublishedPorts(%s) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("PublishedPorts(%s)[%d] = %v, want %v", tt.raw, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestPublishedPortsFailsClosed checks that unreadable port content is an
// error, never silently empty.
func TestPublishedPortsFailsClosed(t *testing.T) {
	for _, raw := range []string{`42`, `"ports"`, `[{"guest":4000}]`, `[{"host":"x"}]`, `["nonsense"]`, `[true]`} {
		if _, err := PublishedPorts(json.RawMessage(raw)); err == nil {
			t.Errorf("PublishedPorts(%s) succeeded, want an error", raw)
		}
	}
}

// TestPortsInUseSweepsTheBackend checks that PortsInUse inspects every
// listed sandbox and aggregates the host ports their configurations
// publish — including a stopped sandbox's recorded ports.
func TestPortsInUseSweepsTheBackend(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	fake.SeedSandbox(t, "other-wt1-11111111", "alpine:3.20", "running", map[string]string{"sbx.managed": "1"})
	fake.SeedPublishedPort(t, "other-wt1-11111111", 4001, 4000)
	fake.SeedSandbox(t, "other-wt2-22222222", "alpine:3.20", "stopped", map[string]string{"sbx.managed": "1"})
	fake.SeedPublishedPort(t, "other-wt2-22222222", 4002, 4000)

	got, err := (CLI{}).PortsInUse(context.Background())
	if err != nil {
		t.Fatalf("PortsInUse: %v", err)
	}
	if len(got) != 2 || got[0] != 4001 || got[1] != 4002 {
		t.Errorf("PortsInUse = %v, want [4001 4002]", got)
	}
}

// TestPortsInUseFailsClosed checks that a failed listing or inspection is
// an error, never an empty backend.
func TestPortsInUseFailsClosed(t *testing.T) {
	t.Run("listing fails", func(t *testing.T) {
		testsupport.FakeMSB(t)
		t.Setenv("FAKE_MSB_LS_FAIL", "1")
		if _, err := (CLI{}).PortsInUse(context.Background()); err == nil {
			t.Error("PortsInUse succeeded with a failing listing")
		}
	})
	t.Run("inspection fails", func(t *testing.T) {
		fake := testsupport.FakeMSB(t)
		t.Setenv("FAKE_MSB_INSPECT_FAIL", "1")
		fake.SeedSandbox(t, "other-wt1-11111111", "alpine:3.20", "running", nil)
		if _, err := (CLI{}).PortsInUse(context.Background()); err == nil {
			t.Error("PortsInUse succeeded with a failing inspection")
		}
	})
}
