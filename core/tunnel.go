package core

import (
	"fmt"
	t2score "github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"github.com/xjasonlyu/tun2socks/v2/core/option"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	// Registers the "socks5" scheme that proxy.Parse looks up. The engine
	// package used to pull it in; without it Parse says "unknown protocol".
	_ "github.com/xjasonlyu/tun2socks/v2/proxy/socks5"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const defaultMTU = 1500

// default buffer sizes for TCP endpoints
const (
	tcpBufferMin     = 4 << 10
	tcpBufferDefault = 256 << 10
	tcpBufferMax     = 1 << 20
)

// defaultUDPTimeout is how long tun2socks keeps an idle UDP session by default.
const defaultUDPTimeout = 60 * time.Second

var udpTimeout atomic.Int64 // time.Duration, applied to sessions that start later

func init() { udpTimeout.Store(int64(defaultUDPTimeout)) }

// UDPTimeout is how long a UDP session of the tunnel may stay idle before it is
// closed.
func UDPTimeout() time.Duration { return time.Duration(udpTimeout.Load()) }

// SetUDPTimeout sets the idle time after which a UDP session of the tunnel is
// closed. Every session holds two 64 KB relay buffers while it lives, so the
// timeout bounds how much memory a burst of short flows keeps. It applies to
// sessions that start after the call and is kept across sessions.
func SetUDPTimeout(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("udp timeout %v: want > 0", d)
	}
	udpTimeout.Store(int64(d))
	tunnel.T().SetUDPTimeout(d)
	return nil
}

