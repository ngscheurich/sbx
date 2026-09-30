# Start sbx with worktree identity and a read-only plan

Status: ready-for-agent
Blocked by: none

## Goal

Create the Go module `github.com/ngscheurich/sbx` and `cmd/sbx`. From a nested directory in a temporary Git worktree, `sbx plan` finds the worktree root and that checkout's `sbx.toml`, derives the configuration-independent Sandbox identity and Project volume namespace, validates the required base fields, and describes the intended sandbox without changing host or project state. `sbx` and `sbx --help` list only v1 commands. `exec` and `run` remain the command names for now; see [the spec](../spec.md).

## Acceptance

- Tests use temporary Git repositories, linked worktrees, and missing/outside-worktree cases; identity is stable across config edits and branch switches, and distinct for same-named worktrees at different paths.
- Strict TOML parsing rejects unknown keys, wrong types, invalid required resources and network mode before any action. Explicitly reject not-yet-supported fields rather than silently ignoring them in this first slice.
- The initial Plan is clearly partial until later tickets extend translation, drift, and image-check reporting. It does not create sbx state, pull an image, or call a mutating backend command.

## References

[Discovery, identity, configuration, commands and read-only behavior](../spec.md); [domain vocabulary](../../../CONTEXT.md).
