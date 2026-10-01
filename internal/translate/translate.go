// Package translate turns validated project configuration and Git discovery
// into backend-facing settings: msb create-style options, resolved bind
// paths, and the generated secret-name map. It is a pure seam with no
// backend access, shared by the disposable run and the read-only Plan
// (ADR-0001): configuration describes behavior, never msb syntax.
package translate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
)

// Translation is the backend-facing rendering of one worktree's project
// configuration.
type Translation struct {
	// Workspace is the guest path the worktree root is mounted at; it is
	// also the guest working directory.
	Workspace string
	// Options carries the msb create-style settings. SecretConf is left
	// empty: the caller chooses the file's path when it creates one.
	Options msb.CreateOptions
	// SecretConfYAML is the generated secret-name map, empty when no
	// secrets are declared. It holds secret names, "${HOST_VAR}" source
	// references, and allowed destinations — never secret values.
	SecretConfYAML string
	// Secrets are the declared secrets in name order.
	Secrets []config.SecretConfig
	// SecretNames are the declared secret names in name order.
	SecretNames []string
}

// Sandbox translates one worktree's configuration for the given execution
// mode ("persistent" or "disposable", recorded as the sbx.mode label). It
// resolves bind sources against the worktree root and fails on a missing
// source, before any resource is touched.
func Sandbox(info gitx.Info, cfg config.Config, mode string) (Translation, error) {
	t := Translation{Workspace: cfg.Workspace.Target}

	mounts := []msb.Mount{{Source: info.WorktreeRoot, Target: t.Workspace}}
	var tmpfs []msb.Tmpfs
	for i, m := range cfg.Mounts {
		switch m.Type {
		case "bind":
			source, isFile, err := resolveBindSource(info.WorktreeRoot, m.Source)
			if err != nil {
				return Translation{}, fmt.Errorf("mounts[%d]: %w", i, err)
			}
			mounts = append(mounts, msb.Mount{Source: source, Target: m.Target, ReadOnly: m.ReadOnly, IsFile: isFile})
		case "tmpfs":
			tmpfs = append(tmpfs, msb.Tmpfs{Target: m.Target, Size: m.Size, NoExec: m.NoExec})
		}
	}

	var named []msb.NamedMount
	var owned []msb.OwnedMount
	for _, name := range sortedVolumeNames(cfg) {
		v := cfg.Volumes[name]
		switch v.Scope {
		case "project":
			named = append(named, msb.NamedMount{
				Name:   identity.VolumeName(info.CommonDir, name),
				Target: v.Target,
			})
		case "sandbox":
			owned = append(owned, msb.OwnedMount{Target: v.Target, Kind: v.Kind, Size: v.Size})
		}
	}

	options := msb.CreateOptions{
		Image:  cfg.Image,
		CPUs:   cfg.CPUs,
		Memory: cfg.Memory,
		Mounts: mounts,
		Tmpfs:  tmpfs,
		Named:  named,
		Owned:  owned,
		Env:    sortedEnv(cfg.Env),
		Labels: []msb.Label{
			{Key: "sbx.managed", Value: "1"},
			{Key: "sbx.mode", Value: mode},
			{Key: "sbx.worktree", Value: info.WorktreeRoot},
		},
	}
	switch cfg.Network.Egress {
	case "none":
		options.NoNet = true
	case "allowlist":
		for _, entry := range cfg.Network.Allow {
			options.NetRules = append(options.NetRules, "allow@"+entry)
		}
		// Allowlist mode turns on msb's TLS inspection: strict hostname
		// rules can only see the request authority of intercepted HTTPS.
		options.TLSIntercept = true
	}
	options.DnsNameservers = cfg.Network.DnsNameservers
	// Any declared secret also turns on TLS inspection; msb does this
	// itself when a secret is declared, so no extra flag is needed here.

	t.SecretNames = sortedSecretNames(cfg)
	for _, name := range t.SecretNames {
		t.Secrets = append(t.Secrets, cfg.Secrets[name])
	}
	if len(t.SecretNames) > 0 {
		t.SecretConfYAML = SecretConfYAML(cfg.Secrets, t.SecretNames)
	}

	t.Options = options
	return t, nil
}

// CheckSecretEnv verifies that every declared secret's host environment
// variable exists, before any resource changes. msb resolves secret values
// from its own environment at sandbox start; a missing variable would only
// surface later and possibly after creation, so sbx refuses early. Plan
// does not call this: it needs only the variable's name.
func CheckSecretEnv(cfg config.Config, lookup func(string) (string, bool)) error {
	var missing []string
	for _, name := range sortedSecretNames(cfg) {
		host := cfg.Secrets[name].FromEnv
		if _, ok := lookup(host); !ok {
			missing = append(missing, host)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("secret host environment variable(s) not set: %s; set them before running sbx, the values reach msb only through its subprocess environment",
			strings.Join(missing, ", "))
	}
	return nil
}

// SecretConfYAML renders the unwrapped secret-name map msb loads with
// --secret-conf: a map of secret names to definitions whose value records a
// host environment source as "${NAME}", never plaintext. The map key is the
// guest environment variable name; ${NAME} names the host variable msb
// reads at sandbox start.
func SecretConfYAML(secrets map[string]config.SecretConfig, names []string) string {
	var b strings.Builder
	for _, name := range names {
		s := secrets[name]
		fmt.Fprintf(&b, "%s:\n", name)
		fmt.Fprintf(&b, "  value: \"${%s}\"\n", s.FromEnv)
		if len(s.Allow) == 1 {
			fmt.Fprintf(&b, "  allow: [%s]\n", s.Allow[0])
		} else {
			fmt.Fprintf(&b, "  allow:\n")
			for _, entry := range s.Allow {
				fmt.Fprintf(&b, "    - %s\n", entry)
			}
		}
	}
	return b.String()
}

// resolveBindSource resolves a bind source to an absolute host path:
// absolute as-is, "~/..." against the caller's home directory, and anything
// else against the worktree root. A missing source is an error. It also
// reports whether the source is a regular file, which selects msb's
// --mount-file over --mount-dir.
func resolveBindSource(worktreeRoot, source string) (string, bool, error) {
	var resolved string
	switch {
	case strings.HasPrefix(source, "~"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("expanding %q: %w", source, err)
		}
		resolved = filepath.Join(home, strings.TrimPrefix(source, "~"))
	case filepath.IsAbs(source):
		resolved = source
	default:
		resolved = filepath.Join(worktreeRoot, source)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", false, fmt.Errorf("bind source %q does not exist (resolved to %s)", source, resolved)
	}
	return resolved, !info.IsDir(), nil
}

func sortedVolumeNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Volumes))
	for name := range cfg.Volumes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedSecretNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Secrets))
	for name := range cfg.Secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
