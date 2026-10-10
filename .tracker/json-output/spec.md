# JSON output for scripting

## Goal

Add `--json` to the commands whose stdout sbx can speak for: structured views (`list`, `status`, `plan`), mutations (`up`, `stop`, `rm`, `build`, `port prune`), and `version`. Passing `--json` to `run`, `exec`, `logs`, or `help` is a usage error. The design was settled in a grilling session and recorded in [ADR-0009](../../../docs/adr/0009-json-output.md); the vocabulary — JSON output, Plain output — is defined in [CONTEXT.md](../../../CONTEXT.md).

## Decisions (from ADR-0009)

- **Third stdout mode.** JSON replaces the human surface on stdout; errors and usage errors stay the human `sbx: …` line on stderr; exit codes are unchanged. `--json` and `--plain` are independent levers.
- **Public contract.** Golden tests pin the schema; evolution is additive. Every meaning the prose states becomes a field; empty results are empty arrays; nothing is ever carried only in words the mode removes.
- **Per-command flag.** Each supporting command parses `--json` in its own argument parsing; unsupported commands reject it with a usage error. It is never a leading global flag.
- **No behavior change.** `--json` changes what success prints, never what sbx does: `rm --yes --json` is the scripting path, and `rm --json` without confirmation fails exactly as `rm` does.

## Scope

- The seam: a JSON writer on the shared output and the flag plumbing per command.
- Field projections per command: identity plus outcome for mutations; full meaning-preserving projections for `list`, `status`, and `plan`.
- Golden tests per surface, following the existing `testdata` pattern, plus usage-error tests for the rejecting commands.

## Acceptance

- `go test ./...` passes; every JSON surface has a pinned golden.
- `sbx run --json`, `sbx exec --json`, `sbx logs --json`, and `sbx list --plain --json`-style combinations behave as ADR-0009 states (reject vs. independent, per command).
- An empty `sbx list --json` prints `[]`.
