package core

import (
	"math"
	"runtime/debug"
	"testing"
)

func TestSetGCTuning(t *testing.T) {
	t.Cleanup(func() {
		debug.SetGCPercent(100)
		debug.SetMemoryLimit(math.MaxInt64)
	})

	if err := SetGCTuning(50, 80<<20); err != nil {
		t.Fatalf("SetGCTuning(50, 80 MB) error: %v", err)
	}
	s := ReadMemorySnapshot()
	if s.GCPercent != 50 || s.MemoryLimitBytes != 80<<20 {
		t.Errorf("after SetGCTuning(50, 80 MB): gogc=%d limit=%d", s.GCPercent, s.MemoryLimitBytes)
	}

	if err := SetGCTuning(100, 0); err != nil { // 0: no limit
		t.Fatalf("SetGCTuning(100, 0) error: %v", err)
	}
	s = ReadMemorySnapshot()
	if s.GCPercent != 100 || s.MemoryLimitBytes != math.MaxInt64 {
		t.Errorf("after SetGCTuning(100, 0): gogc=%d limit=%d, want 100 and no limit", s.GCPercent, s.MemoryLimitBytes)
	}
}

func TestSetGCTuningRejectsGCOff(t *testing.T) {
	t.Cleanup(func() { debug.SetGCPercent(100) })

	// A negative percent turns the GC off: memory would only grow.
	for _, percent := range []int{0, -1} {
		if err := SetGCTuning(percent, 0); err == nil {
			t.Errorf("SetGCTuning(%d, 0) = nil, want an error", percent)
		}
	}
	if got := ReadMemorySnapshot().GCPercent; got != 100 {
		t.Errorf("gogc = %d after rejected calls, want 100 untouched", got)
	}
}
