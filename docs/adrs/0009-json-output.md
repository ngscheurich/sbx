# JSON output as a third output mode

sbx gains `--json` because scripts need to consume sbx's meaning without parsing styled or plain prose. We decided on three output modes — Styled, Plain, and JSON — where JSON output replaces the human surface on stdout with a stable, field-based rendering, while errors and usage errors stay as the human `sbx: …` line on stderr with exit codes unchanged. The schema is a **public contract**: the moment a script parses a field name it is load-bearing, so best-effort serialization is not an option; golden tests pin it and evolution is additive. The accessibility rule extends into JSON mode: every meaning the prose states becomes a field — drift warnings become data, an empty `sbx list` is `[]` — so nothing is ever carried only in words the mode removes.

## Considered options

- **JSON errors on stdout** (`{"error": …}`) — rejected: every scripting environment already keys off exit codes, and it would blur the sbx-bytes/guest-bytes split on stdout.
- **Wrapping guest output in JSON for `run`, `exec`, `logs`** — rejected: these commands' stdout belongs to the guest and passes through byte-identical by design (see `internal/ui`); `--json` is a usage error on them. Their scripting surface already exists: sbx propagates the guest's exit code.
- **Global leading flag, like `--plain`** — rejected: output-format flags are per-command in the dominant CLI convention (`gh --json`, `docker --format`, `kubectl -o`), and a global pre-parser would either silently swallow `--json` on unsupported commands or need to peek at the command name, reintroducing complexity the `exec --` handling avoids. `--json` therefore lives on each supporting command's own parser and is a usage error elsewhere. The `--plain` position wart (leading-only) is deliberate but flagged separately in the tracker.

## Consequences

- Scope: `plan`, `status`, `list`, `up`, `stop`, `rm`, `build`, `port prune`, and `version` accept `--json`; `run`, `exec`, `logs`, and `help` reject it. `help` stays prose for humans.
- Mutations emit minimal result objects — identity plus outcome (e.g. `up` reports created/started/drift; `rm` never auto-confirms: `sbx rm --yes --json` is the scripting path).
- Exact field lists settle at implementation time, pinned by golden tests, following the every-meaning-is-a-field rule.
