# Translate restricted CLI project configuration

Status: resolved
Blocked by: 02, 03

## Goal

Extend strict TOML validation and the msb adapter for Workspace target, bind and tmpfs mounts, environment, network egress/allowlist/DNS, and destination-scoped secrets. Implement the complete `fixtures/restricted-cli/` files from [the spec](../spec.md#restricted-cli-fixture); copy them into temporary Git repositories in tests. Use the same translation in a disposable run and an inspectable, redacted Plan. Reject any declared unsupported setting before a backend mutation. Parsing and translating the restricted fixture's Project volumes is useful now, but executing that fixture with Project volumes waits for ticket 05's preflight checks.

## Acceptance

- Tests assert exact msb arguments and the generated network policy file, resolved bind paths, mount target/name/size validation, egress restrictions, and the fixture's Project volume declarations in Plan translation only (not yet compatibility checks or creation). The sandbox uses the worktree root even when invoked below it.
- A missing `from_env` variable fails before any resource changes. Host values reach msb only through its subprocess environment; plans, errors, state and arguments contain names/placeholders, never values.
- Unknown keys, conflicting mount targets, invalid domain patterns, `egress = "none"` with ports or DNS, and invalid size/scope combinations fail closed. Plan never writes sbx state or project files.
- Until ticket 05 implements Project volume conflict checks, fail closed on creation whenever Project volumes are declared; do not leave the fixture's volume mounts silently unprotected.

## References

[Configuration, secrets, msb translation and restricted fixture](../spec.md); [ADR-0001](../../../docs/adrs/0001-sbx-native-project-configuration.md).

## Comments

Implemented on `main`.

- `internal/config` parses and validates the full translated surface:
  `[workspace]` (default `/workspace`), `[[mounts]]` bind/tmpfs with
  duplicate-target rejection (including the Workspace target), `[volumes]`
  with scope/kind/size/quota rules and `[a-z][a-z0-9_]*` names, `[env]` and
  `[secrets]` with `[A-Za-z_][A-Za-z0-9_]*` names, `dns_nameservers`
  validated as IP or IP:PORT and rejected under `egress = "none"`, and
  allowlist entries shared with secrets. `ports`, `build`, `bootstrap`, and
  `image_check` remain explicitly rejected.
- New `internal/translate` is the shared seam for both modes: it resolves
  bind sources (absolute, `~/...` against home, relative to the worktree
  root; a missing source fails before anything is created), renders
  `msb.CreateOptions`, and generates the secret-name map. `sbx run` calls
  `CheckSecretEnv` before any msb call; `plan` needs only names and skips
  it. All names in plan and arguments; values travel only through the msb
  subprocess's environment.
- `internal/msb.CreateOptions` grew `Tmpfs`, `Named`, `Owned`, `Env`,
  `NetRules`, `DnsNameservers`, `TLSIntercept`, and `SecretConf`;
  `CreateArgs` is now exported and pinned by tests. Flag spellings come from
  `msb create --help` on the installed 0.7.5: `--tmpfs PATH:SIZE[:OPTIONS]`,
  `--mount-named NAME:DEST`, `--mount-owned DEST[:OPTIONS]` with
  `kind=disk,size=S` for disks, `-e`, `--net-rule allow@<entry>`,
  `--net-default-egress deny`, `--tls-intercept`, `--dns-nameserver`, and
  `--secret-conf PATH`.
- Translation decisions recorded in the spec's "Translation to msb":
  allowlist egress uses native `--net-rule` flags plus deny-by-default
  egress and `--tls-intercept` (strict hostname rules need intercepted
  HTTPS); the generated `--net-conf` file idea is dropped entirely, and the
  one generated file is now the `--secret-conf` secret-name map, written
  outside the repository and removed after the run. It holds `${NAME}`
  source references, never values; msb resolves them from its own
  environment at start. Secret guest names come from the map key per the
  secrets documentation; the exact guest-variable spelling is recorded as
  unverified pending ticket 01's host check.
- `sbx run` fails closed on declared Project volumes before any msb call
  until ticket 05's compatibility checks exist. `sbx plan` renders the full
  translation for the persistent mode — mounts, volumes with namespaced
  backend names, environment, network policy, redacted secret map, and the
  `msb create` argument preview — and reports translation failures such as
  a missing bind source instead of a partial plan.
- `fixtures/restricted-cli/` is complete per the spec (Dockerfile, sbx.toml,
  host-notes.txt, host-state/.keep). Tests copy it into temporary
  repositories: verbatim, it fails closed before any msb call ([build] and
  Project volumes are not executable yet); with `[build]` and Project
  volumes removed, the full run argv is pinned, and with only `[build]`
  removed the Plan shows the Project-volume translation. The ticket 12
  fixture test will adopt the executable form as later tickets land.
- UNVERIFIED on a real host: the `:noexec` tmpfs option spelling, and
  whether a `--secret-conf` map keys the guest environment variable as
  documented; both are recorded in the spec's unverified list.

## Comments (addendum)

Code-review fixes folded in: bind/tmpfs field cross-assignments now fail
closed (`size` on a bind, `read_only` on a tmpfs, `noexec` on a bind), test
TOML variants are derived from the fixture file with a
`testsupport.FixtureTOML` strip helper so fixture and tests cannot drift,
and the plan's sandbox-volume line no longer repeats the target.

## Comments (addendum 2)

Real-host testing (the first manual pass) caught a translation bug: ticket
04 kept the `--mount-dir` flag that ticket 03 pinned when the only bind was
the Workspace, and msb rejects a file source with "mount-dir source is not a
directory". sbx now classifies each resolved bind source with the `os.Stat`
it already performs and emits `--mount-file` for a regular file,
`--mount-dir` for a directory. Pinned tests updated on both surfaces.

## Comments (addendum 3)

Manual verification on msb 0.7.5 resolved one unverified item: the `:ro`
bind option is honored. The owner layered the fixture's `read_only = true`
file bind over the Workspace mount itself (`target` inside `/workspace`)
and the guest write to that path was denied, confirming both that msb
applies `:ro` on `--mount-file` and that per-mount-point read-only layers
work over read-write ones. The spec's backend verification records the
observation; `:ro` on `--mount-dir` and the `:noexec` tmpfs spelling are
still unverified.

## Comments (addendum 4)

Manual verification on msb 0.7.5: `msb ls --format json` records carry no
label field at all (only name, image, status, and created_at), while
`msb inspect <name> --format json` reports labels under `active_config`
(or `config` when stopped). Ownership and attribution checks in later tickets must use
`msb inspect`, not the listing; the spec's backend verification records
the schema difference.

## Comments (addendum 5)

Manual verification on msb 0.7.5 resolved the last ticket-04 secret
question: with a `--secret-conf` map, the guest environment variable is
named after the map key (`DEMO_TOKEN`), and its value is the placeholder
`$MSB_DEMO_TOKEN`, which msb substitutes on requests to the secret's
allowed destinations. The spec's backend verification records the
observation and the unverified list now holds only the `:noexec` tmpfs
spelling and `:ro` on `--mount-dir`.
