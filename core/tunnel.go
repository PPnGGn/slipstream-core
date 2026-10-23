package core

import (
	"fmt"
	t2score "github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"github.com/xjasonlyu/tun2socks/v2/core/option"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

const defaultMTU = 1500

// default buffer sizes for TCP endpoints
const (
	tcpBufferMin     = 4 << 10
	tcpBufferDefault = 256 << 10
	tcpBufferMax     = 1 << 20
)

// --- fd-based path (Android) -------------------------------------------------

// CloseTunFd closes a TUN descriptor that never reached StartTun. The app
// detaches the descriptor from its ParcelFileDescriptor before handing it
// over, so from then on the core is the only one that can close it: if the
// core does not, the VPN interface stays up with nothing reading from it and
// the phone has no network until the process dies.
func CloseTunFd(fd int) {
	_ = os.NewFile(uintptr(fd), "tun").Close()
}

// StartTun starts tun2socks on the TUN descriptor fd and takes ownership of
// it: on success tun2socks closes it with the device, and when StartTun
// refuses to start it closes it itself.
func StartTun(fd int) error {
	p, ok := activeProxyParams()
	if !ok {
		CloseTunFd(fd)
		return fmt.Errorf("start tun: xray is not running")
	}

	engine.Insert(&engine.Key{
		Proxy:  socksProxyURL(p).String(),
		Device: fmt.Sprintf("fd://%d", fd),
		// Not "info": engine logs "[STACK] device <-> proxy" with the full proxy URL,
		// which will carry the SOCKS password.
		LogLevel: "warn",
	})
	engine.Start()

	InstallLogForwarding()

	return nil
}

func StopTun() {
	engine.Stop()
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

func StartTunIO(rw io.ReadWriter) error {
	ioMu.Lock()
	defer ioMu.Unlock()

	if ioActive != nil {
		return fmt.Errorf("tun2socks is already running")
	}

	p, ok := activeProxyParams()
	if !ok {
		return fmt.Errorf("start tun: xray is not running")
	}
	px, err := proxy.Parse(socksProxyURL(p))
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
		Options: []option.Option{
			option.WithTCPSendBufferSizeRange(tcpBufferMin, tcpBufferDefault, tcpBufferMax),
			option.WithTCPReceiveBufferSizeRange(tcpBufferMin, tcpBufferDefault, tcpBufferMax),
		},
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
	active := ioActive
	ioActive = nil

	active.stack.Close()
	active.endpoint.Close()
	waitWithTimeout(active.endpoint.Wait, 2*time.Second)
	waitWithTimeout(active.stack.Wait, 2*time.Second)
	closeRelayedConns()
}

func waitWithTimeout(wait func(), timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		LogError("StopTunIO: wait timed out; read side likely still blocked", "iobridge")
	}
}

func FreeMemory() {
	debug.FreeOSMemory()
}
