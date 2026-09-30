# Bootstrap once, recover explicitly, and serialize creation

Status: ready-for-agent
Blocked by: 06

## Goal

Run Bootstrap after each new persistent sandbox and disposable run is created, through the configured guest shell in the Workspace. Record persistent completion atomically in host state only after success, keyed by Sandbox identity and msb `created_at` with the hash of the definition that ran. Serialize persistent create/start/bootstrap under a per-sandbox cross-process lock; never hold it while a user's guest command runs. Add `up --retry-bootstrap` and incomplete/stale Bootstrap reporting.

## Acceptance

- A failed or interrupted persistent Bootstrap is not retried by plain `up`, but `exec` can still be used for repair with a warning; `--retry-bootstrap` explicitly retries. Changed Bootstrap is reported without Creation drift. A stale marker from a removed/recreated sandbox is not reused.
- A disposable Bootstrap failure always removes its sandbox, including Sandbox volumes. A caller waiting on the lock gets a message, and a concurrent creator does not run Bootstrap twice.
- Tests simulate failure after the guest code succeeds but before the completion marker is persisted. The next `up` remains incomplete; neither `--allow-stale` nor a read-only command marks it complete. No live label mutation or implicit restart is used.

## References

[Bootstrap and locks](../spec.md); [ADR-0002](../../../docs/adrs/0002-persistent-sandbox-safety.md).
