# Global flag position ergonomics: `--plain` only works before the command name

Status: resolved
Blocked by: none

## Problem

`sbx --plain list` works but `sbx list --plain` is rejected. This is deliberate (`cli.Run` strips `--plain` only before the command name, protecting command-owned flags and `exec`'s `--` separator), but users find the asymmetry surprising — it surfaced during the `--json` design session (ADR-0009).

## Question

Should global flags (`--plain`, `--no-color`) be accepted in both positions — before and after the command name — and if so, how do we keep `exec`'s `--` guest-argument semantics intact? Any fix must not silently swallow flags on commands that don't support them.

## Context

`--json` chose per-command placement per ADR-0009, so this issue is orthogonal to JSON output and can be resolved independently. Leading-only globals match git's convention; dual-position matches cobra-style CLIs.

## Comments

Accepted dual-position global accessibility flags: `--plain`, `--no-color`, and `-p` work before or after the command, including between `port` and `prune`. Repeated spellings are harmless. Parsing stops at the first `--`; guest arguments and stdio remain unchanged for both `exec` and `run`. Only the registered global spellings are consumed, so unsupported command flags still produce usage errors and `--json` remains per command.

Alias arguments are classified after expansion reveals their ownership. With `ls = "exec -- ls"`, `sbx ls --plain` still passes the flag to the guest; `sbx --plain ls` selects Plain output for sbx. Global flags inside expansions work as if typed, including through one alias hop.

Implemented through `cli.Run` and alias dispatch. Tests cover all global spellings and positions, repeated flags, nested commands, help/version, JSON combinations, unsupported flags, alias expansion, and exact guest argv preservation. Updated the README, accessibility guide, and ADR-0009’s historical placement note.

Validation passed: targeted CLI tests, `go test -race ./... -count=1`, `go build ./...`, `go vet ./...`, formatting, `go mod tidy -diff`, and `git diff --check`. The real-backend smoke test was not run.
