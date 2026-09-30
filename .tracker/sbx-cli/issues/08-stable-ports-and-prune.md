# Reserve loopback ports safely across worktrees

Status: ready-for-agent
Blocked by: 06

## Goal

Implement named TCP ports for persistent sandboxes using a locked, atomically updated registry under the host state directory. Reuse a Sandbox identity's reservations or choose from 4001–4099. Check existing local msb sandboxes' published ports as well as registry entries: msb 0.7.3 accepted a duplicate published port even while the first guest served HTTP. Preserve reservations through `stop` and `rm`; add `sbx port prune` for truly missing sandboxes. Disposable runs publish no ports.

## Acceptance

- Fake msb tests cover concurrent callers, multiple worktrees, unmanaged msb sandboxes occupying candidates, stale registry records, bind/probe races, exhausted range, and backend-inspection failure. Never assume msb rejects a duplicate port or remove a partially created sandbox automatically.
- Inspect reports published ports for a created sandbox; discrepancy handling matches [the spec](../spec.md#port-reservations), and read-only commands report without correcting or reserving.
- `port prune` keeps stopped sandboxes' reservations and refuses to prune when inspection fails. `rm` keeps its reservation for future recreation.

## References

[Port reservations and backend verification](../spec.md).
