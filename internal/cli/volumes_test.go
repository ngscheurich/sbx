// Project volume compatibility tests: stable names across sibling
// worktrees, reuse of equal definitions, refusal of conflicting ones, the
// fail-closed inspection rules, and the volumes' lifecycles — all against
// the stateful fake msb with temporary linked worktrees.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/testsupport"
)

// projectVolumeTOML declares one plain Project directory volume.
const projectVolumeTOML = `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.cache]
target = "/cache"
scope = "project"

[network]
egress = "public"
`

// mixedVolumesTOML declares one Project volume and one Sandbox disk
// volume, for the lifecycle tests.
const mixedVolumesTOML = `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.shared_cache]
target = "/opt/shared-cache"
scope = "project"

[volumes.data]
target = "/var/lib/sbx-app"
scope = "sandbox"
kind = "disk"
size = "2G"

[network]
egress = "public"
`

// projectVolumeName derives the backend name a worktree's Project volume
// gets, the way the CLI does.
func projectVolumeName(t *testing.T, worktree, logical string) string {
	t.Helper()
	info, err := gitx.Discover(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	return identity.VolumeName(info.CommonDir, logical)
}

// namedMountArgs returns the --mount-named values of every run or create
// call the fake recorded.
func namedMountArgs(fake testsupport.Log) []string {
	var out []string
	for _, c := range fake.Calls() {
		if c.Args[0] != "run" && c.Args[0] != "create" {
			continue
		}
		for i, a := range c.Args {
			if a == "--mount-named" && i+1 < len(c.Args) {
				out = append(out, c.Args[i+1])
			}
		}
	}
	return out
}

// TestSiblingWorktreesShareProjectVolumes checks that two linked worktrees
// of one repository mount the same backend volume by name, and that the
// second worktree reuses the stored volume rather than making another.
func TestSiblingWorktreesShareProjectVolumes(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, repo := fixtureRepo(t, projectVolumeTOML)

	sibling := t.TempDir() + "/wt2"
	git(t, repo, "worktree", "add", sibling, "-b", "other")
	writeFile(t, sibling+"/sbx.toml", projectVolumeTOML)

	for _, wt := range []string{worktree, sibling} {
		code, _, stderr := sbxRun(t, wt, []string{"run", "--", "true"}, nil)
		if code != 0 {
			t.Fatalf("run in %s failed: %s", wt, stderr)
		}
	}

	mounts := namedMountArgs(fake)
	if len(mounts) != 2 {
		t.Fatalf("recorded --mount-named values = %v, want 2", mounts)
	}
	if mounts[0] != mounts[1] {
		t.Errorf("sibling worktrees mounted different volumes: %q vs %q", mounts[0], mounts[1])
	}
	want := projectVolumeName(t, worktree, "cache") + ":/cache"
	if mounts[0] != want {
		t.Errorf("mounted volume = %q, want %q", mounts[0], want)
	}
	if got := fake.VolumeNames(); len(got) != 1 || got[0] != projectVolumeName(t, worktree, "cache") {
		t.Errorf("backend volumes = %v, want exactly the one shared volume", got)
	}
}

// TestUnrelatedClonesDoNotShareProjectVolumes checks that the same logical
// volume name in two unrelated repositories derives two backend names.
func TestUnrelatedClonesDoNotShareProjectVolumes(t *testing.T) {
	testsupport.FakeMSB(t)
	wt1, _ := fixtureRepo(t, projectVolumeTOML)
	wt2, _ := fixtureRepo(t, projectVolumeTOML)

	for _, wt := range []string{wt1, wt2} {
		code, _, stderr := sbxRun(t, wt, []string{"run", "--", "true"}, nil)
		if code != 0 {
			t.Fatalf("run in %s failed: %s", wt, stderr)
		}
	}
	name1 := projectVolumeName(t, wt1, "cache")
	name2 := projectVolumeName(t, wt2, "cache")
	if name1 == name2 {
		t.Errorf("unrelated clones share the backend volume name %q", name1)
	}
}

// TestEqualDefinitionsReuseProjectVolume checks that a volume created by
// one worktree is reused by a later command whose declaration matches
// exactly — here a sized disk with its non-null capacity report.
func TestEqualDefinitionsReuseProjectVolume(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.data]
target = "/data"
scope = "project"
kind = "disk"
size = "8G"

[network]
egress = "public"
`)
	// Simulate the volume a sibling worktree's earlier run created, with
	// the non-null capacity_bytes a real disk volume reports.
	fake.SeedVolume(t, projectVolumeName(t, worktree, "data"), "disk", testsupport.Int64(8589934592), nil)

	code, _, stderr := sbxRun(t, worktree, []string{"run", "--", "true"}, nil)
	if code != 0 {
		t.Fatalf("run with an equal existing volume failed: %s", stderr)
	}
	if got := namedMountArgs(fake); len(got) != 1 || got[0] != projectVolumeName(t, worktree, "data")+":/data:kind=disk,size=8G" {
		t.Errorf("mounted volumes = %v, want the disk with its definition", got)
	}
	if got := fake.VolumeNames(); len(got) != 1 {
		t.Errorf("backend volumes = %v, want the single reused volume", got)
	}
}

// TestConflictingProjectVolumeFailsBeforeCreation checks that a declared
// definition that disagrees with the existing volume stops both run and up
// before any mutation, naming the volume, both definitions, and the way
// out.
func TestConflictingProjectVolumeFailsBeforeCreation(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, _ := fixtureRepo(t, `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.data]
target = "/data"
scope = "project"
kind = "disk"
size = "8G"

