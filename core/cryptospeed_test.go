package core

import (
	"testing"
	"time"
)

// Software AES-GCM runs at tens of MB/s (about 40 on an iPhone 14), the
// instructions of the processor at several GB/s. This is the check that tells
// the two apart; 200 MB/s keeps a wide margin on both sides, also on a loaded
// machine or under the race detector.
//
// It fails where Go cannot tell that the processor has the instructions. That
// is iOS with the Go of the module cache, which is why the framework is built
// with the copy of the toolchain from package toolchain (see the Makefile): run
// this test on a device through MeasureAESGCM of the ios package, not here.
func TestAESGCMRunsOnHardware(t *testing.T) {
	if got := MeasureAESGCM(200 * time.Millisecond); got < 200 {
		t.Fatalf("AES-GCM runs at %.0f MB/s: Go uses its software implementation on this machine", got)
	}
}

func TestMeasureAESGCMTakesTheTimeItIsGiven(t *testing.T) {
	start := time.Now()
	MeasureAESGCM(100 * time.Millisecond)
	if took := time.Since(start); took < 100*time.Millisecond || took > time.Second {
		t.Errorf("took %v for 100 ms", took)
	}
}
