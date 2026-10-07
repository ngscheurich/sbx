# Finish persistent secrets and read-only views

Status: resolved
Blocked by: 01, 05, 06, 07, 08, 10

## Goal

Apply ticket 01's real-host result to secret-bearing sandbox restarts: supply throwaway host values again if msb requires them, or document the observed retained behavior without exposing values. Complete `sbx plan`, `status`, `logs`, and help for all supported fields: intended msb arguments and network policy, redacted secret names, image-check and Bootstrap status, Creation drift, Project volume conflicts, endpoints, and tentative ports. Read-only commands remain useful on a drifted sandbox and never mutate host or project state.

## Acceptance

- Tests use fake msb and Docker, missing secrets, stopped and drifted sandboxes, stale host markers, mismatched Project volumes, and nonempty port registries. Snapshots and status never contain host secret values.
- `plan` reveals no secrets or network-policy temp files; `status` reports observed ports without silently correcting reservations. `logs` forwards backend failures and output.
- If ticket 01 cannot verify restart substitution, explicitly fail closed for secret-bearing restarts and retain an open follow-up instead of guessing.

## References

[CLI, secrets, lifecycle, plans and read-only commands](../spec.md).

## Answer

Implemented in two parts, against the fake msb and Docker:

**Secret-bearing restarts.** Ticket 01 verified that `msb start` re-resolves
each secret from its own environment on every start (a stopped sandbox whose
host variable was absent failed with `invalid config: secret PROBE_TOKEN: host
environment variable PROBE_TOKEN is not set`). The restart refusal is gone:
`ensureRunning` now starts a stopped secret-bearing sandbox, with the host
values reaching msb only through its subprocess environment, and
`preparePersistent`'s `CheckSecretEnv` still refuses any missing variable
before any resource changes, naming it. The fake msb's `start` branch fails
the same way when `FAKE_MSB_START_REQUIRES_ENV` names an absent variable.
Tests: `TestSecretBearingRestartSuppliesHostValues` (restart succeeds, start
reaches the backend, snapshot and output never contain the value) and
`TestSecretBearingRestartFailsClosedOnMissingSecret` (no start call, error
names the variable).

**Plan's live-sandbox report.** `plan` now ends with an `existing sandbox:`
section when the backend holds one under the identity: backend state,
Creation drift (reported, never enforced — the plan stays usable on drift),
Bootstrap completion (complete/changed/incomplete), and the observed
published endpoints inspection is authoritative for, with unreadable
listings, failed drift evaluation, and failed marker reads reported rather
than guessed. An unowned same-named sandbox is reported as never adopted.
Listing and inspection are the plan's only backend calls, all read-only; the
closing "This plan is partial" note is gone. Tests in
`internal/cli/plan_live_test.go` cover the running and stopped sandbox,
drift, unowned, and the absent-sandbox case; `TestLogsForwardsBackendFailures`
covers logs' failure forwarding. `status`'s read-only port report and `logs`'
pass-through were already complete from tickets 06–08.