[network]
egress = "public"
`)
	fake.SeedVolume(t, projectVolumeName(t, worktree, "data"), "disk", testsupport.Int64(4294967296), nil)

	for _, args := range [][]string{{"run", "--", "true"}, {"up"}} {
		before := len(fake.Calls())
		code, _, stderr := sbxRun(t, worktree, args, nil)
		if code == 0 {
			t.Fatalf("%v succeeded against a conflicting volume", args)
		}
		for _, want := range []string{
			`"data"`,
			"declared in sbx.toml: disk volume, size \"8G\"",
			"capacity 4294967296 bytes",
			"another logical name",
			"nothing was created or changed",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%v stderr is missing %q:\n%s", args, want, stderr)
			}
		}
		for _, c := range fake.Calls()[before:] {
			switch c.Args[0] {
			case "create", "run", "image", "start":
				t.Errorf("%v mutated the backend despite the conflict: %q", args, c.Args)
			}
		}
	}
}

// TestBranchesDisagreeingOnProjectVolume checks the ADR-0003 case: two
// worktrees on different branches declare the same Project volume
// differently; the branch that created it wins the name, and the other
// fails before creation.
func TestBranchesDisagreeingOnProjectVolume(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	const withSize = `
image = "alpine:3.20"
cpus = 1
memory = "1G"

[volumes.data]
target = "/data"
scope = "project"
kind = "disk"
size = "%s"

