package core

import (
	"runtime"
	"testing"
	"time"
)

// leaveGarbage leaves n bytes the runtime took from the OS and nobody uses.
func leaveGarbage(n int) {
	b := make([]byte, n)
	runtime.KeepAlive(b)
}

func TestMemoryReturnLoopHandsMemoryBack(t *testing.T) {
	l := startMemoryReturn(20 * time.Millisecond)
	defer l.stop()

	leaveGarbage(64 << 20)
	before := ReadMemorySnapshot().RetainedBytes

	// No explicit FreeMemory call: the loop must do it on its own.
	deadline := time.Now().Add(2 * time.Second)
	for ReadMemorySnapshot().RetainedBytes > before-48<<20 {
		if time.Now().After(deadline) {
			t.Fatalf("retained %d MB before, %d MB 2 s later; want 48+ MB handed back",
				before>>20, ReadMemorySnapshot().RetainedBytes>>20)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartMemoryReturnRunsOneLoop(t *testing.T) {
	t.Cleanup(StopMemoryReturn)

	StartMemoryReturn()
	first := memReturn
	if first == nil {
		t.Fatal("memory return loop not started")
	}
	StartMemoryReturn()
	if memReturn != first {
		t.Error("second StartMemoryReturn replaced the running loop")
	}

	StopMemoryReturn()
	if memReturn != nil {
		t.Error("memory return loop still set after StopMemoryReturn")
	}
	StopMemoryReturn() // nothing running: must not panic
}
