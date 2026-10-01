# sbx: local, worktree-scoped sandbox CLI

## Goal

Build `sbx`, a standalone Go CLI (`github.com/ngscheurich/sbx`) that gives each Git worktree isolated local development sandboxes without making projects author microsandbox commands. An `sbx.toml` at each worktree root describes the environment. A worktree may have one persistent sandbox and can also start disposable runs; both use the same project configuration and mount the Workspace read-write at `/workspace` by default.

The v1 backend is the local `msb` CLI, invoked as a subprocess ([ADR-0006](../../docs/adrs/0006-drive-msb-through-its-cli.md)). Project configuration and commands describe behavior, not msb syntax ([ADR-0001](../../docs/adrs/0001-sbx-native-project-configuration.md)). A future backend that cannot provide a required capability must reject the project rather than weaken isolation, egress restrictions, secret handling, or storage lifetime.

[CONTEXT.md](../../CONTEXT.md) defines the vocabulary used here, and [docs/adrs/](../../docs/adrs/) records the trade-offs behind v1. Two fixture projects, fully specified below, define acceptance.

## Scope

- v1 runs on macOS and Linux against a local microsandbox installation. Windows and microsandbox cloud are out of scope.
- One `sbx.toml` per worktree root and one sandbox configuration per project. No non-Git identity, nested projects, or multiple profiles.
- No language- or framework-specific code paths, stack detection, presets, `sbx init`, or implicit image builds. Stack-specific behavior belongs in project configuration and project scripts.
- Installation from source (`go install ./cmd/sbx`) is enough. Release downloads and Homebrew packaging come later.

### Deferred beyond v1

These are out of scope for v1, not abandoned:

- Machine-scoped volumes shared across unrelated projects.
- Backend-specific project configuration.
- Host commands run after a build.
- Commands that list sandboxes across worktrees, list or remove volumes, or query a single port. Use `git worktree list`, `sbx status`, and `msb` itself to inspect or delete retained volumes and orphaned sandboxes. `msb volume rm` bypasses sbx's checks and can destroy shared data; sbx itself never deletes a Project volume.
- Guest tasks (a `[tasks]` table with dependencies and an `sbx task` command). Projects run their own scripts through the Workspace.
- A `--publish` option for disposable runs.
- Plans for disposable runs; `sbx plan` covers the persistent sandbox only.
- A broad real-host verification suite beyond the smoke test below.

## Project configuration

### Discovery and identity

sbx finds the worktree root from any directory inside it and reads that worktree's checked-out `sbx.toml`. It fails with an actionable error outside a Git worktree or when the file is missing. It resolves the worktree root and the repository's common Git directory to absolute, symlink-free paths before deriving any name or hash.

The **project basename** is the name of the common Git directory's parent when that directory is named `.git`, and otherwise the directory's own name without a trailing `.git`: `/src/app/.git` and `/src/app.git` both yield `app`.

**Sandbox identity** is `<project>-<worktree>-<hash>`: the project and worktree basenames, each lowercased, with every character outside `[a-z0-9]` replaced by `-` and cut to 32 characters, followed by an always-present 8-character hash of the worktree path. It depends on no `sbx.toml` value, so editing configuration, switching branches, or adding a same-named worktree never renames an existing sandbox ([ADR-0005](../../docs/adrs/0005-configuration-independent-sandbox-identity.md)). Moving a worktree gives it a new identity; adopting the old sandbox is out of scope.

Project volumes are named from a hash of the common Git directory and the logical volume name, so sibling worktrees share them and unrelated clones do not ([ADR-0003](../../docs/adrs/0003-project-volume-namespacing.md)). Moving or renaming the repository orphans its Project volumes; sbx neither adopts nor deletes them. Because each worktree reads its own `sbx.toml`, branches can declare the same Project volume differently. Before creating a sandbox, and in `plan`, sbx compares each declared Project volume with the existing volume's kind, size, and quota from `msb volumes --format json`; msb's own refusal to mount a conflicting volume is a backstop. Confirm that JSON includes non-null capacity and quota values for volumes configured with them before implementing this check. On a mismatch, `plan` reports it and mutating commands fail before changing any resource, naming the volume and both definitions and suggesting a new logical name. sbx never resizes or overwrites an existing volume.

### Fields

