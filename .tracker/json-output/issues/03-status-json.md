# `status --json`

Status: resolved
Blocked by: 01

The full meaning-preserving projection of the status report:
`{sandbox, exists, owned?, status?, created_at?, created_from_image_contents?,
drift?, ports?, bootstrap?, bootstrap_declared?}`.

- Not created → `{sandbox, exists: false}` plus `bootstrap_declared`.
- Unowned → `{sandbox, exists: true, owned: false, status}`.
- Drift is `{}` when checked and clean, `{unknown}` when the
  configuration does not translate, else notes and setting/was/now
  entries.
- Ports reuse the collected findings (published host/guest pairs and
  registry discrepancy lines); no section when nothing is published and
  nothing disagrees, matching the prose.
- Split `reportPorts` into collect + render so both surfaces share one
  read.

Golden-test against the seeded fake backend, as the plain status golden
does.

## Comments

Implemented the status projection and shared port collection/rendering. Goldens cover absent, unowned, owned, clean, drifted, changed-Bootstrap, and untranslatable configurations, including published ports and registry discrepancies. JSON failures leave stdout empty.
