# Writing Go in sbx

A guide to writing idiomatic Go for this codebase, distilled from [Effective Go](https://go.dev/doc/effective_go), the Go team's [Code Review Comments](https://go.dev/wiki/CodeReviewComments), the standard library, and the conventions this repo has already settled in its own adapters and command runners.

sbx is a CLI written in Go that reads a worktree's `sbx.toml`, derives the sandbox identity from Git locations, translates the configuration into backend settings, and drives the microsandbox CLI (`msb`) as a subprocess — plus a Docker subprocess for `sbx build`. There is no server, no RPC, no network boundary of our own — the "boundaries" that shape sbx's Go design are the filesystem (reading configuration, writing state) and the child-process calls to `msb`, `docker`, and `git`.

Read §§1–4 before writing your first package. Jump to whichever layer your task touches, then come back to §15 for our local conventions and §16 for the open questions where the ecosystem hasn't converged. When in doubt, prefer stdlib idioms over inventing new ones.

---

## 1. Project layout

A Go module has a conventional shape. The Go team's [`golang-standards/project-layout`](https://github.com/golang-standards/project-layout) is *not* an official standard, but most popular CLIs converge on a subset of it. sbx uses this:

```
go.mod                              # module manifest
go.sum                              # locked deps (committed)
cmd/
  sbx/
    main.go                         # CLI binary entry point — tiny, just calls cli.Run and exits with its code
internal/                           # private code; nothing outside this module can import these packages
  cli/                              # command dispatch and the per-command runners
  config/                           # strict loading + validation of sbx.toml
  docker/                           # adapter for the Docker CLI (sbx build only)
  gitx/                             # Git discovery: worktree root, common Git dir
  identity/                         # configuration-independent sandbox identity
  msb/                              # adapter for the microsandbox CLI
  plan/                             # read-only preview of the intended sandbox
  ports/                            # host port allocation
  state/                            # on-disk state: snapshots, locks, reservations
  testsupport/                      # fake msb/docker executables shared by tests
  translate/                        # pure config → backend-settings seam
  volumes/                          # volume naming + compatibility checks
fixtures/                           # example projects for integration-style tests
```

`go.mod`:

```
module github.com/ngscheurich/sbx

go 1.27.1

require github.com/BurntSushi/toml v1.6.0

require (
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
)
```

The toolchain is pinned in `mise.toml` at the repo root. Use `mise install` to match.

Common commands:

| What | Command |
| --- | --- |
| Build | `go build ./...` |
| Build the CLI binary | `mise build` (emits `bin/sbx-$(GOOS)-$(GOARCH)`) |
| Run | `go run ./cmd/sbx plan` |
| Test | `go test ./...` |
| Test with race detector | `go test -race ./...` |
| Format | `gofmt -w .` or `goimports -w .` |
| Vet | `go vet ./...` |
| CI gates | `mise ci` (test, vet, gofmt check) |
| Update deps | `go get -u ./... && go mod tidy` |

`gofmt` (and its strict superset `goimports`, which also fixes import grouping) is non-negotiable, has no options, and is enforced by `mise ci`. This is on purpose: it eliminates bikeshedding, and every Go project looks the same to a new reader.

### Why `internal/`?

A directory named `internal` is special to the `go` tool: only packages rooted at the parent of `internal/` may import it. For a single-module project that means *only this repo's code* can import `internal/...`. This is the cheapest possible way to keep our domain types from leaking into anyone else's dependency tree and to give ourselves freedom to break internal APIs.

The rule of thumb: **default everything to `internal/`**. Promote a package to `pkg/` only when you have a concrete external consumer and have decided to make stability commitments to them. sbx has none today.

## 2. Packages and imports

A Go file declares its package in the first non-comment line. Every file in the same directory must share that package name. There is no other naming knob — the directory *is* the package.

```go
// Package plan composes the read-only preview of the sandbox sbx would
// create for this worktree.
package plan

import (
    "fmt"
    "strings"

    "github.com/ngscheurich/sbx/internal/config"
    "github.com/ngscheurich/sbx/internal/identity"
)
```

Conventions, in order of preference:

1. **`goimports` groups imports into three blocks** separated by blank lines: standard library, third-party, this module. Don't fight this — set up your editor to run `goimports` on save.
2. **No dot imports** (`import . "fmt"`). They obscure where identifiers come from.
3. **No bare aliases for stylistic reasons.** Alias only on genuine name collision or when the import path's last segment doesn't match the package name (rare; usually a sign of a poorly-named third-party module).
4. **Side-effect imports** (`import _ "embed"`) get their own block at the bottom with the `_` blank identifier and a brief comment explaining why.

### Package naming

- **Lowercase, single word, no underscores or mixedCaps.** `identity`, not `Identity` or `identity_helper`.
- **Singular.** `config`, not `configs`. The package is one concept; the slice type is plural.
- **No stutter with the type it owns.** The `config` package exports `Config`, not `ConfigConfig`. It loads with `Load`, not `LoadConfig` (called as `config.Load(...)`).
- **Avoid grab-bag names.** `util`, `common`, `helpers`, `misc` are smells — they grow without bound and tell the reader nothing. If you need three string helpers, put them in the package that already uses them, or inline.

## 3. Naming

Hard rules from the compiler:

- **Exported identifiers start with an uppercase letter**: `Config`, `Load`, `Plan`. Anything that begins lowercase is package-private.
- **No other visibility modifiers.** No `public` / `private` keywords; just case.

Strong conventions (Go team and stdlib):

- **MixedCaps** for multi-word identifiers, not `snake_case` or `kebab-case`. `ImageCheck`, not `image_check` or `Imagecheck`. (TOML keys stay `snake_case` — `image_check` — see §9; the Go identifiers that wrap them are MixedCaps.)
- **Initialisms keep their case.** `ID`, `URL`, `JSON`, `HTTP`, `UDS`. So `SandboxID`, `parseTOML`. *Mixed case versions like `Id` or `Url` are wrong* — `go vet` and most linters will flag this.
- **Receivers are short**: `c CLI`, `p *Plan`, `t *Translation`. One or two letters, the same letter every time the same type is the receiver. Never `self` or `this`.
- **Getters drop the `Get` prefix.** A getter for the `name` field is `Name()`, not `GetName()`. Setters keep `Set`: `SetName(s string)`.
- **Errors**: variables of type `error` start with `Err` (`ErrNotFound`); error types end in `Error` (`type ParseError struct{...}`).
- **Interfaces named after the method**, with `-er` suffix when there's one method: `Reader`, `Writer`, `Stringer`. Multi-method interfaces describe the role, not the implementation.
- **Boolean-returning functions are predicates** starting with `Is`, `Has`, `Can`, or read as one: `IsEmpty()`, `HasTemplate()`, `CanApply()`. A method named `Applied()` returning `bool` is fine; `GetApplied()` is not.
- **Prefer a useful zero value over a constructor.** `msb.CLI{}` is the model: its zero value runs `msb` from `PATH`, no `NewCLI` needed. Where a constructor does earn its place it is `New` or `NewT` where `T` disambiguates.

Avoid stuttering across package boundaries: `config.Config` reads as `config.Config` from outside the package, which is fine but mildly stuttery. Where a type is the package's central concept a little stutter is accepted (the stdlib has `time.Time`, `context.Context`, and so does this repo); resist it for secondary types.

## 4. Functions, methods, and receivers

```go
// Load reads and validates the sbx.toml at path, returning a Config ready
// to translate.
func Load(path string) (Config, error) {
    // ...
}
```

Conventions:

- **`context.Context` is the first argument**, conventionally named `ctx`. Functions that do I/O, that may block, or that should be cancellable take a context. Pure functions (string parsing, identity derivation from known paths) don't. sbx's work is mostly short-lived, but the context is forwarded to running guest processes, so every command that execs `msb`, `docker`, or `git` takes a `ctx` and honors cancellation.
- **`error` is the last return value.** Always. `(T, error)`, never `(error, T)`.
- **Multiple return values are normal**, but if you find yourself returning four or five, define a struct.
- **No named return values** except as documentation for `(int, int, error)`-style returns where the meaning isn't obvious from types. Naked `return` (relying on named returns) is a smell outside of very short functions; it makes the flow harder to follow.

### Receivers: value vs pointer

The choice is mostly mechanical:

- **Use a pointer receiver if the method mutates the receiver**, or if the type contains a `sync.Mutex` / other non-copyable field, or if the type is large.
- **Use a value receiver only for small, immutable, value-like types** — `msb.CLI` is one string field and is passed by value everywhere; `config.Config` owns maps and slices and is borderline — §16.2 decides the default.
- **Be consistent within a type**: if any method needs a pointer receiver, *all* methods on that type take a pointer receiver. Mixing the two is a documented Go gotcha — value receivers on a pointer-receiver type silently copy.

```go
// Good — all methods on *Translation take a pointer.
func (t *Translation) Validate() error
func (t *Translation) Workspace() string   // even read-only methods stay on the pointer
```

### Options structs and functional options

For constructors with more than two or three configurable knobs, prefer a plain **options struct** over a long parameter list — `msb.CreateOptions` is the model: named fields, a documented zero value, passed positionally.

Reach for **functional options** only when defaults must be explicit at the call site and the option set must grow without breaking callers:

```go
type Option func(*CreateOptions)

func WithWaitDelay(d time.Duration) Option { return func(o *CreateOptions) { o.WaitDelay = d } }
```

Both patterns give callers a future-proof API: adding a knob is non-breaking, and defaults are explicit. *Don't* reach for either when you only have one optional knob — a second function or a bare parameter is fine.

## 5. Structs and embedding

Structs are the workhorse. Model domain entities as structs with named, typed fields:

```go
type Config struct {
    Image      string                  `toml:"image"`
    CPUs       float64                 `toml:"cpus"`
    Memory     string                  `toml:"memory"`
    Shell      string                  `toml:"shell"`
    Mounts     []MountConfig           `toml:"mounts"`
    Volumes    map[string]VolumeConfig `toml:"volumes"`
    Secrets    map[string]SecretConfig `toml:"secrets"`
    // ...
}
```

- **Zero values should be useful.** `msb.CLI{}` is the model: its zero value runs `msb` from `PATH`, no constructor needed. A value that is meaningless until validated — a `Config` must come from `Load` — documents that and keeps construction in one place.
- **Composition over inheritance**: Go has no inheritance. Embedding (a field whose type is itself a struct or interface, declared without a field name) gives you method promotion. Use it sparingly — it works well for *capability composition* (mixing in a `sync.Mutex`, a logger) but poorly for *domain modelling*. When you find yourself reaching for it to express "a sandbox volume is a kind of volume," use an explicit field or an interface instead.

### Struct literals

Always use **keyed struct literals**: `Config{Image: img, CPUs: cpus}`, not `Config{img, cpus}`. Positional literals break silently when a field is added or reordered. `go vet` enforces this for stdlib types; we enforce it everywhere via `golangci-lint`.

### Field tags and TOML

`BurntSushi/toml` uses `toml:"..."` tags to map struct fields to TOML keys:

```go
type Config struct {
    Image      string                  `toml:"image"`
    CPUs       float64                 `toml:"cpus"`
    ImageCheck string                  `toml:"image_check"`
    Volumes    map[string]VolumeConfig `toml:"volumes"`
}
```

When the on-disk shape and the domain type coincide, decode straight into the exported type, as `internal/config` does. Reach for an unexported mirror struct only when the two must drift independently — a config format that gains fields the domain type shouldn't see yet. See §9.

## 6. Interfaces (consumer-side)

Go interfaces are structural — any type with the right methods satisfies the interface, no `implements` declaration needed. The standard library makes heavy use of this with tiny, single-method interfaces (`io.Reader`, `io.Writer`, `fmt.Stringer`).

**Define interfaces where they are consumed, not where they are implemented.** The package that needs a behavior defines its own minimal interface; the concrete type declares none on the *producer* side. This is the most-cited difference from Java/C# practice and it is genuinely important:

- It keeps interfaces small (only the methods the caller actually needs).
- It avoids speculative abstractions ("I might want to swap this out one day").
- It makes mocking trivial — the test in the consuming package defines its own fake.

sbx today defines no production interfaces, and that's not an accident: its one extension seam is the `msb` subprocess. `msb.CLI` is a concrete struct passed by value, and tests stand in a fake `msb` executable (§12) rather than a fake interface. A fake process is cruder than a fake interface, but it exercises the real seam — argument construction, environment, output parsing — instead of a Go-shaped abstraction of it. The rules above apply the day an in-process second implementation appears.

Concrete corollary: **a Go function should usually accept interfaces and return concrete types.** Accepting an interface lets callers pass anything; returning the concrete type gives callers the full API. Returning an interface is appropriate when the concrete type is genuinely an implementation detail.

The bar for declaring an interface is: *there are, today, two or more concrete implementations, or there will be in the next change.* Otherwise just use the concrete type.

## 7. Error handling

Go has no exceptions. Errors are values, returned alongside results, checked explicitly.

```go
c, err := config.Load(path)
if err != nil {
    return fmt.Errorf("load sbx.toml %s: %w", path, err)
}
```

### 7.1 The `if err != nil` block

The single most common piece of Go code. Read it as a return-on-error short-circuit. Don't try to make it less verbose with helpers; the language has rejected several proposals to do so, and the explicitness is a feature.

### 7.2 Wrapping with `%w` (Go 1.13+)

`fmt.Errorf("context: %w", err)` produces a new error that *wraps* the original. Wrap when you cross a layer boundary and want to add context but preserve the underlying error for `errors.Is` / `errors.As`:

```go
func loadConfig(path string) (config.Config, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        return config.Config{}, fmt.Errorf("read sbx.toml %s: %w", path, err)
    }
    // ...
}
```

The verb is `%w`, not `%s` or `%v`. `%w` preserves the chain; `%s` flattens it to a string and loses `errors.Is` support.

### 7.3 `errors.Is` and `errors.As`

```go
// Sentinel comparison
if errors.Is(err, os.ErrNotExist) { return nil }

// Typed extraction
var pe *toml.ParseError
if errors.As(err, &pe) {
    return fmt.Errorf("malformed sbx.toml at line %d", pe.Line)
}
```

Use these for *behaviour* (not-found, malformed-configuration, backend-failure). Don't write `if err.Error() == "foo"` — string-comparing errors is brittle, and `errors.Is` is the supported idiom.

### 7.4 Sentinel errors vs typed errors

Two patterns, both common, both fine in their place:

**Sentinel** — a package-level error value, compared with `errors.Is`:

```go
var ErrNotFound = errors.New("sandbox not found")

if errors.Is(err, ErrNotFound) { ... }
```

Use for *categorical* failures where the caller only needs to know "this happened" — `io.EOF`, `os.ErrNotExist`, `context.Canceled`.

**Typed** — a struct that satisfies `error`, with fields the caller might want:

```go
type ExecError struct {
    Sandbox string
    Command string
    Output  string
}

func (e *ExecError) Error() string {
    return fmt.Sprintf("exec %s in sandbox %s failed", e.Command, e.Sandbox)
}
```

Use when the caller wants *details* — which sandbox, which command, what output. Extract with `errors.As`.

In practice a single subsystem often exports both: sentinels for the cheap cases, typed errors for the rich cases. Don't agonise — start with sentinels, promote to typed when a caller actually needs structured data. The `plan` package shows a third shape when partial results are worth rendering: each check's failure becomes a report field (`Plan.TranslateErr`, `Plan.VolumeCheckErr`) instead of a returned error, so one bad check doesn't hide the rest of the picture.

### 7.5 Aggregated errors

When one pass over many items can fail independently per item, collect every failure and report them together rather than short-circuiting on the first. Go's stdlib has `errors.Join` (Go 1.20+) for joining multiple errors into one that `errors.Is` traverses; use it, or build a small typed `MultiError` if individual errors need to be iterated for per-item reporting:

```go
var errs []error
for _, v := range declared {
    if err := checkVolume(ctx, box, v); err != nil {
        errs = append(errs, fmt.Errorf("%s: %w", v.Logical, err))
    }
}
if len(errs) > 0 {
    return errors.Join(errs...)
}
```

When partial results are worth showing, prefer sbx's report-field shape instead: carry each failure on the result (`Plan.TranslateErr`, `Plan.VolumeCheckErr`) and render what succeeded alongside what failed.

### 7.6 `panic` and `recover`

`panic` is for *programmer errors* — invariants the code itself violates, like indexing past a slice or dereferencing a nil pointer. Never use it for control flow, and never use it to signal "expected" failures (missing configuration, TOML parse errors, backend failures).

**Reserve `panic` for `main` and `init`-time impossibilities**, paired with a comment that makes the invariant explicit. `recover` is even rarer — the only legitimate use is at goroutine boundaries to keep one bad worker from killing the program.

### 7.7 Don't drop errors

```go
// Wrong
c, _ := config.Load(path)

// Right
c, err := config.Load(path)
if err != nil { /* handle or propagate */ }
```

The blank identifier silences the compiler but lies to the reader. The only legitimate uses of `_` for an error are:

- Closing a `*os.File` you only opened to read (when the close error genuinely doesn't matter — and even then, log it).
- Type assertions you've already validated.

If an error is truly safe to ignore, write a comment saying why.

## 8. Concurrency: goroutines, channels, context

sbx is mostly serial: discover the worktree, translate the configuration, then drive `msb`. There is no long-running server and no event loop today. So most code should *not* spawn goroutines directly — but when concurrency is introduced (running the plan's independent checks in parallel is a plausible future change), follow these rules.

### 8.1 Goroutines have owners

Every goroutine you launch must have a clear answer to two questions: *how does it stop*, and *who is waiting for it?*

- **How it stops**: a `context.Context` it watches, a channel close, or a finite amount of work it will complete on its own.
- **Who waits**: a `sync.WaitGroup`, an `errgroup.Group`, or a result channel the caller reads.

A goroutine without both is a leak. `go func() { for { ... } }()` with no exit condition is almost always wrong.

### 8.2 `context.Context` for cancellation

`context.Context` propagates cancellation signals through call chains. Pass it explicitly; never store it in a struct except for the rare top-of-program long-lived context.

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

return box.Stop(ctx, sandbox)
```

- **Always call `cancel`** — `defer cancel()` is the right pattern even when the context will expire by timeout, because cancelling early releases resources.
- **Don't pass `nil`** as a context; use `context.TODO()` to mark "I don't know yet" or `context.Background()` for genuine top-of-program.
- **Don't put values in context** that aren't request-scoped. `context.Value` is not a parameter-passing mechanism.

### 8.3 Parallel checks (future)

If the plan's independent checks ever run concurrently, `golang.org/x/sync/errgroup` is the clean shape — it gives cancellation + first-error collection in one type, and pairs naturally with the aggregated-error strategy in §7.5:

```go
g, ctx := errgroup.WithContext(ctx)
for _, check := range checks {
    check := check
    g.Go(func() error { return check(ctx, &p) })
}
return g.Wait()
```

Until then, the serial loop is correct and simpler.

### 8.4 `sync` primitives

For shared mutable state, `sync.Mutex` (or `sync.RWMutex`) is the standard. Keep critical sections short. `sync.Once` for one-shot initialisation. `sync.WaitGroup` for fan-in.

### 8.5 Race detector

`go test -race ./...` catches data races at test time. Run it in CI on every change. The flag costs ~5–10× CPU and memory; that's fine for tests, never for production builds.

## 9. TOML configuration

### 9.1 TOML

`BurntSushi/toml` decodes into a struct with `toml:"..."` tags. When the on-disk shape and the domain type coincide, decode straight into the exported type, as `internal/config` does:

```go
type Config struct {
    Image      string                  `toml:"image"`
    CPUs       float64                 `toml:"cpus"`
    Memory     string                  `toml:"memory"`
    Shell      string                  `toml:"shell"`
    ImageCheck string                  `toml:"image_check"`
    Volumes    map[string]VolumeConfig `toml:"volumes"`
    Secrets    map[string]SecretConfig `toml:"secrets"`
    // ...
}
```

Note the mapping: TOML keys are `snake_case` (`image_check`), Go identifiers are MixedCaps (`ImageCheck`). The tag bridges them.

Decode **strictly**: a key sbx doesn't translate is an error, not a silently ignored line — configuration the backend would never see must fail loudly before anything is created (see `config`'s `supportedFields`). Reach for an unexported mirror struct only when the on-disk format and the domain type must drift independently.

### 9.2 From config to backend options

Translation lives in its own pure package, `translate`: it turns a validated `config.Config` plus Git discovery into backend-facing settings (`msb.CreateOptions`, resolved bind paths, secret-name mappings) and touches no process, no backend, and no host state. Configuration describes behavior; the adapter decides syntax.

Keep this seam pure. Because `translate` has no side effects, the read-only `sbx plan` and the mutating commands share one translation path, and every test of configuration semantics is a plain function call — no backend, no fake executable needed.

## 10. External processes and file I/O

This is sbx's actual boundary — the filesystem (configuration, state) and the subprocess adapters. sbx never speaks a socket protocol of its own: `msb`, `docker`, and `git` are CLIs, and every backend effect is a command line.

### 10.1 File I/O

Prefer `os.ReadFile` / `os.WriteFile` for whole-file operations and `os.MkdirAll` for directory creation (the Go equivalent of `mkdir -p`). Check existence with `os.Stat` and `errors.Is(err, os.ErrNotExist)` rather than a separate `is_file` call:

```go
func configPath(worktreeRoot string) (string, error) {
    p := filepath.Join(worktreeRoot, "sbx.toml")
    info, err := os.Stat(p)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return "", fmt.Errorf("%s: no sbx.toml in this worktree", p)
        }
        return "", fmt.Errorf("stat %s: %w", p, err)
    }
    if info.IsDir() {
        return "", fmt.Errorf("%s: not a file", p)
    }
    return p, nil
}
```

Use `filepath.Join`, not string concatenation with `"/"` — it handles platform separators and cleaning.

### 10.2 Child processes

`os/exec` is the stdlib way to run external commands. Always set up the command, then run it; check the error, distinguishing `*exec.ExitError` for non-zero exit codes:

```go
func (c CLI) Stop(ctx context.Context, sandbox string) error {
    cmd := exec.CommandContext(ctx, c.binary(), "stop", "--name", sandbox)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    cmd.WaitDelay = waitDelay // a canceled subprocess can't hang Wait forever
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("msb stop %s: %w: %s", sandbox, err, stderr.String())
    }
    return nil
}
```

Conventions:

- **`exec.Command(name, args...)`** builds the command; `.Run()` waits for completion and errors on non-zero exit, `.Output()` captures stdout and is the right call when you need the output (like `git rev-parse` or `msb`'s JSON inspection output).
- **Don't shell out via `sh -c`** when you can call the binary directly. `exec.Command("docker", "save", img, archive)` is safer than `exec.Command("sh", "-c", "docker save "+img+" "+archive)` — no shell injection, no quoting bugs.
- **Capture or discard stderr deliberately.** `cmd.Stderr = &buf` when you want it for an error message; otherwise it goes nowhere.
- **Set up `cmd.Env` / `cmd.Dir` explicitly** if the command cares — child processes inherit the parent's environment by default, which is usually what sbx wants for `msb`, `docker`, and `git`. The exception is where the backend must be forced: the `msb` adapter pins `MSB_BACKEND` in the child's environment rather than trusting the ambient one.
- **Bound the subprocess's lifetime.** Set `cmd.WaitDelay` so a canceled command that leaks an inherited output stream can't block `Wait` forever — both the `msb` and `docker` adapters do.

### 10.3 The Docker build pipeline

`sbx build` is the only command that builds images, and it never talks to msb's image store directly. The pipeline is three subprocess steps, spelled with long flags throughout:

1. `docker build` the project's image from the configured context and Dockerfile.
2. `docker save` it to a temporary archive outside the repository.
3. `msb load --input <archive>` to import it into msb's separate image store.

The step order is the contract: the archive is a temporary file (cleaned up on failure), and the import must not run against a half-built image. New image plumbing goes through this pipeline, not around it.

## 11. CLI patterns: a hand-rolled dispatcher

sbx does not use a CLI framework. The command surface is small and stable, so `internal/cli` dispatches with a plain `switch` and returns the process exit code:

```go
// cmd/sbx/main.go
func main() {
    os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
```

```go
// internal/cli/cli.go

// Run dispatches one command line and returns the process exit code.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
    if len(args) == 0 {
        fmt.Fprint(stdout, helpText)
        return exitOK
    }
    switch args[0] {
    case "plan":
        return runPlan(ctx, stdout, stderr)
    case "rm":
        return runRm(ctx, args, stdin, stdout, stderr)
    // ...
    default:
        fmt.Fprintf(stderr, "sbx: unknown command %q\n\n%s", args[0], helpText)
        return exitUsage
    }
}
```

Conventions:

- **The dispatcher returns the exit code, not `error`.** Exit codes follow common conventions: `0` success, `1` runtime failure, `2` usage error. A command runner translates its errors into a code and a message; `main` never inspects them.
- **`stdin`/`stdout`/`stderr` are parameters, not globals.** Every runner writes through the writers it was given, which makes the whole command tree testable with a `bytes.Buffer` — no framework accessors, no `os.Stdout` anywhere below `main`.
- **One runner per command.** `runPlan`, `runRm`, and friends; the persistent-sandbox commands share `persistent.go`.
- **Errors print as `sbx: <what failed>` to stderr.** One line, no stack, no usage dump on a runtime error — usage output is for usage errors (exit code 2) and `--help`.
- **Flags are parsed explicitly** (`parsePersistentFlags`) and gated before anything mutates: `sbx rm` reads `--yes` before it will remove anything.
- **The context is forwarded to guest processes.** A canceled `sbx exec` must reach the running guest; that is why `ctx` is the first parameter of `Run` and every runner.
- **Long help is a const** (`helpText`, `portHelpText`), kept in step with the command table; the dispatcher's `default` and empty-args cases both print it.
- **Output strings are prose.** Human-facing messages use typographic apostrophes and quotation marks (`worktree’s`, `“sbx port”`); anything a program consumes — `%q` output, JSON, log keys — stays straight. See [`prose.md`](prose.md), “Program output”.

### Output

For human-facing output (the rendered plan report, error messages), write to the `stdout` / `stderr` writers `Run` received. The exit code is the other half of the contract: `0` success, `1` on failure, `2` on usage errors. Report-style commands prefer sbx's report-field shape (§7.5): render what succeeded alongside what failed, and reserve a returned failure for when nothing useful could be produced at all.

## 12. Testing

The standard library's `testing` package is sufficient. Conventions:

- **`*_test.go` files live alongside the code they test**, in the same package (or in `package foo_test` for black-box testing).
- **Test functions are `func TestXxx(t *testing.T)`**.
- **`t.Run("name", func(t *testing.T) { ... })`** for subtests — gives per-case failures and selective re-running with `go test -run TestSomething/case_one`.
- **`t.Parallel()`** at the top of any test that doesn't share state with siblings.
- **`t.Helper()`** in any helper function that calls `t.Fatal` / `t.Error`, so failure messages point at the caller.
- **`t.Cleanup(fn)`** instead of `defer` for teardown; runs even when a parent test fails.
- **`testing.T.TempDir()`** and **`testing.T.Context()`** (Go 1.24+) — let the framework manage scratch directories and cancellation. sbx's tests lean on `TempDir` heavily: write an `sbx.toml` into a scratch worktree, point discovery at it, and run the command end-to-end without touching a real checkout.

### Table-driven tests

The dominant style. One test function, a slice of cases, a `t.Run` per case:

```go
func TestLoad(t *testing.T) {
    cases := []struct {
        name    string
        input   string
        want    config.Config
        wantErr bool
    }{
        {"full configuration", validTOML, expectedConfig, false},
        {"unknown field", unknownFieldTOML, config.Config{}, true},
        {"bad cpus value", badCPUTOML, config.Config{}, true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // ...
        })
    }
}
```

### Assertion style

The lean is stdlib `if got != want { t.Errorf(...) }`, plus `github.com/google/go-cmp/cmp` for struct diffs when plain equality won't read well:

```go
if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("Config mismatch (-want +got):\n%s", diff)
}
```

Avoid `testify`: the Go team explicitly recommends against it (over-stuffed, encourages bad patterns like `suite`), and `golangci-lint` defaults flag it.

### Golden files

For tests of rendered reports or generated artifacts, write the expected output to `testdata/<name>.golden` and compare. Add a `-update` flag to regenerate on changes:

```go
var update = flag.Bool("update", false, "update golden files")