| Concept | Shape | Meaning |
| --- | --- | --- |
| Image and resources | Required `image`, `cpus`, `memory`; optional `shell` | OCI image reference, resource limits, and guest shell. `shell` defaults to `/bin/sh`. |
| Workspace | Optional `[workspace]` with `target` (default `/workspace`) | Binds the worktree root, not the invocation subdirectory, read-write at `target`, which is also the working directory. |
| Extra mounts | `[[mounts]]` with `type = "bind"`, `source`, `target`, optional `read_only`; or `type = "tmpfs"`, `target`, `size`, optional `noexec` (default `false`) | Host binds and guest tmpfs. Bind sources are absolute, relative to the worktree root, or start with `~`; a missing source is an error. |
| Volumes | `[volumes.<name>]` with `target`, required `scope = "sandbox" \| "project"`, optional `kind = "dir" \| "disk"` (default `dir`), `size` (required for disks), `quota` (Project-scope directories only) | Sandbox volumes are private and removed with their sandbox; Project volumes are shared across the project's worktrees and retained. |
| Network | Required `[network]` with `egress = "public" \| "allowlist" \| "none"`, `allow` (allowlist only), optional `dns_nameservers` | There is no implicit egress. `public` allows public destinations but not private or host networks; `allowlist` allows only `allow`; `none` blocks inbound and outbound traffic. |
| Ports | `[ports.<name>]` with `guest` | Named TCP guest services, published for the persistent sandbox on host loopback ports from 4001–4099. Disposable runs publish none. No host-port pinning. |
| Environment | `[env]` string values | Guest environment for every sandbox. |
| Secrets | `[secrets.<name>]` with `from_env = "HOST_VAR"` and required `allow` | See Secrets below. |
| Build | `[build]` with `context`, optional `dockerfile`, `target`, `platform` (default `linux/<host architecture>`) | Docker/buildx recipe for `image`. Without `[build]`, the image is **prebuilt**. |
| Image check | Optional `image_check` path | Project-owned guest script; see Images and Image checks. |
| Bootstrap | `[bootstrap]` with `run` | Guest shell code run once in every new sandbox. Editing it never causes Creation drift. |

TOML is parsed strictly, and every validation failure occurs before any resource changes. Beyond type errors and unknown keys, these are invalid:

- duplicate mount targets, and guest ports outside 1–65535;
- volume and port names not matching `[a-z][a-z0-9_]*`, and secret and `[env]` names not matching `[A-Za-z_][A-Za-z0-9_]*`;
- a disk volume without `size`, a directory volume with `size`, and `quota` on a disk or Sandbox volume;
- `allow` outside allowlist mode, an empty `allow` in allowlist mode, and ports or `dns_nameservers` with `egress = "none"`.

Sizes use msb's format, an integer with a `K`, `M`, or `G` suffix such as `512M`. `allow` entries, for the network and for secrets, are exact domains or `*.`-prefixed suffixes of at least two labels; `*.example.com` also matches `example.com`. sbx has no interpolation language: a leading `~` expands only in bind sources, and guest shell code is never subject to host substitution.

Allowlist mode and any declared secret turn on msb's TLS inspection, whose certificate authority msb adds to the guest's system trust store. Guest programs that use their own CA bundle or pin certificates need project configuration, such as `NODE_EXTRA_CA_CERTS`, to reach allowlisted HTTPS hosts.

Every field applies to both execution modes. A disposable run gets fresh Sandbox volumes, removed with it, while Project volumes and Workspace writes persist. Disposable runs can coexist with the persistent sandbox, so concurrent writes to the Workspace or Project volumes are the project's responsibility.

### Secrets

A secret's guest environment variable `<name>` holds a placeholder, and msb substitutes the host value of `from_env` only in requests to its `allow` destinations. A missing `from_env` variable fails sandbox creation before any resource changes; `plan` needs only the variable's name. The value reaches msb only through the msb subprocess's environment, never its arguments or a file, and sbx never writes it to configuration, state, a Plan, status, or diagnostics. sbx does not sanitize guest output, `sbx logs`, or backend tools' output, which may contain whatever those programs print.

### Translation to msb

