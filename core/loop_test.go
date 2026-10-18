package core

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoopRunsUntilStopped(t *testing.T) {
	var calls atomic.Int32
	l := startLoop(5*time.Millisecond, func() { calls.Add(1) })

	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 {
		if time.Now().After(deadline) {
			l.stop()
			t.Fatalf("got %d calls in 1s, want at least 2", calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
	l.stop()

	after := calls.Load()
	time.Sleep(30 * time.Millisecond) // several intervals
	if got := calls.Load(); got != after {
		t.Errorf("%d more calls after stop", got-after)
	}
}

func TestLoopStopLeavesNoGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	l := startLoop(time.Hour, func() {})
	l.stop()

	// The goroutine has closed done but may not have returned yet; give it a moment.
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before start, %d after stop", before, runtime.NumGoroutine())
		}
		time.Sleep(time.Millisecond)
	}
}
