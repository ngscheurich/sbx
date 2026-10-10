package plan

import (
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/ports"
)

type jsonPlan struct {
	Project         string           `json:"project"`
	WorktreeRoot    string           `json:"worktree_root"`
	CommonGitDir    string           `json:"common_git_dir"`
	Sandbox         string           `json:"sandbox"`
	VolumeNamespace string           `json:"volume_namespace"`
	TranslateError  string           `json:"translate_error,omitempty"`
	Config          *jsonConfig      `json:"config,omitempty"`
	Translation     *jsonTranslation `json:"translation,omitempty"`
	CreateArgv      []string         `json:"create_argv,omitempty"`
	Live            *jsonLive        `json:"live,omitempty"`
}

type jsonConfig struct {
	Image  string  `json:"image"`
	CPUs   float64 `json:"cpus"`
	Memory string  `json:"memory"`
	Shell  string  `json:"shell"`
}

type jsonTranslation struct {
	Workspace         string              `json:"workspace"`
	Mounts            []jsonBind          `json:"mounts"`
	Tmpfs             []jsonTmpfs         `json:"tmpfs"`
	ProjectVolumes    []jsonProjectVolume `json:"project_volumes"`
	SandboxVolumes    []jsonSandboxVolume `json:"sandbox_volumes"`
	Environment       []string            `json:"environment"`
	Network           jsonNetwork         `json:"network"`
	Ports             []jsonPortOutlook   `json:"ports"`
	BootstrapDeclared bool                `json:"bootstrap_declared"`
	ImageCheck        *jsonImageCheck     `json:"image_check,omitempty"`
	Secrets           []jsonSecret        `json:"secrets"`
	VolumeCheckError  string              `json:"volume_check_error,omitempty"`
	PortRegistryError string              `json:"port_registry_error,omitempty"`
}

type jsonBind struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
	IsFile   bool   `json:"is_file"`
}

type jsonTmpfs struct {
	Target string `json:"target"`
	Size   string `json:"size"`
	NoExec bool   `json:"noexec"`
}

type jsonProjectVolume struct {
	Name     string              `json:"name"`
	Backend  string              `json:"backend"`
	Target   string              `json:"target"`
	Kind     string              `json:"kind"`
	Size     string              `json:"size,omitempty"`
	Quota    string              `json:"quota,omitempty"`
	Status   volumeState         `json:"status"`
	Conflict *jsonVolumeConflict `json:"conflict,omitempty"`
}

type jsonVolumeConflict struct {
	Existing   msb.VolumeInfo `json:"existing"`
	Mismatches []string       `json:"mismatches"`
}

type jsonSandboxVolume struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Size   string `json:"size,omitempty"`
}

type jsonNetwork struct {
	Policy         string   `json:"policy"`
	Allow          []string `json:"allow"`
	DNSNameservers []string `json:"dns_nameservers"`
	TLSIntercept   bool     `json:"tls_intercept"`
	DenyByDefault  bool     `json:"deny_by_default"`
}

type jsonPortOutlook struct {
	Name      string         `json:"name"`
	Guest     int            `json:"guest"`
	State     string         `json:"state"`
	Host      int            `json:"host,omitempty"`
	HostRange *jsonPortRange `json:"host_range,omitempty"`
}

type jsonPortRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

