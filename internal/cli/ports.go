// Port reservations in the CLI: the shared helpers the persistent-sandbox
// commands use to reserve and reconcile a sandbox's published ports, the
// read-only port report for status, and `sbx port prune`. The policy itself
// lives in the ports package.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/ports"
	"github.com/ngscheurich/sbx/internal/translate"
	"github.com/ngscheurich/sbx/internal/ui"
)

// declaredPorts converts a translation's declared ports for the ports
// package, which stays independent of the translation's shape.
func declaredPorts(tr translate.Translation) []ports.Declared {
	out := make([]ports.Declared, 0, len(tr.Ports))
	for _, p := range tr.Ports {
		out = append(out, ports.Declared{Name: p.Name, Guest: p.Guest})
	}
	return out
}

// publishOf renders the chosen assignments as the creation options' publish
// list, in assignment order.
func publishOf(assigns []ports.Assignment) []msb.Publish {
	out := make([]msb.Publish, 0, len(assigns))
	for _, a := range assigns {
		out = append(out, msb.Publish{HostPort: a.Host, GuestPort: a.Guest})
	}
	return out
}

// probeLoopback reports whether a host loopback port is currently free to
// bind. A free-port probe is not a reservation: a race with another process
// publishing the same port cannot be ruled out by probing, so any later
// collision is reported, never claimed safe. An address already in use is
// a normal probe outcome (occupied); any other failure is an error, never
// read as a free port.
func probeLoopback(port int) (bool, error) {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		l.Close()
		return true, nil
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && errors.Is(opErr.Err, syscall.EADDRINUSE) {
		return false, nil
	}
	return false, fmt.Errorf("probing 127.0.0.1:%d: %w", port, err)
}

// reservePorts picks or reuses the worktree sandbox's host ports before
// creation and points the translation's publish list at them. A failure
// aborts the caller before any resource changes. The assignments are
// returned for the creation report's endpoints.
func reservePorts(ctx context.Context, box msb.CLI, p *persistent) ([]ports.Assignment, error) {
	if len(p.tr.Ports) == 0 {
		return nil, nil
	}
	assigns, err := ports.Reserve(p.id.Sandbox, declaredPorts(p.tr),
		func() ([]int, error) { return box.PortsInUse(ctx) }, probeLoopback)
	if err != nil {
		return nil, err
	}
	p.tr.Options.Publish = publishOf(assigns)
	return assigns, nil
}

// reconcileCreatedPorts inspects a sandbox that was just created and
// reconciles the registry with what the backend reports.
func reconcileCreatedPorts(ctx context.Context, box msb.CLI, p *persistent) error {
	if len(p.tr.Ports) == 0 {
		return nil
	}
	s, err := box.Inspect(ctx, p.id.Sandbox)
	if err != nil {
		return fmt.Errorf("verifying the new sandbox’s published ports: %w", err)
	}
	return reconcilePorts(p, s)
}

// withPorts appends the creation report's endpoints to an action message.
func withPorts(msg string, endpoints []portEndpoint, st ui.Styles) string {
	if len(endpoints) == 0 {
		return msg
	}
	return msg + "\n" + st.Heading.Render("published ports:") + "\n" + formatEndpoints(endpoints)
}

func formatEndpoints(endpoints []portEndpoint) string {
	var b strings.Builder
	for _, p := range endpoints {
		fmt.Fprintf(&b, "%s: 127.0.0.1:%d -> guest %d\n", p.Name, p.Host, p.Guest)
	}
	return b.String()
}

// reconcilePorts makes the registry agree with what the backend reports
// for an existing sandbox — inspect's report is authoritative once the
// sandbox exists. Mutating commands correct a stale registry entry unless
// it conflicts with another reservation, in which case they fail rather
// than take the port.
func reconcilePorts(p *persistent, s msb.Sandbox) error {
	if len(p.tr.Ports) == 0 {
		return nil
	}
	actual, err := s.PortsOf()
	if err != nil {
		return fmt.Errorf("reading the sandbox’s published ports: %w", err)
	}
	return ports.Reconcile(p.id.Sandbox, declaredPorts(p.tr), actual)
}

