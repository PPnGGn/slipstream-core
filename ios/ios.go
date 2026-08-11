package ios

import (
	"fmt"
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

func Start(configJson string, flow PacketFlow, socksPort int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ios.Start: panic recovered: %v", r)
		}
	}()
	if err := slipcore.StartXray(configJson); err != nil {
		return err
	}
	if err := slipcore.StartTunIO(packetFlowRW{flow: flow}, socksPort); err != nil {
		_ = slipcore.StopXray()
		return err
	}
	return nil
}

func Stop() error {
	slipcore.StopTunIO()
	return slipcore.StopXray()
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
