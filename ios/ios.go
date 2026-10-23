package ios

import (
	slipcore "slipstream-core/core"
)

func init() {
	// Let the heap grow to 1.2x live data between GCs: the Network Extension
	// lives under a hard memory limit. No memory limit of our own yet; it needs
	// a measurement on a device with a large geo config.
	_ = slipcore.SetGCTuning(20, 0)
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

// SetVerboseLogs turns on info-level core logs (xray access + DNS log,
// tun2socks connections) from the next Start. Debug builds only: the app
// passes its own debug flag, so release builds never call it with true.
func SetVerboseLogs(on bool) {
	slipcore.SetVerboseLogs(on)
}

// Version returns the embedded xray-core version, e.g. "26.9.9".
func Version() string {
	return slipcore.XrayVersion()
}

type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

func QueryTraffic() *TrafficStats {
	up, down := slipcore.QueryTraffic()
	return &TrafficStats{UplinkBytes: up, DownlinkBytes: down}
}

// MemoryStats is xray-core/tun2socks's own memory footprint: the Go runtime's,
// apart from the rest of the extension. See slipcore.MemorySnapshot for what
// each field means.
//
// gomobile binds only this package's types, hence the copy. Fields must match
// MemorySnapshot exactly (names, types, order): QueryMemory converts one into
// the other, so any drift stops the build instead of silently zeroing a field.
type MemoryStats struct {
	TotalBytes     int64
	ReleasedBytes  int64
	RetainedBytes  int64
	HeapInuseBytes int64
	StackBytes     int64
	Goroutines     int64
	GCCycles       int64

	GCPercent        int64
	MemoryLimitBytes int64
}

func QueryMemory() *MemoryStats {
	m := MemoryStats(slipcore.ReadMemorySnapshot())
	return &m
}

func FreeMemory() {
	slipcore.FreeMemory()
}
