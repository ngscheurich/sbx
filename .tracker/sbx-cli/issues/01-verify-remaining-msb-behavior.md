# Verify remaining local msb behavior

Status: ready-for-human
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