func collectPorts(sandbox string, s msb.Sandbox) *portFindings {
	report := &portFindings{}
	actual, err := s.PortsOf()
	if err != nil {
		report.PublishedErr = err.Error()
		return report
	}
	report.Published = namedEndpoints(nil, actual)
	lines, err := ports.Discrepancies(sandbox, actual)
	if err != nil {
		report.RegistryErr = err.Error()
	} else {
		report.Discrepancies = lines
	}
	if len(report.Published) == 0 && len(lines) == 0 && err == nil {
		return nil
	}
	return report
}

func renderPorts(report *portFindings, w io.Writer, st ui.Styles) {
	if report == nil {
		return
	}
	if report.PublishedErr != "" {
		fmt.Fprintf(w, "%s\n", st.Warning.Render(fmt.Sprintf("ports: the sandbox’s published ports are unreadable (%s)", report.PublishedErr)))
		return
	}
	if len(report.Published) > 0 {
		fmt.Fprintf(w, "%s\n", st.Heading.Render("ports:"))
		for _, p := range report.Published {
			fmt.Fprintf(w, "  - 127.0.0.1:%d -> guest %d\n", p.Host, p.Guest)
		}
	}
	if report.RegistryErr != "" {
		fmt.Fprintf(w, "%s\n", st.Warning.Render(fmt.Sprintf("port registry: unreadable (%s)", report.RegistryErr)))
		return
	}
	if len(report.Discrepancies) > 0 {
		fmt.Fprintf(w, "%s\n", st.Heading.Render("port registry discrepancies (a mutating command such as `sbx up` corrects these; status does not):"))
		for _, line := range report.Discrepancies {
			fmt.Fprintf(w, "  - %s\n", line)
		}
	}
}

// runPortPrune implements `sbx port prune`: remove Port reservations for
// sandboxes that no longer exist, keep those for existing ones — including
// stopped ones — and fail safely when the backend cannot be inspected,
// changing nothing.
func runPortPrune(ctx context.Context, args []string, out *output) int {
	flags, err := parsePersistentFlags("port prune", args[1:], []string{"--json"})
	if err != nil {
		return out.usagef("%v", err)
	}
	out.json = flags["--json"]
	box := msb.CLI{}
	if _, err := box.LocalContext(ctx); err != nil {
		return out.fail(err)
	}
	// The backend is inspected before anything is pruned: a failed listing
	// is never read as "nothing exists".
	entries, err := box.List(ctx)
	if err != nil {
		return out.fail(fmt.Errorf("inspecting the backend before pruning: %w", err))
	}
	exists := map[string]bool{}
	for _, e := range entries {
		exists[e.Name] = true
	}
	removed, err := ports.Prune(func(sandbox string) bool { return exists[sandbox] })
	if err != nil {
		return out.fail(err)
	}
	if out.json {
		pruned := make([]prunedReservation, 0, len(removed))
		for _, r := range removed {
			pruned = append(pruned, prunedReservation{Sandbox: r.Sandbox, Name: r.Name, Host: r.Port, Guest: r.Guest})
		}
		return out.writeJSON(struct {
			Pruned []prunedReservation `json:"pruned"`
		}{Pruned: pruned})
	}
	if len(removed) == 0 {
		fmt.Fprintf(out.stdout, "port registry: nothing to prune; every reservation belongs to a sandbox the backend still knows (stopped sandboxes keep theirs)\n")
		return exitOK
	}
	fmt.Fprintf(out.stdout, "%s\n", out.styles.Heading.Render(fmt.Sprintf("port registry: removed %d reservation(s) for sandbox(es) that no longer exist:", len(removed))))
	for _, r := range removed {
		fmt.Fprintf(out.stdout, "  - %s %s: released host port %d (guest %d)\n", r.Sandbox, r.Name, r.Port, r.Guest)
	}
	fmt.Fprintf(out.stdout, "reservations for existing sandboxes, stopped ones included, were kept.\n")
	return exitOK
}