[network]
egress = "public"
`
	worktree, repo := fixtureRepo(t, fmt.Sprintf(withSize, "8G"))
	if code, _, stderr := sbxRun(t, worktree, []string{"run", "--", "true"}, nil); code != 0 {
		t.Fatalf("first branch's run failed: %s", stderr)
	}
	created := fake.VolumeNames()
	if len(created) != 1 {
		t.Fatalf("first run created volumes %v, want exactly one", created)
	}

	// A second linked worktree on another branch declares the same logical
	// volume with a different size: the branches disagree.
	sibling := t.TempDir() + "/wt2"
	git(t, repo, "worktree", "add", sibling, "-b", "smaller")
	writeFile(t, sibling+"/sbx.toml", fmt.Sprintf(withSize, "4G"))

	before := len(fake.Calls())
	code, _, stderr := sbxRun(t, sibling, []string{"run", "--", "true"}, nil)
	if code == 0 {
		t.Fatal("the disagreeing branch's run succeeded")
	}
	for _, want := range []string{`"data"`, `"4G"`, "8589934592 bytes", "another logical name"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	for _, c := range fake.Calls()[before:] {
		if c.Args[0] == "run" || c.Args[0] == "create" {
			t.Errorf("creation was attempted despite the conflict: %q", c.Args)
		}
	}
	if got := fake.VolumeNames(); len(got) != 1 || got[0] != created[0] {
		t.Errorf("backend volumes changed to %v; the existing volume must stay untouched", got)
	}
}

// TestVolumeInspectionFailureFailsClosed checks that a failed or malformed
// volume listing stops a mutating command before anything changes: it is
// never read as an empty backend or a compatible definition.
func TestVolumeInspectionFailureFailsClosed(t *testing.T) {
	for _, env := range []struct{ key, val string }{
		{"FAKE_MSB_VOLUMES_FAIL", "1"},
		{"FAKE_MSB_VOLUMES_MALFORMED", "1"},
	} {
		t.Run(env.key, func(t *testing.T) {
			fake := testsupport.FakeMSB(t)
			t.Setenv(env.key, env.val)
			worktree, _ := fixtureRepo(t, projectVolumeTOML)

			code, _, stderr := sbxRun(t, worktree, []string{"run", "--", "true"}, nil)
			if code == 0 {
				t.Fatal("run proceeded without a readable volume listing")
			}
			if !strings.Contains(stderr, "volumes") {
				t.Errorf("stderr does not name the volume inspection: %s", stderr)
			}
			for _, c := range fake.Calls() {
				if c.Args[0] != "context" && c.Args[0] != "volumes" {
					t.Errorf("mutating or image call reached the backend: %q", c.Args)
				}
			}
		})
	}
}

// TestProjectVolumesSurviveRemovalAndDisposableRuns checks the lifecycle
// split: Sandbox volumes die with their sandbox, while Project volumes
// remain after `sbx rm` and across disposable runs.
func TestProjectVolumesSurviveRemovalAndDisposableRuns(t *testing.T) {
	worktree, fake := persistentFixture(t, mixedVolumesTOML)
	projectName := projectVolumeName(t, worktree, "shared_cache")

	if code, _, stderr := sbxUp(t, worktree); code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if !fake.VolumeExists(t, projectName) {
		t.Fatalf("creation did not make the project volume %q; volumes: %v", projectName, fake.VolumeNames())
	}
	if owned := ownedVolumeNames(fake); len(owned) != 1 {
		t.Fatalf("creation made owned volumes %v, want exactly one", owned)
	}

	// A disposable run alongside the persistent sandbox gets fresh owned
	// volumes, removed with it; the Project volume persists.
	if code, _, stderr := sbxRun(t, worktree, []string{"run", "--", "true"}, nil); code != 0 {
		t.Fatalf("disposable run failed: %s", stderr)
	}
	if got := ownedVolumeNames(fake); len(got) != 1 {
		t.Errorf("after the disposable run, owned volumes = %v, want only the persistent sandbox's", got)
	}
	if !fake.VolumeExists(t, projectName) {
		t.Errorf("the disposable run lost the project volume; volumes: %v", fake.VolumeNames())
	}

	// Removing the persistent sandbox removes its Sandbox volumes and
	// keeps the Project volume.
	if code, _, stderr := sbxRun(t, worktree, []string{"rm", "--yes"}, nil); code != 0 {
		t.Fatalf("rm failed: %s", stderr)
	}
	if got := ownedVolumeNames(fake); len(got) != 0 {
		t.Errorf("after rm, owned volumes = %v, want none", got)
	}
	if got := fake.VolumeNames(); len(got) != 1 || got[0] != projectName {
		t.Errorf("after rm, volumes = %v, want only the kept project volume %q", got, projectName)
	}
}

// ownedVolumeNames lists the fake backend's owned (Sandbox) volumes.
func ownedVolumeNames(fake testsupport.Log) []string {
	var out []string
	for _, name := range fake.VolumeNames() {
		if strings.HasPrefix(name, "owned-") {
			out = append(out, name)
		}
	}
	return out
}

// TestPlanReportsVolumeConflictsWithoutChangingAnything checks that `sbx
// plan` surfaces a Project volume conflict in its report while staying
// read-only.
func TestPlanReportsVolumeConflictsWithoutChangingAnything(t *testing.T) {
	fake := testsupport.FakeMSB(t)
	worktree, repo := fixtureRepo(t, projectVolumeTOML)
	backendName := projectVolumeName(t, worktree, "cache")
	fake.SeedVolume(t, backendName, "disk", testsupport.Int64(8589934592), nil)

	beforeWorktree := dirSnapshot(t, worktree)
	beforeRepo := dirSnapshot(t, repo)
	beforeCalls := len(fake.Calls())

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	for _, want := range []string{
		"project volume conflicts",
		`"cache"`,
		backendName,
		"declared in sbx.toml: directory volume",
		"existing volume:      disk volume, capacity 8589934592 bytes",
		"another logical name",
		"sbx changed nothing",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan output is missing %q:\n%s", want, stdout)
		}
	}
	for _, c := range fake.Calls()[beforeCalls:] {
		if c.Args[0] != "volumes" {
			t.Errorf("plan called %q; only the read-only listing is allowed", c.Args)
		}
	}
	if after := dirSnapshot(t, worktree); after != beforeWorktree {
		t.Errorf("plan changed the worktree")
	}
	if after := dirSnapshot(t, repo); after != beforeRepo {
		t.Errorf("plan changed the repository")
	}
}

// TestPlanReportsUninspectableVolumes checks that plan renders a failed
// listing as unknown rather than as compatibility.
func TestPlanReportsUninspectableVolumes(t *testing.T) {
	testsupport.FakeMSB(t)
	t.Setenv("FAKE_MSB_VOLUMES_FAIL", "1")
	worktree, _ := fixtureRepo(t, projectVolumeTOML)

	code, stdout, stderr := sbxRun(t, worktree, []string{"plan"}, nil)
	if code != 0 {
		t.Fatalf("plan failed: %s", stderr)
	}
	if !strings.Contains(stdout, "compatibility could not be checked") {
		t.Errorf("plan does not report the failed inspection:\n%s", stdout)
	}
	if strings.Contains(stdout, "reuses the existing volume") || strings.Contains(stdout, "will be created") {
		t.Errorf("plan claimed a compatibility outcome despite the failed inspection:\n%s", stdout)
	}
}

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
