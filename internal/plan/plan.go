// Package plan composes the read-only preview of the persistent sandbox sbx
// would create for this worktree. Planning changes no host or project state.
package plan

import (
	"fmt"
	"strings"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/translate"
	"github.com/ngscheurich/sbx/internal/volumes"
)

// Plan describes the intended persistent sandbox for one worktree. It is
// deliberately partial in this release: Creation drift, image-check status,
// and tentative ports are added by later releases.
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
	// Translation is the worktree's translated backend settings.
	Translation translate.Translation
	// TranslateErr carries a translation failure, such as a missing bind
	// source, so the Plan can report it instead of a partial translation.
	TranslateErr error
	// VolumeReport is the Project volume compatibility outcome, set by the
	// caller after checking the backend's listing. Nil means the check did
	// not run: Compose performs no backend I/O.
	VolumeReport *volumes.Report
	// VolumeCheckErr records a failed or malformed volume inspection. An
	// uninspectable backend is reported, never read as an empty listing or
	// as compatible definitions.
	VolumeCheckErr error
}

// Compose derives the Plan from Git discovery and validated configuration.
// It performs no I/O beyond resolving bind sources, and writes nothing.
func Compose(info gitx.Info, cfg config.Config) Plan {
	id := identity.Derive(info.CommonDir, info.WorktreeRoot)
	p := Plan{
		Project:         identity.ProjectBasename(info.CommonDir),
		WorktreeRoot:    info.WorktreeRoot,
		CommonDir:       info.CommonDir,
		Sandbox:         id.Sandbox,
		VolumeNamespace: identity.VolumeNamespace(info.CommonDir),
		Config:          cfg,
	}
	tr, err := translate.Sandbox(info, cfg, "persistent")
	if err != nil {
		p.TranslateErr = err
		return p
	}
	// The preview is of the persistent creation, which always names its
	// sandbox; the disposable run is the path that omits --name.
	tr.Options.Name = id.Sandbox
	// The generated files' paths are chosen when a sandbox is created; the
	// Plan shows the maps' content and placeholders instead. Each placeholder
	// appears only when its file would actually be generated, so the rendered
	// arguments match what msb create would receive.
	if tr.SecretConfYAML != "" {
		tr.Options.SecretConf = secretConfPlaceholder
	}
	if tr.FsConfYAML != "" {
		tr.Options.FsConf = fsConfPlaceholder
	}
	p.Translation = tr
	return p
}

// secretConfPlaceholder and fsConfPlaceholder stand in for the generated
// files' paths in rendered msb arguments.
const (
	secretConfPlaceholder = "<secret map generated at creation>"
	fsConfPlaceholder     = "<filesystem configuration generated at creation>"
)

