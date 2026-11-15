//go:build !windows

package android

import (
	"os"
	"syscall"
	"testing"
)

// tunFdLike returns a descriptor the test owns, standing in for the one the
// app hands to Start after detachFd(): from that call on, Start owns it.
func tunFdLike(t *testing.T) int {
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

func fdOpen(fd int) bool {
	var st syscall.Stat_t
	return syscall.Fstat(fd, &st) == nil
}

func closeFd(fd int) {
	_ = syscall.Close(fd)
}