The msb adapter passes settings as msb command-line flags rather than a generated sandbox YAML file, because msb expands `${NAME}` in YAML and resolves relative paths against the file. Sandbox volumes become `--mount-owned`, Project volumes `--mount-named`, bind sources absolute paths, and `[env]` entries `-e` flags. The only generated file is a `--net-conf` network policy containing validated domains and nameservers, written outside the repository. The adapter stays a small, testable seam, not a speculative second backend.

## Acceptance fixtures

Place the fixtures under `fixtures/restricted-cli/` and `fixtures/stateful-web/`, and keep their contents in sync with the automated tests. Tests copy each into its own temporary Git repository before running sbx, and build both images explicitly with `sbx build`.

### Restricted CLI fixture

`Dockerfile`:

```dockerfile
FROM golang:1.23-bookworm
```

`sbx.toml`:

```toml
image = "sbx-restricted-cli:latest"
cpus = 2
memory = "2G"
shell = "/bin/bash"

[[mounts]]
type = "bind"
source = "./host-notes.txt"
target = "/mnt/host-notes.txt"
read_only = true

[[mounts]]
type = "bind"
source = "./host-state"
target = "/mnt/host-state"

[volumes.go_mod]
target = "/go/pkg/mod"
scope = "project"

[volumes.go_build]
target = "/root/.cache/go-build"
scope = "project"

[network]
egress = "allowlist"
allow = ["proxy.golang.org", "sum.golang.org", "example.com"]
dns_nameservers = ["1.1.1.1", "8.8.8.8"]

[secrets.TEST_TOKEN]
from_env = "SBX_FIXTURE_TOKEN"
allow = ["example.com"]

[build]
context = "."
dockerfile = "Dockerfile"
```

The fixture also contains `host-notes.txt` with `read-only fixture\n` and `host-state/.keep`, so the writable bind source exists in a checkout. Tests supply a throwaway `SBX_FIXTURE_TOKEN`. `sbx run` opens Bash in a disposable run and `sbx run -- go version` runs a command in one; `sbx up` and `sbx exec -- go version` are the persistent equivalents. Tests check both binds, the Workspace at `/workspace`, the Project volumes, the allowlist, DNS, and the secret's translation. It declares no ports or Bootstrap.

### Stateful web fixture

`Dockerfile`:

```dockerfile
FROM python:3.12-slim
```

`sbx.toml`:

```toml
image = "sbx-stateful-web:latest"
cpus = 2
memory = "4G"
shell = "/bin/sh"
image_check = "image-check.sh"

[[mounts]]
type = "tmpfs"
target = "/tmp"
size = "512M"

[volumes.shared_cache]
target = "/opt/shared-cache"
scope = "project"

[volumes.artifacts]
target = "/vm/artifacts"
scope = "sandbox"
kind = "disk"
size = "8G"

[volumes.data]
target = "/var/lib/sbx-app"
scope = "sandbox"
kind = "disk"
size = "2G"

[network]
egress = "public"

[ports.web]
guest = 4000

[env]
SBX_DATA_DIR = "/var/lib/sbx-app"

[build]
context = "."
dockerfile = "Dockerfile"

[bootstrap]
run = 'printf "bootstrapped\n" > "$SBX_DATA_DIR/bootstrapped"'
```

Executable scripts, run from the Workspace: `scripts/setup.sh`:

```sh
#!/bin/sh
set -eu
test -f "$SBX_DATA_DIR/bootstrapped"
printf 'ready\n' > "$SBX_DATA_DIR/ready"
```

`scripts/test.sh`:

```sh
#!/bin/sh
set -eu
"$(dirname "$0")/setup.sh"
test -f "$SBX_DATA_DIR/ready"
printf 'built\n' > /vm/artifacts/result
```

`scripts/server.sh`:

```sh
#!/bin/sh
set -eu
"$(dirname "$0")/setup.sh"
exec python3 -m http.server 4000 --bind 0.0.0.0 --directory /workspace
```

`image-check.sh`, which passes only when the Workspace is absent:

```sh
#!/bin/sh
set -eu
command -v python3 >/dev/null
test ! -e /workspace/index.html
```

