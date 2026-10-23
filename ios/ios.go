package ios

import (
	"io"
	"runtime/debug"
	slipcore "slipstream-core/core"
)

func init() {
	debug.SetGCPercent(20)
	slipcore.InstallLogForwarding()
}

type Handler interface {
	OnLog(level string, message string, source string)
}

type handlerAdapter struct{ h Handler }

func (a handlerAdapter) OnLog(level, message, source string) {
	a.h.OnLog(level, message, source)
}

func SetHandler(h Handler) {
	if h == nil {
		slipcore.SetLogHandler(nil)
		return
	}
	slipcore.SetLogHandler(handlerAdapter{h: h})
}

type PacketFlow interface {
	ReadPacket() ([]byte, error)
	WritePacket(packet []byte) error
}

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

// Start starts xray and the tunnel over flow. A failed Start leaves nothing
// running; a Start while a session runs is refused.
func Start(configJson string, flow PacketFlow, assetDir string) error {
	return slipcore.StartIO(configJson, packetFlowRW{flow: flow}, assetDir)
}

// Stop ends the session. Safe to call at any time and any number of times; if
// a Start is still working, Stop waits for it and then stops what it started.
func Stop() error {
	return slipcore.Stop()
}

type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

func QueryTraffic() *TrafficStats {
	up, down := slipcore.QueryTraffic()
	return &TrafficStats{UplinkBytes: up, DownlinkBytes: down}
}

func FreeMemory() {
	slipcore.FreeMemory()
}
