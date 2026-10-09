// Package ports holds the port-reservation policy on top of the host
// state directory's registry: choosing loopback ports for a persistent
// sandbox's declared guest services, reconciling the registry with what
// the backend actually reports, and pruning reservations for sandboxes
// that no longer exist.
//
// The rules: a sandbox
// reuses its reservations or picks from 4001–4099; a free-port probe is
// not a reservation; candidates must also avoid ports the backend's
// existing sandboxes publish, because msb can accept a duplicate published
// port while the first guest still serves it; and once a sandbox exists,
// msb inspect's report is authoritative. sbx never removes a partially
// created sandbox to retry a port, and it reports collisions rather than
// claiming an endpoint is safe.
package ports

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
)

// The host loopback range reserved for sbx's persistent sandboxes.
const (
	FirstPort = 4001
	LastPort  = 4099
)

// Declared is one [ports.<name>] entry: the port's name and the guest TCP
// port its service listens on.
type Declared struct {
	Name  string
	Guest int
}

// Assignment is one chosen host loopback port for one declared port.
type Assignment struct {
	Name  string
	Guest int
	Host  int
}

// Reserve assigns host loopback ports for a sandbox's declared ports and
// records them in the registry, atomically under the registry's
// cross-process lock. Existing reservations for the Sandbox identity are
// reused — a port collision with another reservation fails rather than
// silently changing that reservation — and new picks scan the reserved
// range in order, skipping ports any registry record holds, ports the
// backend's existing sandboxes publish (backendPorts), and ports the
// probe reports as occupied. A failed backend inspection or probe aborts
// the reservation with nothing written: an uninspectable backend is never
// read as an empty one.
func Reserve(sandbox string, declared []Declared, backendPorts func() ([]int, error), probe func(port int) (bool, error)) ([]Assignment, error) {
	var assigns []Assignment
	err := state.UpdatePortReservations(func(records []state.PortReservation) ([]state.PortReservation, error) {
		// A record duplicating another's port would make every later
		// decision unsound; refuse it instead of trusting the registry.
		byPort := map[int]state.PortReservation{}
		for _, r := range records {
			if prior, dup := byPort[r.Port]; dup {
				return nil, fmt.Errorf("the port registry holds two reservations for port %d (%s’s %s and %s’s %s); correct or prune it with `sbx port prune`",
					r.Port, prior.Sandbox, prior.Name, r.Sandbox, r.Name)
			}
			byPort[r.Port] = r
		}

		// The backend's published ports are needed only when something
		// must be picked, and are loaded at most once: an inspection
		// failure aborts the whole reservation before any write.
		var backendUsed map[int]bool
		backendLoaded := false
		loadBackend := func() (map[int]bool, error) {
			if backendLoaded {
				return backendUsed, nil
			}
			ports, err := backendPorts()
			if err != nil {
				return nil, fmt.Errorf("inspecting the backend’s published ports before choosing candidates: %w", err)
			}
			backendUsed = map[int]bool{}
			for _, p := range ports {
				backendUsed[p] = true
			}
			backendLoaded = true
			return backendUsed, nil
		}

		updated := records
		for _, d := range declared {
			// Reuse this sandbox's own reservation for the name, whatever
			// it is: a sandbox's ports change only by recreating it.
			reused := false
			for i, r := range updated {
				if r.Sandbox != sandbox || r.Name != d.Name {
					continue
				}
				if other, conflict := byPort[r.Port]; conflict && (other.Sandbox != sandbox || other.Name != d.Name) {
					return nil, fmt.Errorf("port %d is reserved for %s’s %s; the registry for %s’s %s cannot also hold it",
						r.Port, other.Sandbox, other.Name, sandbox, d.Name)
				}
				// The declared guest port travels with the record so the
				// registry can be read against later inspection reports.
				updated[i].Guest = d.Guest
				assigns = append(assigns, Assignment{Name: d.Name, Guest: d.Guest, Host: r.Port})
				reused = true
				break
			}
			if reused {
				continue
			}

			used, err := loadBackend()
			if err != nil {
				return nil, err
			}
			port := 0
			for candidate := FirstPort; candidate <= LastPort; candidate++ {
				if _, held := byPort[candidate]; held {
					continue
				}
				if used[candidate] {
					continue
				}
				free, err := probe(candidate)
				if err != nil {
					return nil, fmt.Errorf("probing candidate port %d: %w", candidate, err)
				}
				if !free {
					continue
				}
				port = candidate
				break
			}
			if port == 0 {
				return nil, fmt.Errorf("no host port is available for %s: every port from %d to %d is reserved, already published by the backend, or occupied on the loopback interface", d.Name, FirstPort, LastPort)
			}
			record := state.PortReservation{Sandbox: sandbox, Name: d.Name, Guest: d.Guest, Port: port}
			updated = append(updated, record)
			byPort[port] = record
			assigns = append(assigns, Assignment{Name: d.Name, Guest: d.Guest, Host: port})
		}
		return updated, nil
	})
	if err != nil {
		return nil, err
	}
	return assigns, nil
}

