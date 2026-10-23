package core

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/netip"
)

// TunnelNetwork is what the platform gives the virtual interface of one
// session: its addresses, the address the system's DNS queries are sent to when
// the client answers them itself, and the MTU.
//
// The apps used to give every session the same ones (10.0.0.2/24 on Android,
// 10.10.10.2/24 on iOS, fd00:10:10:10::2/64 on both). Any app that can read the
// addresses of the interfaces, and those of a VPN are readable by every app that
// is not kept out of it, could look for exactly these. A network drawn anew for
// each session leaves nothing fixed to look for.
type TunnelNetwork struct {
	Address4 string // the device, in 10.0.0.0/8
	Prefix4  int    // 24
	Mask4    string // the same as a mask, for iOS
	DNS4     string // another host of the same /24; nothing answers there, the client's routing takes the queries
	Address6 string // the device, in fd00::/8 (unique local, RFC 4193)
	Prefix6  int    // 64
	MTU      int    // the one the stack of the core is built for; the platform must not choose another
}

// NewTunnelNetwork draws the network of a new session from the system's
// randomness.
func NewTunnelNetwork() (TunnelNetwork, error) {
	return newTunnelNetwork(rand.Reader)
}

// newTunnelNetwork draws from r, which tests replace.
//
// The IPv4 network is a /24 of 10.0.0.0/8 (65 534 of them), the device and the
// DNS two different hosts of it. The two networks the apps used before are left
// out. The IPv6 network is a unique local /64 with 56 random bits after fd, and
// the device a random 64-bit interface id, as with privacy addresses.
//
// The MTU is not drawn. The stack of the core is built for defaultMTU: a larger
// packet from the system would be cut or dropped, and a smaller MTU of the
// interface would make the system refuse the larger packets the stack hands
// back (datagrams of a remote host fill the 1500 bytes of a path). The value
// is passed on so that the platform takes it from here rather than from a
// literal of its own.
func newTunnelNetwork(r io.Reader) (TunnelNetwork, error) {
	var a, b int
	for {
		var err error
		if a, err = uniform(r, 256); err != nil {
			return TunnelNetwork{}, err
		}
		if b, err = uniform(r, 256); err != nil {
			return TunnelNetwork{}, err
		}
		if (a == 0 && b == 0) || (a == 10 && b == 10) { // 10.0.0/24 and 10.10.10/24, see above
			continue
		}
		break
	}
	// Hosts 1 to 254: not the network address, not the broadcast one.
	host, err := uniform(r, 254)
	if err != nil {
		return TunnelNetwork{}, err
	}
	dnsHost, err := uniform(r, 253) // one of the other 253
	if err != nil {
		return TunnelNetwork{}, err
	}
	if dnsHost >= host {
		dnsHost++
	}

	var v6 [16]byte
	if _, err := io.ReadFull(r, v6[:]); err != nil {
		return TunnelNetwork{}, fmt.Errorf("tunnel network: %w", err)
	}
	v6[0] = 0xfd // the rest of the first 8 bytes is the global id and the subnet id
	for allZero(v6[8:]) {
		if _, err := io.ReadFull(r, v6[8:]); err != nil {
			return TunnelNetwork{}, fmt.Errorf("tunnel network: %w", err)
		}
	}

	return TunnelNetwork{
		Address4: fmt.Sprintf("10.%d.%d.%d", a, b, host+1),
		Prefix4:  24,
		Mask4:    "255.255.255.0",
		DNS4:     fmt.Sprintf("10.%d.%d.%d", a, b, dnsHost+1),
		Address6: netip.AddrFrom16(v6).String(),
		Prefix6:  64,
		MTU:      defaultMTU,
	}, nil
}

// uniform returns a number in [0, n) with every value equally likely: the
// bytes are drawn again when they would fall outside, so the low values are not
// favoured by a remainder.
func uniform(r io.Reader, n int64) (int, error) {
	v, err := rand.Int(r, big.NewInt(n))
	if err != nil {
		return 0, fmt.Errorf("tunnel network: %w", err)
	}
	return int(v.Int64()), nil
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
