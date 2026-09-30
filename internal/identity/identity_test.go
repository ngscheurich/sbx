package identity

import "testing"

func TestProjectBasename(t *testing.T) {
	tests := []struct {
		commonDir string
		want      string
	}{
		{"/src/app/.git", "app"},
		{"/src/app.git", "app"},
		{"/worktrees/repo.dot.git", "repo.dot"},
		{"/src/app/.git/", "app"},
	}
	for _, tt := range tests {
		if got := ProjectBasename(tt.commonDir); got != tt.want {
			t.Errorf("ProjectBasename(%q) = %q, want %q", tt.commonDir, got, tt.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"My App", "my-app"},
		{"repo.name", "repo-name"},
		{"Go_Project", "go-project"},
		{"UPPER", "upper"},
		{"a", "a"},
		{"", ""},
		{"spaces   here", "spaces---here"},
		{"/lead/slash", "-lead-slash"},
	}
	for _, tt := range tests {
		if got := Sanitize(tt.in); got != tt.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeCutsTo32(t *testing.T) {
	long := "0123456789abcdef0123456789abcdef-more"
	got := Sanitize(long)
	if got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("Sanitize(%q) = %q, want 32 characters", long, got)
	}
}

func TestSanitizeKeepsDigitsAndHyphens(t *testing.T) {
	if got := Sanitize("go-mod-2"); got != "go-mod-2" {
		t.Errorf("Sanitize = %q", got)
	}
}

func TestHashPathIsStableAndEightChars(t *testing.T) {
	a := HashPath("/tmp/x/repo/wt1")
	b := HashPath("/tmp/x/repo/wt1")
	c := HashPath("/tmp/x/repo/wt2")
	if a != b {
		t.Error("HashPath not stable for the same path")
	}
	if a == c {
		t.Error("HashPath identical for different paths")
	}
	if len(a) != 8 {
		t.Errorf("HashPath length = %d, want 8", len(a))
	}
	for _, r := range a {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("HashPath contains non-hex character %q", r)
		}
	}
}

func TestDerive(t *testing.T) {
	got := Derive("/src/app/.git", "/src/app/wt1")
	if got.Project != "app" {
		t.Errorf("Project = %q, want app", got.Project)
	}
	if got.Worktree != "wt1" {
		t.Errorf("Worktree = %q, want wt1", got.Worktree)
	}
	if got.Sandbox != "app-wt1-"+got.Hash {
		t.Errorf("Sandbox = %q, want app-wt1-%s", got.Sandbox, got.Hash)
	}
	if len(got.Hash) != 8 {
		t.Errorf("Hash length = %d, want 8", len(got.Hash))
	}
}

func TestDeriveSanitizesComponents(t *testing.T) {
	got := Derive("/src/My App/.git", "/src/My App/Feature_X")
	if got.Project != "my-app" {
		t.Errorf("Project = %q, want my-app", got.Project)
	}
	if got.Worktree != "feature-x" {
		t.Errorf("Worktree = %q, want feature-x", got.Worktree)
	}
}

func TestVolumeNamespace(t *testing.T) {
	ns := VolumeNamespace("/src/app/.git")
	if len(ns) != 8 {
		t.Errorf("VolumeNamespace length = %d, want 8", len(ns))
	}
	if ns != VolumeNamespace("/src/app/.git") {
		t.Error("VolumeNamespace not stable")
	}
	if ns == VolumeNamespace("/src/other/.git") {
		t.Error("VolumeNamespace equal for different common dirs")
	}
}

func TestVolumeName(t *testing.T) {
	got := VolumeName("/src/app/.git", "go_mod")
	if got != VolumeNamespace("/src/app/.git")+"-go_mod" {
		t.Errorf("VolumeName = %q", got)
	}
	// Sibling worktrees of one project share volume names.
	if VolumeName("/src/app/.git", "cache") != VolumeName("/src/app/.git", "cache") {
		t.Error("VolumeName differs for the same project")
	}
}
