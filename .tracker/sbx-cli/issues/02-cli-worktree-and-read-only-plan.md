# Start sbx with worktree identity and a read-only plan

Status: resolved
Blocked by: none

## Goal

Create the Go module `github.com/ngscheurich/sbx` and `cmd/sbx`. From a nested directory in a temporary Git worktree, `sbx plan` finds the worktree root and that checkout's `sbx.toml`, derives the configuration-independent Sandbox identity and Project volume namespace, validates the required base fields, and describes the intended sandbox without changing host or project state. `sbx` and `sbx --help` list only v1 commands. `exec` and `run` remain the command names for now; see [the spec](../spec.md).

## Acceptance

- Tests use temporary Git repositories, linked worktrees, and missing/outside-worktree cases; identity is stable across config edits and branch switches, and distinct for same-named worktrees at different paths.
- Strict TOML parsing rejects unknown keys, wrong types, invalid required resources and network mode before any action. Explicitly reject not-yet-supported fields rather than silently ignoring them in this first slice.
- The initial Plan is clearly partial until later tickets extend translation, drift, and image-check reporting. It does not create sbx state, pull an image, or call a mutating backend command.

## References

[Discovery, identity, configuration, commands and read-only behavior](../spec.md); [domain vocabulary](../../../CONTEXT.md).

## Comments

Implemented in db98804, 430038e, and e9cf0ce on `main`.

- Go module `github.com/ngscheurich/sbx` and `cmd/sbx`; `sbx plan` finds the
  worktree root and checkout's `sbx.toml` from a nested directory, derives
  the Sandbox identity and Project volume namespace (ADR-0003, ADR-0005),
  and renders a clearly-partial preview with no host or project writes.
- Strict TOML: unknown keys, wrong types, invalid sizes, egress, and
  allowlist entries rejected; all not-yet-translatable fields rejected
  explicitly (`workspace`, `mounts`, `volumes`, `secrets`, `ports`, `env`,
  `build`, `bootstrap`, `image_check`, `dns_nameservers`).
- Help lists only v1 commands; `exec` and `run` keep their names.
- Tests cover temp repos, linked worktrees, missing/outside-worktree cases,
  identity stability across config edits and branch switches, and distinct
  identities for same-named worktrees at different paths; macOS symlink
  resolution is handled (the `/private` prefix).
- Choice deferred to ticket 05: exact literal Project volume name shape is
  `<namespace hash>-<logical name>`.
