package core

import (
	"errors"
	"fmt"
	"io"
	"runtime/pprof"
	"sync/atomic"
	"time"
)

// maxCPUProfileSeconds bounds one profile: the call blocks for that long and
// the sampling costs a little CPU of its own.
const maxCPUProfileSeconds = 120

var (
	// cpuProfileBusy is true while a profile is being taken. Go samples one at
	// a time; checking here keeps a second request from creating (and emptying)
	// the file the first one is filling.
	cpuProfileBusy    atomic.Bool
	errCPUProfileBusy = errors.New("a CPU profile is already being taken")
)

// WriteCPUProfile samples where the CPU time of the process goes for the given
// number of seconds, writes the profile into dir as cpu[-name].pprof and
// returns the file's path. It blocks for the whole time, so the caller must not
// be a thread that cannot wait. Read it with `go tool pprof -top`; run the
// traffic that is being measured while it samples.
func WriteCPUProfile(dir, name string, seconds int) (string, error) {
	if seconds < 1 || seconds > maxCPUProfileSeconds {
		return "", fmt.Errorf("cpu profile: %d seconds, want 1 to %d", seconds, maxCPUProfileSeconds)
	}
	if !cpuProfileBusy.CompareAndSwap(false, true) {
		return "", errCPUProfileBusy
	}
	defer cpuProfileBusy.Store(false)

	return writeDump(dir, "cpu", name, ".pprof", func(w io.Writer) error {
		if err := pprof.StartCPUProfile(w); err != nil {
			return err
		}
		time.Sleep(time.Duration(seconds) * time.Second)
		pprof.StopCPUProfile()
		return nil
	})
}
