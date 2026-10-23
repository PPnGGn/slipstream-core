package ios

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// exported lists the exported functions and types a package file declares at
// top level. gomobile binds exactly these, one package per platform, so the
// two packages are what the apps see.
func exported(t *testing.T, path string) (funcs, types map[string]bool) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	funcs, types = map[string]bool{}, map[string]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.IsExported() {
				funcs[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
					types[ts.Name.Name] = true
				}
			}
		}
	}
	return funcs, types
}

func onlyIn(a, b map[string]bool) []string {
	var out []string
	for name := range a {
		if !b[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// The two bound packages drifted apart before: iOS had no Version, no
// SetVerboseLogs, no QueryMemory. They may differ, but only where a platform
// really differs, and every such difference is listed here with its reason, so
// a new function on one side either gets its counterpart or an entry.
func TestAndroidAndIosExposeTheSameAPI(t *testing.T) {
	androidFuncs, androidTypes := exported(t, "../android/android.go")
	iosFuncs, iosTypes := exported(t, "ios.go")

	androidOnly := map[string]string{
		// The memory and GC debugging commands are driven by an adb broadcast.
		"SetMemoryLog":       "adb debug command",
		"SetGCTuning":        "adb debug command",
		"WriteHeapProfile":   "adb debug command",
		"WriteGoroutineDump": "adb debug command",
		// The Android app shares the process with the VPN service and is kept
		// out of the tunnel; the iOS app is not and runs in another process.
		"LocalHTTPProxy": "the iOS app is not excluded from the tunnel",
		"LocalProxy":     "the iOS app is not excluded from the tunnel",
	}
	iosOnly := map[string]string{
		"PacketFlow": "iOS has no TUN descriptor, packets come through the flow",
	}

	check := func(kind string, android, ios map[string]bool) {
		for _, name := range onlyIn(android, ios) {
			if _, ok := androidOnly[name]; !ok {
				t.Errorf("%s %s exists in android but not in ios, and is not a listed difference", kind, name)
			}
		}
		for _, name := range onlyIn(ios, android) {
			if _, ok := iosOnly[name]; !ok {
				t.Errorf("%s %s exists in ios but not in android, and is not a listed difference", kind, name)
			}
		}
	}
	check("func", androidFuncs, iosFuncs)
	check("type", androidTypes, iosTypes)
}
