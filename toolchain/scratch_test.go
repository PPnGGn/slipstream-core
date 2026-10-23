package toolchain

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The second change: the HTTP/2 client of Go keeps a buffer for every stream
// that has a request body, min(the frame size of the server, 512 KB). On iOS,
// with XHTTP, that was 13 MB of the live heap of a network extension that is
// killed at 50 MB. The copy of the toolchain makes it 16 KB, the frame size every
// peer accepts.

func TestScratchCapIsWrittenForTheFileGoShips(t *testing.T) {
	got := sha256Hex(installedTransport(t))
	if got != ScratchPinnedSHA256() {
		t.Fatalf("Go ships another %s now (sha256 %s, pinned %s): see whether the buffer of the "+
			"streams is still 512 KB and the line is still there, then pin the new hash",
			ScratchRelPath, got, ScratchPinnedSHA256())
	}
}

func TestScratchCapChangesOneLineOfTheFile(t *testing.T) {
	orig := installedTransport(t)
	got, err := ApplyScratchCap(orig)
	if err != nil {
		t.Fatal(err)
	}

	was, now := strings.Split(string(orig), "\n"), strings.Split(string(got), "\n")
	if len(was) != len(now) {
		t.Fatalf("the file has %d lines, was %d: a line was added or removed", len(now), len(was))
	}
	var changed []string
	for i := range was {
		if was[i] != now[i] {
			changed = append(changed, fmt.Sprintf("line %d: %q -> %q", i+1, was[i], now[i]))
		}
	}
	if len(changed) != 1 {
		t.Fatalf("%d lines changed, want 1:\n%s", len(changed), strings.Join(changed, "\n"))
	}
	if !strings.Contains(changed[0], "const max = 16 << 10") {
		t.Errorf("the changed line is not the new constant: %s", changed[0])
	}
}

// The changed file is Go, and the constant is where the function that sizes the
// buffer reads it.
func TestScratchCapSetsTheConstantOfTheFunctionThatSizesTheBuffer(t *testing.T) {
	got, err := ApplyScratchCap(installedTransport(t))
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "transport.go", got, 0)
	if err != nil {
		t.Fatalf("the changed file does not parse: %v", err)
	}
	var value string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "frameScratchBufferLen" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if ok && len(spec.Names) == 1 && spec.Names[0].Name == "max" && len(spec.Values) == 1 {
				if be, ok := spec.Values[0].(*ast.BinaryExpr); ok {
					value = fmt.Sprintf("%s %s %s", be.X.(*ast.BasicLit).Value, be.Op, be.Y.(*ast.BasicLit).Value)
				}
			}
			return true
		})
		return false
	})
	if value != "16 << 10" {
		t.Errorf("max in frameScratchBufferLen is %q, want 16 << 10", value)
	}
}

func TestScratchCapRefusesAFileWithoutTheLineOrWithItTwice(t *testing.T) {
	line := []byte("const max = 512 << 10")
	orig := installedTransport(t)
	if !bytes.Contains(orig, line) {
		t.Fatal("the installed file does not have the line; the pin test says why")
	}
	if _, err := ApplyScratchCap(bytes.ReplaceAll(orig, line, []byte("const max = 256 << 10"))); err == nil {
		t.Error("no error for a file without the line")
	}
	twice := append(append([]byte(nil), orig...), []byte("\nfunc other() {\n\tconst max = 512 << 10\n}\n")...)
	if _, err := ApplyScratchCap(twice); err == nil {
		t.Error("no error for a file with the line twice")
	}
}

func TestPrepareChangesBothFilesAndNothingElse(t *testing.T) {
	src := fakeRoot(t, "go1.27.3", installedOriginal(t))
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(src, dest); err != nil {
		t.Fatal(err)
	}

	want, err := ApplyScratchCap(installedTransport(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dest, ScratchRelPath)); !bytes.Equal(got, want) {
		t.Errorf("%s of the copy is not the original with the cap applied", ScratchRelPath)
	}
	if got := read(t, filepath.Join(dest, RelPath)); !bytes.Equal(got, Replacement()) {
		t.Errorf("%s of the copy is not the replacement", RelPath)
	}
	if got := sha256Hex(read(t, filepath.Join(src, ScratchRelPath))); got != ScratchPinnedSHA256() {
		t.Error("the source file of the HTTP/2 client was changed")
	}
	if !bytes.Equal(read(t, filepath.Join(dest, "src/other/file.go")), read(t, filepath.Join(src, "src/other/file.go"))) {
		t.Error("another file differs from the source")
	}
}

func TestPrepareRefusesAnHTTP2FileThatIsNotTheOneItWasWrittenFor(t *testing.T) {
	changed := append(installedTransport(t), []byte("\n// Go changed this file\n")...)
	src := fakeRootWith(t, "go1.27.3", installedOriginal(t), changed)
	dest := filepath.Join(t.TempDir(), "go")

	created, err := Prepare(src, dest)
	if err == nil {
		t.Fatal("no error for an HTTP/2 file that is not the pinned one")
	}
	if created {
		t.Error("created = true with an error")
	}
	if !strings.Contains(err.Error(), "changed") || !strings.Contains(err.Error(), ScratchRelPath) {
		t.Errorf("the error does not say what changed: %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a destination was left behind")
	}
}

