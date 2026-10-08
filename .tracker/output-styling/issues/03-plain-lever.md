# The `--plain` lever

Status: resolved

## Goal

Add a global `--plain` flag (or `--no-color`; pick one spelling when the time comes, and keep the other as an alias if it's cheap) that drops **all** decoration — color, bold, and any non-color decoration — at an interactive terminal, regardless of what `colorprofile` would detect.

## Why it is deferred, and its trigger

Under Lip Gloss v2 the writer-bound profile already strips every escape when output is piped or `NO_COLOR` is set, and today's styling is color and bold only — nothing `--plain` would remove that the writer doesn't already (see [ADR-0007](../../../docs/adrs/0007-style-output-with-charm-v2.md)). The flag earns its existence the day sbx ships its **first non-color decoration** — a border, a status glyph, or an in-place redraw — because those are gated by "is a terminal," not by color detection, and no portable signal says a screen reader is attached. `--plain` is the only lever that drops them at a TTY; it is an accessibility lever, not a piping convenience.

This issue is therefore blocked on that trigger condition, not on priority. When any decoration beyond color and bold lands, implement this in the same change.

## Acceptance

- `--plain` at a TTY produces output byte-identical to piped output.
- The flag is documented in `accessibility.md`'s degrade-by-destination section as the accessibility lever, and in help.

## Comments

Implemented with the trigger that unblocked it: the drift table's bordered
rendering became the shared `ui.Table` (drawn by `sbx list` too), and
`--plain` — alias `--no-color`, leading the command line — shipped in the
same change. It forces the NoTTY writer profile (dropping color and bold
even under `CLICOLOR_FORCE`) and renderers drop table borders. Pinned by
`TestWritersStripAllDecorationWhenPlain`, `TestTablePlainDropsBorders`,
`TestPlainListPlainGolden`, and `testdata/list-plain.golden`; documented in
`accessibility.md`, top-level help, and an update note in ADR-0007. The
pipers' guarantee is unchanged: without the flag, a pipe never receives
ANSI, and border glyphs are content in both destinations.
