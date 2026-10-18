package core

import "runtime/metrics"

// runtime/metrics names this file reads; `go doc runtime/metrics` lists them all.
const (
	metricTotal       = "/memory/classes/total:bytes"
	metricReleased    = "/memory/classes/heap/released:bytes"
	metricHeapObjects = "/memory/classes/heap/objects:bytes"
	metricHeapUnused  = "/memory/classes/heap/unused:bytes"
	metricStacks      = "/memory/classes/heap/stacks:bytes"
	metricGoroutines  = "/sched/goroutines:goroutines"
	metricGCCycles    = "/gc/cycles/total:gc-cycles"
)

// memoryMetricNames is every metric snapshotFromMetrics reads.
var memoryMetricNames = []string{
	metricTotal,
	metricReleased,
	metricHeapObjects,
	metricHeapUnused,
	metricStacks,
	metricGoroutines,
	metricGCCycles,
}

// MemorySnapshot is one reading of this Go runtime's memory: xray-core and
// tun2socks, without the host process around them (Flutter/Skia on Android).
type MemorySnapshot struct {
	// TotalBytes is everything the runtime has mapped (≈ MemStats.Sys). It
	// still counts pages already handed back to the OS, so it rarely drops.
	TotalBytes int64
	// ReleasedBytes is the part of TotalBytes returned to the OS.
	ReleasedBytes int64
	// RetainedBytes = TotalBytes - ReleasedBytes: what the OS still charges
	// the process for Go. Rises and falls with real use.
	RetainedBytes int64
	// HeapInuseBytes is heap spans holding objects (≈ MemStats.HeapInuse).
	HeapInuseBytes int64
	// StackBytes is goroutine stacks (≈ MemStats.StackInuse).
	StackBytes int64
	Goroutines int64
	// GCCycles counts finished GC cycles since the process started.
	GCCycles int64
}

// snapshotFromMetrics turns metric name -> value into a snapshot. Pure, so
// tests can feed it any numbers; a missing name reads as 0.
func snapshotFromMetrics(m map[string]uint64) MemorySnapshot {
	total := m[metricTotal]
	released := m[metricReleased]
	var retained uint64
	if total > released {
		retained = total - released
	}
	return MemorySnapshot{
		TotalBytes:     int64(total),
		ReleasedBytes:  int64(released),
		RetainedBytes:  int64(retained),
		HeapInuseBytes: int64(m[metricHeapObjects] + m[metricHeapUnused]),
		StackBytes:     int64(m[metricStacks]),
		Goroutines:     int64(m[metricGoroutines]),
		GCCycles:       int64(m[metricGCCycles]),
	}
}

// ReadMemorySnapshot reads the live runtime metrics. Unlike
// runtime.ReadMemStats it does not stop the world, so it is cheap enough to
// call several times a second without stalling traffic.
func ReadMemorySnapshot() MemorySnapshot {
	samples := make([]metrics.Sample, len(memoryMetricNames))
	for i, name := range memoryMetricNames {
		samples[i].Name = name
	}
	metrics.Read(samples)

	values := make(map[string]uint64, len(samples))
	for _, s := range samples {
		// KindBad: this Go does not know the name. Leaving it out makes the
		// field read 0 instead of panicking in Uint64().
		if s.Value.Kind() == metrics.KindUint64 {
			values[s.Name] = s.Value.Uint64()
		}
	}
	return snapshotFromMetrics(values)
}
