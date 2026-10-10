// Version-report tests: the rendering of stamped build information into the
// `sbx version` line, covering the shapes a real binary can carry.
package cli

import (
	"runtime/debug"
	"testing"
)

func TestVersionString(t *testing.T) {
	tests := []struct {
		name string
		bi   *debug.BuildInfo
		want string
	}{
		{
			// -buildvcs=false, a source tarball, or the zero value: nothing
			// to name, so the fallback line stands.
			name: "unstamped",
			bi:   &debug.BuildInfo{},
			want: "sbx (development build)",
		},
		{
			name: "checkout build",
			bi: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
				{Key: "vcs.modified", Value: "false"},
			}},
			want: "sbx devel (commit 0123456789ab)",
		},
		{
			name: "dirty checkout build",
			bi: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
				{Key: "vcs.modified", Value: "true"},
			}},
			want: "sbx devel (commit 0123456789ab, modified)",
		},
		{
			name: "untagged commit's pseudo-version folds into devel",
			bi: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.0.0-20261008012251-a34f406244b2+dirty"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "a34f406244b2ffffa34f406244b2ffffa34f4062"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: "sbx devel (commit a34f406244b2, modified)",
		},
		{
			name: "installed from a release",
			bi: &debug.BuildInfo{
				Main: debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "fedcba9876543210fedcba9876543210fedcba98"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: "sbx v1.2.3 (commit fedcba987654)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionData(tt.bi).String(); got != tt.want {
				t.Errorf("version report = %q, want %q", got, tt.want)
			}
		})
	}
}
