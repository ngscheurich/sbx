// Package config loads and validates a worktree's sbx.toml strictly, so every
// failure is reported before any resource is created or changed.
package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// supportedFields describes the configuration surface this build translates.
// Later releases extend it; anything else must be rejected explicitly rather
// than silently ignored.
const supportedFields = `image, cpus, memory, shell, image_check, [workspace],
[mounts], [volumes], [env], [secrets], [bootstrap], [network] (with allow
and dns_nameservers), and [build]`

// notYetSupported are field names that the spec defines but this build does
// not translate yet. Declaring any of them is an error, not a warning.
var notYetSupported = map[string]struct{}{
	"ports": {},
}

// Config is the validated content of one worktree's sbx.toml.
type Config struct {
	Image     string                  `toml:"image"`
	CPUs      float64                 `toml:"cpus"`
	Memory    string                  `toml:"memory"`
	Shell     string                  `toml:"shell"`
	Workspace WorkspaceConfig         `toml:"workspace"`
	Mounts    []MountConfig           `toml:"mounts"`
	Volumes   map[string]VolumeConfig `toml:"volumes"`
	Env       map[string]string       `toml:"env"`
	Secrets   map[string]SecretConfig `toml:"secrets"`
	Network   NetworkConfig           `toml:"network"`
	// ImageCheck is the optional path of the project-owned guest script
	// that gates image use: it runs in an isolated sandbox from the image
	// alone before any sandbox uses the image (ADR-0004).
	ImageCheck string `toml:"image_check"`
	// Build is the optional [build] recipe; nil means the image is prebuilt
	// and may be pulled, while a declared recipe means `sbx build` builds
	// and imports it and nothing else may.
	Build     *BuildConfig    `toml:"build"`
	Bootstrap BootstrapConfig `toml:"bootstrap"`
}

// BuildConfig is the optional [build] table: the Docker recipe for the
// project's image.
type BuildConfig struct {
	// Context is the build context directory: absolute, relative to the
	// worktree root, or starting with "~".
	Context string `toml:"context"`
	// Dockerfile names the Dockerfile, resolved like Context; empty means
	// <context>/Dockerfile, which Docker defaults to.
	Dockerfile string `toml:"dockerfile"`
	// Target is the optional Dockerfile stage to build.
	Target string `toml:"target"`
	// Platform is the target platform, defaulting to linux/<host
	// architecture>.
	Platform string `toml:"platform"`
}

// BootstrapConfig is the optional [bootstrap] table: guest shell code run
// once in every new sandbox, through the configured shell in the Workspace.
// Editing it never causes Creation drift; a changed definition is reported
// against the recorded completion instead.
type BootstrapConfig struct {
	// Run is the guest shell code.
	Run string `toml:"run"`
}

// BootstrapDeclared reports whether the configuration carries a Bootstrap
// definition. A declared-but-empty run is rejected at load, so a non-empty
// Run here always names real guest code.
func (c Config) BootstrapDeclared() bool {
	return strings.TrimSpace(c.Bootstrap.Run) != ""
}

// WorkspaceConfig is the optional [workspace] table. An absent table keeps
// the default target.
type WorkspaceConfig struct {
	Target string `toml:"target"`
}

// DefaultWorkspaceTarget is the Workspace target when [workspace] is absent:
// the worktree root is mounted read-write here, and it is also the guest
// working directory.
const DefaultWorkspaceTarget = "/workspace"

// MountConfig is one [[mounts]] entry: a host bind or a guest tmpfs.
type MountConfig struct {
	// Type is "bind" or "tmpfs".
	Type string `toml:"type"`
	// Source is the host path of a bind: absolute, relative to the worktree
	// root, or starting with "~".
	Source string `toml:"source"`
	// Target is the absolute guest mount point.
	Target string `toml:"target"`
	// ReadOnly makes a bind read-only.
	ReadOnly bool `toml:"read_only"`
	// Size sizes a tmpfs, in msb's size format.
	Size string `toml:"size"`
	// NoExec mounts a tmpfs with the noexec option (default false).
	NoExec bool `toml:"noexec"`
}

