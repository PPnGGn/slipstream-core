package core

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// formatMemoryLine renders a snapshot for the debug log, in MB (1024²) with
// one decimal: enough to see a trend, short enough for a logcat line.
func formatMemoryLine(s MemorySnapshot) string {
	return fmt.Sprintf("mem: retained=%.1fMB total=%.1fMB heap=%.1fMB stacks=%.1fMB goroutines=%d gc=%d",
		mb(s.RetainedBytes), mb(s.TotalBytes), mb(s.HeapInuseBytes), mb(s.StackBytes),
		s.Goroutines, s.GCCycles)
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
	memLog = startLoop(3*time.Second, func() { log.Print(formatMemoryLine(ReadMemorySnapshot())) })
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
