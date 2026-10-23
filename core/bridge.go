package core

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	xctx "github.com/xtls/xray-core/common/ctx"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
)

// bridgeInboundTag is the tag the traffic of the tunnel carries into xray's
// routing. It is the tag of the SOCKS inbound this bridge replaced, and the
// rules of providers and of the app (the DNS hijack) are written for it.
const bridgeInboundTag = "socks"

// xrayProxy is the proxy tun2socks relays the connections of the device
// through: it hands them to xray's dispatcher inside the process.
//
// It replaces a SOCKS inbound on a loopback port. Such a port can be found by
// any app on the phone that scans 127.0.0.1, and a SOCKS or HTTP proxy that
// answers a probe gives away that a proxy client is installed, whatever the
// password. With the bridge nothing listens at all.
type xrayProxy struct {
	inst  *xcore.Instance
	level uint32 // the policy level of the traffic, 8 for the SOCKS inbound it replaced
}

func newXrayProxy(inst *xcore.Instance, level uint32) *xrayProxy {
	return &xrayProxy{inst: inst, level: level}
}

// newContext prepares what the SOCKS inbound used to put into the context of a
// connection: the inbound tag and user level for routing and policy, and
// sniffing of the host name of HTTP and TLS for routing only (the connection
// still goes to the original address).
//
// The context is detached from the one tun2socks dials with, which it cancels
// as soon as the dial returns, while xray ties the life of the connection to the
// context it was dispatched with. The returned cancel ends the connection.
func (p *xrayProxy) newContext(md *M.Metadata, network xnet.Network) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = xctx.ContextWithID(ctx, session.NewID())
	ctx = session.ContextWithInbound(ctx, &session.Inbound{
		Source: xnet.Destination{
			Network: network,
			Address: xnet.IPAddress(md.SrcIP.Unmap().AsSlice()),
			Port:    xnet.Port(md.SrcPort),
		},
		Tag:  bridgeInboundTag,
		Name: "socks",
		User: &protocol.MemoryUser{Level: p.level},
	})
	return session.ContextWithContent(ctx, &session.Content{
		SniffingRequest: session.SniffingRequest{
			Enabled:                        true,
			OverrideDestinationForProtocol: []string{"http", "tls"},
			RouteOnly:                      true,
		},
	}), cancel
}

func destinationOf(md *M.Metadata, network xnet.Network) xnet.Destination {
	return xnet.Destination{
		Network: network,
		Address: xnet.IPAddress(md.DstIP.Unmap().AsSlice()),
		Port:    xnet.Port(md.DstPort),
	}
}

// DialContext implements proxy.Proxy.
func (p *xrayProxy) DialContext(ctx context.Context, md *M.Metadata) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dest := destinationOf(md, xnet.Network_TCP)
	if !dest.IsValid() {
		return nil, errors.New("bridge: invalid destination " + md.DestinationAddress())
	}
	cctx, cancel := p.newContext(md, xnet.Network_TCP)
	conn, err := xcore.Dial(cctx, p.inst, dest)
	if err != nil {
		cancel()
		return nil, err
	}
	return &cancelConn{Conn: conn, cancel: cancel}, nil
}

// DialUDP implements proxy.Proxy.
func (p *xrayProxy) DialUDP(md *M.Metadata) (net.PacketConn, error) {
	if !destinationOf(md, xnet.Network_UDP).IsValid() {
		return nil, errors.New("bridge: invalid destination " + md.DestinationAddress())
	}
	cctx, cancel := p.newContext(md, xnet.Network_UDP)
	pc, err := xcore.DialUDP(cctx, p.inst)
	if err != nil {
		cancel()
		return nil, err
	}
	return &idlePacketConn{PacketConn: pc, cancel: cancel}, nil
}

// cancelConn ends the context of the connection when it is closed.
type cancelConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *cancelConn) Close() error {
	err := c.Conn.Close()
	c.cancel()
	return err
}

// idleTimeoutError is what a read returns when its deadline has passed.
type idleTimeoutError struct{}

func (idleTimeoutError) Error() string   { return "i/o timeout" }
func (idleTimeoutError) Timeout() bool   { return true }
func (idleTimeoutError) Temporary() bool { return true }

// idlePacketConn gives xray's UDP connection what it lacks: read deadlines. Its
// own SetReadDeadline does nothing, so a session would never idle out and the
// relay's timeout would not work.
//
// The deadline is armed on a timer that closes the connection when it fires. The
// relay moves the deadline forward with every datagram in either direction, so
// a deadline that passes means the session has been idle for the whole timeout
// and is over; closing is what the relay does next anyway. A read cut short by
// it reports a timeout, not the closed connection.
type idlePacketConn struct {
	net.PacketConn
	cancel context.CancelFunc

	mu      sync.Mutex
	timer   *time.Timer
	expired atomic.Bool
}

func (c *idlePacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.IsZero() {
		if c.timer != nil {
			c.timer.Stop()
		}
		return nil
	}
	d := time.Until(t)
	if c.timer == nil {
		c.timer = time.AfterFunc(d, c.expire)
	} else {
		c.timer.Reset(d)
	}
	return nil
}

func (c *idlePacketConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *idlePacketConn) expire() {
	c.expired.Store(true)
	_ = c.PacketConn.Close()
}

func (c *idlePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err != nil && c.expired.Load() {
		return 0, nil, idleTimeoutError{}
	}
	return n, addr, err
}

func (c *idlePacketConn) Close() error {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	err := c.PacketConn.Close()
	c.cancel()
	return err
}
