package ios

import (
	"fmt"
	"time"

	slipcore "slipstream-core/core"
)

func init() {
	// Let the heap grow to 1.2x live data between GCs: the Network Extension
	// lives under a hard memory limit. No memory limit of our own yet; it needs
	// a measurement on a device with a large geo config.
	_ = slipcore.SetGCTuning(20, 0)
	slipcore.InstallLogForwarding()
}

// Handler is implemented by the app and receives the log lines of the core, see
// slipcore.LogHandler. It is called from one goroutine of the core, in order, and
// should return fast: lines are dropped when it falls far behind.
type Handler interface {
	OnLog(level string, message string, source string)
}

type handlerAdapter struct{ h Handler }

func (a handlerAdapter) OnLog(level, message, source string) {
	a.h.OnLog(level, message, source)
}

// SetHandler sets the receiver of the core's log lines; nil removes it. Set it
// before Start to see the lines of the start itself.
func SetHandler(h Handler) {
	if h == nil {
		slipcore.SetLogHandler(nil)
		return
	}
	slipcore.SetLogHandler(handlerAdapter{h: h})
}

// PacketFlow is the device, implemented by the Network Extension's bridge.
// ReadPacket blocks until the next IP packet leaves the device and returns an
// error when the flow is closed: that error ends the tunnel, so it must be
// returned only then (empty packets are skipped). WritePacket delivers a packet
// to the device; the slice is a copy the callee may keep.
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

// TunnelNetwork is what the extension gives the virtual interface of one
// session: addresses, the mask of the IPv4 one, a DNS address of the same /24
// (nothing answers there; for a client that answers the system's queries itself)
// and the MTU. Take them from NewTunnelNetwork for every session and do not use
// fixed ones: an app that reads the addresses of the interfaces could look for
// them. The MTU is the one the core's stack is built for, use it as it is.
type TunnelNetwork struct {
	Address4 string
	Prefix4  int64
	Mask4    string
	DNS4     string
	Address6 string
	Prefix6  int64
	MTU      int64
}

// NewTunnelNetwork draws the network of a new session from the system's
// randomness. An error means there was none, and the session must not start
// with a made-up one.
func NewTunnelNetwork() (*TunnelNetwork, error) {
	n, err := slipcore.NewTunnelNetwork()
	if err != nil {
		return nil, err
	}
	return &TunnelNetwork{
		Address4: n.Address4, Prefix4: int64(n.Prefix4), Mask4: n.Mask4, DNS4: n.DNS4,
		Address6: n.Address6, Prefix6: int64(n.Prefix6), MTU: int64(n.MTU),
	}, nil
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

// BuildCommit returns the commit of slipstream-core this framework was built
// from (with "-dirty" after it when the tree had uncommitted changes), or
// "unknown" for a build that did not stamp it. For a bug report: Version says
// which xray is inside, this says which core.
func BuildCommit() string {
	return slipcore.BuildCommit()
}

// TrafficStats are the bytes counted through the proxy outbound since Start.
type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

// QueryTraffic reads the counters; zeros when no session runs or the config
// does not count (see slipcore.OutboundTag). Cheap, safe to poll.
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

// QueryMemory reads the Go runtime's memory now. It does not stop the world, so
// it can be polled every second.
func QueryMemory() *MemoryStats {
	m := MemoryStats(slipcore.ReadMemorySnapshot())
	return &m
}

// SetGCTuning changes the GC settings at once: percent is GOGC (> 0), limitMB
// a soft memory limit in MB (0 = none). For measuring memory under the
// extension's limit on a live session. Debug builds only: it refuses to run
// unless SetVerboseLogs(true) was called.
func SetGCTuning(percent int, limitMB int) error {
	if err := slipcore.RequireDebug(); err != nil {
		return err
	}
	return slipcore.SetGCTuning(percent, int64(limitMB)<<20)
}

// SetUDPTimeout sets, in seconds, how long an idle UDP session of the tunnel
// lives (15 by default on iOS, where the extension is killed at about 50 MB;
// 0 takes the setting back). Every session holds a relay buffer while it
// lives. For measuring memory; applies to sessions that start after the call
// and is kept across sessions of the process. Debug builds only, with the same
// refusal as SetGCTuning.
func SetUDPTimeout(seconds int) error {
	if err := slipcore.RequireDebug(); err != nil {
		return err
	}
	return slipcore.SetUDPTimeout(time.Duration(seconds) * time.Second)
}

// WriteHeapProfile writes the core's heap profile into dir as
// heap[-name].pprof and returns the path; read it with `go tool pprof -top`.
// Debug builds only, with the same refusal as SetGCTuning.
func WriteHeapProfile(dir string, name string) (string, error) {
	if err := slipcore.RequireDebug(); err != nil {
		return "", err
	}
	return slipcore.WriteHeapProfile(dir, name)
}

// WriteGoroutineDump writes every goroutine's stack into dir as
// goroutines[-name].txt and returns the path. Debug builds only, with the same
// refusal as SetGCTuning.
func WriteGoroutineDump(dir string, name string) (string, error) {
	if err := slipcore.RequireDebug(); err != nil {
		return "", err
	}
	return slipcore.WriteGoroutineDump(dir, name)
}

// MeasureAESGCM encrypts for a third of a second with AES-GCM and returns the
// speed, "2330 MB/s". Software AES-GCM is tens of MB/s and the instructions of
// the processor several GB/s, so it shows which one this framework uses: Go does
// not know that iOS has the instructions unless the framework was built by make
// bind-ios, see package toolchain. Debug builds only, with the same refusal as
// SetGCTuning.
func MeasureAESGCM() (string, error) {
	if err := slipcore.RequireDebug(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%.0f MB/s", slipcore.MeasureAESGCM(300*time.Millisecond)), nil
}

// FreeMemory runs a GC and hands every free page back to the OS. It blocks while
// the GC runs; the extension calls it from sleep().
func FreeMemory() {
	slipcore.FreeMemory()
}
