package android

import (
	"math"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	slipcore "slipstream-core/core"
)

func TestQueryMemory(t *testing.T) {
	m := QueryMemory()
	if m == nil {
		t.Fatal("QueryMemory() = nil")
	}
	if m.TotalBytes <= 0 {
		t.Errorf("TotalBytes = %d, want > 0", m.TotalBytes)
	}
	if m.RetainedBytes <= 0 || m.RetainedBytes > m.TotalBytes {
		t.Errorf("RetainedBytes = %d, want in (0, TotalBytes=%d]", m.RetainedBytes, m.TotalBytes)
	}
	if m.Goroutines < 1 {
		t.Errorf("Goroutines = %d, want >= 1", m.Goroutines)
	}
}

// The service gives the virtual interface a network drawn anew for each session:
// the ones the app used to give every time (10.0.0.2/24, fd00:10:10:10::2/64) are
// what another app would look for.
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

// The artifact tells which commit of the core it was built from.
func TestBuildCommitIsTheCores(t *testing.T) {
	if BuildCommit() == "" || BuildCommit() != slipcore.BuildCommit() {
		t.Errorf("BuildCommit() = %q, want the core's %q", BuildCommit(), slipcore.BuildCommit())
	}
}

func TestLoadingTunesGC(t *testing.T) {
	s := slipcore.ReadMemorySnapshot()
	if s.GCPercent != 25 {
		t.Errorf("gogc = %d after the package loaded, want 25", s.GCPercent)
	}
	if s.MemoryLimitBytes != math.MaxInt64 {
		t.Errorf("memory limit = %d, want none", s.MemoryLimitBytes)
	}
}

// tunFdLike returns a descriptor the test owns, standing in for the one the
// app hands to Start after detachFd(): from that call on, Start owns it.
func tunFdLike(t *testing.T) int {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	fd, err := syscall.Dup(int(r.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	return fd
}

func fdOpen(fd int) bool {
	var st syscall.Stat_t
	return syscall.Fstat(fd, &st) == nil
}

// A failed Start must not leave the TUN descriptor open. The app has already
// detached it from its ParcelFileDescriptor, so nobody else will close it, and
// with the descriptor open the VPN interface stays up with nothing reading
// from it: the phone has no network until the process dies.
func TestStartClosesTunFdOnFailure(t *testing.T) {
	for name, config := range map[string]string{
		"config that is not JSON":      "not json",
		"config that is not an object": "[]",
	} {
		t.Run(name, func(t *testing.T) {
			fd := tunFdLike(t)

			if err := Start(config, fd, "", ""); err == nil {
				t.Fatal("Start() succeeded with a broken config")
			}
			if fdOpen(fd) {
				syscall.Close(fd)
				t.Errorf("descriptor %d is still open after a failed Start", fd)
			}
		})
	}
}

// The debug commands exist for adb broadcasts that only a debuggable app
// registers. The core must not rely on that alone: in a release build, where
// SetVerboseLogs(true) is never called, they refuse to run.
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
	if got, err := MeasureAESGCM(); err == nil {
		t.Errorf("MeasureAESGCM() = %q in a release build, want an error", got)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("release build left %d files in the dump directory", len(left))
	}
	if got := slipcore.ReadMemorySnapshot().GCPercent; got != 25 {
		t.Errorf("gogc = %d after a refused SetGCTuning, want 25", got)
	}
}

func TestDebugCommandsWorkInDebugBuilds(t *testing.T) {
	slipcore.SetVerboseLogs(true)
	t.Cleanup(func() {
		slipcore.SetVerboseLogs(false)
		_ = slipcore.SetGCTuning(25, 0)
	})
	dir := t.TempDir()

	if _, err := WriteHeapProfile(dir, "x"); err != nil {
		t.Errorf("WriteHeapProfile() in a debug build: %v", err)
	}
	if _, err := WriteGoroutineDump(dir, "x"); err != nil {
		t.Errorf("WriteGoroutineDump() in a debug build: %v", err)
	}
	if got, err := MeasureAESGCM(); err != nil || !strings.HasSuffix(got, " MB/s") {
		t.Errorf("MeasureAESGCM() in a debug build = %q, %v; want a speed in MB/s", got, err)
	}
	if err := SetGCTuning(50, 0); err != nil {
		t.Errorf("SetGCTuning() in a debug build: %v", err)
	}
	if got := slipcore.ReadMemorySnapshot().GCPercent; got != 50 {
		t.Errorf("gogc = %d after SetGCTuning(50), want 50", got)
	}
}

func TestStopKeepsMemoryLog(t *testing.T) {
	slipcore.SetVerboseLogs(true) // debug build
	SetMemoryLog(true)
	t.Cleanup(func() {
		SetMemoryLog(false)
		slipcore.SetVerboseLogs(false)
	})

	if err := Stop(); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if !slipcore.MemoryLogRunning() {
		t.Error("Stop turned the memory log off; it must keep logging across server switches")
	}
}

// makeGarbage leaves n bytes the runtime took from the OS and nobody uses —
// what a stopped session becomes.
func makeGarbage(n int) {
	b := make([]byte, n)
	runtime.KeepAlive(b)
}

// waitHandedBack fails the test unless retained memory drops by want bytes
// within 2 s. The calls under test free in the background; an idle process
// would otherwise keep the garbage for minutes, so 2 s tells the two apart.
func waitHandedBack(t *testing.T, before, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := slipcore.ReadMemorySnapshot().RetainedBytes
		if after <= before-want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("retained %d MB before, %d MB 2 s later; want %d+ MB handed back",
				before>>20, after>>20, want>>20)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStopHandsMemoryBackToOS(t *testing.T) {
	makeGarbage(64 << 20)
	before := slipcore.ReadMemorySnapshot().RetainedBytes

	if err := Stop(); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	waitHandedBack(t, before, 48<<20)
}

func TestFreeMemoryHandsMemoryBackToOS(t *testing.T) {
	makeGarbage(64 << 20)
	before := slipcore.ReadMemorySnapshot().RetainedBytes

	FreeMemory()
	waitHandedBack(t, before, 48<<20)
}
