package ios

import (
	"errors"
	"math"
	"net/netip"
	"os"
	"testing"
	"time"

	slipcore "slipstream-core/core"
)

// scriptedFlow answers ReadPacket from a fixed list, then with an error.
type scriptedFlow struct {
	packets [][]byte
	end     error
	written [][]byte
}

func (f *scriptedFlow) ReadPacket() ([]byte, error) {
	if len(f.packets) == 0 {
		return nil, f.end
	}
	p := f.packets[0]
	f.packets = f.packets[1:]
	return p, nil
}

func (f *scriptedFlow) WritePacket(p []byte) error {
	f.written = append(f.written, p)
	return nil
}

const mtu = 1500

// The debug commands exist for the debug build of the app, which sets
// SetVerboseLogs(true). In a release build the core refuses them: they write
// files into a directory the caller names and change the GC of the process.
func TestDebugCommandsNeedVerboseLogs(t *testing.T) {
	slipcore.SetVerboseLogs(false)
	dir := t.TempDir()

	if path, err := WriteHeapProfile(dir, "x"); err == nil {
		t.Errorf("WriteHeapProfile() = %q in a release build, want an error", path)
	}
	if path, err := WriteGoroutineDump(dir, "x"); err == nil {
		t.Errorf("WriteGoroutineDump() = %q in a release build, want an error", path)
	}
	if err := SetGCTuning(50, 0); err == nil {
		t.Error("SetGCTuning() succeeded in a release build, want an error")
	}
	if err := SetUDPTimeout(5); err == nil {
		t.Error("SetUDPTimeout() succeeded in a release build, want an error")
	}
	if got := slipcore.UDPTimeout(); got != 60*time.Second {
		t.Errorf("UDP timeout = %v after a refused SetUDPTimeout, want 60s", got)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("release build left %d files in the dump directory", len(left))
	}
	if got := slipcore.ReadMemorySnapshot().GCPercent; got != 20 {
		t.Errorf("gogc = %d after a refused SetGCTuning, want 20", got)
	}
}

func TestDebugCommandsWorkInDebugBuilds(t *testing.T) {
	slipcore.SetVerboseLogs(true)
	t.Cleanup(func() {
		slipcore.SetVerboseLogs(false)
		_ = slipcore.SetGCTuning(20, 0)
		_ = slipcore.SetUDPTimeout(0)
	})
	dir := t.TempDir()

	if err := SetUDPTimeout(15); err != nil {
		t.Errorf("SetUDPTimeout() in a debug build: %v", err)
	}
	if got := slipcore.UDPTimeout(); got != 15*time.Second {
		t.Errorf("UDP timeout = %v after SetUDPTimeout(15), want 15s", got)
	}
	if _, err := WriteHeapProfile(dir, "x"); err != nil {
		t.Errorf("WriteHeapProfile() in a debug build: %v", err)
	}
	if _, err := WriteGoroutineDump(dir, "x"); err != nil {
		t.Errorf("WriteGoroutineDump() in a debug build: %v", err)
	}
	if err := SetGCTuning(50, 40); err != nil {
		t.Errorf("SetGCTuning() in a debug build: %v", err)
	}
	s := slipcore.ReadMemorySnapshot()
	if s.GCPercent != 50 || s.MemoryLimitBytes != 40<<20 {
		t.Errorf("gogc = %d, limit = %d after SetGCTuning(50, 40); want 50 and 40 MB", s.GCPercent, s.MemoryLimitBytes)
	}
}

