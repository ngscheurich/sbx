# Aliases: project-configured shorthands for sbx commands

Status: resolved

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

- Only the first word of an invocation is an alias candidate: `sbx ls -la` expands; a subcommand such as `sbx port ls` never resolves `ls` as an alias.
- An alias's value is a full sbx command line, split on whitespace and
  re-dispatched through the same internal entry point `Run` uses, with any
  trailing arguments from the original invocation appended. No host shell is
  involved, so there are no quoting or expansion surprises.
- Re-dispatch adds no output of its own. Builtin names supplied by an expansion never trigger shadow warnings; only a builtin name the user typed can warn.
- An expansion may resolve through one further alias (`ll = "ls"` works when `ls` is itself an alias). A third alias candidate, at depth ≥ 2, is a usage error ("alias chain too deep"), which also makes `a = "a"` impossible to loop.
- Builtins always win. If `[aliases]` declares `exec` and the user types `sbx exec …`, the builtin runs and sbx prints one styled `warning:` line on stderr — only when that name is invoked, never on other commands. The warning uses the styled stderr writer, so Plain output retains the same words without ANSI.
- Configuration-free invocations — help, version, list, and per-command help — skip shadow warnings rather than loading configuration just to warn. `list`’s exception follows [ADR-0008](../../docs/adrs/0008-backend-global-list.md).

### Names

- Load-time rejection only for names that can never be invoked: empty names, names containing whitespace, and names starting with `-` (indistinguishable from flags in use).
- A name that shadows a builtin or a top-level flag word (`help`, `version`) is *permitted* in configuration — a config file mistake must not poison every command for the worktree, and aliases are not creation-time settings. Commands that read configuration apply the runtime builtin-wins warning above; configuration-free invocations keep their exception.

### Relationship to sandbox machinery

- Aliases never affect Sandbox identity, Creation drift, or the plan's
  translation. They are host-side command sugar with the same standing as
  `[bootstrap]`: editing them must not mark a persistent sandbox drifted.
- Sandbox identity already never derives from editable configuration
  (ADR-0005); this follows the same principle.

### Config loading on unknown commands

- An unknown first word falls to the dispatcher’s `default:` branch, which loads configuration through the same `discoverConfig` path used by project-scoped commands:
  - discovery or load failure reports that error — the same behavior as `sbx plan` in the same directory, and more informative than "unknown command" alone;
  - a valid table that does not contain the word produces the unchanged "unknown command" error;
  - a hit re-dispatches.
- `sbx help`, version invocations, `sbx list`, and per-command help stay configuration-free.

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
  `testdata/help.golden` pins the help byte-for-byte.

## Acceptance

- `sbx ls` with `[aliases] ls = "exec -- ls"` in `sbx.toml` runs
  `sbx exec -- ls`, with identical exit code, stdout, and stderr bytes as
  typing the expansion directly.
- Trailing arguments append: `sbx ls -la` ≡ `sbx exec -- ls -la`.
- A shadowing alias warns once on stderr when a configuration-reading builtin is invoked, and the builtin runs. Configuration-free invocations keep their normal bytes and do not warn, even with shadowing aliases or broken project configuration.
- `a = "a"` (or any chain deeper than one alias hop) fails with a usage error,
  not a hang.
- A worktree with an invalid `sbx.toml` reports the config error for an
  unknown command; outside a worktree it reports the discovery error.
- Aliases declared or edited never appear as Creation drift in `sbx status`
  or `sbx plan`, and never change the Sandbox identity.
- Strict parsing accepts `[aliases]` and rejects non-string values, empty
  values, and uninvocable names with a clear load error.
- Plain output through a pipe or under `NO_COLOR` stays byte-identical for non-alias invocations without shadow warnings. A shadow warning uses the existing `warning:` prefix and preserves its words across Styled and Plain output.

## Comments

The implementation landed in `77f7a10`; the tracker status had not been updated. Checked the current implementation against acceptance and added regression coverage where the original tests were narrower than the spec. No runtime changes were required.

Reconciled two spec inconsistencies rather than changing established behavior: shadow warnings use the existing `warning:` prefix, not the `sbx:` error prefix; configuration-free help, version, list, and per-command help cannot discover shadowing aliases without giving up their configuration-free contract. The original design already required help/version to stay configuration-free, and ADR-0008 requires the same for list. The spec, README, and example configuration now state those exceptions explicitly. Also clarified that only the first word is an alias candidate and that the depth cap rejects a third alias candidate, not the builtin reached after two expansions.

Acceptance evidence lives in [CLI alias tests](../../internal/cli/aliases_test.go), [guest argument tests](../../internal/cli/global_flags_test.go), [configuration tests](../../internal/config/config_test.go), and the existing Plain output goldens:

| Acceptance area | Evidence |
| --- | --- |
| Direct invocation equivalence, including nonzero guest exit status | `TestAliasMatchesDirectInvocation` compares exit code and both streams for bare, trailing-argument, and one-hop invocations. |
| Exact guest argv and first-word-only resolution | `TestAliasDispatchesAsIfTyped`, `TestAliasThroughOneAlias`, `TestGlobalFlagsPreserveGuestArguments`, and `TestAliasesOnlyResolveFirstWord`. |
| Builtin wins, warns once, and does not warn on unrelated commands | `TestShadowedAliasWarnsAndBuiltinWins` covers pipe, `NO_COLOR`, `--plain`, and forced styling; `TestConfigFreeCommandsIgnoreShadowAliases` pins the configuration-free exceptions. |
| Bounded chains and cycles | `TestAliasChainTooDeep` covers self-reference, a two-alias cycle, and three aliases, all rejected before backend calls. |
| Configuration and discovery failures on unknown commands | `TestUnknownCommandWithBrokenConfigReportsLoadError`, `TestUnknownCommandOutsideWorktreeMatchesPlanError`, and `TestUnknownCommandWithoutAliasUnchanged`. |
| No identity, translation, or Creation drift changes | `TestAliasesNeverDrift` compares complete status and plan JSON reports before adding aliases and after editing them. |
| Strict configuration validation | `TestLoadAliases` also accepts builtin-shadowing names; the existing rejection tests cover uninvocable names, empty values, and non-string values. |
| Plain output stability | Existing Plain goldens pass unchanged; shadow warning bytes match exactly across pipe, `NO_COLOR`, and `--plain` cases. |

Validation passed: targeted alias/configuration/output tests, `go test -race ./... -count=1`, `go build ./...`, `go vet ./...`, formatting, `go mod tidy -diff`, and `git diff --check`. This was an automated acceptance check using fake backend executables; no real-backend smoke test was run.