// Whether a copy made earlier is still good depends on both changes: a copy
// made when only the first one existed must be made again.
func TestPrepareMakesAgainACopyThatHasOnlyTheFirstChange(t *testing.T) {
	src := fakeRoot(t, "go1.27.3", installedOriginal(t))
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(src, dest); err != nil {
		t.Fatal(err)
	}
	// What the earlier version of this package left: the stamp of one change.
	old := fmt.Sprintf("go1.27.3\n%s\n%s\n", sha256Hex(Replacement()), PinnedSHA256())
	if err := os.WriteFile(filepath.Join(dest, stampName), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Prepare(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("the copy with the stamp of the first change only was kept")
	}
}

// What the second change is for, measured in the toolchain that was made: 20
// streams with a request body open at once, the live heap after a collection.
// With the stock toolchain it grows by about 10 MB (20 buffers of 512 KB, the
// server of httptest allows a frame that big), with the copy by well under 4.
// The same program runs in both, so a program that measures nothing fails the
// stock side. Copies the toolchain and builds a program twice, not in -short.
func TestStreamsWithABodyCostLittleInTheCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the whole toolchain and builds a program")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(streamsProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	growth := func(goroot string) int {
		t.Helper()
		cmd := exec.Command(filepath.Join(goroot, "bin", "go"), "run", "main.go")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOROOT="+goroot, "GOTOOLCHAIN=local", "GOFLAGS=", "GO111MODULE=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", goroot, err, out)
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "growth=%d", &n); err != nil {
			t.Fatalf("output of the program: %q", out)
		}
		return n
	}

	stock := growth(installedRoot(t))
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(installedRoot(t), dest); err != nil {
		t.Fatal(err)
	}
	copied := growth(dest)

	t.Logf("live heap grew by %.1f MB with 20 open streams in the stock toolchain, %.1f MB in the copy",
		float64(stock)/1e6, float64(copied)/1e6)
	if stock < 8<<20 {
		t.Errorf("stock toolchain: %d bytes, want at least 8 MB: the program does not measure the buffers", stock)
	}
	if copied > 4<<20 {
		t.Errorf("copy: %d bytes, want under 4 MB", copied)
	}
}

// An upload through the HTTP/2 client of the copy still arrives whole: 5 MB in
// chunks that are not a multiple of the frame size, a body of unknown length.
func TestUploadThroughTheCopyArrivesWhole(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the whole toolchain and builds a program")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(uploadProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "go")
	if _, err := Prepare(installedRoot(t), dest); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(dest, "bin", "go"), "run", "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOROOT="+dest, "GOTOOLCHAIN=local", "GOFLAGS=", "GO111MODULE=off")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "OK") {
		t.Fatalf("upload through the copy: %v\n%s", err, out)
	}
}

const streamsProgram = `package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
)

func live() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func main() {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		io.Copy(io.Discard, r.Body) // until the client closes the body
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	client := srv.Client()

	// One request that finishes first: the connection, the handshake, the pools.
	pr, pw := io.Pipe()
	go func() { pw.Write([]byte("x")); pw.Close() }()
	resp, err := client.Post(srv.URL, "text/plain", pr)
	if err != nil {
		fmt.Println("warm-up:", err)
		os.Exit(1)
	}
	if resp.ProtoMajor != 2 {
		fmt.Println("not HTTP/2:", resp.Proto)
		os.Exit(1)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	base := live()

	const n = 20
	var opened, written sync.WaitGroup
	writers := make([]*io.PipeWriter, n)
	resps := make([]*http.Response, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		pr, pw := io.Pipe()
		writers[i] = pw
		req, _ := http.NewRequest("POST", srv.URL, pr)
		opened.Add(1)
		written.Add(1)
		go func(i int) {
			defer opened.Done()
			resps[i], errs[i] = client.Do(req)
		}(i)
		go func(pw *io.PipeWriter) {
			defer written.Done()
			pw.Write([]byte("x"))
		}(pw)
	}
	opened.Wait()
	written.Wait()
	for _, err := range errs {
		if err != nil {
			fmt.Println("stream:", err)
			os.Exit(1)
		}
	}
	grown := live()
	for i := 0; i < n; i++ {
		writers[i].Close()
		resps[i].Body.Close()
	}
	fmt.Printf("growth=%d\n", int64(grown)-int64(base))
}
`

const uploadProgram = `package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
)

func main() {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, _ := io.Copy(h, r.Body)
		fmt.Fprintf(w, "%d %x %d", n, h.Sum(nil), r.ProtoMajor)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	want := sha256.New()
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		chunk := make([]byte, 100_000)
		for i := range chunk {
			chunk[i] = byte(i * 7)
		}
		for i := 0; i < 50; i++ {
			want.Write(chunk)
			pw.Write(chunk)
		}
		pw.Close()
		close(done)
	}()
	req, _ := http.NewRequest("POST", srv.URL, pr)
	resp, err := srv.Client().Do(req)
	if err != nil {
		fmt.Println("FAIL", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	<-done
	var n, proto int
	var sum string
	fmt.Sscanf(string(body), "%d %s %d", &n, &sum, &proto)
	if n != 5_000_000 || proto != 2 || sum != fmt.Sprintf("%x", want.Sum(nil)) {
		fmt.Println("FAIL", n, proto, sum)
		os.Exit(1)
	}
	fmt.Println("OK")
}
`
