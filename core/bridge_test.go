package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	xcore "github.com/xtls/xray-core/core"
)

// startBridge runs an xray with no inbound at all, a direct and a blackhole
// outbound and the given routing rules, and returns the in-process bridge to it.
func startBridge(t *testing.T, rules string) *xrayProxy {
	t.Helper()
	cfg := fmt.Sprintf(`{
		"outbounds": [{"protocol": "freedom", "tag": "direct"}, {"protocol": "blackhole", "tag": "block"}],
		"routing": {"rules": %s}
	}`, rules)
	built, err := buildCoreConfig(cfg, "")
	if err != nil {
		t.Fatalf("buildCoreConfig: %v", err)
	}
	inst, err := xcore.New(built)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = inst.Close() })
	return newXrayProxy(inst, 8)
}

func metadata(network M.Network, dst netip.AddrPort) *M.Metadata {
	return &M.Metadata{
		Network: network,
		SrcIP:   netip.MustParseAddr("10.0.0.2"),
		SrcPort: 50000,
		DstIP:   dst.Addr(),
		DstPort: dst.Port(),
	}
}

// echoTCP serves a TCP echo on loopback and returns its address.
func echoTCP(t *testing.T) netip.AddrPort {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return l.Addr().(*net.TCPAddr).AddrPort()
}

func echoUDP(t *testing.T) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

// roundTrip sends msg over c and reports what comes back within a second.
func roundTrip(c net.Conn, msg string) (string, error) {
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		return "", err
	}
	buf := make([]byte, len(msg))
	n, err := io.ReadFull(c, buf)
	return string(buf[:n]), err
}

func TestBridgeCarriesTCP(t *testing.T) {
	p := startBridge(t, `[]`)
	echo := echoTCP(t)

	c, err := p.DialContext(context.Background(), metadata(M.TCP, echo))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "ping"); err != nil || got != "ping" {
		t.Errorf("echo through the bridge = %q, %v; want \"ping\"", got, err)
	}
}

// tun2socks cancels the context it dials with as soon as the dial returns. The
// connection must not depend on it: xray ties the life of a link to the context
// it was dispatched with.
func TestBridgeConnectionOutlivesTheDialContext(t *testing.T) {
	p := startBridge(t, `[]`)
	echo := echoTCP(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, err := p.DialContext(ctx, metadata(M.TCP, echo))
	cancel() // what tun2socks does right after the dial
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	time.Sleep(100 * time.Millisecond)
	if got, err := roundTrip(c, "still here"); err != nil || got != "still here" {
		t.Errorf("echo after the dial context was cancelled = %q, %v", got, err)
	}
}

// The traffic that used to come in through the SOCKS inbound carries its tag
// into routing, and providers write rules for it. The bridge must keep doing it.
func TestBridgeTagsTrafficAsTheSocksInbound(t *testing.T) {
	echo := echoTCP(t)

	blocked := startBridge(t, `[{"type": "field", "inboundTag": ["socks"], "outboundTag": "block"}]`)
	c, err := blocked.DialContext(context.Background(), metadata(M.TCP, echo))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "x"); err == nil {
		t.Errorf("a rule on the \"socks\" inbound tag did not catch the traffic, got %q back", got)
	}

	other := startBridge(t, `[{"type": "field", "inboundTag": ["somewhere-else"], "outboundTag": "block"}]`)
	c2, err := other.DialContext(context.Background(), metadata(M.TCP, echo))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c2.Close()
	if got, err := roundTrip(c2, "y"); err != nil || got != "y" {
		t.Errorf("a rule on another tag caught the traffic: %q, %v", got, err)
	}
}

// The SOCKS inbound sniffed the host name of HTTP and TLS for routing only
// (the connection still goes to the original address). Domain rules depend on it.
func TestBridgeSniffsTheHostForRouting(t *testing.T) {
	p := startBridge(t, `[{"type": "field", "domain": ["full:blocked.example"], "outboundTag": "block"}]`)
	echo := echoTCP(t)

	for _, tc := range []struct {
		host   string
		passes bool
	}{{"blocked.example", false}, {"fine.example", true}} {
		c, err := p.DialContext(context.Background(), metadata(M.TCP, echo))
		if err != nil {
			t.Fatalf("DialContext: %v", err)
		}
		req := "GET / HTTP/1.1\r\nHost: " + tc.host + "\r\n\r\n"
		got, err := roundTrip(c, req)
		c.Close()
		if passed := err == nil && got == req; passed != tc.passes {
			t.Errorf("host %q passed = %v (%q, %v), want %v", tc.host, passed, got, err, tc.passes)
		}
	}
}

func TestBridgeCarriesUDP(t *testing.T) {
	p := startBridge(t, `[]`)
	echo := echoUDP(t)

	pc, err := p.DialUDP(metadata(M.UDP, echo))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer pc.Close()
	if _, err := pc.WriteTo([]byte("hello"), net.UDPAddrFromAddrPort(echo)); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, from, err := pc.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("ReadFrom = %q, %v; want \"hello\"", buf[:n], err)
	}
	if from.String() != echo.String() {
		t.Errorf("datagram came from %v, want %v", from, echo)
	}
}

// xray's own UDP connection ignores deadlines, so a session would never idle
// out and the relay's timeout would not work. The bridge makes a read deadline
// end the wait with a timeout error, as a socket does.
func TestBridgeUDPHonoursTheReadDeadline(t *testing.T) {
	p := startBridge(t, `[]`)
	pc, err := p.DialUDP(metadata(M.UDP, echoUDP(t)))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer pc.Close()

	_ = pc.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	done := make(chan error, 1)
	go func() {
		_, _, err := pc.ReadFrom(make([]byte, 64))
		done <- err
	}()
	select {
	case err := <-done:
		var ne net.Error
		if !asNetError(err, &ne) || !ne.Timeout() {
			t.Errorf("ReadFrom after the deadline = %v, want a timeout error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFrom is still waiting long after its deadline")
	}
}

func asNetError(err error, target *net.Error) bool {
	ne, ok := err.(net.Error)
	if ok {
		*target = ne
	}
	return ok
}
