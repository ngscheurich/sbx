# Implement `sbx list`

Status: ready-for-agent

Type: task

## Goal

Implement `sbx list` per `.tracker/sbx-list/spec.md`:

- New `internal/cli/list.go` with `runList`; dispatch case in `cli.go`.
- List the backend (`msb.CLI.List`), inspect each entry, show only
  sandboxes whose effective configuration carries the `sbx.managed` label.
- Render a table: Name, Image, Status, Created — sorted by name, Created
  as `2006-01-02 15:04` local time, bold headings only.
- Empty backend: print `no sbx sandboxes`, exit 0.
- Listing or inspection failure: one `sbx: …` error line, exit 1 (fail
  closed — a failed listing is never an empty one).
- Help entry after `status`: `List every sbx sandbox on this machine`;
  re-pin `testdata/help.txt`.
- Golden tests for table and empty cases, following the existing
  `golden_test.go` pattern.

## Acceptance

- [x] `sbx list` from outside any Git repository lists owned sandboxes
- [x] Unowned backend sandboxes never appear
- [x] Piped output is byte-identical to terminal output (Plain output)
- [x] Top-level help lists `list`; golden fixture updated

## Comments

Implemented 2025. `internal/cli/list.go` (runList, renderList,
formatCreated), dispatch + help entry in `internal/cli/cli.go`, tests in
`internal/cli/list_test.go` with goldens `testdata/list.golden` and
`testdata/list-empty.golden`; `testdata/help.golden` re-pinned. Scope
decision recorded in `docs/adrs/0008-backend-global-list.md`; the Owned
sandbox term added to `CONTEXT.md`. Full suite passes; verified end to
end against a fake msb outside a Git repository (no ANSI through a pipe).

Follow-up from review: the listing was first rendered with hand-rolled
two-space columns; it now draws through `ui.Table`, the bordered idiom
extracted from the drift report (`state.RenderDrift` refactored onto the
same helper, bytes unchanged). `--plain`/`--no-color` shipped in the same
change per `.tracker/output-styling/issues/03-plain-lever.md` — it leads
the command line (`sbx --plain list`), forces the NoTTY writer profile,
and drops table borders. Help text is not a table and was left as-is.
