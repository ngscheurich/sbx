# Aliases: project-configured shorthands for sbx commands

Status: ready-for-agent

## Goal

Projects repeat long sbx invocations — `sbx exec -- ls`, `sbx exec -- npm test`
— the way early Git users repeated long plumbing commands. Git solved this with
aliases resolved at dispatch time; sbx does the same, sourced from project
configuration so each project defines the shorthands its workflow needs:

```toml
[aliases]
ls = "exec -- ls"
```

With that table, `sbx ls -la` behaves exactly as if the user typed
`sbx exec -- ls -la`. The design below was settled in a grilling session; every
decision listed here is agreed.

## Design

### Resolution semantics

- Only the first word of an invocation is an alias candidate: `sbx ls -la`
  expands; `sbx port <anything>` never consults `[aliases]`.
- An alias's value is a full sbx command line, split on whitespace and
  re-dispatched through the same internal entry point `Run` uses, with any
  trailing arguments from the original invocation appended. No host shell is
  involved, so there are no quoting or expansion surprises.
- Re-dispatch is silent: alias output is indistinguishable from typing the
  expanded command.
- An expansion may resolve through one further alias (`ll = "ls"` works when
  `ls` is itself an alias). An internal depth counter enforces the cap:
  depth ≥ 2 is a usage error ("alias chain too deep"), which also makes
  `a = "a"` impossible to loop.
- Builtins always win. If `[aliases]` declares `exec` and the user types
  `sbx exec …`, the builtin runs and sbx prints one styled warning line on
  stderr — only when the shadowed name is actually invoked, never on other
  commands. The warning goes through the styled stderr writer, so Plain
  output keeps its `sbx:` prefix convention.

### Names

- Load-time rejection only for names that can never be invoked: empty names,
  names containing whitespace, and names starting with `-` (indistinguishable
  from flags in use).
- A name that shadows a builtin or a top-level flag word (`help`, `version`)
  is *permitted* in configuration — a config file mistake must not poison
  every command for the worktree, and aliases are not creation-time settings.
  The runtime builtin-wins warning above covers it.

### Relationship to sandbox machinery

- Aliases never affect Sandbox identity, Creation drift, or the plan's
  translation. They are host-side command sugar with the same standing as
  `[bootstrap]`: editing them must not mark a persistent sandbox drifted.
- Sandbox identity already never derives from editable configuration
  (ADR-0005); this follows the same principle.

### Config loading on unknown commands

- Today an unknown first word falls to the dispatcher's `default:` branch
  without reading `sbx.toml`, because no command ever needed it. With aliases,
  the `default:` branch loads configuration through the same `discoverConfig`
  path every other command uses:
  - discovery or load failure reports that error — the same behavior as
    `sbx plan` in the same directory, and more informative than today's
    "unknown command" alone;
  - a valid table that does not contain the word produces today's unchanged
    "unknown command" error;
  - a hit re-dispatches.
- `sbx help` and `sbx --version` stay config-free: they are handled before the
  dispatch switch.

### Configuration surface

- `[aliases]` is a top-level table whose values are strings. `supportedFields`
  in `internal/config` admits the key so strict parsing no longer rejects it;
  non-string values get the usual strict load error.
- Alias names follow the name rules above; values must be non-empty after
  splitting (an empty value is a load-time error, like an empty `[bootstrap]`
  run).
- `sbx.example.toml` gains a commented `[aliases]` block documenting the
  feature, in the file's established style.
- The top-level help does not list aliases: they are per-project, and
  `testdata/help.txt` pins the help byte-for-byte.

## Acceptance

- `sbx ls` with `[aliases] ls = "exec -- ls"` in `sbx.toml` runs
  `sbx exec -- ls`, with identical exit code, stdout, and stderr bytes as
  typing the expansion directly.
- Trailing arguments append: `sbx ls -la` ≡ `sbx exec -- ls -la`.
- A shadowing alias warns once on stderr when invoked, and the builtin runs.
- `a = "a"` (or any chain deeper than one alias hop) fails with a usage error,
  not a hang.
- A worktree with an invalid `sbx.toml` reports the config error for an
  unknown command; outside a worktree it reports the discovery error.
- Aliases declared or edited never appear as Creation drift in `sbx status`
  or `sbx plan`, and never change the Sandbox identity.
- Strict parsing accepts `[aliases]` and rejects non-string values, empty
  values, and uninvocable names with a clear load error.
- Plain output through a pipe or under `NO_COLOR` stays byte-identical to
  today's output for every non-alias invocation, and the new warning line
  follows the `sbx:` stderr convention.
