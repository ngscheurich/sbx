# ADR-0001: Use an sbx-native project configuration

Passing microsandbox YAML through sbx would make backend syntax and defaults part of the project's configuration contract. sbx instead validates one `sbx.toml` and translates its network, secret, mount, and storage requirements into msb configuration and commands. This costs translation work but lets sbx define those guarantees explicitly rather than inheriting them from a backend configuration file.