// VolumeConfig is one [volumes.<name>] entry.
type VolumeConfig struct {
	// Target is the absolute guest mount point.
	Target string `toml:"target"`
	// Scope is "sandbox" (private, removed with its sandbox) or "project"
	// (shared across the project's worktrees and retained).
	Scope string `toml:"scope"`
	// Kind is "dir" (default) or "disk".
	Kind string `toml:"kind"`
	// Size is required for disks, in msb's size format.
	Size string `toml:"size"`
	// Quota limits a Project-scope directory volume.
	Quota string `toml:"quota"`
}

// SecretConfig is one [secrets.<name>] entry. The guest environment variable
// <name> holds a placeholder; the backend substitutes the host value of
// FromEnv only in requests to the Allow destinations.
type SecretConfig struct {
	// FromEnv is the host environment variable holding the value.
	FromEnv string `toml:"from_env"`
	// Allow lists the destinations the value may be sent to.
	Allow []string `toml:"allow"`
}

// NetworkConfig is the required [network] table.
type NetworkConfig struct {
	Egress         string   `toml:"egress"`
	Allow          []string `toml:"allow"`
	DNSNameservers []string `toml:"dns_nameservers"`
}

// Load parses and validates the sbx.toml at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("sbx.toml: %w", err)
	}
	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("sbx.toml: %w", err)
	}
	if err := strictFields(md); err != nil {
		return Config{}, err
	}
	if md.IsDefined("bootstrap") && strings.TrimSpace(cfg.Bootstrap.Run) == "" {
		return Config{}, fmt.Errorf("sbx.toml: bootstrap.run is required when [bootstrap] is declared; it is the guest shell code run once in every new sandbox")
	}
	if md.IsDefined("image_check") && strings.TrimSpace(cfg.ImageCheck) == "" {
		return Config{}, fmt.Errorf("sbx.toml: image_check is declared empty; name the project-owned guest script that gates image use, or remove the declaration")
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults fills optional fields with their documented defaults so
// callers always see a complete configuration.
func (c *Config) applyDefaults() {
	if c.Shell == "" {
		c.Shell = "/bin/sh"
	}
	if c.Workspace.Target == "" {
		c.Workspace.Target = DefaultWorkspaceTarget
	}
	for name, v := range c.Volumes {
		if v.Kind == "" {
			v.Kind = "dir"
			c.Volumes[name] = v
		}
	}
	if c.Build != nil && c.Build.Platform == "" {
		// OCI architecture names match GOARCH for the platforms v1 runs on.
		c.Build.Platform = "linux/" + runtime.GOARCH
	}
}

// strictFields rejects every key the target struct does not recognize, so
// typos and not-yet-supported fields are errors rather than silent omissions.
func strictFields(md toml.MetaData) error {
	if len(md.Undecoded()) == 0 {
		return nil
	}
	keys := md.Undecoded()
	for _, key := range keys {
		for _, part := range key {
			if _, unsupported := notYetSupported[part]; unsupported {
				return fmt.Errorf("sbx.toml: %q is declared but not supported yet; this build of sbx accepts %s only", part, supportedFields)
			}
		}
	}
	var names []string
	for _, key := range keys {
		names = append(names, `"`+key.String()+`"`)
	}
	return fmt.Errorf("sbx.toml: unknown field(s) %s; this build of sbx accepts %s", strings.Join(names, ", "), supportedFields)
}

// Validate reports every required-but-missing or invalid field. All failures
// here happen before any action, never after a resource change.
func (c *Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Image) == "" {
		problems = append(problems, "image: required, an OCI image reference")
	}
	if math.IsNaN(c.CPUs) || math.IsInf(c.CPUs, 0) {
		problems = append(problems, "cpus: must be a finite number of CPU cores")
	} else if c.CPUs <= 0 {
		problems = append(problems, "cpus: required, a positive number of CPU cores")
	}
	if !sizeRe.MatchString(c.Memory) {
		problems = append(problems, `memory: required, an integer with a "K", "M", or "G" suffix such as 512M`)
	}
	problems = append(problems, c.validateWorkspace()...)
	problems = append(problems, c.validateMounts()...)
	problems = append(problems, c.validateVolumes()...)
	problems = append(problems, c.validateMountPointConflicts()...)
	problems = append(problems, c.validateEnv()...)
	problems = append(problems, c.validateSecrets()...)
	problems = append(problems, c.Network.validate()...)
	problems = append(problems, c.validateBuild()...)
	if len(problems) > 0 {
		return fmt.Errorf("sbx.toml is invalid:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

func (c *Config) validateWorkspace() []string {
	if c.Workspace.Target == "" {
		return nil
	}
	if !strings.HasPrefix(c.Workspace.Target, "/") {
		return []string{fmt.Sprintf("workspace: target %q must be an absolute guest path", c.Workspace.Target)}
	}
	if p := envRefProblem("workspace", c.Workspace.Target); p != "" {
		return []string{p}
	}
	return nil
}

// envRefProblem rejects "${" in a guest mount target: msb expands "${NAME}"
// references, so a target containing one could silently mount somewhere other
// than declared. Fail closed, mirroring the generated-YAML check.
func envRefProblem(where, target string) string {
	if strings.Contains(target, "${") {
		return fmt.Sprintf("%s: target %q contains %q, which msb would treat as an environment reference; choose a guest path without it", where, target, "${")
	}
	return ""
}

// validateMounts checks each mount's shape and rejects duplicate targets,
// including the Workspace target, which is a mount of its own.
func (c *Config) validateMounts() []string {
	var problems []string
	seen := map[string]string{c.Workspace.Target: "the workspace"}
	for i, m := range c.Mounts {
		where := fmt.Sprintf("mounts[%d]", i)
		switch m.Type {
		case "bind":
			if strings.TrimSpace(m.Source) == "" {
				problems = append(problems, where+": bind source is required")
			}
			if m.NoExec {
				problems = append(problems, where+": noexec belongs to tmpfs mounts")
			}
		case "tmpfs":
			if !sizeRe.MatchString(m.Size) {
				problems = append(problems, where+`: tmpfs size is required, an integer with a "K", "M", or "G" suffix such as 512M`)
			}
			if m.Source != "" {
				problems = append(problems, where+": tmpfs mounts take no source")
			}
			if m.ReadOnly {
				problems = append(problems, where+": read_only belongs to bind mounts")
			}
		case "":
			problems = append(problems, where+`: type is required, "bind" or "tmpfs"`)
		default:
			problems = append(problems, fmt.Sprintf("%s: type %q is not one of \"bind\" or \"tmpfs\"", where, m.Type))
		}
		if !strings.HasPrefix(m.Target, "/") {
			problems = append(problems, fmt.Sprintf("%s: target %q must be an absolute guest path", where, m.Target))
		} else if p := envRefProblem(where, m.Target); p != "" {
			problems = append(problems, p)
		}
		if m.Type == "bind" && m.Size != "" {
			problems = append(problems, where+": bind mounts take no size; size belongs to tmpfs mounts")
		}
		if prior, dup := seen[m.Target]; dup {
			problems = append(problems, fmt.Sprintf("%s: duplicate mount target %q (already used by %s)", where, m.Target, prior))
		} else {
			seen[m.Target] = where
		}
	}
	return problems
}

// validateMountPointConflicts rejects duplicate targets across every kind of
// guest mount point: the Workspace target, mount targets, and volume targets
// all occupy guest paths, and overlapping mounts have undefined behavior in
// msb, so the conflict must surface here rather than after resource changes.
// Volume names iterate in sorted order for deterministic messages.
func (c *Config) validateMountPointConflicts() []string {
	var problems []string
	seen := map[string]string{}
	if c.Workspace.Target != "" {
		seen[c.Workspace.Target] = "the workspace"
	}
	for i, m := range c.Mounts {
		if strings.HasPrefix(m.Target, "/") {
			seen[m.Target] = fmt.Sprintf("mounts[%d]", i)
		}
	}
	names := make([]string, 0, len(c.Volumes))
	for name := range c.Volumes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := c.Volumes[name].Target
		if !strings.HasPrefix(target, "/") {
			continue
		}
		if prior, dup := seen[target]; dup {
			problems = append(problems, fmt.Sprintf("volumes.%s: duplicate mount target %q (already used by %s)", name, target, prior))
		} else {
			seen[target] = "volumes." + name
		}
	}
	return problems
}

func (c *Config) validateVolumes() []string {
	var problems []string
	for name, v := range c.Volumes {
		if !volumeNameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("volumes: name %q must match [a-z][a-z0-9_]*", name))
		}
		switch v.Scope {
		case "sandbox", "project":
		case "":
			problems = append(problems, fmt.Sprintf("volumes.%s: scope is required, \"sandbox\" or \"project\"", name))
		default:
			problems = append(problems, fmt.Sprintf("volumes.%s: scope %q is not one of \"sandbox\" or \"project\"", name, v.Scope))
		}
		kind := v.Kind
		if kind == "" {
			kind = "dir"
		}
		if kind != "dir" && kind != "disk" {
			problems = append(problems, fmt.Sprintf("volumes.%s: kind %q is not one of \"dir\" or \"disk\"", name, kind))
		}
		if !strings.HasPrefix(v.Target, "/") {
			problems = append(problems, fmt.Sprintf("volumes.%s: target %q must be an absolute guest path", name, v.Target))
		} else if p := envRefProblem("volumes."+name, v.Target); p != "" {
			problems = append(problems, p)
		}
		switch {
		case kind == "disk" && !sizeRe.MatchString(v.Size):
			problems = append(problems, fmt.Sprintf("volumes.%s: a disk volume requires size, an integer with a \"K\", \"M\", or \"G\" suffix such as 8G", name))
		case kind == "dir" && v.Size != "":
			problems = append(problems, fmt.Sprintf("volumes.%s: a directory volume takes no size", name))
		}
		if v.Quota != "" {
			if !sizeRe.MatchString(v.Quota) {
				problems = append(problems, fmt.Sprintf("volumes.%s: quota must be an integer with a \"K\", \"M\", or \"G\" suffix such as 4G", name))
			}
			if kind == "disk" || v.Scope == "sandbox" {
				problems = append(problems, fmt.Sprintf("volumes.%s: quota is only valid on a Project-scope directory volume", name))
			}
		}
	}
	return problems
}

