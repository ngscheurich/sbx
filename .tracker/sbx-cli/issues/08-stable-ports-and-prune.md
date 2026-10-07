# Reserve loopback ports safely across worktrees

Status: resolved
Blocked by: 06

## Goal

Implement named TCP ports for persistent sandboxes using a locked, atomically updated registry under the host state directory. Reuse a Sandbox identity's reservations or choose from 4001–4099. Check existing local msb sandboxes' published ports as well as registry entries: msb 0.7.3 accepted a duplicate published port even while the first guest served HTTP. Preserve reservations through `stop` and `rm`; add `sbx port prune` for truly missing sandboxes. Disposable runs publish no ports.

## Acceptance

- Fake msb tests cover concurrent callers, multiple worktrees, unmanaged msb sandboxes occupying candidates, stale registry records, bind/probe races, exhausted range, and backend-inspection failure. Never assume msb rejects a duplicate port or remove a partially created sandbox automatically.
- Inspect reports published ports for a created sandbox; discrepancy handling matches [the spec](../spec.md#port-reservations), and read-only commands report without correcting or reserving.
- `port prune` keeps stopped sandboxes' reservations and refuses to prune when inspection fails. `rm` keeps its reservation for future recreation.

## References

[Port reservations and backend verification](../spec.md).

## Comments (addendum 1)

Implemented across `internal/config` (the `[ports.<name>]` table: required
`guest` in 1–65535, `[a-z][a-z0-9_]*` names, rejected with
`egress = "none"`), `internal/state/ports.go` (the registry:
`$XDG_STATE_HOME/sbx/ports/registry.json`, read-modify-write under an
flock'd `registry.lock` with atomic file replacement), `internal/ports`
(the policy: Reserve, Reconcile, Discrepancies, Prune), `internal/msb`
(the `--publish` translation, tolerant published-port parsing, and the
`PortsInUse` backend sweep), and the CLI wiring (`up`/`exec` reserve
before creation and reconcile after; `status` and `plan` report
read-only; `port prune` is dispatched), with tests in each package plus
`internal/cli/ports_test.go`.

Behavior, all test-pinned against the fake:

- Before creating a persistent sandbox, sbx reuses the identity's
  reservations or picks from 4001–4099 under the registry lock,
  skipping ports any reservation holds, ports the backend's sandboxes
  publish (`ls` + per-sandbox `inspect` — the fake never rejects a
  duplicate publish, matching msb 0.7.3's observed behavior), and ports
  occupied on the loopback (a real bind probe). Backend-inspection or
  probe failures abort before any resource change; an exhausted range
  fails before create. Concurrent callers receive disjoint ports.
- After creation — and on every later `up`/`exec` — what `msb inspect`
  reports is authoritative: stale registry entries are corrected unless
  the correction would take a port another reservation holds, in which
  case the command fails, keeps the registry, and never removes the
  sandbox to retry the port.
- `status` and `plan` report the published mappings and registry
  discrepancies without correcting or reserving; `plan` shows a port
  without a reservation as chosen at creation.
- `sbx port prune` removes reservations for sandboxes the listing no
  longer knows, keeps stopped ones, refuses when the listing fails, and
  reports nothing-to-prune on an empty registry. `rm` and `stop` keep
  reservations; recreation reuses them.
- Disposable runs publish no ports and reserve nothing.

The stateful-web fixture landed with its spec-defined content
(`[ports.web]` among them); its test strips only the `image_check` line
until ticket 10, deriving the variant from the fixture file. The
`--publish` flag spelling is UNVERIFIED on a real host — msb's create
surface for ports was never probed — and is pinned in one place
(`msb.Publish.spec`) for a later probe to confirm or correct, as is the
inspect `ports` report shape (`msb.PublishedPorts` parses every
plausible shape and fails closed).

Still unverified on a real host: the `--publish` spelling and inspect
port shape, and ports end-to-end against a live msb (the smoke test
arrives with ticket 12). The race detector remains unavailable in this
environment (no cgo toolchain); the concurrency tests pass without it.
