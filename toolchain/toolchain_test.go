package toolchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The tests count the hash on their own, not with the code they test.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// installedRoot is the GOROOT of the toolchain that runs the tests.
func installedRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func installedOriginal(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(installedRoot(t), RelPath))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func buildLine(t *testing.T, src []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "//go:build ") {
			return line
		}
	}
	t.Fatalf("no //go:build line in:\n%s", src)
	return ""
}

func installedTransport(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(installedRoot(t), ScratchRelPath))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeRoot is a small GOROOT: a VERSION file, a program in bin, the two files that
// get changed and a few others, one of them a symbolic link, all read-only
// the way the Go module cache keeps a toolchain. The file of the HTTP/2 client
// is the one of the installed Go.
func fakeRoot(t *testing.T, version string, original []byte) string {
	t.Helper()
	return fakeRootWith(t, version, original, installedTransport(t))
}

func fakeRootWith(t *testing.T, version string, original, transport []byte) string {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"VERSION":               []byte(version + "\ntime 2026-10-19T22:20:36Z\n"),
		"bin/go":                []byte("#!/bin/sh\necho fake\n"),
		RelPath:                 original,
		ScratchRelPath:          transport,
		"src/internal/cpu/a.go": []byte("package cpu\n"),
		"src/other/file.go":     []byte("package other\n"),
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o444)
		if rel == "bin/go" {
			mode = 0o555
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../src/other/file.go", filepath.Join(root, "lib", "link.go")); err != nil {
		t.Fatal(err)
	}
	// What the module cache does: nothing in it can be written.
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	for _, dir := range []string{"bin", "lib", "src/internal/cpu", "src/internal", "src/other",
		"src/net/http/internal/http2", "src/net/http/internal", "src/net/http", "src/net", "src"} {
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(dir)), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The pin says which file the replacement was written against. When Go changes
// the file (it may one day detect the extensions on iOS itself) this fails and
// someone has to look: is the replacement still needed, is it still right.
func TestReplacementIsWrittenForTheFileGoShips(t *testing.T) {
	got := sha256Hex(installedOriginal(t))
	if got != PinnedSHA256() {
		t.Fatalf("Go ships another %s now (sha256 %s, pinned %s): check whether the replacement "+
			"in toolchain/ is still needed and still right, then pin the new hash", RelPath, got, PinnedSHA256())
	}
}

// The file it takes the place of is for more than iOS; the replacement must be
// chosen by the same build constraint, or another platform would get it too.
func TestReplacementKeepsTheBuildConstraint(t *testing.T) {
	want := buildLine(t, installedOriginal(t))
	if got := buildLine(t, Replacement()); got != want {
		t.Fatalf("constraint of the replacement %q, of the file it replaces %q", got, want)
	}
}

// What it does: says the four extensions are there, as Go does for macOS, and
// nothing else.
func TestReplacementTurnsOnTheCryptoFlagsAndNothingElse(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "cpu_arm64_other.go", Replacement(), parser.ParseComments)
	if err != nil {
		t.Fatalf("the replacement does not parse: %v", err)
	}
	if f.Name.Name != "cpu" {
		t.Fatalf("package %q, want cpu", f.Name.Name)
	}
	var osInit *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "osInit" && fn.Recv == nil {
			osInit = fn
		}
	}
	if osInit == nil {
		t.Fatal("no func osInit")
	}
	var set []string
	for _, stmt := range osInit.Body.List {
		as, ok := stmt.(*ast.AssignStmt)
		if !ok || as.Tok != token.ASSIGN || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			t.Fatalf("osInit may only assign, found %T", stmt)
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok {
			t.Fatalf("osInit assigns to %T, want ARM64.<flag>", as.Lhs[0])
		}
		if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "ARM64" {
			t.Fatalf("osInit assigns to something that is not ARM64.<flag>")
		}
		if v, ok := as.Rhs[0].(*ast.Ident); !ok || v.Name != "true" {
			t.Fatalf("ARM64.%s is not set to true", sel.Sel.Name)
		}
		set = append(set, sel.Sel.Name)
	}
	sort.Strings(set)
	want := []string{"HasAES", "HasPMULL", "HasSHA1", "HasSHA2"}
	if strings.Join(set, ",") != strings.Join(want, ",") {
		t.Fatalf("osInit sets %v, want %v", set, want)
	}
}