The fixture also contains `index.html` with `sbx fixture\n`. The `data` and `artifacts` Sandbox volumes survive `stop` and disappear on `rm`; `shared_cache` survives `rm` and is shared between the project's worktrees. `sbx run -- ./scripts/test.sh` bootstraps a fresh sandbox, runs the scripts, and removes its Sandbox volumes; `sbx exec -- ./scripts/test.sh` does the same in the persistent sandbox. `sbx exec -- ./scripts/server.sh` serves the worktree's current `index.html` through the port reserved for `web`.

## CLI and lifecycle

### Commands

| Command | Behavior |
| --- | --- |
| `sbx`, `sbx --help` | Concise help and, where available, current-project status. |
| `sbx plan` | The Plan for the persistent sandbox: creation or reuse, the msb arguments and network policy, Creation drift, Bootstrap and image-check status, and tentative ports. |
| `sbx build` | Build the image with Docker/buildx, import it with `msb load --input`, and run the Image check. Fails without `[build]`. |
| `sbx up [--retry-bootstrap] [--allow-stale]` | Create or start the persistent sandbox, running Bootstrap after creation, and print its identity and endpoints. |
| `sbx exec [--allow-stale] [-- <argv...>]` | Run `up` if needed, then run argv, or the configured shell with no argument, in the persistent sandbox. Leaves the sandbox running. |
| `sbx run [-- <argv...>]` | The same as `exec`, but in a disposable run. |
| `sbx status`, `sbx logs`, `sbx stop` | Show the persistent sandbox's identity, state, Bootstrap state, and port mappings; show its msb logs; stop it without deleting state. |
| `sbx rm [--yes]` | After confirmation, which noninteractive use gives with `--yes`, remove the persistent sandbox and list the Sandbox volumes lost. Keeps Project volumes and Port reservations. |
| `sbx port prune` | Remove Port reservations for sandboxes that no longer exist; keep those for stopped ones. Fails safely if the backend cannot be inspected. |

`exec` and `run` forward input, output, and signals, and exit with the guest command's status. They pass argv to the guest directly, never through a host shell, and ignore the image's ENTRYPOINT and CMD ([ADR-0007](../../docs/adrs/0007-ignore-image-entrypoint-and-cmd.md)). Bootstrap runs through `<shell> -c` in the Workspace.

**Command names to revisit.** `exec` and `run` differ only in which sandbox they use, yet read as synonyms. Rename them before v1 so each name conveys its mode; this spec uses the current names until then.

### Backend and ownership

v1 has no backend selector. The msb adapter sets a local backend for its subprocesses, confirms the effective selection with `msb context --format json`, and refuses to proceed if a cloud profile overrides it. `msb` is required for sandbox operations and Docker/buildx for `sbx build`. Failures give concrete next steps; sbx never falls back to another backend or swallows a subprocess error, and it propagates the backend's nonzero exit codes.

sbx labels every sandbox it creates with `sbx.managed=1` and `sbx.mode=persistent|disposable|check`. Persistent sandboxes and disposable runs also get `sbx.worktree=<worktree path>`. "Owned" means carrying `sbx.managed=1`. sbx never adopts, modifies, or removes a same-named sandbox it does not own. Labels are editable through `msb modify`, so they guard against accidents, not tampering.

### Persistent sandbox

`up`, and `exec`'s implicit `up`, are idempotent on a healthy sandbox. A per-sandbox lock serializes creating, starting, and bootstrapping; a concurrent caller waits with a message, and the lock is not held while a command runs.

Bootstrap completion is recorded in the host state directory, atomically under the per-sandbox lock and only after Bootstrap succeeds. The marker binds the Sandbox identity and the sandbox's `created_at` from msb inspection to a hash of the Bootstrap definition that ran ([ADR-0002](../../docs/adrs/0002-persistent-sandbox-safety.md)). A missing marker or one belonging to an earlier sandbox is incomplete: plain `up` reports it instead of rerunning partial initialization, and `exec` still works, with a warning, for repairs. If Bootstrap succeeds but sbx exits before writing the marker, explicit retry or repair is still required. `up --retry-bootstrap` runs the current definition and records completion only on success; `--allow-stale` never marks Bootstrap complete. When the current definition's hash differs from the marker, `status` and `plan` say so without blocking use. sbx does not make Bootstrap atomic or repeatable. msb 0.7.3 requires a restart to change a running sandbox's label, so Bootstrap completion cannot be recorded with a live `sbx.bootstrap` label.