// The extension is killed above about 50 MB, and a UDP session that idles for
// a minute holds on to its relay buffers and goroutines. A session started
// through this package lets an idle one go after 15 s (measured on an iPhone:
// 28 MB at the peak of a browsing load, 37 MB at 60 s).
func TestStartUsesAShortUDPTimeout(t *testing.T) {
	t.Cleanup(func() { _ = Stop(); _ = slipcore.SetUDPTimeout(0) })
	_ = slipcore.SetUDPTimeout(0)

	flow := &scriptedFlow{end: errors.New("flow closed")}
	if err := Start(`{"outbounds":[{"protocol":"freedom","tag":"direct"}]}`, flow, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := slipcore.UDPTimeout(); got != 15*time.Second {
		t.Errorf("UDP timeout = %v while the session runs, want 15s", got)
	}
	if err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := slipcore.UDPTimeout(); got != 60*time.Second {
		t.Errorf("UDP timeout = %v after Stop, want the default 60s back", got)
	}
}

// The extension gives the virtual interface a network drawn anew for each
// session: the ones the app used to give every time (10.10.10.2/24,
// fd00:10:10:10::2/64) are what another app would look for.
func TestNewTunnelNetworkIsDrawnPerSession(t *testing.T) {
	a, err := NewTunnelNetwork()
	if err != nil {
		t.Fatalf("NewTunnelNetwork: %v", err)
	}
	b, err := NewTunnelNetwork()
	if err != nil {
		t.Fatalf("NewTunnelNetwork: %v", err)
	}
	if *a == *b {
		t.Errorf("two sessions got the same network %+v", *a)
	}

	want, err := slipcore.NewTunnelNetwork()
	if err != nil {
		t.Fatal(err)
	}
	if a.Prefix4 != int64(want.Prefix4) || a.Prefix6 != int64(want.Prefix6) || a.MTU != int64(want.MTU) {
		t.Errorf("%+v: want the prefixes and MTU of the core's network (%+v)", *a, want)
	}
	v4, v6, dns := netip.MustParseAddr(a.Address4), netip.MustParseAddr(a.Address6), netip.MustParseAddr(a.DNS4)
	if !netip.MustParsePrefix("10.0.0.0/8").Contains(v4) || !netip.PrefixFrom(v4, int(a.Prefix4)).Masked().Contains(dns) || dns == v4 {
		t.Errorf("%+v: want the address and the DNS to be two hosts of one /24 in 10.0.0.0/8", *a)
	}
	if v6.As16()[0] != 0xfd {
		t.Errorf("%+v: want a unique local IPv6 address", *a)
	}
	if a.Mask4 != "255.255.255.0" {
		t.Errorf("Mask4 = %q, want 255.255.255.0 for /24", a.Mask4)
	}
}

func TestQueryMemory(t *testing.T) {
	m := QueryMemory()
	if m == nil {
		t.Fatal("QueryMemory() = nil")
	}
	if m.TotalBytes <= 0 || m.RetainedBytes <= 0 || m.RetainedBytes > m.TotalBytes {
		t.Errorf("RetainedBytes = %d, TotalBytes = %d; want 0 < retained <= total", m.RetainedBytes, m.TotalBytes)
	}
	if m.Goroutines < 1 {
		t.Errorf("Goroutines = %d, want >= 1", m.Goroutines)
	}
}

// The artifact tells which commit of the core it was built from.
func TestBuildCommitIsTheCores(t *testing.T) {
	if BuildCommit() == "" || BuildCommit() != slipcore.BuildCommit() {
		t.Errorf("BuildCommit() = %q, want the core's %q", BuildCommit(), slipcore.BuildCommit())
	}
}

func TestVersionIsTheXrayVersion(t *testing.T) {
	if Version() == "" || Version() != slipcore.XrayVersion() {
		t.Errorf("Version() = %q, want the embedded xray version %q", Version(), slipcore.XrayVersion())
	}
}

func TestSetVerboseLogsReachesTheCore(t *testing.T) {
	t.Cleanup(func() { SetVerboseLogs(false) })

	SetVerboseLogs(true)
	if !slipcore.VerboseLogs() {
		t.Error("SetVerboseLogs(true) did not reach the core")
	}
	SetVerboseLogs(false)
	if slipcore.VerboseLogs() {
		t.Error("SetVerboseLogs(false) did not reach the core")
	}
}

// The extension lives under a hard memory limit; the GC runs at 20 % growth
// from the moment the framework loads and has no memory limit of its own yet.
func TestLoadingTunesTheGC(t *testing.T) {
	s := slipcore.ReadMemorySnapshot()
	if s.GCPercent != 20 || s.MemoryLimitBytes != math.MaxInt64 {
		t.Errorf("gogc = %d, limit = %d after the package loaded; want 20 and none", s.GCPercent, s.MemoryLimitBytes)
	}
}

func TestReadDeliversAPacketIntoTheBuffer(t *testing.T) {
	flow := &scriptedFlow{packets: [][]byte{{0x45, 1, 2, 3}}}
	buf := make([]byte, mtu)

	n, err := packetFlowRW{flow: flow}.Read(buf)
	if err != nil || n != 4 || buf[0] != 0x45 || buf[3] != 3 {
		t.Errorf("Read = %d, %v, %v; want the 4 bytes of the packet", n, err, buf[:n])
	}
}

// What ends the read side ends the tunnel: the stack's read loop stops on the
// first error. So only a real error from the flow may do it. An empty packet
// is nothing the stack can use; it is skipped and the next one is read.
func TestReadSkipsAnEmptyPacket(t *testing.T) {
	flow := &scriptedFlow{packets: [][]byte{{}, nil, {0x45, 9}}}
	buf := make([]byte, mtu)

	n, err := packetFlowRW{flow: flow}.Read(buf)
	if err != nil {
		t.Fatalf("Read ended the read side on an empty packet: %v", err)
	}
	if n != 2 || buf[1] != 9 {
		t.Errorf("Read = %d bytes, %v; want the packet after the empty ones", n, buf[:n])
	}
}

// A packet longer than the buffer cannot be handed over whole. Cutting it to
// fit would give the stack a corrupt packet whose header promises more than
// there is, so it is dropped.
func TestReadDropsAPacketThatDoesNotFit(t *testing.T) {
	tooBig := make([]byte, mtu+1)
	tooBig[0] = 0x45
	flow := &scriptedFlow{packets: [][]byte{tooBig, {0x45, 7}}}
	buf := make([]byte, mtu)

	n, err := packetFlowRW{flow: flow}.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 2 || buf[1] != 7 {
		t.Errorf("Read = %d bytes; want the small packet that followed the oversize one", n)
	}
}

func TestReadReportsTheEndOfTheFlow(t *testing.T) {
	closed := errors.New("flow closed")
	flow := &scriptedFlow{end: closed}

	if _, err := (packetFlowRW{flow: flow}).Read(make([]byte, mtu)); !errors.Is(err, closed) {
		t.Errorf("Read error = %v, want the flow's own error", err)
	}
}

func TestWriteCopiesThePacket(t *testing.T) {
	flow := &scriptedFlow{}
	pkt := []byte{0x45, 1, 2}

	n, err := packetFlowRW{flow: flow}.Write(pkt)
	if err != nil || n != 3 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	pkt[0] = 0 // the caller reuses its buffer
	if flow.written[0][0] != 0x45 {
		t.Error("the flow received the caller's buffer, not a copy")
	}
}
