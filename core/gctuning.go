package core

import (
	"fmt"
	"math"
	"runtime/debug"
)

// SetGCTuning sets how far the heap may grow past live data before the next
// GC (percent, GOGC; Go's default is 100, i.e. up to 2× live) and a soft
// memory limit (limitBytes, GOMEMLIMIT; <= 0 means none). Near the limit Go
// collects more often and hands memory back to the OS sooner. Takes effect
// at once.
func SetGCTuning(percent int, limitBytes int64) error {
	if percent <= 0 {
		// A negative GOGC turns the GC off: memory would only grow.
		return fmt.Errorf("gc percent %d: want > 0", percent)
	}
	if limitBytes <= 0 {
		limitBytes = math.MaxInt64
	}
	debug.SetGCPercent(percent)
	debug.SetMemoryLimit(limitBytes)
	return nil
}
