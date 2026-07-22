// Package ios is the gomobile-bound entry point for the iOS app extension
// (NEPacketTunnelProvider). gomobile binds exactly one package (see the
// Makefile bind-ios target).
//
// iOS has no TUN file descriptor: NEPacketTunnelProvider gives a
// NEPacketTunnelFlow (packet read/write), and the private
// "socket.fileDescriptor" KVC trick has been dead since iOS 15. So instead of
// an fd, Start takes a PacketFlow that the Swift side implements on top of
// NEPacketTunnelFlow. Go wraps it into an io.ReadWriter and feeds the gvisor
// netstack (the Apple-supported path). macOS uses the same design in ../mac.
//
// https://pkg.go.dev/golang.org/x/mobile/cmd/gobind.
package ios

import (
	"io"
	v2core "v2net-core/core"
)

func init() {
	v2core.InstallLogForwarding()
}

// Handler receives log lines forwarded from xray-core and tun2socks.
// Swift implements this and passes it to SetHandler.
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
//
// gomobile turns this into a callback interface Swift conforms to. The
// []byte <-> Data marshaling across the boundary is done by gomobile.
type PacketFlow interface {
	// ReadPacket blocks until the next outbound IP packet is available and
	// returns it. A nil/empty return signals the flow is closed.
	ReadPacket() ([]byte, error)
	// WritePacket delivers one inbound IP packet to the OS tunnel.
	WritePacket(packet []byte) error
}

// packetFlowRW adapts the gomobile PacketFlow interface to an io.ReadWriter,
// which is what core.StartTunIO / the tun2socks iobased endpoint expects.
//
// The iobased endpoint reads one packet per Read and writes one packet per
// Write, so this 1:1 mapping onto PacketFlow's per-packet methods is exact.
type packetFlowRW struct {
	flow PacketFlow
}

func (p packetFlowRW) Read(b []byte) (int, error) {
	pkt, err := p.flow.ReadPacket()
	if err != nil {
		return 0, err
	}
	if len(pkt) == 0 {
		// Treat an empty read as EOF so the endpoint's dispatch loop exits.
		return 0, io.EOF
	}
	return copy(b, pkt), nil
}

func (p packetFlowRW) Write(b []byte) (int, error) {
	// Copy: b is owned by the netstack and reused after Write returns, but the
	// packet may outlive this call once it crosses into Swift.
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
