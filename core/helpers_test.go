package core

import (
	"fmt"
	"net"
)

// freePort asks the OS for a free TCP port on loopback. The core itself no
// longer needs one (nothing listens); tests do, to aim a hostile config at a
// port and see whether anything opens it.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
