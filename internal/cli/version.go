// The version report: what a built binary can say about the source it was
// built from. Go stamps the VCS state into every build on its own, so a
// plain `go build` or `go install` is enough — no linker flags, no release
// tooling. The report keeps the fallback line's old spelling: nothing else
// in sbx depends on it, but it is the shape users saw first.
package cli

import (
	"fmt"
	"runtime/debug"
	"strings"
)

type versionReport struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Modified bool   `json:"modified"`
}

func versionData(bi *debug.BuildInfo) versionReport {
	report := versionReport{Version: "devel"}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			report.Commit = s.Value
		case "vcs.modified":
			report.Modified = s.Value == "true"
		}
	}
	version := bi.Main.Version
	// A pseudo-version is Go's name for an untagged commit; it restates the
	// revision and time the commit field already carries, so a checkout
	// build reports plain "devel" and a tagged release keeps its tag.
	if version == "" || version == "(devel)" || strings.HasPrefix(version, "v0.0.0-") {
		version = "devel"
	}
	report.Version = version
	return report
}

// String shortens the commit for display, falling back when none was stamped.
func (r versionReport) String() string {
	if r.Commit == "" {
		return "sbx (development build)"
	}
	suffix := ""
	if r.Modified {
		suffix = ", modified"
	}
	return fmt.Sprintf("sbx %s (commit %.12s%s)", r.Version, r.Commit, suffix)
}

func currentVersion() versionReport {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return versionData(&debug.BuildInfo{})
	}
	return versionData(bi)
}
