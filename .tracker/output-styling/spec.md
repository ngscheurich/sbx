# Output styling with Charm's v2 stack

## Goal

Style every line sbx itself writes — the `sbx plan` report, `status`, help, the `sbx: …` error lines, and `sbx rm`'s confirmation prompt — using Lip Gloss v2, while keeping plain output byte-identical to today's. Guest commands' stdio passes through untouched, as it does today. Styling is color and bold only: no borders, no box-drawing, no faint/muted, no in-place redraws.

The decision record is [ADR-0007](../../../docs/adrs/0007-style-output-with-charm-v2.md), and the user-observable vocabulary — Plain output and Styled output — is defined in [CONTEXT.md](../../../CONTEXT.md). The governing principles remain [docs/style/accessibility.md](../../../docs/style/accessibility.md).

## Decisions

- **Lip Gloss v2, not v1.** The color profile is writer-bound (`colorprofile`), so degradation by destination is structural: a pipe or buffer gets an `Ascii` profile and every escape is stripped at the writer. Nothing upstream can leak ANSI into a pipe. `NO_COLOR`, `CLICOLOR`/`CLICOLOR_FORCE`, and `TERM` are honored by `colorprofile`; sbx never reimplements them and never honors `FORCE_COLOR`.
- **One rendering path.** Renderers (plan, status, help, errors) take the style set; there is no separate plain variant. Plain output is the same code through an `Ascii`-profile writer, so plain and styled can never diverge structurally.
- **Byte-identical plain output.** What a pipe or `NO_COLOR` sees equals today's output exactly, for plan, status, help, port help, and the error line. Piping behavior never changes.
- **One seam: `internal/ui`.** A new package owns the style set and the writer wrapping. `ui.Writers(stdout, stderr)` returns `colorprofile.Writer`-wrapped writers plus the detected styles; `cli.Run` calls it once at the top and passes the results down. `cmd/sbx` stays a dumb entry point, and human-facing text never touches raw `os.Stdout`.
- **Palette** (named 16-color ANSI only; color is decoration, words carry the meaning — see `accessibility.md`):
  - `sbx:` error prefix — color `1`, bold; the message text stays default.
  - Warnings and Project-volume conflicts — color `3`.
  - Positive states (`reuses the existing volume`, reserved ports, `creation drift: none`) — color `2`.
  - Section headings (`configuration (sbx.toml):`, `sandbox:`, `existing sandbox:`, `Usage:`, `Commands:`) and the plan header line — bold.
  - Everything else — default. No faint/muted anywhere.
- **`--plain` is deferred, with a recorded trigger.** Under v2 it strips nothing the writer doesn't already, so it ships the day the first non-color decoration (border, glyph, in-place redraw) lands. That trigger is recorded here (issue 03), in ADR-0007, and in `accessibility.md`.

## Scope

- All sbx-authored human-facing output across the commands: plan, status, build, up/exec/run announcements, stop, rm, port, logs framing, help, version, and error lines.
- Golden tests pinning plain output byte-identically, plus a styled-path test asserting ANSI appears and severity words survive.

### Deferred

- The `--plain` lever (issue 03) — blocked on its trigger condition, not abandoned.
- Any non-color decoration: borders, box-drawing, glyphs, spinners, in-place repaints.

## Acceptance

- `go test ./...` passes; plain-output goldens for plan, status, help, port help, and one representative error line are byte-identical to pre-styling output.
- A test forces a color profile via `CLICOLOR_FORCE=1` against a buffer and asserts ANSI bytes appear **and** that severity words (`sbx:`, `translation failed`, state names) survive.
- `NO_COLOR` at a TTY and any piped invocation produce zero escape bytes, verified through `ui.Writers`.
- `accessibility.md` speaks `colorprofile` (Lip Gloss v2), not `termenv`.
