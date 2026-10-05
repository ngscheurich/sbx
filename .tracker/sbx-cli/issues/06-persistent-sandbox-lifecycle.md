# Create and reuse a safe persistent sandbox

Status: resolved
Blocked by: 03, 04

## Goal

Add `sbx up`, `exec`, `status`, `logs`, `stop`, and `rm --yes` for the translated configuration. Create a single stable Sandbox identity per worktree. Inspect ownership before any mutation; do not adopt a same-named unowned sandbox. Capture Creation-time settings and actual image digest in a host-state snapshot; detect missing snapshots and drift without recreating a sandbox. `up`/`exec` refuse drift unless `--allow-stale`, and `status`/`logs` remain available. `exec` runs the guest command and leaves its sandbox running.

## Acceptance

- Tests exercise creation, reuse, stopped-to-running transition, guest exit status, unowned-name refusal, changed configuration or image digest, missing snapshot, and `rm` confirmation in interactive and noninteractive contexts. Removing a persistent sandbox does not remove Project volumes or port reservations.
- Reject declared Bootstrap, image checks, Project volumes, and published ports until their tickets land; never silently skip them. Secret-bearing restarts wait for ticket 11 and the ticket 01 host result.
- `status` and `logs` must not write sbx state or mutate the backend; inspection reads `config` when `active_config` is null on a stopped sandbox.

## References

[Persistent lifecycle, ownership, drift and read-only commands](../spec.md); [ADR-0002](../../../docs/adrs/0002-persistent-sandbox-safety.md); [ADR-0005](../../../docs/adrs/0005-configuration-independent-sandbox-identity.md).

## Comments (addendum 1)

Implemented in `internal/cli/persistent.go` with tests in
`internal/cli/persistent_test.go`, on top of the stateful fake-msb seam
(`ls`/`inspect`/`start`/`stop`/`remove` observe earlier creates) and the
`internal/state` host-state package (creation snapshots under
`${XDG_STATE_HOME:-~/.local/state}/sbx/snapshots/<sandbox>.json`, atomic
writes, and the Drift report). `cli.Run` now dispatches `up`, `exec`,
`status`, `logs`, `stop`, and `rm`; help lists them and keeps `build` and
`port prune` as later releases.

Safety rules, all test-pinned against the fake:

- Ownership is decided per sandbox through `msb inspect`'s effective
  configuration layer (`active_config` when running, `config` when
  stopped — msb 0.7.5's `ls` carries no labels), never from a listing.
  An unowned same-named sandbox stops `up`/`exec`/`stop`/`rm` before any
  mutation, with `msb remove` named as the way out; `status` reports it.
- A creation snapshot is written only after `msb create` succeeds. A
  missing snapshot counts as drifted; a corrupt one is an error; an image
  that cannot be inspected fails closed as drift. `up` and `exec` refuse
  drift with both values named, and `--allow-stale` uses the sandbox
  anyway with a warning. sbx never recreates a drifted sandbox.
- Secret-bearing restarts are refused outright (not as drift:
  `--allow-stale` does not bypass them) until ticket 01's host check and
  ticket 11; creation with a secret works and the generated map never
  contains host values.
- `status` and `logs` are read-only: no sbx state writes, no backend
  mutations, no image pulls (inspect reads are fine), and drift is
  reported, never enforced.
- `rm` confirms interactively (`y`/`yes` only) or with `--yes`
  noninteractively; it removes the sandbox and its snapshot, lists the
  Sandbox volumes lost from the snapshot, and never issues a volume or
  port command, so Project volumes and (future) port reservations stay.

Deferred settings fail closed: Project volumes through the shared
preflight, Bootstrap/image-check/ports/build through strict
configuration parsing, and `up --retry-bootstrap` by name. Locks,
Bootstrap markers, ports, image checks, and secret restarts belong to
tickets 07, 08, 10, and 11.

Still unverified on a real host: the `msb start`, `stop`, and `logs`
subcommands have only fake-driven coverage, exec signal forwarding
remains ticket 01's open question, and the persistent lifecycle has not
been exercised against a real msb installation (the smoke test arrives
with ticket 12).
