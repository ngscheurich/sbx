# Create and reuse a safe persistent sandbox

Status: ready-for-agent
Blocked by: 03, 04

## Goal

Add `sbx up`, `exec`, `status`, `logs`, `stop`, and `rm --yes` for the translated configuration. Create a single stable Sandbox identity per worktree. Inspect ownership before any mutation; do not adopt a same-named unowned sandbox. Capture Creation-time settings and actual image digest in a host-state snapshot; detect missing snapshots and drift without recreating a sandbox. `up`/`exec` refuse drift unless `--allow-stale`, and `status`/`logs` remain available. `exec` runs the guest command and leaves its sandbox running.

## Acceptance

- Tests exercise creation, reuse, stopped-to-running transition, guest exit status, unowned-name refusal, changed configuration or image digest, missing snapshot, and `rm` confirmation in interactive and noninteractive contexts. Removing a persistent sandbox does not remove Project volumes or port reservations.
- Reject declared Bootstrap, image checks, Project volumes, and published ports until their tickets land; never silently skip them. Secret-bearing restarts wait for ticket 11 and the ticket 01 host result.
- `status` and `logs` must not write sbx state or mutate the backend; inspection reads `config` when `active_config` is null on a stopped sandbox.

## References

[Persistent lifecycle, ownership, drift and read-only commands](../spec.md); [ADR-0002](../../../docs/adrs/0002-persistent-sandbox-safety.md); [ADR-0005](../../../docs/adrs/0005-configuration-independent-sandbox-identity.md).
