# Complete fixtures, smoke test and user documentation

Status: ready-for-agent
Blocked by: 04, 05, 07, 08, 09, 10, 11

## Goal

Finish and test both acceptance projects exactly as described in [the spec](../spec.md#acceptance-fixtures). Add a README for `go install ./cmd/sbx`, all v1 commands and configuration; add an opt-in, reproducible real-host smoke test that reports missing msb, virtualization or Docker/buildx as **not run**, not success. Keep `exec` and `run` names pending the before-v1 naming revisit.

## Acceptance

- Automated tests copy fixture sources into temporary Git repositories and worktrees; use fake msb and Docker to assert both modes' full translation, cleanup, storage scopes, port registry, security failures, drift, Bootstrap, image checks, and exit status. No language- or stack-specific sbx logic.
- The opt-in test builds both images and exercises only the specified restricted CLI and two-worktree stateful web flows. It never changes fixture sources or real user volumes, and cleans only sandboxes it creates.
- Report precisely which real-host behaviors the smoke test exercised. Live network allowlist enforcement, secret destination enforcement, volume deletion, and HTTP behavior remain unverified if not actually tested.

## References

[Acceptance fixtures and verification](../spec.md); [CONTEXT.md](../../../CONTEXT.md).
