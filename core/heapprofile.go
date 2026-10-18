package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// heapProfileFileName maps the name from the dump command to a file name:
// "" → heap.pprof, "before" → heap-before.pprof. The name comes from an adb
// broadcast, so only ASCII letters, digits, '-' and '_' pass — nothing that
// could point outside the directory.
func heapProfileFileName(name string) (string, error) {
	if name == "" {
		return "heap.pprof", nil
	}
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return "", fmt.Errorf("heap profile name %q: only letters, digits, - and _", name)
		}
	}
	return "heap-" + name + ".pprof", nil
}

// WriteHeapProfile writes the heap profile into dir and returns the file's
// path; read it with `go tool pprof -top`. Runs a GC first: the profile shows
// the state as of the last finished GC, and under light load that can be
// minutes old.
func WriteHeapProfile(dir, name string) (string, error) {
	file, err := heapProfileFileName(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, file)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}