if *update {
    os.WriteFile(goldenPath, got, 0644)
}
want, _ := os.ReadFile(goldenPath)
if !bytes.Equal(got, want) { /* fail with diff */ }
```

### Testing the subprocess adapters

The adapters shell out to `msb`, `docker`, and `git` and are the hard part to test. sbx's seam is the executable itself: `internal/testsupport` ships a fake `msb` (and fake `docker`) — shell scripts that record every invocation and behave according to `FAKE_MSB_*` environment variables — and tests put them on `PATH` instead of a real backend. Tests therefore exercise the real seam: argument construction, environment, output parsing, error paths.

Two auxiliary techniques:

- **Extract the command construction** into a function that returns `*exec.Cmd` without running it, so a test can assert the command and args without spawning a process.
- **Integration-test against the real backend** only where the fake can't answer, and keep those tests skippable so `go test ./...` passes without an installation (see §16.3).

## 13. Logging

Use the standard library's `log/slog` (Go 1.21+). Structured, levelled, contextual. sbx is a short-lived CLI with no log stream today — user-facing output is the rendered reports on stdout and `sbx: …` error lines on stderr, and that's all. Reach for `slog` only when diagnostic output that isn't user-facing earns its place (e.g. behind a `--debug` flag).

```go
import "log/slog"

logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

logger.Debug("resolved state dir", "path", stateDir)
```

Conventions:

- **Text handler for a CLI** (human-readable); JSON handler only if some tool consumes the output.
- **Levels**: `Debug` (verbose dev), `Info` (normal events), `Warn` (degraded but functional), `Error` (operation failed). No `Fatal` — errors should propagate to a caller that can decide.
- **Key conventions**: `snake_case`, stable across the codebase.
- **No secrets in logs.**

## 14. Documentation comments

Godoc renders any comment *immediately preceding* a top-level declaration as that declaration's documentation. Conventions:

- **Package comment** on the file that declares the package, starts with `// Package <name> `:

  ```go
  // Package config loads and validates a worktree's sbx.toml strictly, so
  // every failure is reported before any resource is created or changed.
  package config
  ```

- **Exported declarations** start their doc comment with the identifier name:

  ```go
  // Load reads and validates the sbx.toml at path, returning a Config ready
  // to translate.
  func Load(path string) (Config, error) { ... }
  ```

  Starting with the identifier matters because tools (`go doc`, IDE hovers) display the first sentence as a one-liner.

- **Doc links** use bracket notation: `[Config.Image]`, `[msb.CreateOptions]`. Renders as a link on pkg.go.dev and in modern IDEs.
- **Examples** (`func ExampleLoad()` etc.) are surfaced inline in godoc and double as tests with an `// Output:` comment.
- **Don't document obvious things.** `// Config represents the configuration.` is worse than no comment; delete it. Document *why* and *invariants*, not *what the name already says*.
- **If a comment explains the name, the name is wrong.** Rename the declaration rather than annotate it.
- **Ordering-dependency comments state the dependency and stop.** When a comment survives because one statement must run before another, write it in that shape — `// Ensure the state dir before saving the snapshot, so the write doesn't fail on a missing dir` — and nothing more.
- **No references to documents outside the code.** A comment never cites `docs/adrs/…`, `docs/specs/…`, or a bare `ADR-0001`. Compress any non-obvious *why* into the comment itself and drop the pointer — the reasoning is found from `docs/adrs/`, not a footnote in the source. A `[msb.CreateOptions]` doc link to another Go symbol is fine; a link out to prose under `docs/` is not. Succinct by default, not by hard cap: keep the lines a real invariant needs, cut everything else. Unexported functions rarely earn a comment at all — name them well and leave them bare.
- **Wrap comment prose at 80 columns.** `gofmt` reflows code but leaves comment text as written, so wrap by hand. (See [`prose.md`](prose.md).)

