# Finish persistent secrets and read-only views

Status: ready-for-agent
Blocked by: 01, 05, 06, 07, 08, 10

## Goal

Apply ticket 01's real-host result to secret-bearing sandbox restarts: supply throwaway host values again if msb requires them, or document the observed retained behavior without exposing values. Complete `sbx plan`, `status`, `logs`, and help for all supported fields: intended msb arguments and network policy, redacted secret names, image-check and Bootstrap status, Creation drift, Project volume conflicts, endpoints, and tentative ports. Read-only commands remain useful on a drifted sandbox and never mutate host or project state.

## Acceptance

- Tests use fake msb and Docker, missing secrets, stopped and drifted sandboxes, stale host markers, mismatched Project volumes, and nonempty port registries. Snapshots and status never contain host secret values.
- `plan` reveals no secrets or network-policy temp files; `status` reports observed ports without silently correcting reservations. `logs` forwards backend failures and output.
- If ticket 01 cannot verify restart substitution, explicitly fail closed for secret-bearing restarts and retain an open follow-up instead of guessing.

## References

[CLI, secrets, lifecycle, plans and read-only commands](../spec.md).
