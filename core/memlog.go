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

// memoryLog emits a memory line every interval until stop.
type memoryLog struct {
	quit chan struct{} // closed by stop: tells the loop to return
	done chan struct{} // closed by the loop on its way out
}

func startMemoryLog(interval time.Duration, emit func(string)) *memoryLog {
	l := &memoryLog{quit: make(chan struct{}), done: make(chan struct{})}
	go l.run(interval, emit)
	return l
}

func (l *memoryLog) run(interval time.Duration, emit func(string)) {
	defer close(l.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			emit(formatMemoryLine(ReadMemorySnapshot()))
		case <-l.quit:
			return
		}
	}
}

// stop ends the loop and waits until it has, so no line is emitted after
// stop returns. Call it once: closing quit twice panics.
func (l *memoryLog) stop() {
	close(l.quit)
	<-l.done
}

var (
	memLogMu sync.Mutex
	memLog   *memoryLog
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
	memLog = startMemoryLog(3*time.Second, func(line string) { log.Print(line) })
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
