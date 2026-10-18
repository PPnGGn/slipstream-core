package core

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHeapProfileFileName(t *testing.T) {
	ok := map[string]string{
		"":         "heap.pprof",
		"before":   "heap-before.pprof",
		"after_20": "heap-after_20.pprof",
		"S1-2":     "heap-S1-2.pprof",
	}
	for name, want := range ok {
		got, err := heapProfileFileName(name)
		if err != nil || got != want {
			t.Errorf("heapProfileFileName(%q) = %q, %v; want %q", name, got, err, want)
		}
	}

	// The name arrives in an adb broadcast: nothing that could leave the directory.
	for _, name := range []string{"../x", "a/b", `a\b`, "a b", "a.b", "имя"} {
		if got, err := heapProfileFileName(name); err == nil {
			t.Errorf("heapProfileFileName(%q) = %q, want an error", name, got)
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

func TestWriteHeapProfileBadInput(t *testing.T) {
	if _, err := WriteHeapProfile(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
		t.Error("missing directory: want an error")
	}
	if _, err := WriteHeapProfile(t.TempDir(), "../x"); err == nil {
		t.Error("bad name: want an error")
	}
}
