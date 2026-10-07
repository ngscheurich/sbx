# Styled rendering and plain-output goldens

Status: ready-for-agent
Blocked by: 01

## Goal

Apply the palette from the [spec](../spec.md) through the style set from issue 01, across every surface sbx authors, and pin the guarantees with tests. One rendering path per surface, parameterized by the style set.

Surfaces:

- `sbx plan` report — the header line bold, section headings bold, positive states (`reuses the existing volume`, reserved ports, `creation drift: none`) color `2`, warnings and Project-volume conflicts color `3`.
- `sbx status` report — same treatment.
- Help (`sbx`, `sbx port`) — restructured for rendering so `Usage:`/`Commands:` heading lines and command names can take bold; `Ascii`-profile output aims to stay byte-identical to today's text.
- Error lines on stderr — the `sbx:` prefix color `1` bold, message text default.
- The remaining sbx-authored lines (`build`/`up`/`exec`/`run` announcements, `rm`'s prompt, `stop`, `port`, `logs` framing, version) — headings bold where present; no new decoration.
- Guest command stdio: untouched.

## Acceptance

- **Plain output is byte-identical to pre-styling output**, golden-tested for: the plan report, the status report, both help texts, and one representative error line. Goldens live in each package's `testdata/`, per the existing convention in `internal/plan/testdata/`.
- A styled-path test passes `CLICOLOR_FORCE=1` (and a plain one omits it) through `ui.Writers` into buffers: the styled buffer contains ANSI bytes, and the severity words (`sbx:`, `translation failed`, state names) survive in both — the regression test `accessibility.md` demands.
- No borders, box-drawing, faint/muted, or in-place redraws anywhere; color never carries meaning without its word.
- `mise ci` passes.
