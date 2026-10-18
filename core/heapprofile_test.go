package core

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpFileName(t *testing.T) {
	for _, c := range []struct{ kind, name, ext, want string }{
		{"heap", "", ".pprof", "heap.pprof"},
		{"heap", "before", ".pprof", "heap-before.pprof"},
		{"heap", "after_20", ".pprof", "heap-after_20.pprof"},
		{"heap", "S1-2", ".pprof", "heap-S1-2.pprof"},
		{"goroutines", "switch-5s", ".txt", "goroutines-switch-5s.txt"},
	} {
		got, err := dumpFileName(c.kind, c.name, c.ext)
		if err != nil || got != c.want {
			t.Errorf("dumpFileName(%q, %q, %q) = %q, %v; want %q", c.kind, c.name, c.ext, got, err, c.want)
		}
	}

	// The name arrives in an adb broadcast: nothing that could leave the directory.
	for _, name := range []string{"../x", "a/b", `a\b`, "a b", "a.b", "имя"} {
		if got, err := dumpFileName("heap", name, ".pprof"); err == nil {
			t.Errorf("dumpFileName(heap, %q) = %q, want an error", name, got)
		}
	}
}

func TestWriteHeapProfile(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteHeapProfile(dir, "test")
	if err != nil {
		t.Fatalf("WriteHeapProfile() error: %v", err)
	}
	if want := filepath.Join(dir, "heap-test.pprof"); path != want {
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
	body, err := io.ReadAll(zr)
	if err != nil || len(body) == 0 {
		t.Fatalf("profile body: %d bytes, err %v", len(body), err)
	}
}

// parkForDump signals it is running, then waits — a goroutine the dump must show.
func parkForDump(started chan<- struct{}, release <-chan struct{}) {
	close(started)
	<-release
}

func TestWriteGoroutineDump(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	go parkForDump(started, release)
	defer close(release)
	<-started

	dir := t.TempDir()
	path, err := WriteGoroutineDump(dir, "test")
	if err != nil {
		t.Fatalf("WriteGoroutineDump() error: %v", err)
	}
	if want := filepath.Join(dir, "goroutines-test.txt"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Full stacks: where each goroutine waits and which code started it.
	for _, want := range []string{"core.parkForDump", "created by"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("dump has no %q", want)
		}
	}
}

func TestWriteDumpsBadInput(t *testing.T) {
	for kind, write := range map[string]func(dir, name string) (string, error){
		"heap":       WriteHeapProfile,
		"goroutines": WriteGoroutineDump,
	} {
		if _, err := write(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
			t.Errorf("%s, missing directory: want an error", kind)
		}
		if _, err := write(t.TempDir(), "../x"); err == nil {
			t.Errorf("%s, bad name: want an error", kind)
		}
	}
}