func (c *Config) validateEnv() []string {
	var problems []string
	for name := range c.Env {
		if !envNameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("env: name %q must match [A-Za-z_][A-Za-z0-9_]*", name))
		}
	}
	return problems
}

func (c *Config) validateSecrets() []string {
	var problems []string
	for name, s := range c.Secrets {
		if _, clash := c.Env[name]; clash {
			problems = append(problems, fmt.Sprintf("secrets.%s: conflicts with [env] %s; both define the guest variable %s", name, name, name))
		}
		if !envNameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("secrets: name %q must match [A-Za-z_][A-Za-z0-9_]*", name))
		}
		if strings.TrimSpace(s.FromEnv) == "" {
			problems = append(problems, fmt.Sprintf("secrets.%s: from_env is required, the host environment variable holding the value", name))
		} else if !envNameRe.MatchString(s.FromEnv) {
			problems = append(problems, fmt.Sprintf("secrets.%s: from_env %q must match [A-Za-z_][A-Za-z0-9_]*", name, s.FromEnv))
		}
		if len(s.Allow) == 0 {
			problems = append(problems, fmt.Sprintf("secrets.%s: allow is required and must not be empty", name))
		}
		for _, entry := range s.Allow {
			if !AllowEntryValid(entry) {
				problems = append(problems, fmt.Sprintf("secrets.%s: allow entry %q is not an exact domain or a \"*.\"-prefixed suffix of at least two labels", name, entry))
			}
		}
	}
	return problems
}

