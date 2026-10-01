// Package testsupport provides test doubles shared across sbx's packages:
// a fake msb executable that records its invocations and simulates backend
// behavior, so tests never reach a real microsandbox installation.
package testsupport

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeMsbSh is a POSIX shell script that stands in for the msb CLI. It
// appends every invocation to $FAKE_MSB_LOG/call.<n> — the first line is the
// observed MSB_BACKEND value, then one argument per line — and then behaves
// according to FAKE_MSB_* variables:
//
//	FAKE_MSB_CONTEXT_BACKEND  backend reported by "msb context" (default local)
//	FAKE_MSB_IMAGE_MISSING    comma-separated images that "image inspect" fails for
//	FAKE_MSB_PULL_FAIL        set to make "image pull" fail
//	FAKE_MSB_CREATE_FAIL      substring that makes "create" fail when the name matches
//	FAKE_MSB_EXEC_SLEEP       seconds "exec" sleeps before answering (interruptible via SIGTERM)
//	FAKE_MSB_EXEC_FAIL_START  set to make "exec" exit 127 without running the guest
//	FAKE_MSB_EXIT             exit status of "exec" (default 0)
//	FAKE_MSB_RM_FAIL          set to make "remove" fail
const fakeMsbSh = `#!/bin/sh
dir="${FAKE_MSB_LOG:?}"
n=0
while [ -f "$dir/call.$n" ]; do n=$((n+1)); done
tmp="$dir/tmp.$$"
{
  echo "MSB_BACKEND=$MSB_BACKEND"
  for a in "$@"; do printf '%s\n' "$a"; done
} > "$tmp"
mv "$tmp" "$dir/call.$n"

case "$1" in
context)
  printf '{"backend":"%s"}\n' "${FAKE_MSB_CONTEXT_BACKEND:-local}"
  ;;
image)
  case "$2" in
  inspect)
    case ",$FAKE_MSB_IMAGE_MISSING," in
    *,$3,*) exit 1 ;;
    esac
    printf '{"digest":"sha256:fake"}\n'
    ;;
  pull)
    if [ -n "$FAKE_MSB_PULL_FAIL" ]; then
      echo "sbx-fake-msb: pull failed" >&2
      exit 1
    fi
    ;;
  esac
  ;;
create)
  prev=""
  name=""
  for a in "$@"; do
    if [ "$prev" = "--name" ]; then
      name="$a"
    fi
    prev="$a"
  done
  if [ -n "$FAKE_MSB_CREATE_FAIL" ]; then
    case "$name" in
    *$FAKE_MSB_CREATE_FAIL*)
      echo "sbx-fake-msb: create failed for $name" >&2
      exit 1
      ;;
    esac
  fi
  ;;
exec)
  if [ -n "$FAKE_MSB_EXEC_SLEEP" ]; then
    trap 'exit 143' TERM
    sleep "$FAKE_MSB_EXEC_SLEEP"
  fi
  if [ -n "$FAKE_MSB_EXEC_FAIL_START" ]; then
    echo "sbx-fake-msb: cannot start the guest" >&2
    exit 127
  fi
  if [ -n "$FAKE_MSB_EXEC_NO_STDIN_READ" ]; then
    : > "$dir/stdin"
  else
    cat > "$dir/stdin"
  fi
  printf 'guest-stdout\n'
  printf 'guest-stderr\n' >&2
  exit "${FAKE_MSB_EXIT:-0}"
  ;;
remove)
  if [ -n "$FAKE_MSB_RM_FAIL" ]; then
    echo "sbx-fake-msb: remove failed" >&2
    exit 1
  fi
  ;;
esac
exit 0
`

// Call is one recorded fake-msb invocation.
type Call struct {
	// Index is the invocation's sequence number.
	Index int
	// Backend is the MSB_BACKEND value the fake saw in its environment.
	Backend string
	// Args is the full argv, in order.
	Args []string
}

// FakeMSB installs the fake msb executable at the front of PATH for the
// duration of the test and returns a log the test can query.
func FakeMSB(t *testing.T) Log {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "msb")
	if err := os.WriteFile(script, []byte(fakeMsbSh), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	if err := os.MkdirAll(log, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_MSB_LOG", log)
	return Log{dir: log}
}

// Log is the recorded behavior of a fake msb executable.
type Log struct {
	dir string
}

// Wait blocks until the fake has recorded at least n calls.
func (l Log) Wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if len(l.Calls()) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake msb recorded %d calls, wanted at least %d", len(l.Calls()), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Calls returns every recorded invocation in order.
func (l Log) Calls() []Call {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil
	}
	var calls []Call
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "call.") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(name, "call."))
		if err != nil {
			continue // in-flight temp files are named tmp.<pid>, not call.<n>
		}
		data, err := os.ReadFile(filepath.Join(l.dir, name))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "MSB_BACKEND=") {
			continue
		}
		calls = append(calls, Call{
			Index:   n,
			Backend: strings.TrimPrefix(lines[0], "MSB_BACKEND="),
			Args:    lines[1:],
		})
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].Index < calls[j].Index })
	return calls
}

// Stdin returns what the fake's exec handed to the guest on standard input.
func (l Log) Stdin(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, "stdin"))
	if err != nil {
		t.Fatalf("reading fake msb stdin capture: %v", err)
	}
	return string(data)
}

// FailCreate makes the fake refuse every create whose sandbox name contains
// the given substring, simulating a creation failure.
func (l Log) FailCreate(t *testing.T, substring string) {
	t.Helper()
	t.Setenv("FAKE_MSB_CREATE_FAIL", substring)
}