// Render formats the Plan as human-readable text. The output is
// deterministic and contains no secret values.
func (p Plan) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sbx plan — preview of the persistent sandbox for this worktree\n\n")
	fmt.Fprintf(&b, "project:          %s\n", p.Project)
	fmt.Fprintf(&b, "worktree root:    %s\n", p.WorktreeRoot)
	fmt.Fprintf(&b, "common git dir:   %s\n", p.CommonDir)
	fmt.Fprintf(&b, "sandbox identity: %s\n", p.Sandbox)
	fmt.Fprintf(&b, "volume namespace: %s (project volumes are named %s-<logical name>)\n", p.VolumeNamespace, p.VolumeNamespace)

	if p.TranslateErr != nil {
		fmt.Fprintf(&b, "\ntranslation failed, so the sandbox cannot be planned:\n  %v\n", p.TranslateErr)
		fmt.Fprintf(&b, "\nsbx changed nothing: no sandbox was created and no host or project\nstate was written.\n")
		return b.String()
	}

	c := p.Config
	fmt.Fprintf(&b, "\nconfiguration (sbx.toml):\n")
	fmt.Fprintf(&b, "  image:   %s\n", c.Image)
	fmt.Fprintf(&b, "  cpus:    %s\n", msb.FormatCPUs(c.CPUs))
	fmt.Fprintf(&b, "  memory:  %s\n", c.Memory)
	fmt.Fprintf(&b, "  shell:   %s\n", c.Shell)

	tr := p.Translation
	fmt.Fprintf(&b, "\nsandbox:\n")
	fmt.Fprintf(&b, "  workspace: %s (the worktree root, read-write, also the working directory)\n", tr.Workspace)
	if len(tr.Options.Mounts) > 1 || len(tr.Tmpfs) > 0 {
		fmt.Fprintf(&b, "  mounts:\n")
		for _, m := range tr.Options.Mounts[1:] {
			ro := ""
			if m.ReadOnly {
				ro = " (read-only)"
			}
			fmt.Fprintf(&b, "    - bind %s -> %s%s\n", m.Source, m.Target, ro)
		}
		for _, tf := range tr.Tmpfs {
			extra := ""
			if tf.NoExec {
				extra = ", noexec"
			}
			fmt.Fprintf(&b, "    - tmpfs %s (%s%s)\n", tf.Target, tf.Size, extra)
		}
	}
	if len(tr.ProjectVolumes) > 0 || len(tr.Options.Owned) > 0 {
		fmt.Fprintf(&b, "  volumes:\n")
		for _, pv := range tr.ProjectVolumes {
			fmt.Fprintf(&b, "    - %s: project volume %s at %s%s\n", pv.Logical, pv.Backend, pv.Target, p.volumeStatus(pv))
		}
		if len(tr.ProjectVolumes) > 0 {
			switch {
			case p.VolumeCheckErr != nil:
				fmt.Fprintf(&b, "    (compatibility could not be checked: %v;\n", p.VolumeCheckErr)
				fmt.Fprintf(&b, "     an uninspectable backend is never treated as an empty or compatible one)\n")
			case p.VolumeReport == nil:
				fmt.Fprintf(&b, "    (compatibility with existing volumes is checked before any creation)\n")
			}
		}
		for _, om := range tr.Options.Owned {
			kind := "directory"
			if om.Kind == "disk" {
				kind = "disk (" + om.Size + ")"
			}
			fmt.Fprintf(&b, "    - %s: sandbox %s\n", om.Target, kind)
		}
		if p.VolumeReport != nil && len(p.VolumeReport.Conflicts) > 0 {
			fmt.Fprintf(&b, "\nproject volume conflicts (creation would fail before changing anything):\n")
			for _, c := range p.VolumeReport.Conflicts {
				for _, line := range strings.Split(c.String(), "\n") {
					fmt.Fprintf(&b, "  %s\n", line)
				}
			}
		}
	}
	if len(tr.Options.Env) > 0 {
		fmt.Fprintf(&b, "  environment:\n")
		for _, e := range tr.Options.Env {
			fmt.Fprintf(&b, "    - %s\n", e)
		}
	}
	fmt.Fprintf(&b, "  network: %s\n", describeNetwork(c.Network))
	if c.BootstrapDeclared() {
		fmt.Fprintf(&b, "  bootstrap: runs once in each new sandbox, as `%s -c` in the workspace;\n", c.Shell)
		fmt.Fprintf(&b, "    `sbx status` reports its completion against the live sandbox\n")
	}
	if len(tr.SecretNames) > 0 {
		fmt.Fprintf(&b, "  secrets (values never appear in this plan):\n")
		for i, name := range tr.SecretNames {
			s := tr.Secrets[i]
			fmt.Fprintf(&b, "    - %s: host variable %s, sent only to %s\n", name, s.FromEnv, strings.Join(s.Allow, ", "))
		}
		fmt.Fprintf(&b, "  secret map passed as --secret-conf:\n")
		for _, line := range strings.Split(strings.TrimRight(tr.SecretConfYAML, "\n"), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}

	fmt.Fprintf(&b, "\nmsb arguments at creation:\n")
	fmt.Fprintf(&b, "  msb create %s\n", quoteArgs(msb.CreateArgs(tr.Options)))

	fmt.Fprintf(&b, "\nThis plan is partial: Creation drift, Bootstrap completion against a live\n")
	fmt.Fprintf(&b, "sandbox (see `sbx status`), image-check status, and tentative ports are\n")
	fmt.Fprintf(&b, "added by later releases.\n")
	fmt.Fprintf(&b, "sbx changed nothing: no sandbox was created and no host or project\nstate was written.\n")
	return b.String()
}

// volumeStatus renders one declared Project volume's compatibility outcome
// once the check has run: reuse, creation, or a conflict.
func (p Plan) volumeStatus(pv volumes.Declared) string {
	if p.VolumeReport == nil {
		return ""
	}
	for _, c := range p.VolumeReport.Conflicts {
		if c.Declared.Logical == pv.Logical {
			return " — CONFLICT (see below)"
		}
	}
	for _, name := range p.VolumeReport.Reused {
		if name == pv.Logical {
			return " (reuses the existing volume)"
		}
	}
	for _, name := range p.VolumeReport.Fresh {
		if name == pv.Logical {
			return " (will be created)"
		}
	}
	return ""
}

func describeNetwork(n config.NetworkConfig) string {
	var parts []string
	switch n.Egress {
	case "allowlist":
		parts = append(parts, "egress allowlist, deny by default, TLS interception on")
	case "none":
		parts = append(parts, "egress none (--no-net)")
	default:
		parts = append(parts, "egress public")
	}
	if len(n.DNSNameservers) > 0 {
		parts = append(parts, "dns "+strings.Join(n.DNSNameservers, ", "))
	}
	return strings.Join(parts, ", ")
}

// quoteArgs joins msb arguments for display, quoting any argument that
// contains spaces.
func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			quoted[i] = `"` + a + `"`
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}
