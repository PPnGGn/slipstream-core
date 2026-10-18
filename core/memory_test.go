package core

import (
	"runtime"
	"runtime/metrics"
	"testing"
)

func TestSnapshotFromMetrics(t *testing.T) {
	got := snapshotFromMetrics(map[string]uint64{
		metricTotal:       100 << 20,
		metricReleased:    30 << 20,
		metricHeapObjects: 40 << 20,
		metricHeapUnused:  5 << 20,
		metricStacks:      2 << 20,
		metricGoroutines:  57,
		metricGCCycles:    12,
		metricGOGC:        50,
		metricMemoryLimit: 80 << 20,
	})
	want := MemorySnapshot{
		TotalBytes:       100 << 20,
		ReleasedBytes:    30 << 20,
		RetainedBytes:    70 << 20, // total - released: what the OS still charges us
		HeapInuseBytes:   45 << 20, // live objects + free slots in their spans
		StackBytes:       2 << 20,
		Goroutines:       57,
		GCCycles:         12,
		GCPercent:        50,
		MemoryLimitBytes: 80 << 20,
	}
	if got != want {
		t.Errorf("snapshotFromMetrics() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestSnapshotFromMetricsMissingTotal(t *testing.T) {
	// A name the runtime does not know reads as 0. Released > total must not
	// wrap around in uint64 and report exabytes retained.
	got := snapshotFromMetrics(map[string]uint64{metricReleased: 30 << 20})
	if got.RetainedBytes != 0 {
		t.Errorf("RetainedBytes = %d, want 0 when total is missing", got.RetainedBytes)
	}
}

func TestMemoryMetricNamesSupported(t *testing.T) {
	// A Go upgrade that renames a metric would otherwise just make a field
	// read 0 in the app, with nothing failing.
	known := map[string]metrics.ValueKind{}
	for _, d := range metrics.All() {
		known[d.Name] = d.Kind
	}
	check := func(names []string, want metrics.ValueKind) {
		for _, name := range names {
			kind, ok := known[name]
			if !ok {
				t.Errorf("%s: unknown to this Go runtime", name)
				continue
			}
			if kind != want {
				t.Errorf("%s: kind %v, want %v", name, kind, want)
			}
		}
	}
	check(memoryMetricNames, metrics.KindUint64)
	check([]string{metricGCCPU, metricUserCPU}, metrics.KindFloat64)
}

func TestReadCPUTimesLive(t *testing.T) {
	// Some Go work, then a GC: the runtime only updates CPU stats when a GC ends.
	sink := 0
	for i := range 50_000_000 {
		sink += i
	}
	_ = sink
	runtime.GC()

	c := ReadCPUTimes()
	if c.GCSeconds <= 0 {
		t.Errorf("GCSeconds = %v, want > 0 after runtime.GC()", c.GCSeconds)
	}
	if c.UserSeconds <= 0 {
		t.Errorf("UserSeconds = %v, want > 0 after a busy loop", c.UserSeconds)
	}
}

func TestReadMemorySnapshotLive(t *testing.T) {
	runtime.GC() // so GCCycles is at least 1
	s := ReadMemorySnapshot()

	if s.TotalBytes <= 0 {
		t.Errorf("TotalBytes = %d, want > 0", s.TotalBytes)
	}
	if s.RetainedBytes <= 0 || s.RetainedBytes > s.TotalBytes {
		t.Errorf("RetainedBytes = %d, want in (0, TotalBytes=%d]", s.RetainedBytes, s.TotalBytes)
	}
	if s.HeapInuseBytes <= 0 {
		t.Errorf("HeapInuseBytes = %d, want > 0", s.HeapInuseBytes)
	}
	if s.StackBytes <= 0 {
		t.Errorf("StackBytes = %d, want > 0", s.StackBytes)
	}
	if s.Goroutines < 1 {
		t.Errorf("Goroutines = %d, want >= 1 (this test runs in one)", s.Goroutines)
	}
	if s.GCCycles < 1 {
		t.Errorf("GCCycles = %d, want >= 1 after runtime.GC()", s.GCCycles)
	}
}
