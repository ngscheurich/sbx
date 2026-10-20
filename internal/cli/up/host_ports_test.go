package up

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/harness"
)

func TestUpHostPortsAndDrift(t *testing.T) {
	toml := harness.PersistentTOML + "\nhost_ports = [3000, 8080]\n"
	worktree, fake := harness.PersistentFixture(t, toml)
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	calls := fake.Calls()
	create := calls[len(calls)-1].Args
	for _, rule := range []string{"allow@public", "allow@dns", "allow@host:tcp:3000", "allow@host:tcp:8080"} {
		if !slices.Contains(create, rule) {
			t.Errorf("create argv missing %q: %v", rule, create)
		}
	}
	snapshot, err := os.ReadFile(snapshotPathOf(t, worktree))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), "allow@host:tcp:3000") {
		t.Fatalf("snapshot omitted host access: %s", snapshot)
	}

	for _, ports := range []string{"[8080, 3000]", "[3000]", "[3000, 8081]", "[]"} {
		t.Run(ports, func(t *testing.T) {
			changed := strings.Replace(toml, "[3000, 8080]", ports, 1)
			if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
			before := len(fake.Calls())
			for _, args := range [][]string{{"up"}, {"exec", "--", "true"}} {
				code, _, stderr := harness.SbxRun(t, worktree, args, nil)
				if ports == "[8080, 3000]" {
					if code != 0 {
						t.Fatalf("reordered host ports caused drift: %s", stderr)
					}
					continue
				}
				if code == 0 || !strings.Contains(stderr, "network policy") || !strings.Contains(stderr, "drifted") {
					t.Errorf("%v: exit %d, stderr %s; want network drift refusal", args, code, stderr)
				}
			}
			for _, call := range fake.Calls()[before:] {
				switch call.Args[0] {
				case "create", "start", "stop", "remove", "modify":
					t.Errorf("host-port change mutated the sandbox: %v", call.Args)
				}
			}
		})
	}
}

func TestUpRefusesNewHostAccessOnExistingSandbox(t *testing.T) {
	worktree, fake := harness.PersistentFixture(t, harness.PersistentTOML)
	if code, _, stderr := harness.SbxUp(t, worktree); code != 0 {
		t.Fatalf("first up failed: %s", stderr)
	}
	changed := harness.PersistentTOML + "\nhost_ports = [3000]\n"
	if err := os.WriteFile(filepath.Join(worktree, "sbx.toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(fake.Calls())
	code, _, stderr := harness.SbxUp(t, worktree)
	if code == 0 || !strings.Contains(stderr, "network policy") || !strings.Contains(stderr, "allow@host:tcp:3000") {
		t.Fatalf("exit = %d, stderr = %s; want host-access drift refusal", code, stderr)
	}
	for _, call := range fake.Calls()[before:] {
		switch call.Args[0] {
		case "create", "start", "stop", "remove", "modify":
			t.Errorf("adding host access mutated the sandbox: %v", call.Args)
		}
	}
}
