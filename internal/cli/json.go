package cli

import (
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/translate"
)

type prunedReservation struct {
	Sandbox string `json:"sandbox"`
	Name    string `json:"name"`
	Host    int    `json:"host"`
	Guest   int    `json:"guest"`
}

type upResult struct {
	Sandbox        string         `json:"sandbox"`
	Created        bool           `json:"created"`
	Started        bool           `json:"started"`
	ImageContents  string         `json:"image_contents"`
	Bootstrap      string         `json:"bootstrap"`
	BootstrapRan   bool           `json:"bootstrap_ran"`
	PublishedPorts []portEndpoint `json:"published_ports"`
	Drift          *driftResult   `json:"drift,omitempty"`
}

type statusResult struct {
	Sandbox                  string        `json:"sandbox"`
	Exists                   bool          `json:"exists"`
	Owned                    *bool         `json:"owned,omitempty"`
	Status                   string        `json:"status,omitempty"`
	CreatedAt                string        `json:"created_at,omitempty"`
	CreatedFromImageContents string        `json:"created_from_image_contents,omitempty"`
	Drift                    *driftResult  `json:"drift,omitempty"`
	Ports                    *portFindings `json:"ports,omitempty"`
	Bootstrap                string        `json:"bootstrap,omitempty"`
	BootstrapDeclared        *bool         `json:"bootstrap_declared,omitempty"`
}

type driftResult struct {
	Unknown string         `json:"unknown,omitempty"`
	Notes   []string       `json:"notes,omitempty"`
	Entries []driftSetting `json:"entries,omitempty"`
}

type driftSetting struct {
	Setting string `json:"setting"`
	Was     string `json:"was"`
	Now     string `json:"now"`
}

type portFindings struct {
	Published     []portEndpoint `json:"published,omitempty"`
	Discrepancies []string       `json:"discrepancies,omitempty"`
	PublishedErr  string         `json:"published_error,omitempty"`
	RegistryErr   string         `json:"registry_error,omitempty"`
}

type sandboxVolume struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Size   string `json:"size,omitempty"`
}

type rmResult struct {
	Sandbox               string           `json:"sandbox"`
	Removed               bool             `json:"removed"`
	SandboxVolumes        *[]sandboxVolume `json:"sandbox_volumes,omitempty"`
	SandboxVolumesUnknown string           `json:"sandbox_volumes_unknown,omitempty"`
}

type stopResult struct {
	Sandbox string `json:"sandbox"`
	Stopped bool   `json:"stopped"`
}

type runningAction struct {
	prose        string
	created      bool
	started      bool
	digest       string
	bootstrapRan bool
	ports        []portEndpoint
	drift        *driftResult
}

type portEndpoint struct {
	Name  string `json:"name,omitempty"`
	Host  int    `json:"host"`
	Guest int    `json:"guest"`
}

func projectDrift(notes []string, entries []state.DriftEntry) *driftResult {
	report := &driftResult{Notes: notes}
	for _, e := range entries {
		report.Entries = append(report.Entries, driftSetting{Setting: e.Setting, Was: e.Was, Now: e.Now})
	}
	return report
}

func namedEndpoints(declared []translate.DeclaredPort, actual []msb.PublishedPort) []portEndpoint {
	names := map[int]string{}
	for _, d := range declared {
		names[d.Guest] = d.Name
	}
	endpoints := make([]portEndpoint, 0, len(actual))
	for _, p := range actual {
		endpoints = append(endpoints, portEndpoint{Name: names[p.GuestPort], Host: p.HostPort, Guest: p.GuestPort})
	}
	return endpoints
}

func bootstrapWord(boot bootstrapState) string {
	switch boot {
	case bootstrapComplete:
		return "complete"
	case bootstrapChanged:
		return "changed"
	case bootstrapIncomplete:
		return "incomplete"
	default:
		return "not_declared"
	}
}
