package core

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"gvisor.dev/gvisor/pkg/tcpip"
)

// udpRelayBuffer is the size of the buffer a UDP session reads datagrams into.
// tun2socks' own relay allocates 64 KB, the largest possible datagram, for each
// of the two directions of every session, which with a hundred sessions in a
// busy moment is 12 MB of a Network Extension's 50. Datagrams that arrive over
// a 1500-byte tunnel are far smaller, and so are nearly all that come back:
// DNS answers (EDNS: 4 KB at most), QUIC (about 1.3 KB). A datagram that fills
// the buffer may have been cut and is dropped.
const udpRelayBuffer = 8 << 10

var udpBuffers = sync.Pool{New: func() any {
	b := make([]byte, udpRelayBuffer)
	return &b
}}

// transportHandler is what the stack hands the connections it terminates to.
// TCP goes to tun2socks' tunnel as it is; UDP goes to relayUDP, the same as the
// tunnel's own relay except for the size of its buffers.
type transportHandler struct{ tunnel *tunnel.Tunnel }

func (h transportHandler) HandleTCP(c adapter.TCPConn) { h.tunnel.HandleTCP(c) }

func (h transportHandler) HandleUDP(c adapter.UDPConn) {
	go relayUDP(c, h.tunnel.Proxy(), statistic.DefaultManager, UDPTimeout())
}

// relayUDP carries one UDP session of the app, uc, through the proxy until it
// has been idle for timeout, and closes both ends. It follows tun2socks'
// tunnel.handleUDPConn.
func relayUDP(uc adapter.UDPConn, p proxy.Proxy, manager *statistic.Manager, timeout time.Duration) {
	defer uc.Close()

	id := uc.ID()
	md := &M.Metadata{
		Network: M.UDP,
		SrcIP:   addrOf(id.RemoteAddress),
		SrcPort: id.RemotePort,
		DstIP:   addrOf(id.LocalAddress),
		DstPort: id.LocalPort,
	}

	pc, err := p.DialUDP(md)
	if err != nil {
		tunlog.Warnf("[UDP] dial %s: %v", md.DestinationAddress(), err)
		return
	}
	md.MidIP, md.MidPort = addrPortOf(pc.LocalAddr())

	pc = statistic.NewUDPTracker(pc, md, manager)
	defer pc.Close()

	var remote net.Addr
	if udpAddr := md.UDPAddr(); udpAddr != nil {
		remote = udpAddr
	} else {
		remote = md.Addr()
	}
	pc = &symmetricNATConn{PacketConn: pc, src: md.SourceAddress(), dst: md.DestinationAddress()}

	tunlog.Infof("[UDP] %s <-> %s", md.SourceAddress(), md.DestinationAddress())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); copyDatagrams(pc, uc, remote, timeout) }()
	go func() { defer wg.Done(); copyDatagrams(uc, pc, nil, timeout) }()
	wg.Wait()
}

// copyDatagrams moves datagrams from src to dst (to is the address to send them
// to, nil when dst knows it) until src is idle for timeout, closed or failing.
func copyDatagrams(dst, src net.PacketConn, to net.Addr, timeout time.Duration) {
	bp := udpBuffers.Get().(*[]byte)
	defer udpBuffers.Put(bp)
	buf := *bp

	for {
		_ = src.SetReadDeadline(time.Now().Add(timeout))
		n, _, err := src.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if !(errors.As(err, &ne) && ne.Timeout()) && err != io.EOF {
				tunlog.Debugf("[UDP] copy data: %v", err)
			}
			return
		}
		if n == len(buf) {
			tunlog.Debugf("[UDP] drop a datagram of %d bytes or more", n)
			continue
		}
		if _, err := dst.WriteTo(buf[:n], to); err != nil {
			tunlog.Debugf("[UDP] copy data: %v", err)
			return
		}
		_ = dst.SetReadDeadline(time.Now().Add(timeout))
	}
}

// symmetricNATConn drops the datagrams that do not come from the address the
// session was opened to.
type symmetricNATConn struct {
	net.PacketConn
	src string
	dst string
}

func (c *symmetricNATConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, from, err := c.PacketConn.ReadFrom(p)
		if from != nil && from.String() != c.dst {
			tunlog.Warnf("[UDP] symmetric NAT %s->%s: drop packet from %s", c.src, c.dst, from)
			continue
		}
		return n, from, err
	}
}

func addrOf(a tcpip.Address) netip.Addr {
	ip, _ := netip.AddrFromSlice(a.AsSlice())
	return ip
}

func addrPortOf(a net.Addr) (netip.Addr, uint16) {
	if a == nil {
		return netip.Addr{}, 0
	}
	if v, ok := a.(interface{ AddrPort() netip.AddrPort }); ok {
		ap := v.AddrPort()
		return ap.Addr(), ap.Port()
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}, 0
	}
	return ap.Addr(), ap.Port()
}
