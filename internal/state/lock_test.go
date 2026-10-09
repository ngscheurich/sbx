package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSandboxLockSerializes checks the per-sandbox lock's contract: a first
// holder takes it without blocking, a contender sees it as held instead of
// taking it, the contender blocks until release, a released lock is
// retakeable, and a held lock reports contention. Every mutating persistent
// command leans on this.
func TestSandboxLockSerializes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	lockPath := filepath.Join(mustDir(t), "locks", "app-main-12345678.lock")

	first, held, err := TrySandboxLock("app-main-12345678")
	if err != nil {
		t.Fatalf("TrySandboxLock: %v", err)
	}
	if !held {
		t.Fatal("the first TrySandboxLock on an unlocked sandbox reported contention")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the lock file was not created: %v", err)
	}

	acquired := make(chan *SandboxLock, 1)
	go func() {
		lock, err := LockSandbox("app-main-12345678")
		if err != nil {
			t.Errorf("LockSandbox: %v", err)
			acquired <- nil
			return
		}
		acquired <- lock
	}()

	// While the first lock is held, the contender stays blocked and a
	// second TrySandboxLock reports the contention instead of taking it.
	_, held, err = TrySandboxLock("app-main-12345678")
	if err != nil {
		t.Fatalf("second TrySandboxLock: %v", err)
	}
	if held {
		t.Fatal("TrySandboxLock took a lock another holder still holds")
	}
	select {
	case l := <-acquired:
		t.Fatalf("LockSandbox did not block while the lock was held (got %v)", l)
	case <-time.After(50 * time.Millisecond):
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var contender *SandboxLock
	select {
	case contender = <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the contender never acquired the released lock")
	}

	// A released lock is retakeable once the contender releases.
	if err := contender.Release(); err != nil {
		t.Fatalf("contender Release: %v", err)
	}
	again, held, err := TrySandboxLock("app-main-12345678")
	if err != nil {
		t.Fatalf("TrySandboxLock after release: %v", err)
	}
	if !held {
		t.Fatal("a released lock read as still held")
	}
	if err := again.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

// TestDirRejectsRelativeStateHome checks that a relative XDG_STATE_HOME is
// treated as unset: resolving it against the current directory would
// scatter machine-wide state wherever the caller happened to stand.
func TestDirRejectsRelativeStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "sbx-state")
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("Dir returned a relative path: %s", dir)
	}
	if dir != filepath.Join(mustHome(t), ".local", "state", "sbx") {
		t.Errorf("a relative XDG_STATE_HOME was not treated as unset: %s", dir)
	}
}

func mustDir(t *testing.T) string {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	return dir
}

func mustHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	return home
}
