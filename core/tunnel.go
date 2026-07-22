package core

import (
	"fmt"
	t2score "github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"io"
	"net/url"
	"sync"
)

// defaultMTU matches the MTU the platform tun (NEPacketTunnelFlow / utun) is
// configured with. 1500 is the safe default for the packet-flow path.
const defaultMTU = 1500

// --- fd-based path (Android) -------------------------------------------------
//
// StartTun drives tun2socks from a raw TUN file descriptor. This is the path
// used on Android, where VpnService.Builder().establish() hands us an honest
// fd. Apple platforms (iOS 15+/macOS) never expose that fd, so they use
// StartTunIO below instead.

func StartTun(fd int, socksPort int) error {
	engine.Insert(&engine.Key{
		Proxy:    fmt.Sprintf("socks5://127.0.0.1:%d", socksPort),
		Device:   fmt.Sprintf("fd://%d", fd),
		LogLevel: "info",
	})
	engine.Start()

	InstallLogForwarding()

	return nil
}

func StopTun() {
	engine.Stop()
}

// --- io-based path (iOS / macOS) ---------------------------------------------
//
// StartTunIO drives the same tun2socks/gvisor stack from a plain io.ReadWriter
// instead of an fd. On Apple platforms the NEPacketTunnelProvider gives us a
// NEPacketTunnelFlow (packet read/write), not a TUN fd, so the Swift side wraps
// that flow into an io.ReadWriter and passes it here. This is the
// Apple-supported path (no private "socket.fileDescriptor" KVC trick) and is
// how Go-based iOS VPN clients (e.g. Outline) work.
//
// Whole IP packets are exchanged over rw: each Read must return exactly one IP
// packet, each Write is handed exactly one IP packet — matching how
// NEPacketTunnelFlow's readPacketObjects/writePacketObjects behave.

type ioTunnel struct {
	endpoint *iobased.Endpoint
	stack    *stack.Stack
}

var (
	ioMu     sync.Mutex
	ioActive *ioTunnel
)

func StartTunIO(rw io.ReadWriter, socksPort int) error {
	ioMu.Lock()
	defer ioMu.Unlock()

	if ioActive != nil {
		return fmt.Errorf("tun2socks is already running")
	}

	u, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socksPort))
	if err != nil {
		return fmt.Errorf("failed to parse proxy url: %w", err)
	}
	px, err := proxy.Parse(u)
	if err != nil {
		return fmt.Errorf("failed to build socks proxy: %w", err)
	}
	tunnel.T().SetProxy(px)
	tunnel.T().ProcessAsync()

	ep, err := iobased.New(rw, defaultMTU, 0)
	if err != nil {
		return fmt.Errorf("failed to create io endpoint: %w", err)
	}

	st, err := t2score.CreateStack(&t2score.Config{
		LinkEndpoint:     ep,
		TransportHandler: tunnel.T(),
	})
	if err != nil {
		ep.Close()
		return fmt.Errorf("failed to create netstack: %w", err)
	}

	ioActive = &ioTunnel{endpoint: ep, stack: st}

	InstallLogForwarding()

	return nil
}

func StopTunIO() {
	ioMu.Lock()
	defer ioMu.Unlock()

	if ioActive == nil {
		return
	}
	ioActive.stack.Close()
	ioActive.stack.Wait()
	ioActive.endpoint.Close()
	ioActive = nil
}
