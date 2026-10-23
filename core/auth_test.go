package core

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// The password on the local proxies is what keeps every other app on the phone
// (and, before the config is hardened, the provider's own inbound) from using
// the tunnel. These tests start a real xray with a hardened config and speak to
// its two listeners the way a stranger and the tunnel would.
func startHardenedXray(t *testing.T) proxyParams {
	t.Helper()
	if err := startXray(hardenSample, ""); err != nil {
		t.Fatalf("startXray: %v", err)
	}
	t.Cleanup(func() { _ = stopXray() })
	p, ok := activeProxyParams()
	if !ok {
		t.Fatal("no proxy parameters after startXray")
	}
	return p
}

func dial(t *testing.T, port int) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial port %d: %v", port, err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c
}

func TestSocksInboundRefusesAStranger(t *testing.T) {
	p := startHardenedXray(t)
	c := dial(t, p.socksPort)

	// SOCKS5, one method offered: no authentication.
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatalf("read: %v", err)
	}
	if reply[0] != 5 || reply[1] != 0xff {
		t.Errorf("reply to \"no authentication\" = % x, want 05 ff (no acceptable method)", reply)
	}
}

func TestSocksInboundAcceptsTheSessionPassword(t *testing.T) {
	p := startHardenedXray(t)
	c := dial(t, p.socksPort)

	// SOCKS5, method 2 (user and password), then RFC 1929 with the session's.
	if _, err := c.Write([]byte{5, 1, 2}); err != nil {
		t.Fatalf("write: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(c, method); err != nil || method[0] != 5 || method[1] != 2 {
		t.Fatalf("method reply = % x, %v; want 05 02", method, err)
	}
	auth := append([]byte{1, byte(len(p.user))}, p.user...)
	auth = append(append(auth, byte(len(p.pass))), p.pass...)
	if _, err := c.Write(auth); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	status := make([]byte, 2)
	if _, err := io.ReadFull(c, status); err != nil || status[1] != 0 {
		t.Errorf("auth reply = % x, %v; want 01 00 (success)", status, err)
	}
}

func TestSocksInboundRefusesAWrongPassword(t *testing.T) {
	p := startHardenedXray(t)
	c := dial(t, p.socksPort)

	_, _ = c.Write([]byte{5, 1, 2})
	method := make([]byte, 2)
	_, _ = io.ReadFull(c, method)
	auth := append([]byte{1, byte(len(p.user))}, p.user...)
	auth = append(append(auth, 5), "wrong"...)
	_, _ = c.Write(auth)
	status := make([]byte, 2)
	if _, err := io.ReadFull(c, status); err == nil && status[1] == 0 {
		t.Errorf("auth reply = % x for a wrong password, want a failure", status)
	}
}

func TestHTTPInboundRefusesAStranger(t *testing.T) {
	p := startHardenedXray(t)
	c := dial(t, p.httpPort)

	// A proxy request without credentials.
	if _, err := fmt.Fprintf(c, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want 407 (proxy authentication required)", resp.StatusCode)
	}
}
