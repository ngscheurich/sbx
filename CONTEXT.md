# sbx

sbx gives a project's Git worktrees isolated local development environments through a sandbox backend.

## Language

**Project**:
A source repository whose worktrees share sandbox configuration.

**Worktree**:
A checkout of a project that sbx isolates from sibling checkouts.

**Workspace**:
The current worktree's source as seen inside its sandbox, mounted from the host as the sandbox's working directory.

**Sandbox**:
An isolated local environment that sbx creates through the backend: a persistent sandbox, a disposable run, or a check sandbox.
_Avoid_: VM, except when discussing the backend's machines

**Persistent sandbox**:
The one long-lived sandbox a worktree may have, kept between commands until explicitly removed.

**Disposable run**:
A fresh sandbox for one guest command, removed when that command finishes. It publishes no ports. Writes to the Workspace and Project volumes can outlive it.
_Avoid_: Ephemeral run, session

**Check sandbox**:
A temporary sandbox that runs an Image check, tied to no worktree and removed afterward.

**Owned sandbox**:
A sandbox sbx created through the backend, and the only kind sbx inspects, judges, or removes; sbx never adopts a sandbox it did not create.
_Avoid_: Managed sandbox, except when naming the attribution label

**Alias**:
A project-configured shorthand for an sbx command line, resolved when the first word of an invocation matches no builtin. Expanding it re-dispatches as if typed; aliases never affect sandbox identity or Creation drift.
_Avoid_: Shortcut, macro

**Backend**:
The provider that creates and manages sandboxes; microsandbox is the initial backend.

**Project configuration**:
The settings and commands that describe a project's sandboxes, read from each worktree's checked-out `sbx.toml`; worktrees on different branches may differ.

**Sandbox identity**:
The name that identifies a persistent sandbox, derived from the project basename, worktree basename, and a short hash of the worktree path, never from editable configuration.

**Image check**:
A project-defined guest script run in a check sandbox to verify an image before either execution mode uses it, without the Workspace, volumes, or secrets.

**Bootstrap**:
A project-defined guest initialization command that prepares each new sandbox for work. Failure leaves a persistent sandbox incomplete until explicitly retried or repaired; a disposable run instead ends and is removed.

**Secret**:
A host environment value that the guest sees only as a placeholder, which the backend replaces with the real value in requests to permitted destinations.

**Volume**:
Persistent storage mounted into a sandbox with an explicit scope: one sandbox or one project's worktrees.

**Sandbox volume**:
A volume private to one sandbox that survives stops and starts but is removed with the sandbox.
_Avoid_: Owned volume, outside discussions of microsandbox itself

**Project volume**:
A volume shared by a project's worktree sandboxes and retained when an individual sandbox is removed. It is not shared with unrelated projects.
_Avoid_: Cache, when the contents are not a cache

**Port reservation**:
A stable host port associated with one named guest service in a persistent sandbox; the reservation survives sandbox removal until explicitly pruned.

**Creation-time settings**:
The parts of project configuration fixed when a persistent sandbox is created: image contents, mounts, volumes, resources, environment, network policy, secrets, and ports.

**Creation drift**:
A difference between a persistent sandbox's creation-time settings and the current project configuration.

**Plan**:
A side-effect-free view of sbx's intended actions and backend translation, including tentative ports and redacted secrets.

**Plain output**:
The ANSI-free bytes sbx writes — what a pipe receives and what a terminal shows under `NO_COLOR`; byte-identical to sbx's output before styling existed.
_Avoid_: Raw output, unstyled output

**Styled output**:
The same content as Plain output with decoration — color, emphasis, and table borders — added on top; never the only carrier of a meaning that words do not already state. The `--plain` lever drops every decoration, borders included.
_Avoid_: Rich output, pretty output
