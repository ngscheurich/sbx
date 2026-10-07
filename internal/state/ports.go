// The port registry: the machine-wide record of which Sandbox identity
// holds which named port on which host loopback port. It lives in the host
// state directory, is updated atomically under a cross-process lock, and is
// keyed by Sandbox identity and port name — so a sandbox's reservations
// survive stop, rm, and host restarts, and sibling worktrees never race for
// the same candidate (see the spec's Port reservations).
//
// The registry is storage only: the choice of ports is policy, kept in the
// ports package on top of these primitives.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// PortReservation is one registry record: the named guest port a sandbox
// publishes, and the host loopback port reserved for it.
type PortReservation struct {
	// Sandbox is the Sandbox identity the reservation belongs to.
	Sandbox string `json:"sandbox"`
	// Name is the port's name from [ports.<name>].
	Name string `json:"name"`
	// Guest is the TCP port the service listens on inside the sandbox.
	Guest int `json:"guest"`
	// Port is the reserved host loopback port.
	Port int `json:"port"`
}

// registryPath returns the registry file's path in the host state directory.
func registryPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ports", "registry.json"), nil
}

// registryLockPath returns the registry lock file's path. The file is never
// deleted, the way the per-sandbox lock files are not.
func registryLockPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ports", "registry.lock"), nil
}

// LoadPortReservations reads the registry without taking its lock, for
// read-only commands: status and plan report what the registry says without
// reserving or correcting. A missing registry is no reservations; a corrupt
// one is an error, never silently empty.
func LoadPortReservations() ([]PortReservation, error) {
	path, err := registryPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the port registry: %w", err)
	}
	var records []PortReservation
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("the port registry is unreadable: %w", err)
	}
	return records, nil
}

// UpdatePortReservations runs mutate under the registry's cross-process
// lock and persists the records it returns atomically. The lock serializes
// read-modify-write across processes, so concurrent creators cannot pick
// the same candidate; mutate must not make backend calls that depend on its
// own writes. A mutate error aborts the update with nothing written.
func UpdatePortReservations(mutate func(records []PortReservation) ([]PortReservation, error)) error {
	path, err := registryPath()
	if err != nil {
		return err
	}
	lockPath, err := registryLockPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating the port registry directory: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening the port registry lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking the port registry: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	// Read under the lock: another process may have written since the
	// caller last looked.
	records, err := LoadPortReservations()
	if err != nil {
		return err
	}
	updated, err := mutate(records)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the port registry: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return fmt.Errorf("writing the port registry: %w", err)
	}
	return nil
}
