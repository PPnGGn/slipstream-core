package core

import (
	"encoding/hex"
	"net"
	"strconv"
	"testing"
)

func TestFreePort(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error: %v", err)
	}
	if port < 1 || port > 65535 {
		t.Fatalf("freePort() = %d, want a valid TCP port", port)
	}

	// The port must really be free: we can bind it right away.
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d returned by freePort() is not bindable: %v", port, err)
	}
	l.Close()
}

func TestFreePortDiffersWhileHeld(t *testing.T) {
	first, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error: %v", err)
	}

	// Hold the first port; the OS must not hand it out again.
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(first)))
	if err != nil {
		t.Fatalf("cannot hold port %d: %v", first, err)
	}
	defer l.Close()

	second, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error: %v", err)
	}
	if second == first {
		t.Fatalf("freePort() returned the busy port %d again", first)
	}
}

func TestRandHexLength(t *testing.T) {
	for _, n := range []int{1, 8, 16, 32} {
		got, err := randHex(n)
		if err != nil {
			t.Fatalf("randHex(%d) error: %v", n, err)
		}
		if len(got) != 2*n {
			t.Errorf("randHex(%d) length = %d, want %d", n, len(got), 2*n)
		}
		// DecodeString fails on any non-hex character.
		if _, err := hex.DecodeString(got); err != nil {
			t.Errorf("randHex(%d) = %q is not valid hex: %v", n, got, err)
		}
	}
}

func TestRandHexDiffers(t *testing.T) {
	a, err := randHex(16)
	if err != nil {
		t.Fatalf("randHex(16) error: %v", err)
	}
	b, err := randHex(16)
	if err != nil {
		t.Fatalf("randHex(16) error: %v", err)
	}
	if a == b {
		t.Fatalf("two randHex(16) calls returned the same value %q", a)
	}
}

func TestRandHexInvalidLength(t *testing.T) {
	for _, n := range []int{0, -1} {
		if got, err := randHex(n); err == nil {
			t.Errorf("randHex(%d) = %q, want an error", n, got)
		}
	}
}
