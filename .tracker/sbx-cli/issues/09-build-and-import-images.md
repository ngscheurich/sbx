# Build and import an image explicitly

Status: resolved
Blocked by: 02, 03

## Goal

Add `sbx build` for `[build]`: invoke Docker/buildx with context, Dockerfile, target, platform and tag from project configuration, export to a temporary archive, import with `msb load --input`, and clean the archive unless kept for debugging. No other command builds images. A missing locally built image instructs the caller to build; a missing prebuilt image may be pulled. Compare image content digests, not tags, in Creation drift detection.

## Acceptance

- Fake Docker and msb tests verify exact arguments, import ordering, cleanup after success/failure/cancellation, actionable missing-tool errors, and the no-build-recipe error.
- Test a rebuild that changes content behind the same tag: an existing persistent sandbox is drifted, not replaced; disposable runs may use the new image. No implicit build occurs on `up`, `exec`, or `run`.
- `image_check` remains rejected before a mutation until ticket 10 implements its isolated check. Use a temporary archive outside the repository and never write a generated sandbox YAML.

## References

[Images and build](../spec.md); [ADR-0006](../../../docs/adrs/0006-drive-msb-through-its-cli.md).

## Comments

Implemented 2026-10-07.

- `sbx build [--keep-archive]` (`internal/cli/build.go`): resolves the `[build]` recipe against the worktree root (shared `translate.ResolveHostPath` rules: absolute, worktree-relative, `~`-prefixed, never interpolated), checks both tools up front with install guidance, confirms the local backend before Docker runs, then `docker build` / `docker save --output` to a `sbx-build-*.tar` temporary outside the repository / `msb load --input`. The archive is removed on success, failure, and cancellation; `--keep-archive` retains it and names it. The build/save/load spelling follows `scripts/sbx-load.sh`, verified against msb 0.7.6 — plain `docker build` (buildx is the builder underneath), not a `buildx --output` one-liner.
- Config: `[build]` parses with `context` required and `platform` defaulting to `linux/<host arch>`; `ports`, `bootstrap`, and `image_check` stay rejected, and the build path re-pins that `image_check` fails before any tool runs (isolated check lands in ticket 10).
- Missing images: `[build]` projects get "run `sbx build`" from `up`/`exec`/`run` with no pull attempted; prebuilt projects keep the pull path. Creation drift already compared content digests; `TestRebuildBehindSameTagDriftsPersistentSandbox` now pins the whole rebuild lifecycle through `sbx build` — drifted, never replaced, `--allow-stale` usable, disposable runs take the new image, and docker sees zero calls outside explicit builds.
- New seams: `internal/docker` adapter (argv pinned by unit tests), fake docker in `internal/testsupport` (builtin-only fast paths so missing-tool tests can scrub PATH), fake msb learned `load` with a `FAKE_MSB_LOAD_REQUIRES` marker pinning export-before-import ordering.
- Not done here, by design: running the image check after import (ticket 10), and no git commit was created — this checkout's `.git` points at a worktree gitdir on the original author's machine, which is not reachable from the environment the work ran in.
