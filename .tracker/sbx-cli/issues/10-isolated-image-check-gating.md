# Require isolated image checks in both execution modes

Status: resolved
Blocked by: 06, 09

## Goal

When `image_check` is declared, create a check sandbox from the exact image contents without Workspace, volumes, secrets or project network policy; copy and run the project-owned script, then remove the check sandbox on every exit path. Record successes in host state keyed by image content identity and script hash. `build` fails on a failing check; neither `up`, `exec`, nor `run` uses an image without a matching successful check. Recheck the image identity before sandbox creation and check the image of an existing persistent sandbox even with `--allow-stale`.

## Acceptance

- Fake msb tests cover prebuilt and built images, changed tags or scripts, failed inspections, script failure, guest cancellation, cleanup, and denial before any existing persistent sandbox or its data is touched. A failed check never records success.
- `plan` reports known, pending, or unresolvable check status without launching a sandbox or writing state.
- Add the stateful web fixture's `image-check.sh` unchanged; its missing Workspace is part of the test.

## References

[Image checks and stateful fixture](../spec.md); [ADR-0004](../../../docs/adrs/0004-isolated-image-checks.md).

## Comments

Implemented on `main`.

- `internal/state/imagecheck.go`: success records under
  `${XDG_STATE_HOME:-~/.local/state}/sbx/imagecheck/<sha256(digest, script
  hash)>.json`, holding the digest, the script hash, and the recording
  time — never the script's contents or any value. The key pair is derived
  with a NUL separator and the record's own fields are re-checked on read,
  so a record proves only its exact pair. A corrupt record is an error
  (`ErrCorruptImageCheckRecord`), never a pass or an absence.
  `ImageCheckScriptHash` hashes the script's exact contents ("sha256:…",
  like the Bootstrap definition hash).
- `internal/cli/imagecheck.go`: the check sandbox is created from the
  image with only the project's resource limits and the `sbx.*` labels —
  no Workspace, volumes, secrets, environment, or project network policy
  (msb's create default egress applies). The script travels by content
  through the check exec's standard input (`cat > /tmp/sbx-image-check &&
  chmod +x … && exec …`), so it runs by its own shebang, never as a host
  mount. The sandbox is removed on every exit path, cancellation included
  (`context.WithoutCancel`); a failed or interrupted check records
  nothing, and a passing check whose success cannot be persisted fails
  closed. Names extend the Sandbox identity: `<identity>-imgchk-<6 hex>`.
- Gating, all test-pinned against the stateful fake msb:
  - `sbx run` checks the image after the pull/inspect step and confirms
    the manifest digest is unchanged after the check, before `msb run` —
    a digest that moved mid-check fails with nothing created. A recorded
    pass for the current digest and script is reused.
  - `up`/`exec` run the check on the creation path after `ensureImage` and
    before `create`. An existing sandbox is gated on its *own* effective
    manifest digest, even with `--allow-stale` and while already running;
    when the image reference no longer resolves to the sandbox's contents
    (or cannot be inspected at all), the command fails closed naming both
    digests, without creating a check sandbox or touching the sandbox. A
    changed script forces a re-check and is recorded under the new hash —
    it is not Creation drift and does not enter the snapshot.
  - `sbx build` runs the check after the `msb load` import; a failing
    check fails the build with the image left cached, and no success is
    recorded.
- `image_check` parses in strict TOML (removed from not-yet-supported);
  a declared-empty value is rejected at load, like bootstrap.run. `ports`
  remains rejected until ticket 08.
- `plan` reports the declared check as known (a recorded pass for the
  current digest and script), pending (it will run before the image is
  used), or unresolvable (missing script, uninspectable image, corrupt
  record) — read-only: no check sandbox, no backend mutation, no new host
  state, verified by a purity test that snapshots the record directory.
- The stateful web fixture's `image-check.sh` is added unchanged
  (executable), and a fixture test pins that it reaches the check sandbox
  byte for byte; its missing Workspace is the guest-side property the
  smoke test exercises.
- Fake msb grew per-call stdin captures (`stdin.<n>`; `Log.Stdin` still
  returns the latest) and `FAKE_MSB_CHECK_EXIT`, which fails only the
  image-check exec so tests can fail a check without failing every other
  guest command. `Log.SandboxExists` asserts check-sandbox removal.
- Not verified on a real host, per this environment: the check sandbox's
  boot and exec end-to-end, and the isolated-script behavior against a
  real guest — the smoke test (ticket 12) exercises the stateful web
  flow. UNVERIFIED items elsewhere are unchanged; no new msb flag is
  used (create/exec/remove/image-inspect only).

## Comments (code review)

Two-axis review against the repo standards and this ticket found one real
flaw, closed before the ticket was resolved:

- TOCTOU in the record path: a pass was saved before confirming the image
  still resolved to the digest the check started from, so a tag repointed
  mid-check could attest contents the script never ran against.
  `runCheckSandbox` now re-inspects before recording; a moved tag records
  nothing and the command fails with "nothing was recorded". Pinned by
  `TestImageChangedMidCheckRecordsNothing`.
- Coverage gaps closed: a repointed tag re-checks on the disposable path
  (`TestRunRechecksRepointedTag`, via the fake's
  `FAKE_MSB_IMAGE_DIGEST_FROM`), and an uninspectable image fails `sbx
  build` closed after the import.
- Standards cleanups: the gate is checked only inside the `ensure*`
  functions (call sites no longer double-gate), `imageCheckScript` no
  longer returns an unused path, and the persistent existing-sandbox path
  passes the caller's stdout through instead of a nil writer.
- Declined judgement calls, for the record: `runCheckSandbox`'s parameter
  list and the plan report's status strings match the codebase's existing
  style (positional options, status words as strings); bundling them into
  new types would diverge from the neighboring code without a spec need.
