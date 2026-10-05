package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/translate"
)

// fakeTranslate builds a translation the way translate.Sandbox would, with
// the generated-file paths set the way a creation would set them; the
// snapshot must ignore those paths because they change on every creation.
func fakeTranslation(worktree, memory string) translate.Translation {
	return translate.Translation{
		Workspace: "/workspace",
		Options: msb.CreateOptions{
			Name:   "app-main-12345678",
			Image:  "alpine:3.20",
			CPUs:   2,
			Memory: memory,
			Mounts: []msb.Mount{{Source: worktree, Target: "/workspace"}},
			Labels: []msb.Label{
				{Key: "sbx.managed", Value: "1"},
				{Key: "sbx.mode", Value: "persistent"},
				{Key: "sbx.worktree", Value: worktree},
			},
			SecretConf: "/tmp/sbx-secrets-1.yaml",
			FsConf:     "/tmp/sbx-fs-conf-1.yaml",
		},
		SecretConfYAML: "TEST_TOKEN:\n  value: \"${SBX_TOKEN}\"\n  allow: [example.com]\n",
		FsConfYAML:     "mounts:\n  - tmpfs: { size: \"512M\" }\n    target: \"/tmp\"\n",
	}
}

// TestSnapshotRoundTrip checks that a snapshot survives saving and loading,
// and that a missing snapshot is reported as ErrNoSnapshot rather than an
// empty one.
func TestSnapshotRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tr := fakeTranslation("/repo/wt", "2G")

	snap := Snapshot{ImageDigest: "sha256:fake", Definition: DefinitionOf(tr)}
	if err := SaveSnapshot("app-main-12345678", snap); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The generated-file paths are per-creation temporaries; they must not
	// be captured, or a later identical creation would count as drift.
	if snap.Definition.Options.SecretConf != "" {
		t.Errorf("snapshot captured the secret map's temporary path: %q", snap.Definition.Options.SecretConf)
	}

	loaded, err := LoadSnapshot("app-main-12345678")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(loaded, snap) {
		t.Errorf("round trip changed the snapshot:\n got %+v\nwant %+v", loaded, snap)
	}

	if err := DeleteSnapshot("app-main-12345678"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := LoadSnapshot("app-main-12345678"); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("load after delete = %v, want ErrNoSnapshot", err)
	}
}

// TestDirHonorsXdgStateHome pins the host state directory layout.
func TestDirHonorsXdgStateHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "sbx"); dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}

	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err = Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "sbx"); dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
}

// TestDriftReportsDifferences checks the drift report: identical
// definitions produce none, and each changed aspect is named with both
// values.
func TestDriftReportsDifferences(t *testing.T) {
	created := Snapshot{ImageDigest: "sha256:fake", Definition: DefinitionOf(fakeTranslation("/repo/wt", "2G"))}
	current := DefinitionOf(fakeTranslation("/repo/wt", "2G"))

	if drift := Drift(created, current, "sha256:fake"); len(drift) != 0 {
		t.Errorf("identical definitions drifted: %v", drift)
	}

	changed := DefinitionOf(fakeTranslation("/repo/wt", "4G"))
	drift := Drift(created, changed, "sha256:fake")
	if len(drift) != 1 || !strings.Contains(drift[0], "memory") ||
		!strings.Contains(drift[0], "2G") || !strings.Contains(drift[0], "4G") {
		t.Errorf("memory drift not reported with both values: %v", drift)
	}

	// The image tag resolving to different contents is drift even though
	// nothing in sbx.toml changed.
	drift = Drift(created, current, "sha256:rebuilt")
	if len(drift) != 1 || !strings.Contains(drift[0], "sha256:fake") || !strings.Contains(drift[0], "sha256:rebuilt") {
		t.Errorf("digest drift not reported: %v", drift)
	}

	// An unresolvable image cannot be confirmed and fails closed.
	drift = Drift(created, current, "")
	if len(drift) != 1 || !strings.Contains(drift[0], "could not") {
		t.Errorf("unresolvable image not reported: %v", drift)
	}

	// Labels carry the Sandbox identity (name, mode, worktree), not
	// project configuration, so a definition whose labels differ only
	// through construction never drifts; a moved worktree changes the
	// identity itself instead.
	relabeled := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	relabeled.Options.Labels = []msb.Label{{Key: "sbx.worktree", Value: "/elsewhere"}}
	if drift := Drift(created, relabeled, "sha256:fake"); len(drift) != 0 {
		t.Errorf("labels drifted without a configuration change: %v", drift)
	}

	// A changed secret map drifts without printing any guest values.
	other := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	other.SecretConfYAML = strings.Replace(other.SecretConfYAML, "example.com", "other.example.com", 1)
	drift = Drift(created, other, "sha256:fake")
	if len(drift) != 1 || !strings.Contains(drift[0], "secret") {
		t.Errorf("secret map drift not reported: %v", drift)
	}
}

