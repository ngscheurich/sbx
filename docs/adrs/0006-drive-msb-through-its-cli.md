# ADR-0006: Drive msb through its CLI, not the Go SDK

microsandbox ships a Go SDK with typed results for exit codes, errors, and inspection, which would remove most of the CLI output sbx must otherwise parse and verify. sbx invokes the `msb` CLI instead, for two reasons. The SDK's create path accepts only a raw secret value, which microsandbox persists in its durable sandbox configuration, breaking sbx's rule that secret values are never stored; the CLI records only the host environment variable's name. The SDK also requires CGO and a C toolchain, complicating `go install` and cross-compilation, and can drift from the installed runtime's version. The cost is that sbx depends on CLI output and exit behavior that msb does not fully document.

Revisit if the Go SDK accepts a host environment reference when creating a sandbox; the core runtime and the SDK's `Modify` already support one.
