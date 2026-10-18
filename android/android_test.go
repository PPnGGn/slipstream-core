package android

import (
	"math"
	"runtime"
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

func TestLoadingTunesGC(t *testing.T) {
	s := slipcore.ReadMemorySnapshot()
	if s.GCPercent != 25 {
		t.Errorf("gogc = %d after the package loaded, want 25", s.GCPercent)
	}
	if s.MemoryLimitBytes != math.MaxInt64 {
		t.Errorf("memory limit = %d, want none", s.MemoryLimitBytes)
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
