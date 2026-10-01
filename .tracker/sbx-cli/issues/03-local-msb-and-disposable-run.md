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

[Disposable runs, backend and images](../spec.md); [ADR-0006](../../../docs/adrs/0006-drive-msb-through-its-cli.md). ADR-0007 was removed by decision of the owner (see addendum 3).

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
  exits with 128+sig), the `:ro` mount option spelling, and whether `msb
  remove --force` succeeds while the sandbox runs.

## Comments (addendum 3)

`--entrypoint ""` failed on a real host (`invalid config: entrypoint
executable must not be empty`), and the owner decided sbx should match msb's
native command semantics rather than fight them: ADR-0007 is removed, the
spec's command section and Disposable-runs section were rewritten, and sbx
no longer passes `--entrypoint` at all. `msb run`'s behavior — image CMD
replaced by the command, effective entrypoint preserved around it — now
applies to `sbx run`; `msb exec` (the persistent-sandbox path, ticket 06)
runs argv as given.

## Comments (addendum 2)

Real-host testing led to a design change: `sbx run` now delegates the whole
lifecycle to msb's native one-shot, `msb run` (single subprocess: create,
command, lifecycle), instead of composing `msb create` + `msb exec` + `msb
remove --force`. sbx passes `--name`, the owned/mode/worktree labels, the
Workspace `--mount-dir`, resources, `--no-net` for egress none, `--workdir`,
and an empty `--entrypoint` so the image entrypoint stays out of guest
command selection (ADR-0007); the spec's Disposable-runs section was updated
to match. Cleanup tests were removed with the cleanup step; fake-msb tests
pin the single run call, exit-status and stream propagation, and
interruption status. Still unverified: that `--entrypoint ""` actually
clears the image entrypoint, and what `msb run` leaves behind when the
guest fails or is interrupted.

## Comments (addendum)

Real-host debugging with msb 0.7.3 corrected the initial translation, which
had guessed several flag spellings. Confirmed from `msb create/exec/remove
--help` and recorded in the spec's backend verification: image is a
positional create argument with `--name`, resources are `--cpus`/`--memory`,
the Workspace bind is `--mount-dir SOURCE:DEST`, labels are repeatable
`--label KEY=VALUE`, egress `none` is native `--no-net` (the generated
`--net-conf` file idea is dropped for public/none; allowlist via `--net-rule`
belongs to ticket 04), exec uses `--workdir` plus `--stream` for byte-faithful
forwarding, and removal is `msb remove --force <name>`. `msb context --format
json` reports the backend under `kind`. `--pull` defaults to `if-missing`, so
sbx's explicit inspect-then-pull step is a belt-and-suspenders error reporter,
not the only pull path.
