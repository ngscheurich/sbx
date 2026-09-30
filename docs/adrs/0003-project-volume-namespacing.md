# ADR-0003: Namespace shared volumes by project

Microsandbox named volumes use machine-wide literal names, so reusing a cache name could unintentionally share data between unrelated repositories. sbx derives Project volume names from the repository's common Git directory and the logical volume name: sibling worktrees share the volume, while unrelated clones do not. Project volumes survive removal of one sandbox; Sandbox volumes remain private and are removed with their sandbox.

Keying by location means moving or renaming the repository orphans its Project volumes; a stable ID stored in Git config would avoid that at the cost of writing repository state, which the rarity of moves does not justify. Because each worktree reads its own checked-out `sbx.toml`, branches can disagree about a Project volume's kind, size, or quota. sbx rejects such a conflict instead of hashing the settings into the name, which would silently split sharing and leak volumes that v1 cannot list.
