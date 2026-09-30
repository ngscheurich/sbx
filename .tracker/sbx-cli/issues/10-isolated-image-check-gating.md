# Require isolated image checks in both execution modes

Status: ready-for-agent
Blocked by: 06, 09

## Goal

When `image_check` is declared, create a check sandbox from the exact image contents without Workspace, volumes, secrets or project network policy; copy and run the project-owned script, then remove the check sandbox on every exit path. Record successes in host state keyed by image content identity and script hash. `build` fails on a failing check; neither `up`, `exec`, nor `run` uses an image without a matching successful check. Recheck the image identity before sandbox creation and check the image of an existing persistent sandbox even with `--allow-stale`.

## Acceptance

- Fake msb tests cover prebuilt and built images, changed tags or scripts, failed inspections, script failure, guest cancellation, cleanup, and denial before any existing persistent sandbox or its data is touched. A failed check never records success.
- `plan` reports known, pending, or unresolvable check status without launching a sandbox or writing state.
- Add the stateful web fixture's `image-check.sh` unchanged; its missing Workspace is part of the test.

## References

[Image checks and stateful fixture](../spec.md); [ADR-0004](../../../docs/adrs/0004-isolated-image-checks.md).
