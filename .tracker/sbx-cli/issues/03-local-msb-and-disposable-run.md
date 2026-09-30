# Run a basic disposable sandbox through local msb

Status: ready-for-agent
Blocked by: 02

## Goal

Add a testable msb subprocess seam and `sbx run [-- <argv...>]` for a prebuilt image with base resources, the Workspace, and `public` or `none` egress. Check `msb context --format json` under `MSB_BACKEND=local` and refuse a managed cloud override. Create a uniquely named sandbox with owned and mode labels, execute argv directly or the configured shell, forward standard streams, propagate guest exit status, and remove the sandbox after success, failure, or cancellation. Missing prebuilt images may be pulled. Keep the image's ENTRYPOINT and CMD out of guest command selection.

## Acceptance

- Fake msb tests assert exact argument order, environment boundaries, local-context refusal, stdin/stdout/stderr and exit-status propagation, and cleanup on guest or creation failure. A creation failure must not remove an existing sandbox with the same name.
- No ports on disposable runs. Reject mounts, volumes, secrets, Bootstrap, image checks, and other settings not yet translated before creating anything; later tickets add them.
- Signal forwarding needs the real-host result in ticket 01 before its behavior is considered verified; record any interim implementation as unverified.

## References

[Disposable runs, backend and images](../spec.md); [ADR-0006](../../../docs/adrs/0006-drive-msb-through-its-cli.md); [ADR-0007](../../../docs/adrs/0007-ignore-image-entrypoint-and-cmd.md).
