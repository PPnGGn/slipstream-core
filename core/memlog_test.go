package core

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestFormatMemoryLine(t *testing.T) {
	got := formatMemoryLine(MemorySnapshot{
		TotalBytes:     100 << 20,
		RetainedBytes:  70 << 20,
		HeapInuseBytes: 45 << 20,
		StackBytes:     3 << 19, // 1.5 MB
		Goroutines:     57,
		GCCycles:       12,
	})
	want := "mem: retained=70.0MB total=100.0MB heap=45.0MB stacks=1.5MB goroutines=57 gc=12"
	if got != want {
		t.Errorf("formatMemoryLine() =\n%q\nwant\n%q", got, want)
	}
}

func TestMemoryLogEmitsUntilStopped(t *testing.T) {
	var lines atomic.Int32
	l := startMemoryLog(5*time.Millisecond, func(string) { lines.Add(1) })

	deadline := time.Now().Add(time.Second)
	for lines.Load() < 2 {
		if time.Now().After(deadline) {
			l.stop()
			t.Fatalf("got %d lines in 1s, want at least 2", lines.Load())
		}
		time.Sleep(time.Millisecond)
	}
	l.stop()

	after := lines.Load()
	time.Sleep(30 * time.Millisecond) // several intervals
	if got := lines.Load(); got != after {
		t.Errorf("%d more lines after stop", got-after)
	}
}

func TestStartMemoryLogOnlyInDebugBuilds(t *testing.T) {
	t.Cleanup(func() {
		StopMemoryLog()
		SetVerboseLogs(false)
	})

	SetVerboseLogs(false) // release build
	StartMemoryLog()
	if memLog != nil {
		t.Fatal("release build: memory log started")
	}

	SetVerboseLogs(true) // debug build
	StartMemoryLog()
	first := memLog
	if first == nil {
		t.Fatal("debug build: memory log not started")
	}
	StartMemoryLog()
	if memLog != first {
		t.Error("second StartMemoryLog replaced the running loop")
	}

	StopMemoryLog()
	if memLog != nil {
		t.Error("memory log still set after StopMemoryLog")
	}
	StopMemoryLog() // nothing running: must not panic
}

func TestMemoryLogStopLeavesNoGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	l := startMemoryLog(time.Hour, func(string) {})
	l.stop()

	// The loop has closed done but may not have returned yet; give it a moment.
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before start, %d after stop", before, runtime.NumGoroutine())
		}
		time.Sleep(time.Millisecond)
	}
}
