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
func withPorts(msg string, assigns []ports.Assignment, st ui.Styles) string {
	if len(assigns) == 0 {
		return msg
	}
	return msg + "\n" + st.Heading.Render("published ports:") + "\n" + ports.FormatAssignments(assigns)
}

// formatEndpoints renders the backend's reported ports with their declared
// names, for reuse reports where inspection is authoritative.
func formatEndpoints(declared []translate.DeclaredPort, actual []msb.PublishedPort) string {
	nameByGuest := map[int]string{}
	for _, d := range declared {
		nameByGuest[d.Guest] = d.Name
	}
	var b strings.Builder
	for _, p := range actual {
		fmt.Fprintf(&b, "%s: 127.0.0.1:%d -> guest %d\n", nameByGuest[p.GuestPort], p.HostPort, p.GuestPort)
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

// reportPorts writes the read-only port report status shows: the published
// mappings the backend reports, and any registry discrepancy — reported,
// never corrected or reserved. It needs neither a translatable
// configuration nor a reservation, so it stays usable when the sandbox has
// drifted or sbx.toml no longer declares the ports.
func reportPorts(sandbox string, s msb.Sandbox, w io.Writer, st ui.Styles) {
	actual, err := s.PortsOf()
	if err != nil {
		fmt.Fprintf(w, "%s\n", st.Warning.Render(fmt.Sprintf("ports: the sandbox’s published ports are unreadable (%v)", err)))
		return
	}
	switch {
	case len(actual) == 0:
		// No section at all when nothing is published: most sandboxes
		// declare no ports.
	default:
		fmt.Fprintf(w, "%s\n", st.Heading.Render("ports:"))
		for _, p := range actual {
			fmt.Fprintf(w, "  - 127.0.0.1:%d -> guest %d\n", p.HostPort, p.GuestPort)
		}
	}
	lines, err := ports.Discrepancies(sandbox, actual)
	if err != nil {
		fmt.Fprintf(w, "%s\n", st.Warning.Render(fmt.Sprintf("port registry: unreadable (%v)", err)))
		return
	}
	if len(lines) > 0 {
		fmt.Fprintf(w, "%s\n", st.Heading.Render("port registry discrepancies (a mutating command such as `sbx up` corrects these; status does not):"))
		for _, line := range lines {
			fmt.Fprintf(w, "  - %s\n", line)
		}
	}
}

// runPortPrune implements `sbx port prune`: remove Port reservations for
// sandboxes that no longer exist, keep those for existing ones — including
// stopped ones — and fail safely when the backend cannot be inspected,
// changing nothing.
func runPortPrune(ctx context.Context, args []string, out *output) int {
	// args[0] is "port" and args[1] is "prune"; anything further is usage.
	if len(args) > 2 {
		return out.usagef("port prune takes no arguments or flags, got %q", strings.Join(args[2:], " "))
	}
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
