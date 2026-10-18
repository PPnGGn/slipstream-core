package core

import (
	"sync"
	"time"
)

var (
	memReturnMu sync.Mutex
	memReturn   *loop
)

// startMemoryReturn runs FreeMemory every interval.
func startMemoryReturn(interval time.Duration) *loop {
	return startLoop(interval, FreeMemory)
}

// StartMemoryReturn runs a GC and hands free memory back to the OS every
// 2 minutes until StopMemoryReturn. Without it, what a stopped session leaves
// behind waits for the next GC — and after a switch from a heavy config the
// GC threshold stays high, so on a light one that GC may not come for
// minutes while the monitor shows the old level. Cost measured on a phone:
// one GC per 2 minutes, about 0.1% of the core's CPU.
func StartMemoryReturn() {
	memReturnMu.Lock()
	defer memReturnMu.Unlock()
	if memReturn != nil {
		return
	}
	memReturn = startMemoryReturn(2 * time.Minute)
}

// StopMemoryReturn stops the loop; safe to call when none is running.
func StopMemoryReturn() {
	memReturnMu.Lock()
	defer memReturnMu.Unlock()
	if memReturn == nil {
		return
	}
	memReturn.stop()
	memReturn = nil
}
