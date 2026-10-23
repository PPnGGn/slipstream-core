package core

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"net/netip"
	"testing"
)

// seeded is a reader that gives the same bytes for the same seed, so a test
// that looks at many networks does not depend on luck.
func seeded(seed byte) io.Reader {
	var key [32]byte
	key[0] = seed
	return rand.NewChaCha8(key)
}

func mustNetwork(t *testing.T, r io.Reader) TunnelNetwork {
	t.Helper()
	n, err := newTunnelNetwork(r)
	if err != nil {
		t.Fatalf("newTunnelNetwork: %v", err)
	}
	return n
}

// Every network is usable as it is: the addresses are valid, the address and
// the DNS are two different hosts of the same /24 (neither the network nor the
// broadcast address), the IPv6 address is a unique local one, and the numbers
// the platforms need are consistent with each other.
func TestTunnelNetworkIsUsable(t *testing.T) {
	r := seeded(1)
	for i := 0; i < 2000; i++ {
		n := mustNetwork(t, r)

		addr := netip.MustParseAddr(n.Address4)
		dns := netip.MustParseAddr(n.DNS4)
		if !addr.Is4() || !dns.Is4() {
			t.Fatalf("%+v: want IPv4 addresses", n)
		}
		if n.Prefix4 != 24 || n.Mask4 != "255.255.255.0" {
			t.Fatalf("%+v: want /24 and 255.255.255.0", n)
		}
		subnet := netip.PrefixFrom(addr, n.Prefix4).Masked()
		if !netip.MustParsePrefix("10.0.0.0/8").Contains(addr) {
			t.Fatalf("%+v: the address is not in 10.0.0.0/8", n)
		}
		if !subnet.Contains(dns) {
			t.Fatalf("%+v: the DNS %v is outside the subnet %v", n, dns, subnet)
		}
		if addr == dns {
			t.Fatalf("%+v: the DNS is the address of the device", n)
		}
		for _, a := range []netip.Addr{addr, dns} {
			if last := a.As4()[3]; last == 0 || last == 255 {
				t.Fatalf("%+v: %v is the network or broadcast address of its /24", n, a)
			}
		}

		v6 := netip.MustParseAddr(n.Address6)
		if !v6.Is6() || v6.Is4In6() || v6.As16()[0] != 0xfd {
			t.Fatalf("%+v: want a unique local IPv6 address (fd00::/8)", n)
		}
		if n.Prefix6 != 64 {
			t.Fatalf("%+v: want a /64", n)
		}
		raw := v6.As16()
		if bytes.Equal(raw[8:], make([]byte, 8)) {
			t.Fatalf("%+v: the interface id is zero", n)
		}

		if n.MTU != defaultMTU {
			t.Fatalf("%+v: the MTU must be the one the stack of the core is built for (%d)", n, defaultMTU)
		}
	}
}

// The two networks the apps used to give every session are exactly what a
// scan for this client would look for, so they are never chosen.
func TestTunnelNetworkNeverTheOldFixedOnes(t *testing.T) {
	old := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"),   // Android
		netip.MustParsePrefix("10.10.10.0/24"), // iOS
		netip.MustParsePrefix("fd00:10:10:10::/64"),
	}
	r := seeded(2)
	for i := 0; i < 20000; i++ {
		n := mustNetwork(t, r)
		for _, p := range old {
			for _, s := range []string{n.Address4, n.DNS4, n.Address6} {
				if p.Contains(netip.MustParseAddr(s)) {
					t.Fatalf("%+v is inside the old fixed network %v", n, p)
				}
			}
		}
	}
}

// The rejection of the old networks is not a hole in the draw: with a reader
// that would pick one of them, the next draw is used.
func TestTunnelNetworkRedrawsAnOldOne(t *testing.T) {
	// 10.0.0 and then 10.10.10 as the second and third octets, then a good pair.
	feed := bytes.NewReader([]byte{
		0, 0, // 10.0.0.x: the Android network
		10, 10, // 10.10.10.x: the iOS network
		37, 201, // 10.37.201.x
	})
	r := io.MultiReader(feed, seeded(3))
	n := mustNetwork(t, r)
	if got := netip.MustParseAddr(n.Address4).As4(); got[1] != 37 || got[2] != 201 {
		t.Errorf("Address4 = %v, want 10.37.201.x after the two rejected ones", n.Address4)
	}
}

// Sessions differ: the networks are drawn from the whole 10.0.0.0/8 and the
// host parts and the IPv6 networks are not fixed either.
func TestTunnelNetworkVariesBetweenSessions(t *testing.T) {
	r := seeded(4)
	const draws = 5000
	nets4 := map[netip.Prefix]bool{}
	second := map[byte]bool{}
	hosts := map[byte]bool{}
	dnsHosts := map[byte]bool{}
	nets6 := map[netip.Prefix]bool{}
	for i := 0; i < draws; i++ {
		n := mustNetwork(t, r)
		a := netip.MustParseAddr(n.Address4)
		nets4[netip.PrefixFrom(a, 24).Masked()] = true
		second[a.As4()[1]] = true
		hosts[a.As4()[3]] = true
		dnsHosts[netip.MustParseAddr(n.DNS4).As4()[3]] = true
		nets6[netip.PrefixFrom(netip.MustParseAddr(n.Address6), 64).Masked()] = true
	}
	// 65534 possible /24s: 5000 draws collide in about 190 of them.
	if len(nets4) < draws*95/100 {
		t.Errorf("%d different IPv4 networks in %d draws, want nearly all different", len(nets4), draws)
	}
	if len(second) < 250 {
		t.Errorf("the second octet took %d of 256 values, want the whole range", len(second))
	}
	if len(hosts) < 240 || len(dnsHosts) < 240 {
		t.Errorf("host parts took %d and %d of 254 values, want the whole range", len(hosts), len(dnsHosts))
	}
	if len(nets6) != draws {
		t.Errorf("%d different IPv6 networks in %d draws, want all different", len(nets6), draws)
	}
}

// The same bytes give the same network, so the draw holds no other state.
func TestTunnelNetworkIsAFunctionOfItsRandomness(t *testing.T) {
	a := mustNetwork(t, seeded(5))
	b := mustNetwork(t, seeded(5))
	if a != b {
		t.Errorf("same bytes, different networks:\n%+v\n%+v", a, b)
	}
}

// With no randomness there is no network: a zero or half drawn one would be
// worse than a failed start.
func TestTunnelNetworkFailsWithoutRandomness(t *testing.T) {
	boom := errors.New("no entropy")
	if _, err := newTunnelNetwork(errReader{boom}); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the reader's", err)
	}
	if _, err := newTunnelNetwork(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Error("a reader with too few bytes gave a network")
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// The real source is used by the exported constructor: two calls differ.
func TestNewTunnelNetworkUsesTheSystemRandomness(t *testing.T) {
	a, err := NewTunnelNetwork()
	if err != nil {
		t.Fatalf("NewTunnelNetwork: %v", err)
	}
	b, err := NewTunnelNetwork()
	if err != nil {
		t.Fatalf("NewTunnelNetwork: %v", err)
	}
	if a == b {
		t.Errorf("two sessions got the same network %+v", a)
	}
}
