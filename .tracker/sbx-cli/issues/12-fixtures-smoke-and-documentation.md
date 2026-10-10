# Complete fixtures, smoke test and user documentation

Status: resolved
Blocked by: 04, 05, 07, 08, 09, 10, 11

## Goal

Finish and test both acceptance projects exactly as described in [the spec](../spec.md#acceptance-fixtures). Add a README for `go install ./cmd/sbx`, all v1 commands and configuration; add an opt-in, reproducible real-host smoke test that reports missing msb, virtualization or Docker/buildx as **not run**, not success. Keep `exec` and `run` names pending the before-v1 naming revisit.

## Acceptance

- Automated tests copy fixture sources into temporary Git repositories and worktrees; use fake msb and Docker to assert both modes' full translation, cleanup, storage scopes, port registry, security failures, drift, Bootstrap, image checks, and exit status. No language- or stack-specific sbx logic.
- The opt-in test builds both images and exercises only the specified restricted CLI and two-worktree stateful web flows. It never changes fixture sources or real user volumes, and cleans only sandboxes it creates.
- Report precisely which real-host behaviors the smoke test exercised. Live network allowlist enforcement, secret destination enforcement, volume deletion, and HTTP behavior remain unverified if not actually tested.

## References

[Acceptance fixtures and verification](../spec.md); [CONTEXT.md](../../../CONTEXT.md).

## Comments

Implemented complete fixture acceptance coverage in `internal/cli/build/fixture_acceptance_test.go`, shared committed fixture copies in `internal/harness/seed.go`, executable stateful guest scripts, and the opt-in real-host test in `internal/cli/smoke/smoke_test.go`. Legacy acceptance tests now retain Image checks. The fake backend cleans disposable private storage on early failure as well as normal completion. `README.md` documents installation, commands, the current `network.policy` configuration surface, lifecycle safety, and verification limits; `exec` and `run` remain provisional.

Validation: `go build ./...`, targeted fixture and smoke-runner tests, `go test -race ./...`, `go vet ./...`, `go mod tidy -diff`, formatting, and diff checks passed. Standards review against `ee30078` found three issues (subprocess wait bounds, context parameter order, and host-path construction), all corrected; Spec review found none.

Real-host smoke: **not run** on this environment because Docker is missing and `/dev/kvm` is unavailable. No real-host behavior was verified in this completion. Fake-backed runner tests checked successful orchestration, missing-prerequisite reporting, cleanup after partial persistent creation/Bootstrap failure, and preservation of an unrelated sandbox. Live allowlist enforcement, secret destination enforcement, volume deletion, HTTP behavior, and guest signal forwarding remain unverified.
