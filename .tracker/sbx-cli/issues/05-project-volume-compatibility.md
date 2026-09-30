# Check Project volumes before creation

Status: ready-for-agent
Blocked by: 01, 04

## Goal

Implement stable Project volume names derived from the common Git directory and logical name, Sandbox volume translation, and compatibility checking for every declared Project volume before mutating a sandbox. Read the machine-readable `msb volumes --format json` listing; ticket 01 must first confirm populated `quota_mib` and `capacity_bytes`. A mismatch in kind, size, or quota fails with both definitions and a suggestion to choose another logical name. `sbx plan` reports conflicts without changing anything. Never delete a Project volume.

## Acceptance

- Fake msb and temporary linked-worktree tests prove sibling worktrees share names, unrelated clones do not, equal definitions reuse storage, and incompatible definitions fail before creation even when branches disagree.
- Sandbox volumes are private and removed with their sandbox; Project volumes remain after removal and across disposable runs.
- Missing or malformed volume inspection is not interpreted as an empty list or compatible definition. Document numeric unit conversion from TOML sizes to JSON bytes/MiB and test quota and disk capacity with non-null values.

## References

[Volume fields and namespacing](../spec.md); [ADR-0003](../../../docs/adrs/0003-project-volume-namespacing.md).