// TestDriftReportsEveryAspect checks that mounts, volumes, environment,
// and network policy changes each surface.
func TestDriftReportsEveryAspect(t *testing.T) {
	base := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	created := Snapshot{ImageDigest: "sha256:fake", Definition: base}

	mounts := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	mounts.Options.Mounts = append(mounts.Options.Mounts, msb.Mount{Source: "/repo/notes", Target: "/mnt/notes", ReadOnly: true})
	if drift := Drift(created, mounts, "sha256:fake"); len(drift) != 1 || !strings.Contains(drift[0], "mounts") {
		t.Errorf("mount drift not reported: %v", drift)
	}

	owned := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	owned.Options.Owned = []msb.OwnedMount{{Target: "/scratch", Kind: "dir"}}
	if drift := Drift(created, owned, "sha256:fake"); len(drift) != 1 || !strings.Contains(drift[0], "volumes") {
		t.Errorf("sandbox-volume drift not reported: %v", drift)
	}

	env := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	env.Options.Env = []string{"MODE=test"}
	if drift := Drift(created, env, "sha256:fake"); len(drift) != 1 || !strings.Contains(drift[0], "environment") {
		t.Errorf("environment drift not reported: %v", drift)
	}

	net := DefinitionOf(fakeTranslation("/repo/wt", "2G"))
	net.Options.NetRules = []string{"allow@example.com"}
	net.Options.TLSIntercept = true
	if drift := Drift(created, net, "sha256:fake"); len(drift) != 1 || !strings.Contains(drift[0], "network") {
		t.Errorf("network drift not reported: %v", drift)
	}

	// Multiple changes surface as multiple lines, in a stable order.
	all := DefinitionOf(fakeTranslation("/repo/wt", "4G"))
	all.Options.Env = []string{"MODE=test"}
	all.Options.Owned = []msb.OwnedMount{{Target: "/scratch", Kind: "dir"}}
	if drift := Drift(created, all, "sha256:fake"); len(drift) != 3 {
		t.Errorf("want 3 drift lines, got %v", drift)
	}
}

// TestSaveSnapshotIsAtomicOnDisk checks that saving writes a complete file
// (via rename), so a crash never leaves a half-written snapshot that reads
// as drift-inducing garbage.
func TestSaveSnapshotIsAtomicOnDisk(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	snap := Snapshot{ImageDigest: "sha256:fake", Definition: DefinitionOf(fakeTranslation("/repo/wt", "2G"))}
	if err := SaveSnapshot("app-main-12345678", snap); err != nil {
		t.Fatal(err)
	}
	dir, _ := Dir()
	entries, err := os.ReadDir(filepath.Join(dir, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "app-main-12345678.json" {
		t.Errorf("snapshots directory holds %+v", entries)
	}
	// Overwriting an existing snapshot is the recreation path; it must
	// replace, not accumulate.
	if err := SaveSnapshot("app-main-12345678", snap); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(filepath.Join(dir, "snapshots"))
	if len(entries) != 1 {
		t.Errorf("saving twice accumulated %d files", len(entries))
	}
}
