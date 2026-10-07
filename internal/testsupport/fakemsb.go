// Package testsupport provides test doubles shared across sbx's packages:
// a fake msb executable that records its invocations and simulates backend
// behavior, so tests never reach a real microsandbox installation.
package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeMsbSh is a POSIX shell script that stands in for the msb CLI. It
// appends every invocation to $FAKE_MSB_LOG/call.<n> — NUL-separated fields:
// the observed MSB_BACKEND value, then one field per argument (NUL because
// guest scripts can contain newlines) — and then behaves according to
// FAKE_MSB_* variables:
//
//	FAKE_MSB_CONTEXT_BACKEND   backend reported by "msb context" (default local)
//	FAKE_MSB_IMAGE_MISSING     comma-separated images that "image inspect" fails for
//	FAKE_MSB_IMAGE_DIGEST      manifest digest reported by "image inspect" and
//	                            recorded for created sandboxes (default sha256:fake)
//	FAKE_MSB_PULL_FAIL         set to make "image pull" fail
//	FAKE_MSB_CREATE_FAIL       substring that makes "create" fail when the name matches
//	FAKE_MSB_LOAD_FAIL         set to make "load" fail
//	FAKE_MSB_LOAD_REQUIRES     a path that must exist when "load" runs (lets a
//	                            test pin that the import follows the archive's creation)
//	FAKE_MSB_EXEC_SLEEP         seconds "exec" sleeps before answering (interruptible via SIGTERM)
//	FAKE_MSB_EXEC_FAIL_START    set to make "exec" exit 127 without running the guest
//	FAKE_MSB_EXIT               exit status of "exec" (default 0)
//	FAKE_MSB_RM_FAIL            set to make "remove" fail
//	FAKE_MSB_VOLUMES_FAIL       set to make "volumes" fail
//	FAKE_MSB_VOLUMES_MALFORMED  set to make "volumes" print non-JSON
//	FAKE_MSB_STATUS_OVERRIDE    status word that "ls" and "inspect" report
//	                            regardless of the records' real state (models
//	                            a listing/reality mismatch)
//
// The fake is stateful: created sandboxes live as record files under
// $FAKE_MSB_LOG/store/sbox-<name>, so ls, inspect, start, stop, and remove
// observe what earlier calls created, the way a real backend would. Test
// helpers in this package can seed records directly to simulate sandboxes
// sbx did not create.
//
// Volumes live as record files under $FAKE_MSB_LOG/store/vol-<name>: named
// volumes (--mount-named) persist until tests remove them, and owned
// volumes (--mount-owned) belong to their sandbox — remove deletes a
// sandbox's owned volumes with it, and msb run's one-shot owned volumes
// disappear when the command completes, matching the real backend's
// lifecycle. A named volume's definition comes from the mount options of
// the first mount that created it, the way a real backend fixes a
// volume's shape at creation.
const fakeMsbSh = `#!/bin/sh
dir="${FAKE_MSB_LOG:?}"
store="$dir/store"
mkdir -p "$store"
n=0
while [ -f "$dir/call.$n" ]; do n=$((n+1)); done
tmp="$dir/tmp.$$"
{
  printf 'MSB_BACKEND=%s\0' "$MSB_BACKEND"
  for a in "$@"; do printf '%s\0' "$a"; done
} > "$tmp"
mv "$tmp" "$dir/call.$n"

record_path() {
  printf '%s/sbox-%s' "$store" "$1"
}

json_labels() {
  # Renders the label.<key>=<value> lines of one record as a JSON object.
  out=""
  while IFS= read -r line; do
    case "$line" in
    label.*=*)
      rest=${line#label.}
      out="$out\"${rest%%=*}\":\"${rest#*=}\","
      ;;
    esac
  done < "$1"
  if [ -n "$out" ]; then
    out=${out%,}
  fi
  printf '{%s}' "$out"
}

json_config() {
  # Renders one record as an msb config layer: manifest digest, labels,
  # and the (still empty) published ports.
  digest=$(sed -n 's/^digest=//p' "$1")
  printf '{"manifest_digest":"%s","labels":%s,"ports":[]}' "$digest" "$(json_labels "$1")"
}

missing_sandbox() {
  echo "sbx-fake-msb: sandbox $1 not found" >&2
  exit 1
}

value_flags="--name --cpus --memory --mount-dir --mount-file --env --net-rule --dns-nameserver --secret-conf --fs-conf --workdir"

vol_path() {
  printf '%s/vol-%s' "$store" "$1"
}

size_to_bytes() {
  # Converts msb's size format (512M) to bytes, binary units.
  num=${1%[KMG]}
  suf=${1#"$num"}
  case "$suf" in
  K) mult=1024 ;;
  M) mult=1048576 ;;
  G) mult=1073741824 ;;
  *) mult=1 ;;
  esac
  printf '%s' $((num * mult))
}

parse_vol_opts() {
  # Parses a mount spec's comma-separated options (kind=disk,size=2G)
  # into the vkind, cap, and quo variables.
  saved_ifs=$IFS
  IFS=','
  for kv in $1; do
    case "$kv" in
    kind=*) vkind=${kv#kind=} ;;
    size=*) cap=$(size_to_bytes "${kv#size=}") ;;
    quota=*) quo=$(($(size_to_bytes "${kv#quota=}") / 1048576)) ;;
    esac
  done
  IFS=$saved_ifs
}

record_named() {
  # Records a named volume from a NAME:DEST[:OPTIONS] spec, creating it
  # only if it does not exist yet: remounting reuses the stored volume.
  spec=$1
  vname=${spec%%:*}
  f=$(vol_path "$vname")
  [ -f "$f" ] && return 0
  vkind=dir
  cap=null
  quo=null
  rest=${spec#*:}
  case "$rest" in
  *:*) parse_vol_opts "${rest#*:}" ;;
  esac
  {
    echo "kind=$vkind"
    echo "capacity_bytes=$cap"
    echo "quota_mib=$quo"
    echo "owner="
  } > "$store/volnew.$$"
  mv "$store/volnew.$$" "$f"
}

record_owned() {
  # Records an owned volume from a DEST[:OPTIONS] spec for owner sandbox
  # $2. The backend-generated name embeds the owner so removal can find
  # the sandbox's volumes.
  spec=$1
  owner=$2
  dest=${spec%%:*}
  vid=$(printf '%s' "$dest" | tr '/.' '__')
  f=$(vol_path "owned-$owner-$vid")
  vkind=dir
  cap=null
  quo=null
  case "$spec" in
  *:*) parse_vol_opts "${spec#*:}" ;;
  esac
  {
    echo "kind=$vkind"
    echo "capacity_bytes=$cap"
    echo "quota_mib=$quo"
    echo "owner=$owner"
  } > "$store/volnew.$$"
  mv "$store/volnew.$$" "$f"
}

set_status() {
  f=$(record_path "$1")
  [ -f "$f" ] || missing_sandbox "$1"
  grep -v '^status=' "$f" > "$store/status.$$"
  echo "status=$2" >> "$store/status.$$"
  mv "$store/status.$$" "$f"
}

case "$1" in
load)
  if [ -n "$FAKE_MSB_LOAD_FAIL" ]; then
    echo "sbx-fake-msb: load failed" >&2
    exit 1
  fi
  input=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--input" ]; then
      input="$a"
    fi
    prev="$a"
  done
  if [ -n "$FAKE_MSB_LOAD_REQUIRES" ] && [ ! -e "$FAKE_MSB_LOAD_REQUIRES" ]; then
    echo "sbx-fake-msb: load ran before $FAKE_MSB_LOAD_REQUIRES existed" >&2
    exit 1
  fi
  if [ -z "$input" ] || [ ! -f "$input" ]; then
    echo "sbx-fake-msb: cannot open image archive $input" >&2
    exit 1
  fi
  printf '%s\n' "$input" > "$dir/loaded.$n"
  ;;
context)
  printf '{"backend":"%s"}\n' "${FAKE_MSB_CONTEXT_BACKEND:-local}"
  ;;
image)
  case "$2" in
  inspect)
    case ",$FAKE_MSB_IMAGE_MISSING," in
    *,$3,*) exit 1 ;;
    esac
    printf '{"digest":"%s"}\n' "${FAKE_MSB_IMAGE_DIGEST:-sha256:fake}"
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
  shift
  name=""
  image=""
  labels=""
  named_specs=""
  owned_specs=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --name)
      name="$2"
      shift 2
      ;;
    --mount-named)
      named_specs="$named_specs
$2"
      shift 2
      ;;
    --mount-owned)
      owned_specs="$owned_specs
$2"
      shift 2
      ;;
    --label)
      labels="$labels
$2"
      shift 2
      ;;
    --secret-conf)
      [ -f "$2" ] && cp "$2" "$dir/secret-conf.$n"
      shift 2
      ;;
    --fs-conf)
      [ -f "$2" ] && cp "$2" "$dir/fs-conf.$n"
      shift 2
      ;;
    --tls-intercept|--no-net|--tty|--no-tty|--pull)
      shift
      ;;
    --)
      shift
      break
      ;;
    -*)
      # Any other flag with a value: consume the value too.
      case " $value_flags " in
      *" $1 "*) shift 2 ;;
      *) shift ;;
      esac
      ;;
    *)
      image="$1"
      shift
      ;;
    esac
  done
  if [ -n "$FAKE_MSB_CREATE_FAIL" ]; then
    case "$name" in
    *$FAKE_MSB_CREATE_FAIL*)
      echo "sbx-fake-msb: create failed for $name" >&2
      exit 1
      ;;
    esac
  fi
  f=$(record_path "$name")
  {
    echo "status=running"
    echo "image=$image"
    echo "digest=${FAKE_MSB_IMAGE_DIGEST:-sha256:fake}"
    echo "created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '%s\n' "$labels" | while IFS= read -r l; do
      [ -n "$l" ] && echo "label.$l"
    done
  } > "$store/create.$$"
  mv "$store/create.$$" "$f"
  printf '%s\n' "$named_specs" | while IFS= read -r s; do
    [ -n "$s" ] && record_named "$s"
  done
  printf '%s\n' "$owned_specs" | while IFS= read -r s; do
    [ -n "$s" ] && record_owned "$s" "$name"
  done
  ;;
ls)
  out="["
  first=1
  for f in "$store"/sbox-*; do
    [ -f "$f" ] || continue
    name=${f##*/sbox-}
    image=$(sed -n 's/^image=//p' "$f")
    status=$(sed -n 's/^status=//p' "$f")
    [ -n "$FAKE_MSB_STATUS_OVERRIDE" ] && status=$FAKE_MSB_STATUS_OVERRIDE
    case "$status" in
    running) status=Running ;;
    stopped) status=Stopped ;;
    esac
    created=$(sed -n 's/^created_at=//p' "$f")
    if [ "$first" = 0 ]; then
      out="$out,"
    fi
    out="$out{\"name\":\"$name\",\"image\":\"$image\",\"status\":\"$status\",\"created_at\":\"$created\"}"
    first=0
  done
  printf '%s]\n' "$out"
  ;;
inspect)
  f=$(record_path "$2")
  [ -f "$f" ] || missing_sandbox "$2"
  status=$(sed -n 's/^status=//p' "$f")
  created=$(sed -n 's/^created_at=//p' "$f")
  cfg=$(json_config "$f")
  # The internal state check reads the record's own word; the reported
  # status is capitalized afterward, the way the real msb spells it.
  if [ "$status" = "running" ]; then
    active="$cfg"
  else
    active="null"
  fi
  [ -n "$FAKE_MSB_STATUS_OVERRIDE" ] && status=$FAKE_MSB_STATUS_OVERRIDE
  case "$status" in
  running) status=Running ;;
  stopped) status=Stopped ;;
  esac
  printf '{"name":"%s","status":"%s","created_at":"%s","active_config":%s,"config":%s}\n' "$2" "$status" "$created" "$active" "$cfg"
  ;;
start)
  # A running sandbox refuses to start, the way the real msb does
  # (observed on msb 0.7.6): "sandbox still running ... already running".
  if [ "$(sed -n 's/^status=//p' "$(record_path "$2")" 2>/dev/null)" = "running" ]; then
    echo "error: sandbox still running: cannot start sandbox '$2': already running" >&2
    exit 1
  fi
  set_status "$2" running
  ;;
stop)
  set_status "$2" stopped
  ;;
logs)
  f=$(record_path "$2")
  [ -f "$f" ] || missing_sandbox "$2"
  printf 'fake log line 1 for %s\n' "$2"
  printf 'fake log line 2 for %s\n' "$2"
  ;;
remove)
  if [ -n "$FAKE_MSB_RM_FAIL" ]; then
    echo "sbx-fake-msb: remove failed" >&2
    exit 1
  fi
  shift
  for a in "$@"; do
    case "$a" in
    --force) ;;
    *)
      rm -f "$(record_path "$a")"
      # A sandbox's owned volumes die with it; named volumes stay.
      for v in "$store"/vol-owned-"$a"-*; do
        [ -f "$v" ] && rm -f "$v"
      done
      ;;
    esac
  done
  ;;
volumes)
  if [ -n "$FAKE_MSB_VOLUMES_FAIL" ]; then
    echo "sbx-fake-msb: volume listing failed" >&2
    exit 1
  fi
  if [ -n "$FAKE_MSB_VOLUMES_MALFORMED" ]; then
    printf 'this is not json\n'
    exit 0
  fi
  out="["
  first=1
  for f in "$store"/vol-*; do
    [ -f "$f" ] || continue
    vname=${f##*/vol-}
    vkind=$(sed -n 's/^kind=//p' "$f")
    cap=$(sed -n 's/^capacity_bytes=//p' "$f")
    quo=$(sed -n 's/^quota_mib=//p' "$f")
    if [ "$first" = 0 ]; then
      out="$out,"
    fi
    out="$out{\"name\":\"$vname\",\"kind\":\"$vkind\",\"capacity_bytes\":$cap,\"quota_mib\":$quo}"
    first=0
  done
  printf '%s]\n' "$out"
  ;;
exec|run)
  prev=""
  for a in "$@"; do
    case "$prev" in
    --secret-conf) [ -f "$a" ] && cp "$a" "$dir/secret-conf.$n" ;;
    --fs-conf) [ -f "$a" ] && cp "$a" "$dir/fs-conf.$n" ;;
    --mount-named) record_named "$a" ;;
    --mount-owned) record_owned "$a" "run-$n" ;;
    esac
    prev="$a"
  done
  if [ -n "$FAKE_MSB_EXEC_SLEEP" ]; then
    trap 'exit 143' TERM
    # Run the sleep in the background and wait on it so the TERM trap
    # fires immediately, however the signal arrives.
    sleep "$FAKE_MSB_EXEC_SLEEP" &
    wait "$!"
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
  # A disposable run's owned volumes are removed with it; named volumes
  # persist.
  rm -f "$store"/vol-owned-run-"$n"-*
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
	return Log{callLog{dir: log}}
}

// callLog reads the call recording shared by every fake executable: one
// call.<n> file per invocation, the first line the observed MSB_BACKEND,
// then one argument per line.
type callLog struct {
	dir string
}

// Wait blocks until the fake has recorded at least n calls.
func (l callLog) Wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if len(l.Calls()) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake executable recorded %d calls, wanted at least %d", len(l.Calls()), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Calls returns every recorded invocation in order.
func (l callLog) Calls() []Call {
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
		fields := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "MSB_BACKEND=") {
			continue
		}
		calls = append(calls, Call{
			Index:   n,
			Backend: strings.TrimPrefix(fields[0], "MSB_BACKEND="),
			Args:    fields[1:],
		})
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].Index < calls[j].Index })
	return calls
}

// Log is the recorded behavior of a fake msb executable.
type Log struct {
	callLog
}

// Loaded returns the archive path the fake recorded for the `msb load` call
// at the given index.
func (l Log) Loaded(t *testing.T, index int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, "loaded."+strconv.Itoa(index)))
	if err != nil {
		t.Fatalf("reading fake msb load capture for call %d: %v", index, err)
	}
	return strings.TrimSpace(string(data))
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

// SecretConf returns the content of the secret-name map the fake captured
// from the --secret-conf argument of the call at the given index.
func (l Log) SecretConf(t *testing.T, index int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, "secret-conf."+strconv.Itoa(index)))
	if err != nil {
		t.Fatalf("reading fake msb secret-conf capture for call %d: %v", index, err)
	}
	return string(data)
}

// FsConf returns the content of the filesystem configuration the fake
// captured from the --fs-conf argument of the call at the given index.
func (l Log) FsConf(t *testing.T, index int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, "fs-conf."+strconv.Itoa(index)))
	if err != nil {
		t.Fatalf("reading fake msb fs-conf capture for call %d: %v", index, err)
	}
	return string(data)
}

// FailCreate makes the fake refuse every create whose sandbox name contains
// the given substring, simulating a creation failure.
func (l Log) FailCreate(t *testing.T, substring string) {
	t.Helper()
	t.Setenv("FAKE_MSB_CREATE_FAIL", substring)
}

// SeedSandbox writes a fake-backend record directly, so a test can simulate
// a sandbox sbx did not create — such as an unowned one sharing a Sandbox
// identity — without driving the CLI first. The record shape matches what
// the fake's own create writes.
func (l Log) SeedSandbox(t *testing.T, name, image, status string, labels map[string]string) {
	t.Helper()
	store := filepath.Join(l.dir, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status=%s\n", status)
	fmt.Fprintf(&b, "image=%s\n", image)
	fmt.Fprintf(&b, "digest=%s\n", "sha256:seeded")
	fmt.Fprintf(&b, "created_at=%s\n", "2024-01-01T00:00:00Z")
	names := make([]string, 0, len(labels))
	for key := range labels {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		fmt.Fprintf(&b, "label.%s=%s\n", key, labels[key])
	}
	if err := os.WriteFile(filepath.Join(store, "sbox-"+name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// SeedVolume writes a fake-backend volume record directly, so a test can
// simulate a pre-existing volume — one a sibling worktree's sandbox
// created, or a conflicting one another branch's definition created. Nil
// capacityBytes or quotaMiB render as JSON null in the volumes listing,
// the shape real msb reports for plain directory volumes.
func (l Log) SeedVolume(t *testing.T, name, kind string, capacityBytes, quotaMiB *int64) {
	t.Helper()
	store := filepath.Join(l.dir, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	field := func(v *int64) string {
		if v == nil {
			return "null"
		}
		return strconv.FormatInt(*v, 10)
	}
	content := fmt.Sprintf("kind=%s\ncapacity_bytes=%s\nquota_mib=%s\nowner=\n",
		kind, field(capacityBytes), field(quotaMiB))
	if err := os.WriteFile(filepath.Join(store, "vol-"+name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// VolumeNames returns the name of every volume record the fake backend
// holds, sorted, including sandboxes' owned volumes.
func (l Log) VolumeNames() []string {
	entries, err := os.ReadDir(filepath.Join(l.dir, "store"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "vol-") {
			names = append(names, strings.TrimPrefix(e.Name(), "vol-"))
		}
	}
	sort.Strings(names)
	return names
}

// VolumeExists reports whether the fake backend holds the named volume.
func (l Log) VolumeExists(t *testing.T, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(l.dir, "store", "vol-"+name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat volume record %s: %v", name, err)
	}
	return err == nil
}

// Int64 returns a pointer to v, for seeding nullable volume fields.
func Int64(v int64) *int64 {
	return &v
}

// FixtureTOML reads a fixture sbx.toml and removes the named top-level
// table blocks — each from its "[header]" line to the next "[" header —
// so tests derive variants from the fixture instead of duplicating its
// content and drifting out of sync.
func FixtureTOML(t *testing.T, path string, strip ...string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	stripSet := make(map[string]struct{}, len(strip))
	for _, h := range strip {
		stripSet[h] = struct{}{}
	}
	var out []string
	stripping := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "[") {
			_, drop := stripSet[strings.TrimSpace(line)]
			stripping = drop
			if drop {
				continue
			}
		}
		if !stripping {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
