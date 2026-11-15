//go:build windows

package android

import "testing"

func tunFdLike(t *testing.T) int {
	t.Helper()
	t.Skip("requires unix file descriptors")
	return -1
}

func fdOpen(int) bool {
	return false
}

func closeFd(int) {}
