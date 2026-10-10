# sbx

Worktree-scoped local development sandboxes. Each Git worktree gets its own persistent sandbox, and can also start disposable runs from the same `sbx.toml`. sbx drives the local [microsandbox](https://microsandbox.dev/) CLI (`msb`); projects describe their environment, not backend commands.

sbx targets macOS and Linux. Windows, cloud backends, automatic image builds, stack detection, and guest task orchestration are out of scope. The command names `exec` and `run` are provisional and will be revisited before v1.

## Install from source

Install Git, the Go toolchain pinned in [`mise.toml`](mise.toml), and a local microsandbox installation with working virtualization. Docker with buildx and a running Docker daemon are needed only for `sbx build`. Backend spellings and behavior have been tested against msb 0.7.x; see the [backend verification notes](.tracker/sbx-cli/spec.md#backend-verification) for version-specific limitations.

```sh
git clone https://github.com/ngscheurich/sbx.git
cd sbx
go install ./cmd/sbx
```

Put `$(go env GOPATH)/bin` on `PATH` (or your `GOBIN` if set). If you use mise, `mise install` installs the pinned Go version. Confirm the installation with `sbx --version`.

## Start a project

Commit an `sbx.toml` at the root of each worktree. Run sbx from anywhere inside that worktree; it mounts the worktree root, not the invocation directory.

```toml
image = "python:3.12-slim"
cpus = 2
memory = "2G"

[network]
policy = "public"
```

```sh
sbx plan                         # read-only preview
sbx up                           # create the persistent sandbox
sbx exec -- python3 --version     # use it; leave it running
sbx run -- python3 --version      # fresh disposable run; no published ports
sbx stop                         # retain private storage
sbx rm --yes                     # remove it and its Sandbox volumes
```

Without guest argv, `exec` and `run` open the configured shell (default `/bin/sh`). Guest commands follow `--`; sbx passes their argv directly, without a host shell. Quote guest shell code so the host does not expand it: `sbx exec -- sh -c 'echo "$HOME"'`.

For projects with `[build]`, run `sbx build` explicitly before `up`, `exec`, or `run`. Only images without a build recipe may be pulled automatically.

## Commands

| Command | Behavior and flags |
| --- | --- |
| `sbx`, `sbx --help` | Show concise top-level help. |
| `sbx version`, `sbx --version`, `sbx -V` | Show version information. `version --json` returns fields. |
| `sbx plan [--json]` | Preview persistent creation/reuse, backend arguments, network policy, redacted secrets, storage compatibility, ports, Creation drift, Bootstrap, and Image-check state. Does not run checks or reserve ports. |
| `sbx build [--keep-archive] [--json]` | Build with Docker, export a temporary archive, import with `msb load --input`, and run any Image check. Requires `[build]`. Removes the archive unless explicitly retained for debugging. |
| `sbx up [--retry-bootstrap] [--allow-stale] [--json]` | Create/start this worktree’s persistent sandbox and show its identity and endpoints. Healthy reuse is idempotent. |
| `sbx exec [--allow-stale] [-- <argv...>]` | Bring the persistent sandbox up if needed, run argv or the configured shell, and leave it running. |
| `sbx run [-- <argv...>]` | Run argv or the shell in a fresh disposable sandbox. Publishes no ports; removal belongs to native `msb run`. |
| `sbx status [--json]` | Read-only identity, state, Creation drift, Bootstrap state, and published ports. |
| `sbx list [--json]` | List every Owned sandbox on the local backend, across projects and worktrees. Works outside a Git repository without project configuration. |
| `sbx logs` | Show this worktree’s persistent sandbox logs. |
| `sbx stop [--json]` | Stop the persistent sandbox without deleting storage or sbx state. |
| `sbx rm [--yes] [--json]` | Remove the persistent sandbox and report its lost Sandbox volumes. Interactive use asks for confirmation; scripts must pass `--yes`. |
| `sbx port prune [--json]` | Release reservations for sandboxes that no longer exist. Keeps reservations for stopped sandboxes; fails safely if the backend cannot be inspected. |

`--plain` (`-p`, alias `--no-color`) is a global accessibility flag **before the command**: `sbx --plain status`. It drops color, emphasis, and table borders. `NO_COLOR` drops ANSI styling; use `--plain` to drop all decoration. Guest stdio is untouched.

`--json` is per command, not global. Supported commands emit one JSON value on stdout; build/check progress goes to stderr, and errors never appear on stdout. It is not supported by help, `exec`, `run`, or `logs`. Empty collections are arrays, not human messages.

Guest command output and exit status pass through. Usage errors exit 2; sbx runtime errors normally exit 1. Interrupts cancel the local backend subprocess, but **guest signal forwarding is not guaranteed**: observed msb versions tear down an exec session on SIGTERM rather than deliver it to guest traps. Terminal sessions may use a PTY; piped sessions use stream mode for `exec` and non-TTY mode for `run`.

## Configuration

See [`sbx.example.toml`](sbx.example.toml) for an annotated configuration and the complete projects under [`fixtures/`](fixtures/). The current configuration key for egress is **`[network].policy`**, as in those fixtures.

TOML keys are checked strictly. Unknown keys, invalid values, conflicting guest mount targets, and missing bind sources fail before resources change. sbx has no interpolation language: `~` expands in host paths where supported, but guest shell code is never substituted by the host. `${` in mount paths is rejected because the backend would interpret it as an environment reference.

| Setting | Shape and defaults |
| --- | --- |
| `image` | Required OCI image reference. |
| `cpus`, `memory` | Required positive CPU count and memory size. Sizes are positive integers with `K`, `M`, or `G`, e.g. `512M` or `2G`. |
| `shell` | Optional guest shell, default `/bin/sh`. |
| `[workspace]` | Optional `target`, default `/workspace`. Binds the current worktree read-write and sets the guest working directory. |
| `[[mounts]]` | `type = "bind"`, `source`, `target`, optional `read_only`; or `type = "tmpfs"`, `target`, `size`, optional `noexec` (default `false`). Bind sources may be absolute, worktree-relative, or start with `~`; guest targets must be absolute. |
| `[volumes.<name>]` | `target`, required `scope = "sandbox"` or `"project"`, optional `kind = "dir"` (default) or `"disk"`. Disks require `size`; directories take no `size`. Optional `quota` is valid only for Project directory volumes. |
| `[network]` | Required `policy = "public"`, `"allowlist"`, or `"none"`; `allow` only for allowlist mode, where it must be nonempty. Optional `dns_nameservers` accepts IP addresses or `IP:PORT`. No implicit network access. |
| `[ports.<name>]` | Required `guest` TCP port, 1–65535. Guest ports must be unique. Persistent sandboxes publish on `127.0.0.1`, using stable host reservations in 4001–4099. No host-port pinning or disposable publishing. |
| `[env]` | String-valued guest environment variables. |
| `[secrets.<name>]` | Required `from_env` host variable name and nonempty destination `allow` list. The guest receives a placeholder, never the real value from sbx. |
| `[build]` | Required `context`, optional `dockerfile`, `target`, `platform` (default `linux/<host architecture>`). Host paths resolve against the worktree root and support leading `~`. An omitted Dockerfile defaults to `<context>/Dockerfile`. |
| `image_check` | Optional host path to a project-owned guest script that gates image use. |
| `[bootstrap]` | Required nonempty `run` when declared: guest shell code run once per new sandbox, in the Workspace. |
| `[aliases]` | String-valued shorthands for sbx command lines. See below. |

Volume and port names match `[a-z][a-z0-9_]*`; environment and secret names match `[A-Za-z_][A-Za-z0-9_]*`. Environment and secret names cannot collide. `policy = "none"` forbids ports and custom DNS because it blocks inbound and outbound traffic.

### Network and secrets

`public` permits public destinations, not private or host networks. `allowlist` permits only exact domains or `*.`-prefixed suffixes of at least two labels; `*.example.com` also matches `example.com`. `none` blocks all traffic.

```toml
[network]
policy = "allowlist"
allow = ["example.com", "*.example.org"]

[secrets.API_TOKEN]
from_env = "PROJECT_API_TOKEN"
allow = ["example.com"]
```

Export the host variable before creating or restarting a sandbox. A missing variable fails before resource changes. Secret values reach `msb` only through its environment; arguments, generated maps, state, Plan, and sbx-generated diagnostics contain names and references, not values. The backend substitutes the placeholder only on requests to that secret’s allowed destinations.

Allowlist policy and secrets enable TLS inspection, and msb installs its CA in the guest system trust store. Programs with private CA bundles or certificate pinning need project-specific trust configuration. sbx does not redact guest output, logs, or backend tool output; do not print secrets there.

### Storage, identity, and safety

- **Workspace:** host source changes survive every execution mode.
- **Sandbox volumes:** private to a sandbox; survive `stop`/`up`, disappear with `rm` or the end of a disposable run.
- **Project volumes:** shared by sibling worktrees, namespaced by the common Git directory; survive sandbox removal and are never deleted by sbx. Concurrent writes are the project’s responsibility.

Before creation, sbx checks existing Project volumes for kind, size, and quota conflicts; it never resizes or overwrites them. Use a new logical name to change a shared definition. Named-directory quota enforcement remains unverified against the observed msb CLI versions; do not treat the configuration alone as proof of enforced capacity limits.

Sandbox identity comes from the project basename, worktree basename, and an eight-character worktree-path hash, not configuration. Changing branches or configuration does not rename it. Moving a worktree changes its identity; moving the common Git directory orphans Project volumes. sbx does not adopt old or unowned resources.

Creation-time settings include image contents, mounts, storage, resources, environment, network, secrets, and ports. Drift makes `up` and `exec` refuse reuse; `--allow-stale` explicitly permits it without recreating or repairing anything. `run` remains independent. Use `status` and `plan` to inspect drift, then deliberately remove/recreate only when losing private data is acceptable. Ownership labels guard against accidents, not hostile tampering.

Port reservations survive `rm` until `port prune`. sbx checks the host registry, loopback availability, and backend publications; a race with another backend publisher is still possible. Backend inspection is authoritative once a sandbox exists.

Host records live under `${XDG_STATE_HOME:-~/.local/state}/sbx`: snapshots, Bootstrap markers, Image-check successes, reservations, and locks. Read-only commands never write those records, pull images, run checks, or mutate sandboxes.

### Image checks and Bootstrap

An Image check runs in a temporary sandbox from the image alone, without the Workspace, volumes, or secrets. sbx copies the script by content, runs it, and removes that sandbox. A success is keyed by image contents and script contents, not a tag; either execution mode requires a matching success. `--allow-stale` does not bypass this gate. `build` fails if its check fails, even if the imported image remains cached.

Bootstrap runs through the configured shell after each new sandbox is created. A successful persistent Bootstrap is recorded for that sandbox’s creation; a failure leaves it incomplete. Plain `up` does not retry partial initialization. Use `up --retry-bootstrap` explicitly, or `exec` for repairs (it warns and allows the command). Editing Bootstrap is reported but is not Creation drift. Projects must make their own retries safe. A disposable run executes Bootstrap before the guest command and ends on failure.

### Aliases

```toml
[aliases]
test = "exec -- ./scripts/test.sh"
fresh_test = "run -- ./scripts/test.sh"
```

`sbx test` expands the configured command and appends any additional arguments. Expansions split on whitespace: they are not shell syntax, and quotes or variable substitutions have no special meaning. Builtins always win over same-named aliases, with a warning. An alias may expand through one other alias; deeper chains and cycles fail. Aliases affect neither identity nor Creation drift.

## Verification

```sh
go build ./...                    # compile/typecheck
go test ./...                     # no virtualization required
go test -race ./...
go vet ./...
```

Automated acceptance tests commit complete fixture copies into temporary Git repositories and worktrees. Fake `msb` and Docker executables exercise translation, failure paths, storage lifetimes, port reservations, Bootstrap, drift, isolated Image checks, and exit/output propagation. They verify backend requests and simulated lifetimes, **not live isolation enforcement**. The smoke runner’s automated test also uses fakes; it is not real-host evidence.

### Opt-in real-host smoke test

From this repository, on a host with local msb, working virtualization, Docker/buildx, and enough resources for two 2-CPU/4G web sandboxes:

```sh
SBX_SMOKE=1 go test -v ./internal/cli/smoke -run '^TestRealHostSmoke$' -count=1 -timeout=30m
```

Without opt-in, missing executables, an unavailable Docker daemon/buildx, inaccessible `/dev/kvm` on Linux, or unavailable Hypervisor support on macOS, the test reports **`not run` and `SKIP`**, not a verified pass. Go may still print a package-level `PASS` for a skipped test; check the individual test result. With prerequisites present, build/runtime/assertion failures fail the test rather than being reclassified as unavailable.

The test builds the current CLI, copies both fixtures into private temporary Git repositories, uses private sbx host state and a throwaway token, and never changes the checked-in fixture sources. It builds/imports both images; runs restricted CLI `go version`; checks the web image; creates two persistent web sandboxes; checks identity reuse and distinct identities/ports; runs the web test script in both execution modes; checks a data marker from the first worktree is absent in the second; and removes only the persistent sandboxes it created. Partial persistent creation is registered for cleanup before `up` so Bootstrap failure also gets cleanup.

Each completed step is reported. **Live allowlist enforcement, secret destination enforcement, volume deletion, HTTP behavior, and guest signal forwarding remain unverified.** The test does not start an HTTP server or delete volumes directly. Fixture Project volumes and built/imported images are retained in the backend caches; they are not real user volumes, but repeated runs can leave retained fixture storage. Any manual cleanup through `msb` is your responsibility—never remove a shared volume without checking who uses it.

For domain vocabulary and design decisions, see [`CONTEXT.md`](CONTEXT.md) and [`docs/adrs/`](docs/adrs/).
