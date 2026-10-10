package plan

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/testsupport"
	"github.com/ngscheurich/sbx/internal/volumes"
)

func planJSONGolden(t *testing.T, name string, p Plan) {
	t.Helper()
	got, err := json.MarshalIndent(p.JSON(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	testsupport.Golden(t, filepath.Join("testdata", name+".golden"), append(got, '\n'))
}

func TestJSONGolden(t *testing.T) {
	t.Setenv("SBX_PLAN_TOKEN", "secret-value-never-print")
	p := testPlan(t)
	planJSONGolden(t, "plan-json", p)
	got, err := json.Marshal(p.JSON())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "secret-value-never-print") {
		t.Fatal("plan leaked a secret value")
	}
}

func TestJSONTranslationFailureGolden(t *testing.T) {
	t.Parallel()
	p := testPlan(t)
	p.TranslateErr = errors.New("bind source missing")
	planJSONGolden(t, "plan-translate-error-json", p)
}

func observedPlan(t *testing.T) Plan {
	t.Helper()
	p := testPlan(t)
	p.Config.ImageCheck = "image-check.sh"
	p.Config.Bootstrap.Run = "echo ready"
	p.Config.Ports = map[string]config.PortConfig{"web": {Guest: 80}}
	return Compose(gitx.Info{WorktreeRoot: p.WorktreeRoot, CommonDir: p.CommonDir}, p.Config)
}

func TestJSONLiveGolden(t *testing.T) {
	t.Parallel()
	p := observedPlan(t)
	p.ImageCheck = &ImageCheckReport{State: "pending", Digest: "sha256:current"}
	p.VolumeReport = &volumes.Report{Reused: []string{"cache"}}
	p.Ports = []PortStatus{{Name: "web", Guest: 80, Reserved: 4001}}
	p.Live = &LiveReport{
		Status: "Stopped", Owned: true, Bootstrap: "changed",
		DriftNotes: []string{"image contents unconfirmed"},
		Drift:      []state.DriftEntry{{Setting: "memory", Was: "1G", Now: "2G"}},
		Ports:      []msb.PublishedPort{{HostPort: 4001, GuestPort: 80}},
	}
	planJSONGolden(t, "plan-live-json", p)
}

func TestJSONShapeGolden(t *testing.T) {
	t.Parallel()
	p := observedPlan(t)
	p.Translation.Options.Mounts = append(p.Translation.Options.Mounts, msb.Mount{Source: "/src/shared", Target: "/shared", ReadOnly: true})
	p.Translation.Options.Owned = []msb.OwnedMount{{Target: "/data", Kind: "disk", Size: "2G"}}
	p.Translation.Tmpfs[0].NoExec = true
	p.ImageCheck = &ImageCheckReport{State: "known", Digest: "sha256:current"}
	report, err := volumes.Check(p.Translation.ProjectVolumes, []msb.VolumeInfo{{Name: "53ee4efe-cache", Kind: "disk", CapacityBytes: testsupport.Int64(1073741824)}})
	if err != nil {
		t.Fatal(err)
	}
	p.VolumeReport = &report
	planJSONGolden(t, "plan-shape-json", p)
}

func TestJSONLiveFailuresAndOwnership(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		live LiveReport
		want string
	}{
		{name: "listing", live: LiveReport{ListErr: errors.New("list failed")}, want: `{"list_error":"list failed","state":"unknown"}`},
		{name: "inspection", live: LiveReport{InspectErr: errors.New("inspect failed")}, want: `{"inspect_error":"inspect failed","state":"unknown"}`},
		{name: "unowned", live: LiveReport{Status: "Stopped", Owned: false, Bootstrap: "incomplete"}, want: `{"owned":false,"state":"Stopped"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPlan(t)
			p.Live = &tc.live
			data, err := json.Marshal(p.JSON().Live)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %s, want %s", data, tc.want)
			}
		})
	}
}

func TestJSONErrorsGolden(t *testing.T) {
	t.Parallel()
	p := observedPlan(t)
	p.VolumeCheckErr = errors.New("volume inspection failed")
	p.PortRegistryErr = errors.New("port registry unreadable")
	p.ImageCheck = &ImageCheckReport{State: "unresolvable", Reason: "image unavailable"}
	p.Live = &LiveReport{
		Status: "Running", Owned: true,
		DriftErr:     errors.New("snapshot unreadable"),
		BootstrapErr: errors.New("marker unreadable"),
		PortsErr:     errors.New("ports unreadable"),
	}
	planJSONGolden(t, "plan-errors-json", p)
}
