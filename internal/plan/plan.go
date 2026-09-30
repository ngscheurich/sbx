// Package plan composes the read-only preview of the persistent sandbox sbx
// would create for this worktree. Planning changes no host or project state.
package plan

import (
	"fmt"
	"strings"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
)

// Plan describes the intended persistent sandbox for one worktree. It is
// deliberately partial in this release: msb argument translation, Creation
// drift, image-check status, and tentative ports are added by later releases.
type Plan struct {
	// Project is the sanitized project basename.
	Project string
	// WorktreeRoot is the absolute, symlink-free worktree root.
	WorktreeRoot string
	// CommonDir is the absolute, symlink-free common Git directory.
	CommonDir string
	// Sandbox is the configuration-independent Sandbox identity.
	Sandbox string
	// VolumeNamespace is the Project volume namespace hash.
	VolumeNamespace string
	// Config is the validated sbx.toml content.
	Config config.Config
}

// Compose derives the Plan from Git discovery and validated configuration.
// It performs no I/O beyond what its inputs already hold.
func Compose(info gitx.Info, cfg config.Config) Plan {
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	return Plan{
		Project:         identity.ProjectBasename(info.CommonDir),
		WorktreeRoot:    info.WorktreeRoot,
		CommonDir:       info.CommonDir,
		Sandbox:         id.Sandbox,
		VolumeNamespace: identity.VolumeNamespace(info.CommonDir),
		Config:          cfg,
	}
}

// Render formats the Plan as human-readable text. The output is
// deterministic and contains no secret values.
func (p Plan) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sbx plan — partial preview of the persistent sandbox for this worktree\n\n")
	fmt.Fprintf(&b, "project:          %s\n", p.Project)
	fmt.Fprintf(&b, "worktree root:    %s\n", p.WorktreeRoot)
	fmt.Fprintf(&b, "common git dir:   %s\n", p.CommonDir)
	fmt.Fprintf(&b, "sandbox identity: %s\n", p.Sandbox)
	fmt.Fprintf(&b, "volume namespace: %s (project volumes are named %s-<logical name>)\n", p.VolumeNamespace, p.VolumeNamespace)

	c := p.Config
	fmt.Fprintf(&b, "\nconfiguration (sbx.toml):\n")
	fmt.Fprintf(&b, "  image:   %s\n", c.Image)
	fmt.Fprintf(&b, "  cpus:    %s\n", formatCPUs(c.CPUs))
	fmt.Fprintf(&b, "  memory:  %s\n", c.Memory)
	fmt.Fprintf(&b, "  shell:   %s\n", c.Shell)
	fmt.Fprintf(&b, "  network: %s\n", describeNetwork(c.Network))

	fmt.Fprintf(&b, "\nThis plan is partial: translation into msb arguments, Creation drift,\n")
	fmt.Fprintf(&b, "image-check status, and tentative ports are added by later releases.\n")
	fmt.Fprintf(&b, "sbx changed nothing: no sandbox was created and no host or project\nstate was written.\n")
	return b.String()
}

func formatCPUs(cpus float64) string {
	if cpus == float64(int(cpus)) {
		return fmt.Sprintf("%d", int(cpus))
	}
	return fmt.Sprintf("%g", cpus)
}

func describeNetwork(n config.NetworkConfig) string {
	switch n.Egress {
	case "allowlist":
		return fmt.Sprintf("egress allowlist (%d destinations)", len(n.Allow))
	case "none":
		return "egress none"
	default:
		return "egress public"
	}
}
