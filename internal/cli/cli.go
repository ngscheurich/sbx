// Package cli wires command-line invocations to sbx behavior.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/gitx"
	"github.com/ngscheurich/sbx/internal/identity"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/plan"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/volumes"
)

// Exit codes follow common CLI conventions: 0 for success, 1 for runtime
// failures, 2 for usage errors.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const helpText = `sbx — worktree-scoped local development sandboxes

Usage:
  sbx <command> [flags]

Commands:
  plan    Show what sbx would create for this worktree's persistent sandbox, changing nothing
  build   Build this project's image with Docker and import it into msb (requires [build])
  run     Run a one-off guest command in a disposable sandbox, removed afterward
  up      Create or start this worktree's persistent sandbox, bootstrapping new ones
          (--allow-stale ignores drift; --retry-bootstrap retries an incomplete bootstrap)
  exec    Run a guest command in the persistent sandbox (created or started first), leaving it running
  status  Show the persistent sandbox's identity, state, and drift, changing nothing
  logs    Show the persistent sandbox's msb logs
  stop    Stop the persistent sandbox, keeping its state and volumes
  rm      Remove the persistent sandbox after confirmation (--yes in noninteractive use)
  port prune
          Remove port reservations for sandboxes that no longer exist

Run sbx from any directory inside a Git worktree that has an sbx.toml.
`

// Run dispatches one command line and returns the process exit code. The
// context carries cancellation (sbx forwards it to the running guest) and
// stdin is passed through to guest commands.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, helpText)
		return exitOK
	}
	cmd := args[0]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, helpText)
		return exitOK
	case "plan":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "sbx: plan takes no arguments or flags yet, got %q\n", strings.Join(args[1:], " "))
			return exitUsage
		}
		return runPlan(ctx, stdout, stderr)
	case "build":
		return runBuild(ctx, args, stdout, stderr)
	case "run":
		return runDisposable(ctx, args, stdin, stdout, stderr)
	case "up":
		return runUp(ctx, args, stdout, stderr)
	case "exec":
		return runExec(ctx, args, stdin, stdout, stderr)
	case "status":
		return runStatus(ctx, args, stdout, stderr)
	case "logs":
		return runLogs(ctx, args, stdout, stderr)
	case "stop":
		return runStop(ctx, args, stdout, stderr)
	case "rm":
		return runRm(ctx, args, stdin, stdout, stderr)
	case "port":
		if len(args) < 2 || args[1] != "prune" {
			fmt.Fprintf(stderr, "sbx: unknown command %q; the only port command is %q\n", strings.Join(args, " "), "port prune")
			return exitUsage
		}
		return runPortPrune(ctx, args, stdout, stderr)
	case "-V", "--version", "version":
		fmt.Fprintln(stdout, "sbx (development build)")
		return exitOK
	default:
		fmt.Fprintf(stderr, "sbx: unknown command %q\n\n%s", cmd, helpText)
		return exitUsage
	}
}

func runPlan(ctx context.Context, stdout, stderr io.Writer) int {
	info, cfg, err := discoverConfig(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitFailure
	}
	p := plan.Compose(info, cfg)
	checkPlanVolumes(ctx, msb.CLI{}, &p)
	checkPlanImageCheck(ctx, msb.CLI{}, info, cfg, &p)
	checkPlanPorts(&p)
	checkPlanLive(ctx, msb.CLI{}, info, cfg, &p)
	fmt.Fprint(stdout, p.Render())
	return exitOK
}

// checkPlanImageCheck fills the plan's image-check status. It is
// read-only: the image is inspected and host state is read, but nothing is
// launched, recorded, or changed. An unresolvable status — a missing
// script, an uninspectable image, a corrupt record — is reported, never
// guessed.
func checkPlanImageCheck(ctx context.Context, box msb.CLI, info gitx.Info, cfg config.Config, p *plan.Plan) {
	if cfg.ImageCheck == "" {
		return
	}
	report := &plan.ImageCheckReport{State: "unresolvable"}
	p.ImageCheck = report
	script, err := imageCheckScript(info.WorktreeRoot, cfg.ImageCheck)
	if err != nil {
		report.Reason = err.Error()
		return
	}
	img, err := box.ImageInspect(ctx, cfg.Image)
	if err != nil {
		report.Reason = fmt.Sprintf("msb cannot inspect the image: %v", err)
		return
	}
	report.Digest = img.ManifestDigest
	ok, err := recordedPass(img.ManifestDigest, script)
	if err != nil {
		report.Reason = err.Error()
		return
	}
	if ok {
		report.State = "known"
	} else {
		report.State = "pending"
	}
}

// checkPlanPorts fills the Plan's ports report from a read-only registry
// read. Planning never reserves or corrects: a port without a reservation
// is reported as chosen at creation, and a failed registry read is
// reported, never read as an empty one.
func checkPlanPorts(p *plan.Plan) {
	if p.TranslateErr != nil || len(p.Translation.Ports) == 0 {
		return
	}
	records, err := state.LoadPortReservations()
	if err != nil {
		p.PortRegistryErr = err
		return
	}
	for _, d := range p.Translation.Ports {
		ps := plan.PortStatus{Name: d.Name, Guest: d.Guest}
		for _, r := range records {
			if r.Sandbox == p.Sandbox && r.Name == d.Name {
				ps.Reserved = r.Port
			}
		}
		p.Ports = append(p.Ports, ps)
	}
}

// checkPlanLive fills the plan's report of an existing sandbox under the
// identity, when the backend holds one. It is read-only: listing,
// inspection, drift comparison, and Bootstrap-marker reads change nothing,
// and a drifted sandbox is reported without blocking the plan. The
// reported picture is what status shows; planning adds the intent above it.
func checkPlanLive(ctx context.Context, box msb.CLI, info gitx.Info, cfg config.Config, p *plan.Plan) {
	if p.TranslateErr != nil {
		// A configuration that does not translate leaves only the identity
		// worth reporting; drift against it cannot be computed.
		return
	}
	_, exists, err := findSandbox(ctx, box, p.Sandbox)
	if err != nil {
		p.Live = &plan.LiveReport{ListErr: err}
		return
	}
	if !exists {
		return
	}
	s, err := box.Inspect(ctx, p.Sandbox)
	if err != nil {
		p.Live = &plan.LiveReport{InspectErr: err}
		return
	}
	live := &plan.LiveReport{Status: s.Status, Owned: isOwned(s)}
	p.Live = live
	if !live.Owned {
		// Drift and Bootstrap are sbx's own records; an unowned sandbox has
		// neither, and sbx never inspects or judges what it did not create.
		return
	}
	ip := &persistent{info: info, cfg: cfg, id: identity.Identity{Sandbox: p.Sandbox}, tr: p.Translation}
	drift, err := driftReport(ctx, box, ip)
	if err != nil {
		live.DriftErr = err
	} else {
		live.Drift = drift
	}
	boot, err := assessBootstrap(ip, s.CreatedAt)
	switch boot {
	case bootstrapComplete:
		live.Bootstrap = "complete"
	case bootstrapChanged:
		live.Bootstrap = "changed"
	case bootstrapIncomplete:
		live.Bootstrap = "incomplete"
	}
	if err != nil {
		live.BootstrapErr = err
	}
	ports, err := s.PortsOf()
	if err != nil {
		live.PortsErr = err
		return
	}
	live.Ports = ports
}

// checkPlanVolumes runs the Project volume compatibility check for the
// rendered plan. It is read-only: the backend's listing is inspected and
// nothing changes. A failed or malformed inspection is recorded for the
// report, never read as an empty listing or as compatible definitions.
func checkPlanVolumes(ctx context.Context, box msb.CLI, p *plan.Plan) {
	if p.TranslateErr != nil || len(p.Translation.ProjectVolumes) == 0 {
		return
	}
	report, err := inspectProjectVolumes(ctx, box, p.Translation.ProjectVolumes)
	if err != nil {
		p.VolumeCheckErr = err
		return
	}
	p.VolumeReport = &report
}

// inspectProjectVolumes reads the backend's volume listing and checks
// every declared Project volume against it. The listing's own failure and
// malformed records surface as errors; an unreadable backend is never an
// empty one.
func inspectProjectVolumes(ctx context.Context, box msb.CLI, declared []volumes.Declared) (volumes.Report, error) {
	existing, err := box.Volumes(ctx)
	if err != nil {
		return volumes.Report{}, err
	}
	return volumes.Check(declared, existing)
}

// checkProjectVolumes refuses a sandbox mutation when a declared Project
// volume clashes with the volume the backend already holds under its
// derived name (ADR-0003): every declared volume is compared before
// anything is created, pulled, or started. A missing or malformed
// inspection fails the command rather than passing as an empty listing or
// a compatible definition. sbx never resizes, overwrites, or deletes an
// existing volume.
func checkProjectVolumes(ctx context.Context, box msb.CLI, declared []volumes.Declared) error {
	if len(declared) == 0 {
		return nil
	}
	report, err := inspectProjectVolumes(ctx, box, declared)
	if err != nil {
		return fmt.Errorf("inspecting the backend's volumes before creation: %w", err)
	}
	if len(report.Conflicts) > 0 {
		return fmt.Errorf("declared Project volume(s) conflict with existing backend volumes; nothing was created or changed:\n\n%s", volumes.FormatConflicts(report.Conflicts))
	}
	return nil
}

// discoverConfig finds the worktree root from the current directory and
// loads that checkout's validated sbx.toml.
func discoverConfig(ctx context.Context) (gitx.Info, config.Config, error) {
	info, err := gitx.Discover(ctx, ".")
	if err != nil {
		return gitx.Info{}, config.Config{}, err
	}
	cfg, err := config.Load(filepath.Join(info.WorktreeRoot, "sbx.toml"))
	if err != nil {
		return gitx.Info{}, config.Config{}, err
	}
	return info, cfg, nil
}

// writeTempFile creates a temporary file outside the repository with the
// given content and returns its path.
func writeTempFile(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
