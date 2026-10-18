package core

import (
	"math"
	"strings"
	"testing"
)

func TestFormatMemoryLine(t *testing.T) {
	got := formatMemoryLine(MemorySnapshot{
		TotalBytes:     100 << 20,
		RetainedBytes:  70 << 20,
		HeapInuseBytes: 45 << 20,
		StackBytes:     3 << 19, // 1.5 MB
		Goroutines:     57,
		GCCycles:       12,
		GCPercent:        100,
		MemoryLimitBytes: math.MaxInt64, // Go's "no limit"
	}, CPUTimes{GCSeconds: 1.25, UserSeconds: 30.5})
	want := "mem: retained=70.0MB total=100.0MB heap=45.0MB stacks=1.5MB goroutines=57 gc=12" +
		" cpu=30.5s gccpu=1.25s gogc=100 limit=none"
	if got != want {
		t.Errorf("formatMemoryLine() =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatMemoryLineWithLimit(t *testing.T) {
	got := formatMemoryLine(MemorySnapshot{GCPercent: 50, MemoryLimitBytes: 80 << 20}, CPUTimes{})
	if want := " gogc=50 limit=80MB"; !strings.HasSuffix(got, want) {
		t.Errorf("formatMemoryLine() = %q, want suffix %q", got, want)
	}
}

func TestSetMemoryLog(t *testing.T) {
	t.Cleanup(func() {
		SetMemoryLog(false)
		SetVerboseLogs(false)
	})
	SetVerboseLogs(true) // debug build

	SetMemoryLog(true)
	if !MemoryLogRunning() {
		t.Fatal("SetMemoryLog(true): log not running")
	}
	SetMemoryLog(false)
	if MemoryLogRunning() {
		t.Error("SetMemoryLog(false): log still running")
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
