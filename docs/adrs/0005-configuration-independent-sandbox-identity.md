# ADR-0005: Derive Sandbox identity only from Git

Each worktree reads its own checked-out `sbx.toml`, so any configured value can change on a branch switch or edit. If Sandbox identity included such a value, a change would give the worktree a second persistent sandbox and strand the first, still running and holding its ports, beyond the reach of sbx commands. Sandbox identity therefore uses only the project basename derived from the common Git directory, the worktree basename, and a hash of the worktree path. v1 has no configurable project name.