// startStack builds the network stack on link and hands every connection it
// terminates to tun2socks' tunnel, which relays it to the local SOCKS inbound
// of xray described by p.
//
// This is what tun2socks' engine package does after parsing its settings. The
// engine is not used because it ends the whole process (log.Fatalf) when
// anything fails, instead of returning an error, and because it prints the
// proxy URL, password included, at info level.
func startStack(link stack.LinkEndpoint, p proxyParams, opts ...option.Option) (*stack.Stack, error) {
	px, err := proxy.Parse(socksProxyURL(p))
	if err != nil {
		return nil, fmt.Errorf("failed to build socks proxy: %w", err)
	}
	tunnel.T().SetProxy(px)
	tunnel.T().SetUDPTimeout(UDPTimeout())

	st, err := t2score.CreateStack(&t2score.Config{
		LinkEndpoint:     link,
		TransportHandler: transportHandler{tunnel.T()},
		Options:          opts,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create netstack: %w", err)
	}
	return st, nil
}

// --- fd-based path (Android) -------------------------------------------------

type fdTunnel struct {
	device device.Device
	stack  *stack.Stack
}

// openTunDevice wraps the TUN descriptor fd in a packet device. Offset 0:
// Android's TUN descriptor carries bare IP packets. A variable only so that
// tests can replace it: on non-Linux systems tun2socks wraps the descriptor in
// an os.File that is also closed by its finalizer, after the device has closed
// the raw descriptor, which can close a descriptor some other code has opened
// since. Linux, the only system the fd path runs on, uses a plain endpoint.
var openTunDevice = func(fd int) (device.Device, error) {
	return fdbased.Open(strconv.Itoa(fd), defaultMTU, 0)
}

var (
	fdMu     sync.Mutex
	fdActive *fdTunnel
)

// closeTunFd closes a TUN descriptor that never reached startTun. The app
// detaches the descriptor from its ParcelFileDescriptor before handing it
// over, so from then on the core is the only one that can close it: if the
// core does not, the VPN interface stays up with nothing reading from it and
// the phone has no network until the process dies.
func closeTunFd(fd int) {
	_ = os.NewFile(uintptr(fd), "tun").Close()
}

// startTun starts tun2socks on the TUN descriptor fd and takes ownership of
// it: on success the device closes it when stopTun runs, and when startTun
// refuses to start, or fails, it closes it itself. It returns an error when a
// tunnel is already running, xray is not, or the stack cannot be built.
func startTun(fd int) error {
	fdMu.Lock()
	defer fdMu.Unlock()

	if fdActive != nil {
		closeTunFd(fd)
		return fmt.Errorf("tun2socks is already running")
	}
	p, ok := activeProxyParams()
	if !ok {
		closeTunFd(fd)
		return fmt.Errorf("start tun: xray is not running")
	}
	if fd < 0 {
		return fmt.Errorf("start tun: invalid descriptor %d", fd)
	}

	dev, err := openTunDevice(fd)
	if err != nil {
		closeTunFd(fd)
		return fmt.Errorf("failed to open the tun device: %w", err)
	}
	st, err := startStack(dev, p)
	if err != nil {
		dev.Close() // closes the descriptor with it
		return err
	}
	fdActive = &fdTunnel{device: dev, stack: st}

	InstallLogForwarding()

	return nil
}

// stopTun stops the tunnel startTun started, closing the TUN descriptor, and
// closes the connections still relayed through it. It is safe to call when
// none runs.
func stopTun() {
	fdMu.Lock()
	active := fdActive
	fdActive = nil
	fdMu.Unlock()

	if active != nil {
		active.device.Close()
		active.stack.Close()
		// The app calls this on its main thread: a read that does not return
		// after the descriptor is closed must not hold it up.
		waitWithTimeout("stopTun", active.stack.Wait, 2*time.Second)
	}
	closeRelayedConns()
}

// closeRelayedConns closes every connection tun2socks still relays. Stopping
// the device and stack does not end them, and each keeps its SOCKS connection
// to the stopped xray open — and through it the whole old instance:
// dispatcher, router, geo matchers (~35 MB on a geo config). A keep-alive
// connection can hold that for minutes after a server switch.
func closeRelayedConns() {
	for _, c := range statistic.DefaultManager.Snapshot().Connections {
		_ = c.Close()
	}
}

// --- io-based path (iOS) -----------------------------------------------------

type ioTunnel struct {
	endpoint *iobased.Endpoint
	stack    *stack.Stack
}

var (
	ioMu     sync.Mutex
	ioActive *ioTunnel
)

func startTunIO(rw io.ReadWriter) error {
	ioMu.Lock()
	defer ioMu.Unlock()

	if ioActive != nil {
		return fmt.Errorf("tun2socks is already running")
	}

	p, ok := activeProxyParams()
	if !ok {
		return fmt.Errorf("start tun: xray is not running")
	}

	ep, err := iobased.New(rw, defaultMTU, 0)
	if err != nil {
		return fmt.Errorf("failed to create io endpoint: %w", err)
	}

	st, err := startStack(ep, p,
		option.WithTCPSendBufferSizeRange(tcpBufferMin, tcpBufferDefault, tcpBufferMax),
		option.WithTCPReceiveBufferSizeRange(tcpBufferMin, tcpBufferDefault, tcpBufferMax),
	)
	if err != nil {
		ep.Close()
		return err
	}

	ioActive = &ioTunnel{endpoint: ep, stack: st}

	InstallLogForwarding()

	return nil
}

func stopTunIO() {
	ioMu.Lock()
	defer ioMu.Unlock()

	if ioActive == nil {
		return
	}
	active := ioActive
	ioActive = nil

	active.stack.Close()
	active.endpoint.Close()
	waitWithTimeout("stopTunIO", active.endpoint.Wait, 2*time.Second)
	waitWithTimeout("stopTunIO", active.stack.Wait, 2*time.Second)
	closeRelayedConns()
}

// waitWithTimeout runs wait and gives up after timeout, logging that what did
// not finish. The goroutine that runs wait is left behind in that case.
func waitWithTimeout(what string, wait func(), timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		LogError(what+": wait timed out; read side likely still blocked", "iobridge")
	}
}

// FreeMemory runs a GC and hands every free page back to the OS. It blocks
// while the GC runs, so call it off the main thread.
func FreeMemory() {
	debug.FreeOSMemory()
}
