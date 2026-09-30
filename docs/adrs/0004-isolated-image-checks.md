# ADR-0004: Run image checks in isolated guest sandboxes

Letting each project create its own check VM through `msb` would embed backend lifecycle commands in project code. sbx instead creates a temporary sandbox from the image, runs the project-owned guest image check without workspace mounts, data volumes, or secrets, and removes the sandbox afterward. A check failure leaves existing worktree data alone but blocks persistent and disposable use of the unchecked image; successful checks apply only to the verified image contents and check script.
