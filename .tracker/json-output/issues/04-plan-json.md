# `plan --json`

Status: open
Blocked by: 03

The plan package gains a `JSON()` projection in its own package (cli
cannot import it back): identity fields, then on translation failure
`translate_error` alone; otherwise config, the translated sandbox shape
(mounts beyond the workspace, tmpfs, project volumes with their
compatibility status word, sandbox volumes, environment, network, port
outlooks, `bootstrap_declared`, image check, secrets as name/host
variable/destinations only — values never appear), the msb create argv,
and the live report (state, ownership, drift, bootstrap, observed ports),
each with its error field where the prose warns.

A translation failure collapses the object to identity plus the reason,
exactly as the prose does. Golden-test against the fixture repo.
