# sbx list

Spec from the grilling session of 2025 (design tree fully settled; ADR-0008
records the scope decision).

## Goal

A `sbx list` command that shows every Owned sandbox the backend holds,
across all projects and worktrees on this machine.

## Decisions

- **Backend-global** (ADR-0008): no worktree discovery, no `sbx.toml` read;
  runs from any directory, inside or outside a Git repository. The one
  command that needs no project configuration.
- **Owned only**: every backend listing entry is inspected for the
  `sbx.managed` label; unowned sandboxes are never shown (never-adopt).
  The N+1 inspect cost is accepted; `list` is not a hot path.
- **All owned sandboxes**, with a Status column — running sandboxes are
  visible and stopped ones are not hidden. No running-only filter.
- **Columns**: Name, Image, Status, Created — what the backend listing
  already provides, plus ownership resolved per entry.
- **Sort**: alphabetically by name; deterministic output.
- **Created format**: fixed-format local date, `2006-01-02 15:04`.
- **Styling**: rendered through `ui.Table` — the same bordered table
  idiom as the drift report (lowercase headers, bold; no color on Status
  values, the word carries the meaning). The `--plain` lever drops the
  borders.
- **Empty result**: one line, `no sbx sandboxes`, exit 0.
- **Help**: entry after `status` in the top-level command list, summary
  `List every sbx sandbox on this machine`; `testdata/help.txt` re-pinned.

## Non-goals

- No machine-readable output format (`--format json`) until something
  needs it.
- No identity decoding (project/worktree parsed back out of sandbox
  names) — a new coupling deferred until wanted.
- No filtering flags.