type jsonImageCheck struct {
	Script        string `json:"script"`
	State         string `json:"state"`
	ImageContents string `json:"image_contents,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type jsonSecret struct {
	Name         string   `json:"name"`
	FromEnv      string   `json:"from_env"`
	Destinations []string `json:"destinations"`
}

type jsonLive struct {
	State          string              `json:"state"`
	Owned          *bool               `json:"owned,omitempty"`
	Drift          *jsonDrift          `json:"drift,omitempty"`
	Bootstrap      string              `json:"bootstrap,omitempty"`
	ObservedPorts  *[]jsonObservedPort `json:"observed_ports,omitempty"`
	ListError      string              `json:"list_error,omitempty"`
	InspectError   string              `json:"inspect_error,omitempty"`
	BootstrapError string              `json:"bootstrap_error,omitempty"`
	PortsError     string              `json:"ports_error,omitempty"`
}

type jsonObservedPort struct {
	Host  int `json:"host"`
	Guest int `json:"guest"`
}

type jsonDrift struct {
	Unknown string             `json:"unknown,omitempty"`
	Notes   []string           `json:"notes,omitempty"`
	Entries []jsonDriftSetting `json:"entries,omitempty"`
}

type jsonDriftSetting struct {
	Setting string `json:"setting"`
	Was     string `json:"was"`
	Now     string `json:"now"`
}

// JSON projects the plan's meaning without decoration or secret values.
// Translation failure leaves only identity and its reason.
func (p Plan) JSON() jsonPlan {
	report := jsonPlan{
		Project: p.Project, WorktreeRoot: p.WorktreeRoot, CommonGitDir: p.CommonDir,
		Sandbox: p.Sandbox, VolumeNamespace: p.VolumeNamespace,
	}
	if p.TranslateErr != nil {
		report.TranslateError = p.TranslateErr.Error()
		return report
	}
	c, tr := p.Config, p.Translation
	report.Config = &jsonConfig{Image: c.Image, CPUs: c.CPUs, Memory: c.Memory, Shell: c.Shell}
	translation := &jsonTranslation{
		Workspace: tr.Workspace,
		Mounts:    make([]jsonBind, 0), Tmpfs: make([]jsonTmpfs, 0, len(tr.Tmpfs)),
		ProjectVolumes: make([]jsonProjectVolume, 0, len(tr.ProjectVolumes)),
		SandboxVolumes: make([]jsonSandboxVolume, 0, len(tr.Options.Owned)),
		Environment:    append([]string{}, tr.Options.Env...),
		Network: jsonNetwork{
			Policy: c.Network.Policy, Allow: append([]string{}, c.Network.Allow...),
			DNSNameservers: append([]string{}, tr.Options.DNSNameservers...),
			TLSIntercept:   tr.Options.TLSIntercept || len(tr.SecretNames) > 0,
			DenyByDefault:  c.Network.Policy == "allowlist",
		},
		Ports:             make([]jsonPortOutlook, 0, len(tr.Ports)),
		BootstrapDeclared: c.BootstrapDeclared(), Secrets: make([]jsonSecret, 0, len(tr.SecretNames)),
	}
	for i, m := range tr.Options.Mounts {
		if i == 0 {
			continue
		}
		translation.Mounts = append(translation.Mounts, jsonBind{
			Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly, IsFile: m.IsFile,
		})
	}
	for _, m := range tr.Tmpfs {
		translation.Tmpfs = append(translation.Tmpfs, jsonTmpfs{Target: m.Target, Size: m.Size, NoExec: m.NoExec})
	}
	for _, v := range tr.ProjectVolumes {
		assessment := p.assessVolume(v)
		volume := jsonProjectVolume{
			Name: v.Logical, Backend: v.Backend, Target: v.Target,
			Kind: v.Kind, Size: v.Size, Quota: v.Quota, Status: assessment.state,
		}
		if conflict := assessment.conflict; conflict != nil {
			volume.Conflict = &jsonVolumeConflict{Existing: conflict.Existing, Mismatches: conflict.Mismatches}
		}
		translation.ProjectVolumes = append(translation.ProjectVolumes, volume)
	}
	for _, v := range tr.Options.Owned {
		translation.SandboxVolumes = append(translation.SandboxVolumes, jsonSandboxVolume{Target: v.Target, Kind: v.Kind, Size: v.Size})
	}
	for _, d := range tr.Ports {
		port := jsonPortOutlook{Name: d.Name, Guest: d.Guest}
		if p.PortRegistryErr != nil {
			port.State = "unknown"
		} else {
			for _, ps := range p.Ports {
				if ps.Name == d.Name {
					port.Host = ps.Reserved
				}
			}
			if port.Host != 0 {
				port.State = "reserved"
			} else {
				port.State = "chosen_at_creation"
				port.HostRange = &jsonPortRange{First: ports.FirstPort, Last: ports.LastPort}
			}
		}
		translation.Ports = append(translation.Ports, port)
	}
	for i, name := range tr.SecretNames {
		translation.Secrets = append(translation.Secrets, jsonSecret{
			Name: name, FromEnv: tr.Secrets[i].FromEnv, Destinations: tr.Secrets[i].Allow,
		})
	}
	if p.VolumeCheckErr != nil {
		translation.VolumeCheckError = p.VolumeCheckErr.Error()
	}
	if p.PortRegistryErr != nil {
		translation.PortRegistryError = p.PortRegistryErr.Error()
	}
	if c.ImageCheck != "" {
		check := &jsonImageCheck{Script: c.ImageCheck, State: "unchecked"}
		if p.ImageCheck != nil {
			check.State, check.ImageContents, check.Reason = p.ImageCheck.State, p.ImageCheck.Digest, p.ImageCheck.Reason
		}
		translation.ImageCheck = check
	}
	report.Translation = translation
	report.CreateArgv = append([]string{"msb", "create"}, msb.CreateArgs(tr.Options)...)
	if p.Live != nil {
		report.Live = p.Live.json()
	}
	return report
}

func (l LiveReport) json() *jsonLive {
	if l.ListErr != nil {
		return &jsonLive{State: "unknown", ListError: l.ListErr.Error()}
	}
	if l.InspectErr != nil {
		return &jsonLive{State: "unknown", InspectError: l.InspectErr.Error()}
	}
	report := &jsonLive{State: l.Status, Owned: &l.Owned}
	if !l.Owned {
		return report
	}
	report.Drift = &jsonDrift{}
	if l.DriftErr != nil {
		report.Drift.Unknown = l.DriftErr.Error()
	} else {
		report.Drift.Notes = l.DriftNotes
		for _, e := range l.Drift {
			report.Drift.Entries = append(report.Drift.Entries, jsonDriftSetting{Setting: e.Setting, Was: e.Was, Now: e.Now})
		}
	}
	report.Bootstrap = l.Bootstrap
	if l.BootstrapErr != nil {
		report.BootstrapError = l.BootstrapErr.Error()
	}
	if l.PortsErr != nil {
		report.PortsError = l.PortsErr.Error()
	} else {
		observed := make([]jsonObservedPort, 0, len(l.Ports))
		for _, p := range l.Ports {
			observed = append(observed, jsonObservedPort{Host: p.HostPort, Guest: p.GuestPort})
		}
		report.ObservedPorts = &observed
	}
	return report
}
