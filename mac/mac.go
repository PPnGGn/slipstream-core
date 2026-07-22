// Package mac is the gomobile-bound entry point for the macOS app's
// NetworkExtension (NEPacketTunnelProvider, running as a system extension).
// gomobile binds exactly one package (see the Makefile bind-mac target).
//
// macOS uses the same design as iOS: NEPacketTunnelProvider gives a
// NEPacketTunnelFlow (packet read/write), not a TUN fd. We deliberately avoid
// the private "socket.fileDescriptor" KVC trick and go the Apple-supported
// route — bridge the flow into an io.ReadWriter feeding the gvisor netstack.
//
// This is a separate package from ios/ (rather than a shared one) because
// gomobile binds one package per target and the two produce distinct
// frameworks; keeping them apart lets each evolve platform-specific surface
// (e.g. system-extension lifecycle helpers) without cross-contaminating the
// iOS binding. Today their bodies are identical and both delegate to core.
//
// https://pkg.go.dev/golang.org/x/mobile/cmd/gobind.
package mac

import (
	"io"
	v2core "v2net-core/core"
)

func init() {
	v2core.InstallLogForwarding()
}

// Handler receives log lines forwarded from xray-core and tun2socks.
type Handler interface {
	OnLog(level string, message string, source string)
}

type handlerAdapter struct{ h Handler }

func (a handlerAdapter) OnLog(level, message, source string) {
	a.h.OnLog(level, message, source)
}

func SetHandler(h Handler) {
	if h == nil {
		v2core.SetLogHandler(nil)
		return
	}
	v2core.SetLogHandler(handlerAdapter{h: h})
}

// PacketFlow is the bridge to NEPacketTunnelFlow, implemented on the Swift
// side. ReadPacket returns exactly one IP packet (blocking until one is
// available); WritePacket hands exactly one IP packet back to the tunnel.
type PacketFlow interface {
	// ReadPacket blocks until the next outbound IP packet is available and
	// returns it. A nil/empty return signals the flow is closed.
	ReadPacket() ([]byte, error)
	// WritePacket delivers one inbound IP packet to the OS tunnel.
	WritePacket(packet []byte) error
}

// packetFlowRW adapts the gomobile PacketFlow interface to an io.ReadWriter,
// which is what core.StartTunIO / the tun2socks iobased endpoint expects.
type packetFlowRW struct {
	flow PacketFlow
}

func (p packetFlowRW) Read(b []byte) (int, error) {
	pkt, err := p.flow.ReadPacket()
	if err != nil {
		return 0, err
	}
	if len(pkt) == 0 {
		return 0, io.EOF
	}
	return copy(b, pkt), nil
}

func (p packetFlowRW) Write(b []byte) (int, error) {
	packet := make([]byte, len(b))
	copy(packet, b)
	if err := p.flow.WritePacket(packet); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Start brings up xray-core (the SOCKS proxy half) from configJson, then starts
// the tun2socks netstack bridged to the given PacketFlow. socksPort must match
// the inbound SOCKS port in configJson.
func Start(configJson string, flow PacketFlow, socksPort int) error {
	if err := v2core.StartXray(configJson); err != nil {
		return err
	}
	if err := v2core.StartTunIO(packetFlowRW{flow: flow}, socksPort); err != nil {
		_ = v2core.StopXray()
		return err
	}
	return nil
}

func Stop() error {
	v2core.StopTunIO()
	return v2core.StopXray()
}

type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

func QueryTraffic() *TrafficStats {
	up, down := v2core.QueryTraffic()
	return &TrafficStats{UplinkBytes: up, DownlinkBytes: down}
}
