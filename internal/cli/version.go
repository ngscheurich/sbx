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

// versionString renders the `sbx version` line from build information: the
// module version (a release tag for `go install …@version`, "devel" for a
// checkout build) plus the stamped VCS revision, shortened the way Git
// itself prints one, and a marker when the working tree was dirty. An
// unstamped build — -buildvcs=false, a source tarball, a zero value — has
// nothing to name, so it falls back to the development-build line.
func versionString(bi *debug.BuildInfo) string {
	var revision string
	modified := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "sbx (development build)"
	}
	version := bi.Main.Version
	// A pseudo-version is Go's name for an untagged commit; it restates the
	// revision and time the commit field already carries, so a checkout
	// build reports plain "devel" and a tagged release keeps its tag.
	if version == "" || version == "(devel)" || strings.HasPrefix(version, "v0.0.0-") {
		version = "devel"
	}
	suffix := ""
	if modified {
		suffix = ", modified"
	}
	return fmt.Sprintf("sbx %s (commit %.12s%s)", version, revision, suffix)
}

// currentVersion reads the running binary's build information. A failure
// leaves the report empty, which versionString renders as the fallback.
func currentVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return versionString(&debug.BuildInfo{})
	}
	return versionString(bi)
}
