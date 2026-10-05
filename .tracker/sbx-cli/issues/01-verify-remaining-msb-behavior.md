# Verify remaining local msb behavior

Status: resolved
Blocked by: none

## Goal

Close the real-host gaps in [the sbx spec](../spec.md#backend-verification) before dependent implementation. Use a local msb installation, throwaway sandbox and volume names, and throwaway secret values. Never paste secret values or delete unrelated resources. Record the msb version, commands, observed results, and cleanup in the spec; a failed or skipped check stays unverified.

## Checks

- Create a quota-limited named directory and a sized named disk, then confirm that `msb volumes --format json` reports their `quota_mib` and `capacity_bytes` as non-null values. The schema is already observed, but only null values have been captured so far. Remove only the probe sandbox and volumes.
- Check whether interrupting `msb exec` forwards the signal to the guest and what exit code it returns. Separate a guest that exits normally from one interrupted by the terminal.
- Check whether `msb exec` accepts `--stream` while stdin is a terminal (msb 0.7.3 rejects it and keeps `--tty` and `--stream` exclusive). If a newer msb accepts it, sbx can keep redirected stdout byte-faithful when stdin is a terminal instead of forcing `--tty`; if not, the current forced-`--tty` behavior stands. Remove the probe sandbox afterward.
- With a throwaway `--secret`, stop and restart a named sandbox after unsetting its host environment variable. Determine whether `msb start` needs the value again; if a successful start alone cannot establish whether substitution works, report that limitation instead of treating it as proof. Do not use a real credential or send the test value to a third party.

## Done when

Each observation or remaining uncertainty is written to the spec. The findings unblock volume compatibility checks, signal handling, and secret-bearing persistent restarts respectively; independent CLI work need not wait for all three.

## Comments

- Round 2 results (recorded in the spec's Backend verification section): checks 1, 3, and 4 are verified. On msb 0.7.3 (macOS, transcript `msb-verify.txt`): `msb volume create` has no quota option, a sized disk volume populates `capacity_bytes` while `quota_mib` stays null; `msb start` re-reads `--secret` host variables and fails when one is missing, so sbx must supply secret values on every start; bonus finding — `--mount-owned ...:quota=100M` is accepted and enforced (a 200 MiB `dd` stopped at exactly 100 MiB). On msb 0.7.6 (Linux, transcript `msb-verify2.txt`): `--stream` is still rejected with terminal stdin, but the new `--no-stdin` and `--no-tty` flags pass client-side validation under a pty (`--no-tty --stream` stays rejected), giving sbx a byte-faithful scripted-exec path on 0.7.6+.
- Check 2 (signal forwarding) remains unverified: the Linux probe host cannot boot guest sandboxes (no `/dev/kvm`; the sandbox process exits SIGABRT before the agent relay becomes available), so only client-side flag validation was possible there. Run `bash msb-verify2.sh 2>&1 | tee msb-verify2.txt` on the macOS msb host and record the outcome in the spec. Note: the repository's `msb-verify2.txt` is the 0.7.6 Linux run and holds the `--stream` evidence, not signal evidence.
- msb-verify2.sh then ran on the macOS host at msb 0.7.6 (transcript `msb-verify2.txt`, now overwritten with that run): all four checks of this issue are verified and recorded in the spec. Signals are NOT forwarded — a client-side SIGTERM left the guest trap unfired (client exit 143) and the runtime tore the guest session down; a client-side SIGINT also left the trap unfired and the client exited 0, indistinguishable from guest success. Plain `msb exec` with terminal stdin auto-allocates a PTY and mangles redirected stdout (`A\rB\n` → `A\rB\r\n`); `--stream --no-stdin` is byte-faithful end-to-end; `--stream` with terminal stdin is still rejected and works with piped stdin.
- Residual (new spec bullet, beyond this issue's four checks): signal behavior in `--stream --no-stdin` mode — sbx's planned translation — was not probed, nor the fate of a guest command after the client absorbs a SIGINT and exits 0. `msb-verify3.sh` at the repository root is the prepared probe: `bash msb-verify3.sh 2>&1 | tee msb-verify3.txt`.
- msb-verify3.sh ran on the macOS host at msb 0.7.6 (transcript `msb-verify3.txt`): the residual is closed and recorded in the spec. In `--stream --no-stdin` mode, SIGTERM is not forwarded (client dies 143, runtime tears the guest session down) and SIGINT is absorbed — the client waits out the guest and relays its natural exit code (exit 0 arrived only after the guest's 98-second sleep finished; the round-2 SIGINT 0 is the same behavior, not an early detach). Interrupts never reach the guest in either mode. Everything this issue asked for is verified; the spec's only remaining unverified backend item is the volume `quota_mib` question, which no CLI option on 0.7.3–0.7.6 can exercise.
- The msb-verify* scripts and transcripts were throwaway probes; they have been removed from the repository, and the findings recorded above and in the spec stand on their own.
