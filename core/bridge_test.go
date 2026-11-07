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
	xdns "github.com/xtls/xray-core/features/dns"
)

// startBridge runs an xray with no inbound at all, a direct and a blackhole
// outbound and the given routing rules, and returns the in-process bridge to it.
func startBridge(t *testing.T, rules string) *xrayProxy {
	t.Helper()
	return startBridgeConfig(t, fmt.Sprintf(`{
		"outbounds": [{"protocol": "freedom", "tag": "direct"}, {"protocol": "blackhole", "tag": "block"}],
		"routing": {"rules": %s}
	}`, rules))
}

// startBridgeConfig runs an xray with the given config (it must declare no
// inbound) and returns the in-process bridge to it.
func startBridgeConfig(t *testing.T, cfg string) *xrayProxy {
	t.Helper()
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

// fakeDNSName is the name the Fake DNS tests ask a fake address for. The config
// below maps it to loopback, so a connection that reaches the echo server got
// there by way of the name.
const fakeDNSName = "fake.test"

// startFakeDNSBridge runs the bridge to an xray with Fake DNS on, as the DNS
// page's switch writes it: "fakedns" among the DNS servers and no pool block
// (xray makes the pool itself). The freedom outbound resolves names through
// xray's DNS, whose hosts table knows fakeDNSName.
func startFakeDNSBridge(t *testing.T, rules string) *xrayProxy {
	t.Helper()
	return startBridgeConfig(t, fmt.Sprintf(`{
		"dns": {
			"servers": ["fakedns"],
			"hosts": {%q: "127.0.0.1"},
			"queryStrategy": "UseIPv4"
		},
		"outbounds": [
			{"protocol": "freedom", "tag": "direct", "settings": {"domainStrategy": "UseIP"}},
			{"protocol": "blackhole", "tag": "block"}
		],
		"routing": {"rules": %s}
	}`, fakeDNSName, rules))
}

// fakeAddrFor asks the bridge's Fake DNS for the address it hands out for
// fakeDNSName, with the given port: what an app would be told after resolving
// the name through the tunnel.
func fakeAddrFor(t *testing.T, p *xrayProxy, port uint16) netip.AddrPort {
	t.Helper()
	engine, _ := p.inst.GetFeature((*xdns.FakeDNSEngine)(nil)).(xdns.FakeDNSEngine)
	if engine == nil {
		t.Fatal("xray has no Fake DNS engine: \"fakedns\" among the servers did not create one")
	}
	ips := engine.GetFakeIPForDomain(fakeDNSName)
	if len(ips) == 0 {
		t.Fatal("Fake DNS gave no address")
	}
	addr, err := netip.ParseAddr(ips[0].String())
	if err != nil {
		t.Fatalf("fake address %q: %v", ips[0], err)
	}
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(addr) {
		t.Fatalf("fake address %v is outside the pool 198.18.0.0/15", addr)
	}
	return netip.AddrPortFrom(addr, port)
}

// With Fake DNS on, the app connects to the made-up address. For HTTP and TLS the
// name came back from the sniffed bytes anyway; for any other protocol the only
// way back is the fake address itself, so the bridge must ask for it.
func TestBridgeRestoresTheNameOfAFakeAddress(t *testing.T) {
	p := startFakeDNSBridge(t, `[]`)
	echo := echoTCP(t)

	// "ping" is neither HTTP nor TLS: nothing but the address names the host.
	c, err := p.DialContext(context.Background(), metadata(M.TCP, fakeAddrFor(t, p, echo.Port())))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "ping"); err != nil || got != "ping" {
		t.Errorf("echo through a fake address = %q, %v; want \"ping\" (the name was not restored)", got, err)
	}
}

// Routing decides by name, so the restored name has to reach the rules, not only
// the dial: a domain rule must catch traffic that went to the fake address.
//
// Two rules are tried against the same traffic: one for the name, one for a
// different name. Only the first may block it. (A connection that never gets
// restored fails too, so blocking alone would prove nothing; the second row
// shows it was the rule.)
func TestBridgeRoutesAFakeAddressByItsName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		domain  string
		blocked bool
	}{
		{"rule for its name", "full:fake.test", true},
		{"rule for another name", "full:other.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startFakeDNSBridge(t, fmt.Sprintf(
				`[{"type": "field", "domain": [%q], "outboundTag": "block"}]`, tc.domain))
			echo := echoTCP(t)

			c, err := p.DialContext(context.Background(), metadata(M.TCP, fakeAddrFor(t, p, echo.Port())))
			if err != nil {
				t.Fatalf("DialContext: %v", err)
			}
			defer c.Close()
			got, err := roundTrip(c, "ping")
			if passed := err == nil && got == "ping"; passed == tc.blocked {
				t.Errorf("traffic to the fake address passed = %v (%q, %v), want blocked = %v", passed, got, err, tc.blocked)
			}
		})
	}
}

// Chat, calls and games are UDP; they get the same fake addresses.
func TestBridgeRestoresTheNameOfAFakeAddressForUDP(t *testing.T) {
	p := startFakeDNSBridge(t, `[]`)
	echo := echoUDP(t)
	fake := fakeAddrFor(t, p, echo.Port())

	pc, err := p.DialUDP(metadata(M.UDP, fake))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer pc.Close()
	if _, err := pc.WriteTo([]byte("hello"), net.UDPAddrFromAddrPort(fake)); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, _, err := pc.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("ReadFrom = %q, %v; want \"hello\" (the name was not restored)", buf[:n], err)
	}
}

// Turning Fake DNS on must not disturb connections to addresses that are not
// in its pool: the ordinary ones still go where they say.
func TestBridgeLeavesOrdinaryAddressesAloneWithFakeDNSOn(t *testing.T) {
	p := startFakeDNSBridge(t, `[]`)
	echo := echoTCP(t)

	c, err := p.DialContext(context.Background(), metadata(M.TCP, echo))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	if got, err := roundTrip(c, "ping"); err != nil || got != "ping" {
		t.Errorf("echo to an ordinary address = %q, %v; want \"ping\"", got, err)
	}
}
