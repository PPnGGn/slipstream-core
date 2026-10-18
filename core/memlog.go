package core

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// formatMemoryLine renders a snapshot for the debug log, in MB (1024²) with
// one decimal: enough to see a trend, short enough for a logcat line. The CPU
// times are cumulative; their growth between two lines is what counts.
func formatMemoryLine(s MemorySnapshot, c CPUTimes) string {
	limit := "none"
	if s.MemoryLimitBytes != math.MaxInt64 {
		limit = fmt.Sprintf("%.0fMB", mb(s.MemoryLimitBytes))
	}
	return fmt.Sprintf("mem: retained=%.1fMB total=%.1fMB heap=%.1fMB stacks=%.1fMB goroutines=%d gc=%d"+
		" cpu=%.1fs gccpu=%.2fs gogc=%d limit=%s",
		mb(s.RetainedBytes), mb(s.TotalBytes), mb(s.HeapInuseBytes), mb(s.StackBytes),
		s.Goroutines, s.GCCycles, c.UserSeconds, c.GCSeconds, s.GCPercent, limit)
}

func mb(bytes int64) float64 { return float64(bytes) / (1 << 20) }

var (
	memLogMu sync.Mutex
	memLog   *loop
)

// StartMemoryLog writes a memory line every few seconds until StopMemoryLog.
// Debug builds only: a no-op unless SetVerboseLogs(true).
//
// It goes to Go's log package, which gomobile points at logcat (tag GoLog),
// and not to the app's log stream: that one drops lines while no Flutter UI
// is attached — exactly when the app sits in the background during a
// measurement.
func StartMemoryLog() {
	memLogMu.Lock()
	defer memLogMu.Unlock()
	if memLog != nil || !verboseLogs.Load() {
		return
	}
	memLog = startLoop(3*time.Second, func() {
		log.Print(formatMemoryLine(ReadMemorySnapshot(), ReadCPUTimes()))
	})
}

// SetMemoryLog turns the memory log on or off. It is off by default — a line
// every 3 s floods logcat — and not tied to a session, so once on it keeps
// logging across server switches. Turning it on is a no-op unless
// SetVerboseLogs(true) (debug builds).
func SetMemoryLog(on bool) {
	if on {
		StartMemoryLog()
	} else {
		StopMemoryLog()
	}
}

// MemoryLogRunning reports whether the memory log is on.
func MemoryLogRunning() bool {
	memLogMu.Lock()
	defer memLogMu.Unlock()
	return memLog != nil
}

// StopMemoryLog stops the log; safe to call when none is running.
func StopMemoryLog() {
	memLogMu.Lock()
	defer memLogMu.Unlock()
	if memLog == nil {
		return
	}
	memLog.stop()
	memLog = nil
}
