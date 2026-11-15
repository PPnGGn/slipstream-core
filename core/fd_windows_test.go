//go:build windows

package core

import "testing"

func shortSocketPath(t *testing.T) string {
	t.Helper()
	t.Skip("requires unix domain socket path (/tmp/...)")
	return ""
}

func socketPair(t *testing.T) (a, b int) {
	t.Helper()
	t.Skip("requires unix file descriptors")
	return -1, -1
}

func pipePair(t *testing.T) (r, w int) {
	t.Helper()
	t.Skip("requires unix file descriptors")
	return -1, -1
}

func dupPipeFd(t *testing.T) int {
	t.Helper()
	t.Skip("requires unix file descriptors")
	return -1
}

func fdIsOpen(int) bool {
	return false
}

func closeFd(int) {}

func useFileDevices(t *testing.T, _ ...bool) {
	t.Helper()
	t.Skip("requires unix file descriptors")
}
