# Build and import an image explicitly

Status: ready-for-agent
Blocked by: 02, 03

## Goal

Add `sbx build` for `[build]`: invoke Docker/buildx with context, Dockerfile, target, platform and tag from project configuration, export to a temporary archive, import with `msb load --input`, and clean the archive unless kept for debugging. No other command builds images. A missing locally built image instructs the caller to build; a missing prebuilt image may be pulled. Compare image content digests, not tags, in Creation drift detection.

## Acceptance

- Fake Docker and msb tests verify exact arguments, import ordering, cleanup after success/failure/cancellation, actionable missing-tool errors, and the no-build-recipe error.
- Test a rebuild that changes content behind the same tag: an existing persistent sandbox is drifted, not replaced; disposable runs may use the new image. No implicit build occurs on `up`, `exec`, or `run`.
- `image_check` remains rejected before a mutation until ticket 10 implements its isolated check. Use a temporary archive outside the repository and never write a generated sandbox YAML.

## References

[Images and build](../spec.md); [ADR-0006](../../../docs/adrs/0006-drive-msb-through-its-cli.md).