## 15. Conventions to adopt for sbx

These are the locally-decided defaults. Override only with a comment justifying the divergence.

1. **`internal/` for everything by default.** Promote to `pkg/` only when a concrete external consumer exists.
2. **`goimports` (not just `gofmt`) on save.** Three import blocks, alphabetised within each.
3. **Package names are singular, lowercase, no underscores.** No `util`, `common`, `helpers`.
4. **MixedCaps everywhere, initialisms preserved.** `SandboxID`, `parseTOML`, never `SandboxId` or `Parse_toml`. TOML keys stay `snake_case`; the Go identifiers that wrap them are MixedCaps.
5. **No CLI framework: a `switch` dispatcher in `internal/cli`** — `sbx <command> [flags]`, exit codes 0/1/2 (§11).
6. **Plain options structs** (`msb.CreateOptions`) for constructors with several knobs; useful zero values over constructors where possible.
7. **Errors wrapped with `%w`** at every layer boundary. Wrap with a one-phrase prefix identifying *this* layer's role (`fmt.Errorf("read sbx.toml %s: %w", path, err)`).
8. **Sentinel errors for categorical failures, typed errors for structured detail.** Start with sentinels; promote to typed only when a caller needs fields.
9. **Pointer receivers throughout a type if any method needs one.** Don't mix.
10. **No package-level mutable state** beyond logger and configuration.
11. **stdlib `testing` for assertions; fakes as fake executables in `internal/testsupport`**, not mock generators (§12).
12. **`log/slog` (stdlib), text handler for a CLI.**
13. **Doc comments on every exported identifier.** Start with the identifier name. **No references to `docs/`** — compress the *why* inline, drop the citation.
14. **No `init()`** outside `cmd/sbx/main.go`. Hidden init is hidden control flow.
15. **`mise ci` is the CI gate** (`go test`, `go vet`, `gofmt -l`). `golangci-lint` joins when a profile is picked (§16.5).
16. **Commands are verbs; collections are plural.** sbx's persistent commands always act on this worktree's one sandbox, so the command surface is verbs (`plan`, `run`, `up`) — and a flag or type naming a collection uses the plural (`Volumes`, `Mounts`).

