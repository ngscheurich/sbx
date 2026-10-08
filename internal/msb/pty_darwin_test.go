//go:build darwin

package msb

import (
	"bytes"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY returns a file whose descriptor reports as a terminal, for tests
// that exercise the terminal-stdin code paths. On macOS the /dev/ptmx
// master does not answer the terminal ioctl — the master is a kernel
// handle, not the tty itself — so the helper resolves the slave device via
// TIOCPTYGNAME and returns that side instead. The master must stay open for
// the slave's lifetime, or the slave would start returning EOF.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { ptmx.Close() })

	var name [128]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, ptmx.Fd(), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		t.Skipf("no pty slave name available: %v", errno)
	}
	end := bytes.IndexByte(name[:], 0)
	slave, err := os.OpenFile("/dev/"+string(name[:end]), os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty slave available: %v", err)
	}
	t.Cleanup(func() { slave.Close() })
	return slave
}