// Reconcile makes the registry agree with what the backend reports for an
// existing sandbox — inspect's report is authoritative once the sandbox
// exists. Mutating commands call it after creation and on later use; a
// correction that would take a port another reservation holds fails
// instead, leaving the registry unchanged.
func Reconcile(sandbox string, declared []Declared, actual []msb.PublishedPort) error {
	return state.UpdatePortReservations(func(records []state.PortReservation) ([]state.PortReservation, error) {
		byGuest := map[int]int{}
		for _, p := range actual {
			byGuest[p.GuestPort] = p.HostPort
		}
		desired := map[string]int{}
		for _, d := range declared {
			host, ok := byGuest[d.Guest]
			if !ok {
				return nil, fmt.Errorf("sandbox %s does not report a published port for guest %d (%s), so its registry entry cannot be verified", sandbox, d.Guest, d.Name)
			}
			desired[d.Name] = host
		}

		// Nothing to correct when the reported host ports already match
		// the sandbox's records.
		reserved := map[string]int{}
		var reservedHosts []int
		for _, r := range records {
			if r.Sandbox != sandbox {
				continue
			}
			reserved[r.Name] = r.Port
			reservedHosts = append(reservedHosts, r.Port)
		}
		var desiredHosts []int
		for _, host := range desired {
			desiredHosts = append(desiredHosts, host)
		}
		sort.Ints(reservedHosts)
		sort.Ints(desiredHosts)
		if slices.Equal(reservedHosts, desiredHosts) {
			return records, nil
		}

		// A correction must not take a port another reservation holds.
		for name, host := range desired {
			for _, r := range records {
				if r.Port != host {
					continue
				}
				if r.Sandbox == sandbox && r.Name == name {
					continue
				}
				return nil, fmt.Errorf("sandbox %s publishes port %d for %s, but the registry reserves that port for %s’s %s; sbx fails rather than take it",
					sandbox, host, name, r.Sandbox, r.Name)
			}
		}

		var updated []state.PortReservation
		for _, r := range records {
			if r.Sandbox == sandbox {
				continue
			}
			updated = append(updated, r)
		}
		for _, d := range declared {
			updated = append(updated, state.PortReservation{Sandbox: sandbox, Name: d.Name, Guest: d.Guest, Port: desired[d.Name]})
		}
		sort.Slice(updated, func(i, j int) bool {
			a, b := updated[i], updated[j]
			if a.Sandbox != b.Sandbox {
				return a.Sandbox < b.Sandbox
			}
			return a.Name < b.Name
		})
		return updated, nil
	})
}

// Discrepancies compares the registry with what the backend reports for an
// existing sandbox and returns human-readable lines for every mismatch.
// Read-only commands call it: they report without correcting or reserving.
// The returned lines are unindented. A nil slice with no error means the
// registry and the backend agree.
func Discrepancies(sandbox string, actual []msb.PublishedPort) ([]string, error) {
	records, err := state.LoadPortReservations()
	if err != nil {
		return nil, err
	}
	hostByGuest := map[int]int{}
	for _, p := range actual {
		hostByGuest[p.GuestPort] = p.HostPort
	}
	var lines []string
	seen := map[int]bool{}
	for _, r := range records {
		if r.Sandbox != sandbox {
			continue
		}
		host, ok := hostByGuest[r.Guest]
		seen[r.Guest] = true
		switch {
		case !ok:
			lines = append(lines, fmt.Sprintf("the registry reserves %d for %s (guest %d), but the sandbox does not report it published", r.Port, r.Name, r.Guest))
		case host != r.Port:
			lines = append(lines, fmt.Sprintf("the registry reserves %d for %s (guest %d), but the sandbox reports %d", r.Port, r.Name, r.Guest, host))
		}
	}
	for _, p := range actual {
		if seen[p.GuestPort] {
			continue
		}
		lines = append(lines, fmt.Sprintf("the sandbox publishes %d for guest %d, but the registry holds no reservation for it", p.HostPort, p.GuestPort))
	}
	return lines, nil
}

// Prune removes every reservation whose Sandbox identity fails keep — the
// sandboxes that no longer exist — and returns what it removed. Stopped
// sandboxes are kept by the caller's keep function; the registry itself
// records nothing about liveness.
func Prune(keep func(sandbox string) bool) ([]state.PortReservation, error) {
	var removed []state.PortReservation
	err := state.UpdatePortReservations(func(records []state.PortReservation) ([]state.PortReservation, error) {
		var kept []state.PortReservation
		for _, r := range records {
			if keep(r.Sandbox) {
				kept = append(kept, r)
				continue
			}
			removed = append(removed, r)
		}
		return kept, nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// FormatAssignments renders assignments as "name: 127.0.0.1:<host> ->
// guest <guest>" lines, in the given order.
func FormatAssignments(assigns []Assignment) string {
	var b strings.Builder
	for _, a := range assigns {
		fmt.Fprintf(&b, "%s: 127.0.0.1:%d -> guest %d\n", a.Name, a.Host, a.Guest)
	}
	return b.String()
}