## 16. Open questions

Style points where the ecosystem is split, or where this repo hasn't decided. Decide and delete.

1. **Logger placement.** Options: (i) one `*slog.Logger` passed through every constructor, (ii) `slog.Default()` as an ambient global with `slog.SetDefault` once in `main`, (iii) context-carried logger. The Go team's guidance is (i); (ii) is widespread. sbx has almost no logging today, so the question is cheap to defer — pick before the first `--debug` flag lands.

2. **Pointer vs value for domain types.** `*Config` vs `Config` in returned values, function parameters, and slice element types. `Config` owns maps and slices, so it's not tiny — pointers avoid copying it on every pass. Small value types (`msb.CLI`, an `ID` wrapper) stay values. §4's receiver rule then follows: whichever a type settles on, all its methods agree.

3. **Build tags.** Tests that shell out to a real `msb`/`docker` (live checks) want a skip guard or build tags (`//go:build integration`) so `go test ./...` doesn't fail on hosts without the backend. Decide on a small set of canonical tags and document them.

4. **Generics.** Go 1.18+ supports type parameters; the stdlib uses them sparingly (`slices`, `maps`, `cmp`). Probably reach for the stdlib `slices`/`maps` packages and inline the rest rather than writing generic helpers. The temptation to write `MapErr` will be strong; resist unless a concrete duplication justifies it.

