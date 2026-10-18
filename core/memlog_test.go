package core

import "testing"

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
