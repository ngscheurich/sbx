//go:build !darwin

package msb

import (
	"os"
	"testing"
)

// openPTY returns a file whose descriptor reports as a terminal, for tests
// that exercise the terminal-stdin code paths. On non-Darwin Unix the
// /dev/ptmx master itself answers the terminal ioctl, so the master is
// enough.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { ptmx.Close() })
	return ptmx
}
