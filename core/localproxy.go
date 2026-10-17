package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
)

// freePort asks the OS for a free TCP port on loopback and returns its number.
func freePort() (int, error) {
	// Port 0 means "pick any free port for me".
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	// Close it so xray can bind the same port later. Another process may grab
	// the port in between; accepted, the window is tiny.
	defer listener.Close()

	// Addr() is the net.Addr interface; the port lives on the concrete type.
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("find free port: unexpected address type %T", listener.Addr())
	}

	return tcpAddr.Port, nil
}

// randHex returns n random bytes as a hex string of length 2*n.
//
// Uses crypto/rand, not math/rand: the result becomes a proxy password.
func randHex(n int) (string, error) {
	// make([]byte, n) would panic on a negative n.
	if n <= 0 {
		return "", fmt.Errorf("random hex: invalid length %d", n)
	}

	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random hex: %w", err)
	}

	return hex.EncodeToString(buf), nil
}