func TestPrepareCopiesTheTreeAndReplacesOnlyThatFile(t *testing.T) {
	orig := installedOriginal(t)
	src := fakeRoot(t, "go1.27.3", orig)
	dest := filepath.Join(t.TempDir(), "go")

	created, err := Prepare(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("created = false for a destination that did not exist")
	}

	if got := read(t, filepath.Join(dest, RelPath)); !bytes.Equal(got, Replacement()) {
		t.Errorf("%s is not the replacement", RelPath)
	}
	for _, rel := range []string{"VERSION", "bin/go", "src/internal/cpu/a.go", "src/other/file.go"} {
		p := filepath.FromSlash(rel)
		if !bytes.Equal(read(t, filepath.Join(dest, p)), read(t, filepath.Join(src, p))) {
			t.Errorf("%s differs from the source", rel)
		}
	}
	if link, err := os.Readlink(filepath.Join(dest, "lib", "link.go")); err != nil || link != "../src/other/file.go" {
		t.Errorf("the symbolic link was not kept: %q, %v", link, err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "go"))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("bin/go is not executable: %v, %v", info, err)
	}
	// The source is somebody else's (the module cache): not a byte of it changes.
	if got := sha256Hex(read(t, filepath.Join(src, RelPath))); got != sha256Hex(orig) {
		t.Error("the source file was changed")
	}
}

// The copy is ours to change and to delete, whatever the source's modes were.
func TestPrepareGivesFilesThatCanBeWritten(t *testing.T) {
	src := fakeRoot(t, "go1.27.3", installedOriginal(t))
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(src, dest); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{RelPath, "src/other/file.go", "VERSION"} {
		p := filepath.Join(dest, filepath.FromSlash(rel))
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Errorf("%s cannot be written: %v", rel, err)
			continue
		}
		f.Close()
	}
	if err := os.WriteFile(filepath.Join(dest, "src", "other", "new.go"), nil, 0o644); err != nil {
		t.Errorf("a directory of the copy cannot be written: %v", err)
	}
}

func TestPrepareRefusesAFileThatIsNotTheOneItWasWrittenFor(t *testing.T) {
	changed := append(installedOriginal(t), []byte("\n// Go changed this file\n")...)
	src := fakeRoot(t, "go1.27.3", changed)
	dest := filepath.Join(t.TempDir(), "go")

	created, err := Prepare(src, dest)
	if err == nil {
		t.Fatal("no error for a file that is not the pinned one")
	}
	if created {
		t.Error("created = true with an error")
	}
	if !strings.Contains(err.Error(), "changed") || !strings.Contains(err.Error(), RelPath) {
		t.Errorf("the error does not say what changed: %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a destination was left behind")
	}
}

func TestPrepareDoesNotCopyAgainWhenNothingChanged(t *testing.T) {
	src := fakeRoot(t, "go1.27.3", installedOriginal(t))
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(src, dest); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "marker")
	if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Prepare(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("created = true for a copy that is up to date")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the copy was made again, the marker is gone")
	}
}

func TestPrepareCopiesAgainForAnotherGoVersion(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(fakeRoot(t, "go1.27.3", installedOriginal(t)), dest); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "marker")
	if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Prepare(fakeRoot(t, "go1.27.4", installedOriginal(t)), dest)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false for a new version of Go")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the old copy was kept")
	}
	if got := strings.SplitN(string(read(t, filepath.Join(dest, "VERSION"))), "\n", 2)[0]; got != "go1.27.4" {
		t.Errorf("VERSION of the copy is %q", got)
	}
}

// The real thing, from the toolchain that runs the tests: the copy is a Go that
// reports the same version, and for iOS, and only for iOS, it picks the file
// that says the extensions are there. Copies a few hundred MB, so not in -short.
func TestPatchedToolchainPicksTheReplacementForIOSOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the whole toolchain")
	}
	src := installedRoot(t)
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(src, dest); err != nil {
		t.Fatal(err)
	}

	run := func(goos, goarch string, args ...string) string {
		t.Helper()
		cmd := exec.Command(filepath.Join(dest, "bin", "go"), args...)
		cmd.Env = append(os.Environ(), "GOROOT="+dest, "GOTOOLCHAIN=local", "GOFLAGS=",
			"GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s (%s/%s): %v\n%s", strings.Join(args, " "), goos, goarch, err, out)
		}
		return string(out)
	}

	want := strings.SplitN(string(read(t, filepath.Join(src, "VERSION"))), "\n", 2)[0]
	if got := run("darwin", "arm64", "version"); !strings.Contains(got, want) {
		t.Fatalf("go version says %q, the source toolchain is %s", got, want)
	}
	if got := run("ios", "arm64", "list", "-f", "{{.GoFiles}}", "internal/cpu"); !strings.Contains(got, "cpu_arm64_other.go") {
		t.Errorf("for ios/arm64 internal/cpu has %s, want cpu_arm64_other.go among them", got)
	}
	if got := run("darwin", "arm64", "list", "-f", "{{.GoFiles}}", "internal/cpu"); strings.Contains(got, "cpu_arm64_other.go") {
		t.Errorf("for darwin/arm64 internal/cpu has %s: the replacement would reach macOS", got)
	}
	// The replaced file compiles in its package.
	run("ios", "arm64", "build", "internal/cpu")

	if got := sha256Hex(read(t, filepath.Join(src, RelPath))); got != PinnedSHA256() {
		t.Error("the installed toolchain was changed")
	}
}
