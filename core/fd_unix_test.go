//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
)

// shortSocketPath returns a socket path short enough for sockaddr_un (104 bytes
// on macOS), which t.TempDir() often is not.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ss")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

// socketPair returns two connected datagram sockets: one stands in for the TUN
// descriptor (one datagram per packet), the other for the kernel side. The
// test owns b; a belongs to the code under test.
func socketPair(t *testing.T) (a, b int) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	t.Cleanup(func() { syscall.Close(fds[1]) })
	return fds[0], fds[1]
}

// pipePair returns the read end, which stands in for the TUN descriptor, and
// the write end, which the test closes to end the reads, like a device that
// goes away.
func pipePair(t *testing.T) (r, w int) {
	t.Helper()
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	return fds[0], fds[1]
}

// dupPipeFd returns a duplicated pipe descriptor owned by the caller.
func dupPipeFd(t *testing.T) int {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	fd, err := syscall.Dup(int(r.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	return fd
}

func fdIsOpen(fd int) bool {
	var st syscall.Stat_t
	return syscall.Fstat(fd, &st) == nil
}

func closeFd(fd int) {
	_ = syscall.Close(fd)
}

// useFileDevices makes StartTun open descriptors as fileDevice, see
// openTunDevice for why the real one is not used in tests. With stuckRead the
// descriptor stays blocking, so closing the file does not wake the read that
// waits on it, like a TUN read that never returns.
func useFileDevices(t *testing.T, stuckRead ...bool) {
	t.Helper()
	old := openTunDevice
	openTunDevice = func(fd int) (device.Device, error) {
		// Non-blocking, so that closing the file wakes a read waiting on it.
		if len(stuckRead) == 0 || !stuckRead[0] {
			if err := syscall.SetNonblock(fd, true); err != nil {
				return nil, err
			}
		}
		file := os.NewFile(uintptr(fd), "tun-test")
		ep, err := iobased.New(file, defaultMTU, 0)
		if err != nil {
			return nil, err
		}
		return fileDevice{Endpoint: ep, file: file}, nil
	}
	t.Cleanup(func() { openTunDevice = old })
}
