# Translate restricted CLI project configuration

Status: ready-for-agent
Blocked by: 02, 03

## Goal

Extend strict TOML validation and the msb adapter for Workspace target, bind and tmpfs mounts, environment, network egress/allowlist/DNS, and destination-scoped secrets. Implement the complete `fixtures/restricted-cli/` files from [the spec](../spec.md#restricted-cli-fixture); copy them into temporary Git repositories in tests. Use the same translation in a disposable run and an inspectable, redacted Plan. Reject any declared unsupported setting before a backend mutation. Parsing and translating the restricted fixture's Project volumes is useful now, but executing that fixture with Project volumes waits for ticket 05's preflight checks.

## Acceptance

- Tests assert exact msb arguments and the generated network policy file, resolved bind paths, mount target/name/size validation, egress restrictions, and the fixture's Project volume declarations in Plan translation only (not yet compatibility checks or creation). The sandbox uses the worktree root even when invoked below it.
- A missing `from_env` variable fails before any resource changes. Host values reach msb only through its subprocess environment; plans, errors, state and arguments contain names/placeholders, never values.
- Unknown keys, conflicting mount targets, invalid domain patterns, `egress = "none"` with ports or DNS, and invalid size/scope combinations fail closed. Plan never writes sbx state or project files.
- Until ticket 05 implements Project volume conflict checks, fail closed on creation whenever Project volumes are declared; do not leave the fixture's volume mounts silently unprotected.

## References

[Configuration, secrets, msb translation and restricted fixture](../spec.md); [ADR-0001](../../../docs/adrs/0001-sbx-native-project-configuration.md).