Creation-time settings are captured at creation, in a snapshot stored in the host state directory under the Sandbox identity. sbx detects Creation drift when they differ from the current configuration, including when the image tag now points to different contents. `up` and `exec` then refuse and explain the differences; `--allow-stale` uses the sandbox anyway. An owned sandbox with no snapshot counts as drifted. sbx never mutates, deletes, or recreates a drifted sandbox itself, because that could destroy its private data. Drift in the persistent sandbox never blocks `run`.

### Disposable runs

A disposable run creates a uniquely named sandbox with `msb create`, runs Bootstrap and then the command with `msb exec`, and removes the sandbox on completion, failure, or cancellation, including when Bootstrap fails. It has no incomplete state to repair, needs no lock, and publishes no ports.

### Port reservations

sbx's **host state directory**, `${XDG_STATE_HOME:-~/.local/state}/sbx`, holds the machine-wide port registry, image-check successes, creation-time snapshots, Bootstrap completion markers, and locks. The registry is updated atomically under a cross-process lock, and Port reservations are keyed by Sandbox identity and port name. Bootstrap completion markers are host state, not sandbox labels; `rm` clears the marker for the sandbox it removes.

Before creating a persistent sandbox, sbx reuses its reservations or picks free ports from 4001–4099 that no other reservation holds. A free-port probe is not a reservation. Before choosing a port, sbx also checks published ports on existing local msb sandboxes, not only its own registry: msb 0.7.3 can accept a second sandbox publishing a port already used by a guest service. A collision with an existing backend sandbox makes a new candidate ineligible; a collision with an existing reservation fails rather than silently changing that reservation. sbx never removes a partially created sandbox to retry a port. A race with another process publishing the same port cannot be ruled out by probing; sbx must report any collision it detects rather than claim the endpoint is safe. Once a sandbox exists, the ports `msb inspect --format json` reports are authoritative. Mutating commands correct a stale registry entry unless it conflicts with another reservation, in which case they fail rather than take the port; read-only commands only report the discrepancy. A sandbox's ports change only by recreating it. `rm` keeps its reservations until `port prune`.

### Read-only commands

`plan`, `status`, `logs`, and help never pull images, run Image checks, create or modify sandboxes, reserve ports, write project files, or write sbx state. They remain usable when the persistent sandbox has drifted.

## Images and Image checks

`sbx build` uses Docker/buildx to build `image` from `[build]` into a temporary archive, imports it with `msb load --input`, and deletes the archive unless asked to keep it for debugging. Nothing else builds images. Before `up`, `exec`, or `run` uses an image, sbx may pull a missing prebuilt image; a missing image with `[build]` fails with an instruction to run `sbx build`.

If `image_check` is declared, sbx creates a check sandbox from the image alone, with no Workspace, volumes, or secrets; copies in the script; runs it; and removes the check sandbox on completion, failure, or cancellation ([ADR-0004](../../docs/adrs/0004-isolated-image-checks.md)). A failed check fails `build`, although the imported image may stay cached.

**Neither mode may use an image with an unmet Image check.** sbx records successes in the host state directory, keyed by the image's content identity from `msb image inspect --format json` and a hash of the script, never by tag alone. Before `up`, `exec`, or `run` uses an image, sbx resolves the image that would actually run and runs the check unless a matching success exists; an inspection failure is not a success. Before creating a sandbox, sbx confirms the image has not changed since the check. For an existing persistent sandbox, it checks that sandbox's image even with `--allow-stale`. A changed script requires a new success but is not Creation drift. A failed check never touches an existing sandbox or its data. `plan` reports check status as known, pending, or unresolvable.

## Acceptance and verification

Automated tests run without virtualization, using fake `msb` and Docker executables and temporary Git repositories and worktrees. They cover:

- validation, including unknown keys and scopes, and help that lists only v1 commands;
- both fixtures' translation into exact msb arguments and network policy files;
- refusal of cloud selection;
- ownership labels and Creation drift, including a missing snapshot;
- Bootstrap failure, interruption, retry with a changed definition, changed-Bootstrap reports, and stale host markers after sandbox replacement;
- disposable-run cleanup, including after failures;
- secret redaction in sbx-generated output, and Plan purity;
- Image-check gating in both modes, including prebuilt and changed images;
- that both modes ignore ENTRYPOINT and CMD, and that disposable runs publish no ports;
- Port reservations, collisions with unmanaged msb sandboxes, concurrent callers, and pruning;
- volume kinds and scopes, Sandbox-volume removal, Project-volume retention, and Project-volume conflicts;
- exit-status and output propagation.