// validateBuild checks the optional [build] recipe. The context's existence
// is checked where the worktree root is known (at build time), not here.
func (c *Config) validateBuild() []string {
	if c.Build == nil {
		return nil
	}
	var problems []string
	if strings.TrimSpace(c.Build.Context) == "" {
		problems = append(problems, "build: context is required, the host directory Docker builds from")
	}
	if c.Build.Platform != "" && !platformRe.MatchString(c.Build.Platform) {
		problems = append(problems, fmt.Sprintf("build: platform %q must be os/architecture with an optional variant, such as linux/amd64 or linux/arm/v7", c.Build.Platform))
	}
	return problems
}

// sizeRe matches msb's size format: a nonzero integer without leading zeros,
// with a K, M, or G suffix.
var sizeRe = regexp.MustCompile(`^[1-9][0-9]*[KMG]$`)

// platformRe matches an OCI platform: os/architecture with an optional
// variant, such as linux/amd64 or linux/arm/v7.
var platformRe = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+(/[a-z0-9]+)?$`)

// volumeNameRe matches volume and port names.
var volumeNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// envNameRe matches environment variable and secret names.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (n *NetworkConfig) validate() []string {
	var problems []string
	switch n.Egress {
	case "public", "none", "allowlist":
	default:
		problems = append(problems, `network: egress is required and must be "public", "allowlist", or "none"`)
	}
	if n.Egress != "allowlist" && len(n.Allow) > 0 {
		problems = append(problems, `network: allow is only valid with egress = "allowlist"`)
	}
	if n.Egress == "allowlist" {
		if len(n.Allow) == 0 {
			problems = append(problems, `network: allow is required and must not be empty with egress = "allowlist"`)
		}
		for _, entry := range n.Allow {
			if !AllowEntryValid(entry) {
				problems = append(problems, fmt.Sprintf("network: allow entry %q is not an exact domain or a \"*.\"-prefixed suffix of at least two labels", entry))
			}
		}
	}
	if n.Egress == "none" {
		if len(n.DNSNameservers) > 0 {
			problems = append(problems, `network: dns_nameservers is invalid with egress = "none", which allows no traffic at all`)
		}
	}
	for _, ns := range n.DNSNameservers {
		if !nameserverValid(ns) {
			problems = append(problems, fmt.Sprintf("network: dns_nameservers entry %q is not an IP address or IP:PORT", ns))
		}
	}
	return problems
}

// nameserverValid reports whether an entry is an IP address or IP:PORT, the
// forms msb's --dns-nameserver accepts.
func nameserverValid(entry string) bool {
	if host, _, err := net.SplitHostPort(entry); err == nil {
		return net.ParseIP(host) != nil
	}
	return net.ParseIP(entry) != nil
}

// AllowEntryValid reports whether an allowlist entry is an exact domain or a
// `*.`-prefixed suffix of at least two labels, such as "example.com" or
// "*.example.com". Entries are compared case-insensitively downstream.
func AllowEntryValid(entry string) bool {
	e := strings.ToLower(entry)
	if suffix := strings.HasPrefix(e, "*."); suffix {
		e = strings.TrimPrefix(e, "*.")
	}
	labels := strings.Split(e, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || !labelRe.MatchString(label) {
			return false
		}
	}
	return true
}

// labelRe matches one DNS label: alphanumeric, with interior hyphens.
var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
