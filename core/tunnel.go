package core

import (
	"fmt"
	t2score "github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"github.com/xjasonlyu/tun2socks/v2/core/option"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"io"
	"net/url"
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
