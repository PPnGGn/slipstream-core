package ios

import (
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

// Read hands the next packet of the flow to the stack. Only an error of the
// flow ends the read side, and with it the tunnel: the stack stops reading on
// the first error. A packet it cannot use is skipped instead: an empty one, and
// one longer than b (the MTU), which cut to fit would reach the stack as a
// corrupt packet whose header promises more than it carries.
func (p packetFlowRW) Read(b []byte) (int, error) {
	for {
		pkt, err := p.flow.ReadPacket()
		if err != nil {
			return 0, err
		}
		if len(pkt) == 0 || len(pkt) > len(b) {
			continue
		}
		return copy(b, pkt), nil
	}
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
//
// Make the flow's blocked ReadPacket return an error before calling Stop (the
// app's bridge does it in close()). Stop waits for the read side for two
// seconds and then gives up, logging it, and the reading goroutine stays
// behind until ReadPacket returns.
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