5. **Linter configuration.** `golangci-lint` ships with a dozen analysers; the defaults are conservative. Pick a profile — enable `errcheck`, `gocritic`, `gosec`, `revive` at least — and check it in as `.golangci.yml`, wired into `mise ci`. Pin a version; the tool changes defaults regularly.

6. **Cross-compilation and distribution.** The `build` mise task already emits per-platform binaries (`bin/sbx-$(GOOS)-$(GOARCH)`). Keep the binary CGO-free — the current dependency set is — and if a dependency ever requires CGO, build per-platform in CI. A `goreleaser` config is the natural distribution story for release binaries.

---

## Appendix A: Quick reference

```go
// Package config loads and validates a worktree's sbx.toml.
package config

import (
    "fmt"
    "os"

    "github.com/BurntSushi/toml"
)

// Config is the validated content of one worktree's sbx.toml.
type Config struct {
    Image  string  `toml:"image"`
    CPUs   float64 `toml:"cpus"`
    Memory string  `toml:"memory"`
    Shell  string  `toml:"shell"`
}

// Load reads and validates the sbx.toml at path.
func Load(path string) (Config, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        return Config{}, fmt.Errorf("read %s: %w", path, err)
    }
    var c Config
    if _, err := toml.Decode(string(raw), &c); err != nil {
        return Config{}, fmt.Errorf("parse %s: %w", path, err)
    }
    if c.Image == "" {
        return Config{}, fmt.Errorf("parse %s: image is required", path)
    }
    return c, nil
}
```

## Appendix B: Reading list

- [Effective Go](https://go.dev/doc/effective_go) — the closest thing to an official style document.
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) — terse, opinionated, the source of half the conventions in this guide.
- [Practical Go](https://dave.cheney.net/practical-go) by Dave Cheney — long-form opinions on package design, error handling, naming.
- [`BurntSushi/toml` docs](https://pkg.go.dev/github.com/BurntSushi/toml) — struct-tag decoding, `Marshaler`/`Unmarshaler`.
- [`pkg.go.dev/os/exec`](https://pkg.go.dev/os/exec) — the canonical child-process API.
- [`pkg.go.dev/log/slog`](https://pkg.go.dev/log/slog) — structured logging in the stdlib.
