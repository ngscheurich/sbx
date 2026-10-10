# JSON for the mutations: up, stop, rm, build, port prune

Status: open
Blocked by: 01

Minimal result objects — identity plus outcome:

- `up` → `{sandbox, created, started, image_contents, bootstrap,
  published_ports}`; both booleans false means already running.
  `ensureRunning` returns its action as a struct (prose, created, started,
  digest, ports) so the prose and the JSON cannot drift.
- `stop` → `{sandbox, stopped}`; false means already stopped.
- `rm` → `{sandbox, removed, sandbox_volumes, sandbox_volumes_unknown}`;
  volumes carry kind and size, the unknown reason replaces the list when
  no creation snapshot recorded it.
- `build` → `{image, platform, archive?}`; archive only under
  `--keep-archive`.
- `port prune` → `{pruned: [{sandbox, name, host, guest}]}`; nothing to
  prune is an empty array.

`rm --json` without `--yes` keeps today's refusal unchanged. Golden-test
what the fakes can reach.
