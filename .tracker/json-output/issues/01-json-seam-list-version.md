# JSON seam, `list --json`, `version --json`

Status: open
Blocked by: none

The output struct gains the JSON lever and a writer: an `encoding/json`
encoder, two-space indent, trailing newline, snake_case fields, written
through the styled stdout writer. Supporting commands parse `--json` in
their own flag parsing (it is per-command, per ADR-0009); `run`, `exec`,
and `logs` reject it through their existing parsers.

First surfaces: `list --json` emits the array of owned sandboxes with the
backend's raw RFC3339 `created_at` (an empty listing is `[]`), and
`version --json` emits `{version, commit, modified}` from the same data
`versionString` renders — refactor the report into data + renderer so the
human line cannot drift from the object.

Golden-test both surfaces per the spec's acceptance.
