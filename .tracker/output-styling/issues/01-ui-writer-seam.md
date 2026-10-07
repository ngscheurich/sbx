# ui package and the writer seam

Status: ready-for-agent

## Goal

Create `internal/ui`, the single seam the accessibility guide names. It owns:

- the style set (one type holding the lipgloss styles for the palette in the [spec](../spec.md): error prefix color `1` bold, warning color `3`, positive color `2`, headings and the plan title bold);
- `ui.Writers(stdout, stderr) io.Writer, io.Writer, Styles` — wrapping the writers `cli.Run` receives in `colorprofile.Writer`s and returning the profile-detected styles alongside.

`cli.Run` calls it once at the top and passes the wrapped writers and styles down; `cmd/sbx` stays a dumb entry point. Rendering packages take the style set as a parameter (per spec: one rendering path, no plain variant).

## Acceptance

- `colorprofile` detection is inherited, not reimplemented: `NO_COLOR`, `CLICOLOR`/`CLICOLOR_FORCE`, and `TERM` behave as `colorprofile` defines; `FORCE_COLOR` is not honored.
- A bytes.Buffer passed through `ui.Writers` (not a TTY) strips all ANSI; the same buffer with `CLICOLOR_FORCE=1` in the injected environ does not.
- No code outside `internal/ui` imports `colorprofile` or detects profiles; nothing anywhere writes to raw `os.Stdout`.
- No behavior change yet: all output through the seam is still byte-identical to today's (styles applied in issue 02).
