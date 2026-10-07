# Check Project volumes before creation

Status: resolved
Blocked by: 01, 04

## Goal

Implement stable Project volume names derived from the common Git directory and logical name, Sandbox volume translation, and compatibility checking for every declared Project volume before mutating a sandbox. Read the machine-readable `msb volumes --format json` listing; ticket 01 must first confirm populated `quota_mib` and `capacity_bytes`. A mismatch in kind, size, or quota fails with both definitions and a suggestion to choose another logical name. `sbx plan` reports conflicts without changing anything. Never delete a Project volume.

## Acceptance

- Fake msb and temporary linked-worktree tests prove sibling worktrees share names, unrelated clones do not, equal definitions reuse storage, and incompatible definitions fail before creation even when branches disagree.
- Sandbox volumes are private and removed with their sandbox; Project volumes remain after removal and across disposable runs.
- Missing or malformed volume inspection is not interpreted as an empty list or compatible definition. Document numeric unit conversion from TOML sizes to JSON bytes/MiB and test quota and disk capacity with non-null values.

## References

[Volume fields and namespacing](../spec.md); [ADR-0003](../../../docs/adrs/0003-project-volume-namespacing.md).

## Answer

Implemented and tested against the fake msb with temporary linked worktrees.

- `msb volumes --format json` is read through `msb.CLI.Volumes`
  (`internal/msb/volumes.go`). A failed command, unparseable output, or a
  record without a name or kind is an error, never an empty list.
- The compatibility check lives in the new `internal/volumes` package:
  every declared Project volume (logical name, derived backend name, kind,
  size, quota, exposed by the translation as `Translation.ProjectVolumes`)
  is compared with the existing record before any sandbox mutation
  (`sbx run`, and `sbx up`/`sbx exec` on the creation path) and in
  `sbx plan`, which reports conflicts and failed inspections without
  changing anything. A mismatch in kind, size, or quota fails naming the
  volume, both definitions, and a suggested new logical name; sbx never
  resizes, overwrites, or deletes an existing volume.
- Unit conversion is documented in the `volumes` package doc: TOML sizes
  convert to bytes in binary units (1K=1024, 1M=1048576, 1G=1073741824)
  and compare with `capacity_bytes` directly; quotas compare with
  `quota_mib` × 1048576. Non-null capacity (8589934592 for 8G) and quota
  (4096 MiB for 4G) are covered by tests.
- Ticket 01 caveat: a populated `quota_mib` was never observed (no msb
  CLI option sets a named volume's quota; `capacity_bytes` was confirmed).
  The check therefore fails closed: a declared quota against a null report
  is a conflict, and the `--mount-named NAME:DEST:kind/size/quota` option
  spelling (mirroring the verified `--mount-owned` options) is marked
  UNVERIFIED in `internal/msb/msb.go`.
- The fake msb now keeps a volume store: named volumes persist (removed
  by nothing sbx does), owned volumes die with their sandbox and at the
  end of a disposable run. Tests prove sibling worktrees share names,
  unrelated clones do not, equal definitions reuse storage, conflicting
  branches fail before creation, and inspection failures fail closed.
- Note: the work was left uncommitted — this container's `.git` points at
  a gitdir (`/Users/nick/...`) that does not exist here, so git cannot
  run.
