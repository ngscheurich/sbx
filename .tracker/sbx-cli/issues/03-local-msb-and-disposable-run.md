# Run a basic disposable sandbox through local msb

Status: resolved
Blocked by: 02

## Goal

Add a testable msb subprocess seam and `sbx run [-- <argv...>]` for a prebuilt image with base resources, the Workspace, and `public` or `none` egress. Check `msb context --format json` under `MSB_BACKEND=local` and refuse a managed cloud override. Create a uniquely named sandbox with owned and mode labels, execute argv directly or the configured shell, forward standard streams, propagate guest exit status, and remove the sandbox after success, failure, or cancellation. Missing prebuilt images may be pulled. Keep the image's ENTRYPOINT and CMD out of guest command selection.

## Acceptance

- Fake msb tests assert exact argument order, environment boundaries, local-context refusal, stdin/stdout/stderr and exit-status propagation, and cleanup on guest or creation failure. A creation failure must not remove an existing sandbox with the same name.
- No ports on disposable runs. Reject mounts, volumes, secrets, Bootstrap, image checks, and other settings not yet translated before creating anything; later tickets add them.
- Signal forwarding needs the real-host result in ticket 01 before its behavior is considered verified; record any interim implementation as unverified.

## References

[Disposable runs, backend and images](../spec.md); [ADR-0006](../../../docs/adrs/0006-drive-msb-through-its-cli.md); [ADR-0007](../../../docs/adrs/0007-ignore-image-entrypoint-and-cmd.md).

## Comments

Implemented on `main`.

- `internal/msb` is the testable msb subprocess seam (ADR-0006): `LocalContext`
  (parses `msb context --format json` and refuses any non-local selection),
  `Create`, `Exec`, `Remove`, `PullIfMissing` (`msb image inspect` then
  `msb image pull` for a missing prebuilt image), and `WriteNetConfNone` for
  `egress = "none"`. Every subprocess runs with `MSB_BACKEND=local` forced in
  its environment, overriding any inherited value.
- `sbx run [-- <argv...>]` creates a uniquely named sandbox
  (`<identity>-run-<6 hex>`) with `sbx.managed=1`, `sbx.mode=disposable`, and
  `sbx.worktree` labels; mounts the Workspace read-write at `/workspace`; runs
  argv directly (or the configured shell with no argv) with `--workdir
  /workspace`; forwards stdin/stdout/stderr; and propagates the guest exit
  status. The sandbox is removed on success, failure, and cancellation
  (`context.WithoutCancel` keeps cleanup alive); a creation failure removes
  nothing.
- Egress `allowlist` is rejected before any msb call; mounts, volumes,
  secrets, Bootstrap, image checks, and ports remain rejected by the strict
  config loader.
- Shared fake-msb test double lives in `internal/testsupport`; tests in both
  packages pin exact argv, the environment boundary, and stream/exit
  propagation against it.
- **Unverified on a real host (pending ticket 01):** signal forwarding into
  the guest (sbx currently SIGTERMs the msb subprocess's process group and
  exits with 128+sig), the `--workdir` flag spelling on `msb exec`, the
  `--mount source:target[:ro]` and `--net-conf` flag spellings, the generated
  net-conf file schema, and whether `msb create`/`msb image pull` accept these
  forms as written. All are marked UNVERIFIED in code comments.
