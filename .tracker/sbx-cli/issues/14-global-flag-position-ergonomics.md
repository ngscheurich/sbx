# Global flag position ergonomics: `--plain` only works before the command name

Status: open
Blocked by: none

## Problem

`sbx --plain list` works but `sbx list --plain` is rejected. This is deliberate (`cli.Run` strips `--plain` only before the command name, protecting command-owned flags and `exec`'s `--` separator), but users find the asymmetry surprising — it surfaced during the `--json` design session (ADR-0009).

## Question

Should global flags (`--plain`, `--no-color`) be accepted in both positions — before and after the command name — and if so, how do we keep `exec`'s `--` guest-argument semantics intact? Any fix must not silently swallow flags on commands that don't support them.

## Context

`--json` chose per-command placement per ADR-0009, so this issue is orthogonal to JSON output and can be resolved independently. Leading-only globals match git's convention; dual-position matches cobra-style CLIs.
