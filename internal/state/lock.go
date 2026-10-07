// Per-sandbox cross-process locks: an flock on a lock file in the host
// state directory serializes persistent create, start, and bootstrap
// across sbx processes, so a concurrent caller waits rather than racing a
// creation or running Bootstrap twice. The lock is never held while a
// user's guest command runs. sbx targets macOS and Linux, so flock(2) is
// always available.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SandboxLock is a held per-sandbox cross-process lock.
type SandboxLock struct {
	f *os.File
}

// sandboxLockPath returns the lock file path for a Sandbox identity. The
// file is never deleted: removing it would race a concurrent opener, and a
// leftover empty lock file is harmless.
func sandboxLockPath(sandbox string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "locks", sandbox+".lock"), nil
}

// openSandboxLock opens (creating if needed) the sandbox's lock file.
func openSandboxLock(sandbox string) (*SandboxLock, error) {
	path, err := sandboxLockPath(sandbox)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating the lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the lock for %s: %w", sandbox, err)
	}
	return &SandboxLock{f: f}, nil
}

// TrySandboxLock takes the per-sandbox lock without blocking. held is
// false — with no error — when another process owns it, so the caller can
// say it is waiting before blocking on LockSandbox.
func TrySandboxLock(sandbox string) (lock *SandboxLock, held bool, err error) {
	l, err := openSandboxLock(sandbox)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		l.f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("locking %s: %w", sandbox, err)
	}
	return l, true, nil
}

// LockSandbox takes the per-sandbox lock, blocking until it is held.
func LockSandbox(sandbox string) (*SandboxLock, error) {
	l, err := openSandboxLock(sandbox)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX); err != nil {
		l.f.Close()
		return nil, fmt.Errorf("locking %s: %w", sandbox, err)
	}
	return l, nil
}

// Release frees the lock and closes its file.
func (l *SandboxLock) Release() error {
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	if err != nil {
		return fmt.Errorf("releasing the sandbox lock: %w", err)
	}
	if cerr != nil {
		return fmt.Errorf("closing the sandbox lock: %w", cerr)
	}
	return nil
}
