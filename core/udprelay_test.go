package core

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type datagram struct {
	data []byte
	addr net.Addr
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// fakePacketConn is a net.PacketConn over channels. Like a UDP socket it cuts a
// datagram that does not fit the buffer it is read into, silently.
type fakePacketConn struct {
	in     chan datagram
	out    chan datagram
	closed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	deadline time.Time
}

func newFakePacketConn() *fakePacketConn {
	return &fakePacketConn{in: make(chan datagram, 64), out: make(chan datagram, 64), closed: make(chan struct{})}
}

func (c *fakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	d := c.deadline
	c.mu.Unlock()
	var expired <-chan time.Time
	if !d.IsZero() {
		wait := time.Until(d)
		if wait <= 0 {
			return 0, nil, timeoutError{}
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case dg := <-c.in:
		return copy(p, dg.data), dg.addr, nil
	case <-expired:
		return 0, nil, timeoutError{}
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *fakePacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.out <- datagram{append([]byte(nil), p...), addr}
	return len(p), nil
}

func (c *fakePacketConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
func (c *fakePacketConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}
func (c *fakePacketConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:40000"))
}
func (c *fakePacketConn) SetDeadline(t time.Time) error {
	return c.SetReadDeadline(t)
}
func (c *fakePacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *fakePacketConn) SetWriteDeadline(time.Time) error { return nil }

// fakeUDPConn is the application's side of a session as the stack hands it over.
type fakeUDPConn struct {
	*fakePacketConn
	id stack.TransportEndpointID
}

func (c *fakeUDPConn) Read([]byte) (int, error)      { return 0, errors.New("not used") }
func (c *fakeUDPConn) Write([]byte) (int, error)     { return 0, errors.New("not used") }
func (c *fakeUDPConn) RemoteAddr() net.Addr          { return c.LocalAddr() }
func (c *fakeUDPConn) ID() stack.TransportEndpointID { return c.id }

// fakeProxy dials the same fake remote for every session.
type fakeProxy struct {
	proxy.Proxy
	remote *fakePacketConn
	err    error
}

func (p fakeProxy) DialUDP(*M.Metadata) (net.PacketConn, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.remote, nil
}

// relayed is one relayed UDP session: the app talks to 8.8.8.8:53.
type relayed struct {
	app    *fakeUDPConn
	remote *fakePacketConn
	done   chan struct{}
}

var dnsServer = net.UDPAddrFromAddrPort(netip.MustParseAddrPort("8.8.8.8:53"))

func startRelay(t *testing.T, timeout time.Duration) *relayed {
	t.Helper()
	s := &relayed{
		app: &fakeUDPConn{
			fakePacketConn: newFakePacketConn(),
			id: stack.TransportEndpointID{
				LocalAddress:  tcpip.AddrFromSlice([]byte{8, 8, 8, 8}),
				LocalPort:     53,
				RemoteAddress: tcpip.AddrFromSlice([]byte{10, 10, 10, 2}),
				RemotePort:    50000,
			},
		},
		remote: newFakePacketConn(),
		done:   make(chan struct{}),
	}
	go func() {
		relayUDP(s.app, fakeProxy{remote: s.remote}, statistic.DefaultManager, timeout)
		close(s.done)
	}()
	t.Cleanup(func() { s.app.Close(); s.remote.Close() })
	return s
}

func receive(t *testing.T, c *fakePacketConn) datagram {
	t.Helper()
	select {
	case dg := <-c.out:
		return dg
	case <-time.After(2 * time.Second):
		t.Fatal("no datagram within 2 s")
		return datagram{}
	}
}

func TestRelayUDPForwardsBothWays(t *testing.T) {
	s := startRelay(t, 5*time.Second)

	s.app.in <- datagram{[]byte("query"), nil}
	got := receive(t, s.remote)
	if string(got.data) != "query" || got.addr.String() != "8.8.8.8:53" {
		t.Errorf("remote got %q to %v, want \"query\" to 8.8.8.8:53", got.data, got.addr)
	}

	s.remote.in <- datagram{[]byte("answer"), dnsServer}
	if back := receive(t, s.app.fakePacketConn); string(back.data) != "answer" {
		t.Errorf("app got %q, want \"answer\"", back.data)
	}
}

// A datagram from another address than the one the app talked to is not part
// of the session (symmetric NAT); the session goes on with the right ones.
func TestRelayUDPDropsDatagramsFromOtherAddresses(t *testing.T) {
	s := startRelay(t, 5*time.Second)
	stranger := net.UDPAddrFromAddrPort(netip.MustParseAddrPort("6.6.6.6:53"))

	s.remote.in <- datagram{[]byte("spoof"), stranger}
	s.remote.in <- datagram{[]byte("real"), dnsServer}

	if got := receive(t, s.app.fakePacketConn); string(got.data) != "real" {
		t.Errorf("app got %q, want the datagram of the right address only", got.data)
	}
}

// The relay buffers are small: a datagram that fills one may have been cut, and
// is dropped instead of passed on damaged. The session is not harmed by it.
func TestRelayUDPDropsADatagramThatMayBeCut(t *testing.T) {
	s := startRelay(t, 5*time.Second)

	s.app.in <- datagram{make([]byte, udpRelayBuffer+500), nil}
	s.app.in <- datagram{[]byte("small"), nil}

	if got := receive(t, s.remote); string(got.data) != "small" {
		t.Errorf("remote got %d bytes, want the small datagram after the dropped one", len(got.data))
	}
}

func TestRelayUDPEndsAndClosesAfterTheTimeout(t *testing.T) {
	s := startRelay(t, 100*time.Millisecond)

	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the session is still open long after its timeout")
	}
	if !s.app.isClosed() || !s.remote.isClosed() {
		t.Errorf("app closed: %v, remote closed: %v; want both closed when the session ends", s.app.isClosed(), s.remote.isClosed())
	}
}

func TestRelayUDPGivesUpWhenTheProxyCannotDial(t *testing.T) {
	app := &fakeUDPConn{fakePacketConn: newFakePacketConn()}
	done := make(chan struct{})
	go func() {
		relayUDP(app, fakeProxy{err: errors.New("no route")}, statistic.DefaultManager, time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayUDP did not return after a failed dial")
	}
	if !app.isClosed() {
		t.Error("the app's side is left open after a failed dial")
	}
}

// Every session used to hold two 64 KB buffers for as long as it lived, which
// is what filled the memory of the iOS extension: tens of sessions, or
// hundreds in a busy moment. Hundreds of idle sessions must now cost a small
// fraction of that.
func TestRelayUDPSessionsAreCheap(t *testing.T) {
	const sessions = 300
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before := heap()
	all := make([]*relayed, 0, sessions)
	for i := 0; i < sessions; i++ {
		all = append(all, startRelay(t, time.Minute))
	}
	time.Sleep(200 * time.Millisecond) // let every relay reach its first read
	after := heap()
	runtime.KeepAlive(all)

	perSession := (after - before) / sessions
	t.Logf("%d idle sessions: %d KB of heap each", sessions, perSession>>10)
	// Two 64 KB buffers alone would be 128 KB. The fakes and channels of the
	// test itself take a few KB each, hence the generous margin.
	if perSession > 40<<10 {
		t.Errorf("an idle UDP session costs %d KB of heap, want well under the 128 KB of two 64 KB buffers", perSession>>10)
	}
}
