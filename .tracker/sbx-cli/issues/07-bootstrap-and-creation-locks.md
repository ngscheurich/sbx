# Bootstrap once, recover explicitly, and serialize creation

Status: resolved
Blocked by: 06

## Goal

Run Bootstrap after each new persistent sandbox and disposable run is created, through the configured guest shell in the Workspace. Record persistent completion atomically in host state only after success, keyed by Sandbox identity and msb `created_at` with the hash of the definition that ran. Serialize persistent create/start/bootstrap under a per-sandbox cross-process lock; never hold it while a user's guest command runs. Add `up --retry-bootstrap` and incomplete/stale Bootstrap reporting.

## Acceptance

- A failed or interrupted persistent Bootstrap is not retried by plain `up`, but `exec` can still be used for repair with a warning; `--retry-bootstrap` explicitly retries. Changed Bootstrap is reported without Creation drift. A stale marker from a removed/recreated sandbox is not reused.
- A disposable Bootstrap failure always removes its sandbox, including Sandbox volumes. A caller waiting on the lock gets a message, and a concurrent creator does not run Bootstrap twice.
- Tests simulate failure after the guest code succeeds but before the completion marker is persisted. The next `up` remains incomplete; neither `--allow-stale` nor a read-only command marks it complete. No live label mutation or implicit restart is used.

## References

[Bootstrap and locks](../spec.md); [ADR-0002](../../../docs/adrs/0002-persistent-sandbox-safety.md).

## Comments (addendum 1)

Implemented across `internal/config` (the `[bootstrap]` table with a
required `run`, rejected when declared empty), `internal/state`
(`bootstrap.go` completion markers and `lock.go` per-sandbox flock
locks), `internal/cli/bootstrap.go` (assessment, the run-and-record
path, the disposable wrapper, and the waiting-lock helper), and the
persistent/disposable command wiring, with tests in
`internal/cli/bootstrap_test.go` and `internal/state/bootstrap_test.go`.

Behavior, all test-pinned against the fake:

- A new persistent sandbox is bootstrapped through `<shell> -c` in the
  Workspace right after creation. The marker
  (`$XDG_STATE_HOME/sbx/bootstrap/<sandbox>.json`) is written
  atomically, under the per-sandbox lock, only after the guest code
  succeeds, and binds the Sandbox identity, the sandbox's `created_at`,
  and the sha256 of the exact definition that ran. The next `up` does
  not rerun it.
- A failed Bootstrap (guest exit status), an interrupted one (ctx
  cancellation mid-exec), and a persistence failure after the guest
  code succeeded (via the `saveBootstrapMarker` seam) all leave the
  sandbox incomplete: plain `up` reports it and exits nonzero without
  rerunning, `--allow-stale` never marks it complete, `status` reports
  `bootstrap: incomplete` read-only, and `exec` still runs — with a
  warning — for repairs. `up --retry-bootstrap` runs the current
  definition and records completion only on success; without a
  `[bootstrap]` declaration it is an explicit error before any backend
  call.
- A marker whose `created_at` belongs to an earlier incarnation is
  stale: plain `up` reports it incomplete rather than reusing it, and
  a retry rebinds the marker to the current sandbox. `rm` clears the
  marker along with the snapshot.
- Editing `[bootstrap]` is not Creation drift: use is not blocked,
  nothing reruns, and `status`/`up` report the changed definition.
  Bootstrap does not participate in the snapshot's Definition at all.
- The per-sandbox lock (flock on `$XDG_STATE_HOME/sbx/locks/<id>.lock`,
  never deleted) serializes create/start/bootstrap across processes: a
  concurrent `up` prints a waiting message on stderr, then observes the
  completed marker and runs neither a second create nor a second
  Bootstrap. The lock is released before any user guest command runs;
  read-only commands never touch it.
- Disposable runs wrap the guest command as `<shell> -c <wrapper>
  sbx-bootstrap <argv...>`: Bootstrap runs first, and on failure the
  shell exits with Bootstrap's status so `msb run` removes the
  auto-named one-shot sandbox (Sandbox volumes included) — sbx still
  adds no cleanup step of its own, passes no `--name`, and the user
  argv passes through positionally, uninterpreted by any shell.
- The fake msb's call log switched to NUL-separated fields because
  Bootstrap scripts are multi-line; `plan` mentions a declared
  Bootstrap while keeping its no-state, no-backend purity.

Still unverified on a real host: that `msb run` removes a failed
auto-named one-shot's Sandbox volumes exactly as for a success, and
Bootstrap end-to-end against a real msb (the smoke test arrives with
ticket 12). The race detector was unavailable in this environment (no
cgo toolchain); the lock serialization test passes without it.