An **opt-in** smoke test requires real `msb`, virtualization, and Docker/buildx; it reports missing prerequisites as not run, not passed, and never modifies the fixture sources.

- **Restricted CLI:** with a throwaway token set, it runs `sbx build` and checks that `sbx run -- go version` succeeds. It does not claim live enforcement of the allowlist or the secret's destination; the automated tests cover their translation.
- **Stateful web:** it builds and checks the image and creates two worktrees in one repository.
  - It runs `sbx up` twice in the first worktree and once in the second, checking identity reuse and distinct identities and loopback ports.
  - It runs `sbx exec -- ./scripts/test.sh` and `sbx run -- ./scripts/test.sh`.
  - It runs `sbx exec -- sh -c 'echo a > "$SBX_DATA_DIR/marker"'` in the first worktree and checks that `sbx exec -- test ! -e /var/lib/sbx-app/marker` succeeds in the second.
  - It removes the sandboxes it created, without asserting volume deletion or HTTP behavior.

The implementation is complete when the automated tests pass, both fixtures express their isolation and security behavior without stack-specific code, a README documents installation from source, the CLI, and the configuration, and the smoke test is reproducible. **Report which real-host behaviors the smoke test exercised; the rest remain unverified even when it passes.**

## Backend verification

On a real local msb 0.7.3 host, `msb context --format json` confirmed local selection via `MSB_BACKEND`; its JSON reports `{"kind": "local", "source": "MSB_BACKEND"}`, so the effective backend is the `kind` field, not a `backend` field. `msb image inspect alpine --format json` reported a manifest `digest` and config `digest`; `msb inspect --format json` reported `active_config` and `config` with `manifest_digest`, labels, and loopback published ports, plus `status` and `created_at`. `msb exec` returned the guest's exit code 37. A live `msb modify --label` failed and required `--restart` or `--next-start`; the label remained unchanged. The test sandbox was removed. A later named-volume probe confirmed that `msb volume inspect` reports directory kind and quota, and disk kind and capacity, in human-readable text; both probe volumes and the sandbox were removed. `msb volume inspect --help` offers no structured output option. `msb volumes --format json` returns records with `name`, `kind`, `capacity_bytes`, and `quota_mib`; existing directory volumes showed null capacity and quota. Populated disk capacity and directory quota values have not yet been observed in JSON. msb 0.7.3 accepted two running sandboxes that both declared `127.0.0.1:49152` for different guest ports. It also accepted a second sandbox declaring `127.0.0.1:49153` while the first sandbox served HTTP through that port; HTTP still returned 200 afterward. All probes were removed. Inspection of a stopped sandbox showed `active_config: null`, with its labels, image digest, and declared ports still available under `config`.

Still unverified; confirm before implementing dependent behavior:

- whether `msb volumes --format json` populates `capacity_bytes` and `quota_mib` for disk and quota-limited directory volumes;
- whether `msb exec` forwards signals;
- whether `--mount-owned` accepts a directory `quota` (v1 rejects it until confirmed);
- whether `msb start` rereads a `--secret` from its own environment after a stop, which would require sbx to supply the secret on every start.

## Backend references

- [CLI configuration](https://docs.microsandbox.dev/cli/configuration): YAML schema, environment substitution, mounts, networking, and relative paths.
- [Sandbox commands](https://docs.microsandbox.dev/cli/sandbox-commands): create, exec, inspect, modify, remove, labels, published ports, and owned mounts.
- [Image commands](https://docs.microsandbox.dev/cli/image-commands) and [volume commands](https://docs.microsandbox.dev/cli/volume-commands): import, inspection, and named volumes.
- [Secrets](https://docs.microsandbox.dev/sandboxes/secrets) and [TLS inspection](https://docs.microsandbox.dev/networking/tls): placeholders, destination checks, and guest trust.
- [Local or cloud](https://docs.microsandbox.dev/operations/backends) and [`msb context`](https://docs.microsandbox.dev/cli/management#msb-context): backend selection.
