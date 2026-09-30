// Package config loads and validates a worktree's sbx.toml strictly, so every
// failure is reported before any resource is created or changed.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// supportedFields describes the configuration surface this build translates.
// Later releases extend it; anything else must be rejected explicitly rather
// than silently ignored.
const supportedFields = "image, cpus, memory, shell, and [network]"

// notYetSupported are field names that the spec defines but this build does
// not translate yet. Declaring any of them is an error, not a warning.
var notYetSupported = map[string]struct{}{
	"workspace":       {},
	"mounts":          {},
	"volumes":         {},
	"secrets":         {},
	"ports":           {},
	"env":             {},
	"build":           {},
	"bootstrap":       {},
	"image_check":     {},
	"dns_nameservers": {},
}

// Config is the validated content of one worktree's sbx.toml.
type Config struct {
	Image   string        `toml:"image"`
	CPUs    float64       `toml:"cpus"`
	Memory  string        `toml:"memory"`
	Shell   string        `toml:"shell"`
	Network NetworkConfig `toml:"network"`
}

// NetworkConfig is the required [network] table.
type NetworkConfig struct {
	Egress string   `toml:"egress"`
	Allow  []string `toml:"allow"`
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
	if c.CPUs <= 0 {
		problems = append(problems, "cpus: required, a positive number of CPU cores")
	}
	if !sizeRe.MatchString(c.Memory) {
		problems = append(problems, `memory: required, an integer with a "K", "M", or "G" suffix such as 512M`)
	}
	problems = append(problems, c.Network.validate()...)
	if len(problems) > 0 {
		return fmt.Errorf("sbx.toml is invalid:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// sizeRe matches msb's size format: an integer with a K, M, or G suffix.
var sizeRe = regexp.MustCompile(`^[0-9]+[KMG]$`)

func (n *NetworkConfig) validate() []string {
	var problems []string
	switch n.Egress {
	case "public", "none":
		if len(n.Allow) > 0 {
			problems = append(problems, `network: allow is only valid with egress = "allowlist"`)
		}
	case "allowlist":
		if len(n.Allow) == 0 {
			problems = append(problems, `network: allow is required and must not be empty with egress = "allowlist"`)
		}
		for _, entry := range n.Allow {
			if !AllowEntryValid(entry) {
				problems = append(problems, fmt.Sprintf("network: allow entry %q is not an exact domain or a \"*.\"-prefixed suffix of at least two labels", entry))
			}
		}
	default:
		problems = append(problems, `network: egress is required and must be "public", "allowlist", or "none"`)
	}
	return problems
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
