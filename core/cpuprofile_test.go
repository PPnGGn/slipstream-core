package core

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCPUProfile(t *testing.T) {
	dir := t.TempDir()

	path, err := WriteCPUProfile(dir, "test", 1)
	if err != nil {
		t.Fatalf("WriteCPUProfile() error: %v", err)
	}
	if want := filepath.Join(dir, "cpu-test.pprof"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	// A pprof profile is a gzip-compressed protobuf.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("profile is not gzip: %v", err)
	}
	if body, err := io.ReadAll(zr); err != nil || len(body) == 0 {
		t.Fatalf("profile body: %d bytes, err %v", len(body), err)
	}
}

// The profile is for a measurement of seconds, not a recording that runs
// until someone stops it.
func TestWriteCPUProfileRefusesBadInput(t *testing.T) {
	for _, seconds := range []int{0, -1, maxCPUProfileSeconds + 1} {
		if _, err := WriteCPUProfile(t.TempDir(), "x", seconds); err == nil {
			t.Errorf("seconds = %d: want an error", seconds)
		}
	}
	if _, err := WriteCPUProfile(filepath.Join(t.TempDir(), "missing"), "x", 1); err == nil {
		t.Error("missing directory: want an error")
	}
	if _, err := WriteCPUProfile(t.TempDir(), "../x", 1); err == nil {
		t.Error("bad name: want an error")
	}
}

// Go samples one profile at a time. A second request must be refused before it
// touches a file: with the same name it would empty the file the first is filling.
func TestWriteCPUProfileOneAtATime(t *testing.T) {
	dir := t.TempDir()

	// A profile is running: its place is taken.
	if !cpuProfileBusy.CompareAndSwap(false, true) {
		t.Fatal("a profile was already running before the test")
	}
	path, err := WriteCPUProfile(dir, "same", 1)
	if !errors.Is(err, errCPUProfileBusy) {
		t.Errorf("WriteCPUProfile() during a profile = %q, %v; want errCPUProfileBusy", path, err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the refused request left %d files", len(left))
	}

	// Free again once the profile is done.
	cpuProfileBusy.Store(false)
	if _, err := WriteCPUProfile(dir, "after", 1); err != nil {
		t.Errorf("profile after the first finished: %v", err)
	}
}
