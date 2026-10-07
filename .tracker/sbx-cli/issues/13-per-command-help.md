# Per-command help as the home for detail

Status: ready-for-agent

## Goal

The top-level help was shortened to one line per command (issue discussed in
`sbx port` grilling session, 2025). That deleted the only user-visible home
for several details:

- `up --allow-stale` (ignore drift) and `--retry-bootstrap` (retry an
  incomplete bootstrap)
- `rm --yes` for noninteractive use
- the read-only promise on `plan` and `status` ("changing nothing")
- `build`'s requirement of a `[build]` section in `sbx.toml`

Add per-command help — both `sbx help <command>` and `sbx <command> -h` —
and give those details a home there. Bare `sbx port` already prints its own
group help; follow the same shape for the other commands.

## Acceptance

- Every command listed in the top-level help answers `-h`/`--help` and
  `sbx help <command>` with its flags, qualifiers, and exit-relevant
  promises (read-only, confirmation, config requirements).
- Unknown flags on a command point at its per-command help rather than only
  erroring.
- The top-level help stays one line per command; detail never leaks back in.
