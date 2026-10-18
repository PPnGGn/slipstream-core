package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// dumpFileName maps the name from the dump command to a file name:
// ("heap", "", ".pprof") → heap.pprof, ("heap", "before", ".pprof") →
// heap-before.pprof. The name comes from an adb broadcast, so only ASCII
// letters, digits, '-' and '_' pass — nothing that could point outside the
// directory.
func dumpFileName(kind, name, ext string) (string, error) {
	if name == "" {
		return kind + ext, nil
	}
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return "", fmt.Errorf("dump name %q: only letters, digits, - and _", name)
		}
	}
	return kind + "-" + name + ext, nil
}

// writeDump creates dir/<kind>[-name]<ext>, fills it with write and returns
// the file's path.
func writeDump(dir, kind, name, ext string, write func(io.Writer) error) (string, error) {
	file, err := dumpFileName(kind, name, ext)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, file)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := write(f); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// WriteHeapProfile writes the heap profile into dir and returns the file's
// path; read it with `go tool pprof -top`. Runs a GC first: the profile shows
// the state as of the last finished GC, and under light load that can be
// minutes old.
func WriteHeapProfile(dir, name string) (string, error) {
	return writeDump(dir, "heap", name, ".pprof", func(w io.Writer) error {
		runtime.GC()
		return pprof.WriteHeapProfile(w)
	})
}

// WriteGoroutineDump writes every goroutine's full stack — where it waits and
// the line that started it ("created by") — into dir as text and returns the
// file's path. Shows what a session leaves running after Stop.
func WriteGoroutineDump(dir, name string) (string, error) {
	return writeDump(dir, "goroutines", name, ".txt", func(w io.Writer) error {
		return pprof.Lookup("goroutine").WriteTo(w, 2)
	})
}
