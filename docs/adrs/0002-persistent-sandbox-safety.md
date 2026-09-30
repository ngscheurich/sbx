# ADR-0002: Never silently replace a persistent worktree sandbox

A persistent sandbox can contain private data that an automatic recreation would destroy. sbx gives each worktree one stable, path-hashed sandbox identity, refuses to adopt a same-named VM it does not own, and reports Creation drift rather than updating or recreating an existing VM; using stale settings requires an explicit override. Bootstrap can leave partial state, so an incomplete attempt also requires an explicit retry or repair rather than an automatic rerun.
