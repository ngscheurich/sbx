// Published-port inspection: once a sandbox exists, the ports `msb inspect
// --format json` reports are authoritative (see the spec's Port
// reservations). The report's exact shape is not yet pinned by a real-host
// probe — the SandboxConfig keeps the raw JSON for that reason — so the
// parser here accepts every plausible shape tolerantly and fails closed on
// content it cannot read: an unreadable port report is never treated as an
// empty one.
package msb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PublishedPort is one guest TCP service the backend reports as published:
// the guest port and the host loopback port it is published on.
type PublishedPort struct {
	HostPort  int
	GuestPort int
}

// PublishedPorts parses a sandbox configuration layer's raw ports JSON.
// An absent, null, or empty value is no ports; anything else that cannot
// be read as host/guest pairs is an error, never silently empty.
func PublishedPorts(raw json.RawMessage) ([]PublishedPort, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" || trimmed == "{}" {
		return nil, nil
	}
	var doc any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, fmt.Errorf("parsing the published ports %q: %w", trimmed, err)
	}
	var ports []PublishedPort
	switch v := doc.(type) {
	case []any:
		for _, item := range v {
			p, err := publishedPortOfItem(item)
			if err != nil {
				return nil, err
			}
			if p != nil {
				ports = append(ports, *p)
			}
		}
	case map[string]any:
		// A mapping of guest port to host binding, such as
		// {"4000": "127.0.0.1:4001"}.
		for key, value := range v {
			guest, err := portNumber(key)
			if err != nil {
				return nil, fmt.Errorf("parsing the published ports %q: %w", trimmed, err)
			}
			host, err := hostPortOfValue(value)
			if err != nil {
				return nil, fmt.Errorf("parsing the published ports %q: %w", trimmed, err)
			}
			ports = append(ports, PublishedPort{HostPort: host, GuestPort: guest})
		}
	default:
		return nil, fmt.Errorf("parsing the published ports %q: not a list or mapping", trimmed)
	}
	return ports, nil
}

// publishedPortOfItem reads one entry of a ports list: an object with
// host- and guest-port fields under any plausible key spelling, or a
// spec string such as "127.0.0.1:4001:4000" (host:guest) — the loopback
// address is optional and ignored.
func publishedPortOfItem(item any) (*PublishedPort, error) {
	switch v := item.(type) {
	case string:
		parts := strings.Split(v, ":")
		switch len(parts) {
		case 3: // host:hostPort:guestPort
			host, err := portNumber(parts[1])
			if err != nil {
				return nil, fmt.Errorf("parsing the published port %q: %w", v, err)
			}
			guest, err := portNumber(parts[2])
			if err != nil {
				return nil, fmt.Errorf("parsing the published port %q: %w", v, err)
			}
			return &PublishedPort{HostPort: host, GuestPort: guest}, nil
		case 2: // hostPort:guestPort
			host, err := portNumber(parts[0])
			if err != nil {
				return nil, fmt.Errorf("parsing the published port %q: %w", v, err)
			}
			guest, err := portNumber(parts[1])
			if err != nil {
				return nil, fmt.Errorf("parsing the published port %q: %w", v, err)
			}
			return &PublishedPort{HostPort: host, GuestPort: guest}, nil
		default:
			return nil, fmt.Errorf("parsing the published port %q: expected host:guest", v)
		}
	case map[string]any:
		host, ok := portField(v, "host", "host_port", "hostport", "published", "published_port")
		if !ok {
			return nil, fmt.Errorf("parsing the published port %v: no host port field", v)
		}
		guest, ok := portField(v, "guest", "guest_port", "guestport", "container", "container_port", "target", "to")
		if !ok {
			return nil, fmt.Errorf("parsing the published port %v: no guest port field", v)
		}
		return &PublishedPort{HostPort: host, GuestPort: guest}, nil
	default:
		return nil, fmt.Errorf("parsing the published port %v: unsupported shape", item)
	}
}

// portField reads an integer port from the first matching key, accepting
// either a JSON number or a string that may carry a "host:port" binding.
func portField(doc map[string]any, keys ...string) (int, bool) {
	for _, want := range keys {
		for key, value := range doc {
			if !strings.EqualFold(key, want) {
				continue
			}
			switch v := value.(type) {
			case float64:
				return int(v), true
			case string:
				// A "127.0.0.1:4001" binding or a bare port number.
				parts := strings.Split(v, ":")
				if p, err := portNumber(parts[len(parts)-1]); err == nil {
					return p, true
				}
			}
		}
	}
	return 0, false
}

// hostPortOfValue reads a host port from a mapping value: a number or a
// "host:port" (or "host:hostPort:guestPort") string.
func hostPortOfValue(value any) (int, error) {
	switch v := value.(type) {
	case float64:
		return int(v), nil
	case string:
		parts := strings.Split(strings.TrimSpace(v), ":")
		if p, err := portNumber(parts[len(parts)-1]); err == nil {
			return p, nil
		}
		return 0, fmt.Errorf("%q is not a host port binding", v)
	default:
		return 0, fmt.Errorf("%v is not a host port binding", value)
	}
}

// portNumber parses a TCP port number.
func portNumber(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("%q is not a TCP port", s)
	}
	return p, nil
}

// PortsOf returns a sandbox's published ports, read from whichever
// configuration layer the backend keeps for its current state. An empty
// list means the sandbox publishes nothing (or is stopped with nothing
// declared).
func (s Sandbox) PortsOf() ([]PublishedPort, error) {
	return PublishedPorts(s.EffectiveConfig().Ports)
}

// PortsInUse collects every host port published by any sandbox the backend
// currently knows, so a new candidate can avoid them: msb can accept a
// second sandbox publishing a port the first one already serves, so the
// backend's own state — not only sbx's registry — must be consulted. Every
// listed sandbox is inspected; a listing or inspection failure is an error,
// never read as an empty backend.
func (c CLI) PortsInUse(ctx context.Context) ([]int, error) {
	entries, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	var ports []int
	for _, e := range entries {
		s, err := c.Inspect(ctx, e.Name)
		if err != nil {
			return nil, err
		}
		published, err := s.PortsOf()
		if err != nil {
			return nil, err
		}
		for _, p := range published {
			ports = append(ports, p.HostPort)
		}
	}
	return ports, nil
}
