package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeDockerSh is a POSIX shell script that stands in for the docker CLI. It
// records every invocation in the same call.<n> layout as the fake msb —
// the observed MSB_BACKEND value, then one NUL-separated argument — and then
// behaves according to FAKE_DOCKER_* variables:
//
//	FAKE_DOCKER_BUILD_FAIL    set to make "build" fail
//	FAKE_DOCKER_BUILD_SLEEP   seconds "build" sleeps before answering (interruptible via SIGTERM)
//	FAKE_DOCKER_SAVE_FAIL     set to make "save" fail
//
// "save" writes a placeholder archive at its --output path and touches a
// "saved" marker in the log directory, which a test can hand to the fake
// msb as FAKE_MSB_LOAD_REQUIRES to pin that the import follows the export.
//
// The common paths use only shell builtins (no mkdir/mv/sleep), so the fake
// still runs in tests that scrub PATH to simulate missing tools.
const fakeDockerSh = `#!/bin/sh
dir="${FAKE_DOCKER_LOG:?}"
n=0
while [ -f "$dir/call.$n" ]; do n=$((n+1)); done
{
  printf 'MSB_BACKEND=%s\0' "${MSB_BACKEND:-}"
  for a in "$@"; do printf '%s\0' "$a"; done
} > "$dir/call.$n"
case "$1" in
build)
  if [ -n "$FAKE_DOCKER_BUILD_SLEEP" ]; then
    trap 'exit 143' TERM
    sleep "$FAKE_DOCKER_BUILD_SLEEP" &
    wait "$!"
  fi
  if [ -n "$FAKE_DOCKER_BUILD_FAIL" ]; then
    echo "sbx-fake-docker: build failed" >&2
    exit 1
  fi
  printf 'fake docker build output\n'
  ;;
save)
  if [ -n "$FAKE_DOCKER_SAVE_FAIL" ]; then
    echo "sbx-fake-docker: save failed" >&2
    exit 1
  fi
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--output" ]; then
      printf 'fake image archive\n' > "$a"
    fi
    prev="$a"
  done
  : > "$dir/saved"
  ;;
esac
exit 0
`

// DockerLog is the recorded behavior of a fake docker executable.
type DockerLog struct {
	callLog
}

// SavedMarker is the path the fake's "save" touches, for wiring into the
// fake msb's FAKE_MSB_LOAD_REQUIRES.
func (l DockerLog) SavedMarker() string {
	return filepath.Join(l.dir, "saved")
}

// FakeDocker installs the fake docker executable at the front of PATH for
// the duration of the test and returns a log the test can query.
func FakeDocker(t *testing.T) DockerLog {
	t.Helper()
	dir := t.TempDir()
	log := WriteFakeDocker(t, dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// WriteFakeDocker installs the fake docker executable into an existing
// directory without touching PATH, for tests that scrub PATH themselves
// (the fake's common paths use shell builtins only, so it still runs with
// almost nothing on PATH).
func WriteFakeDocker(t *testing.T, dir string) DockerLog {
	t.Helper()
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte(fakeDockerSh), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	if err := os.MkdirAll(log, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DOCKER_LOG", log)
	return DockerLog{callLog{dir: log}}
}
