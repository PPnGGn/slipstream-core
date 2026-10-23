package ios

import (
	"errors"
	"math"
	"testing"

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
