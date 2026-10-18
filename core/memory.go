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
	metricGOGC        = "/gc/gogc:percent"
	metricMemoryLimit = "/gc/gomemlimit:bytes"

	metricGCCPU   = "/cpu/classes/gc/total:cpu-seconds"
	metricUserCPU = "/cpu/classes/user:cpu-seconds"
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
	metricGOGC,
	metricMemoryLimit,
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
	// GCPercent and MemoryLimitBytes are the GC settings in force (GOGC and
	// GOMEMLIMIT; math.MaxInt64 means no limit), see SetGCTuning.
	GCPercent        int64
	MemoryLimitBytes int64
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

		GCPercent:        int64(m[metricGOGC]),
		MemoryLimitBytes: int64(m[metricMemoryLimit]),
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

// CPUTimes is CPU time this Go runtime has spent since the process started:
// GCSeconds by the garbage collector, UserSeconds by everything else Go runs
// (xray, tun2socks). How fast GCSeconds grows against UserSeconds is what GC
// tuning costs. The runtime refreshes both only when a GC cycle ends, so they
// move in steps; compare them over windows of minutes, not seconds.
type CPUTimes struct {
	GCSeconds   float64
	UserSeconds float64
}

func ReadCPUTimes() CPUTimes {
	samples := []metrics.Sample{{Name: metricGCCPU}, {Name: metricUserCPU}}
	metrics.Read(samples)
	read := func(s metrics.Sample) float64 {
		if s.Value.Kind() != metrics.KindFloat64 {
			return 0 // unknown name: see TestMemoryMetricNamesSupported
		}
		return s.Value.Float64()
	}
	return CPUTimes{GCSeconds: read(samples[0]), UserSeconds: read(samples[1])}
}
