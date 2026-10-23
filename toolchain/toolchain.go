// Package toolchain makes the copy of the Go toolchain that builds the iOS
// framework. Two things of the standard library are changed in the copy, both
// measured on an iPhone, where a network extension is killed at about 50 MB.
//
// The processor. Go cannot ask iOS which extensions the processor has, so
// internal/cpu takes them all as absent (cpu_arm64_other.go, which is what
// GOOS=ios uses) and the crypto packages run AES-GCM in software: 35 MB/s instead
// of 2100, and about 40% of the CPU time of the extension during a speed test.
// For macOS Go assumes the extensions (cpu_arm64_darwin.go); the copy does the
// same for iOS by replacing that file.
//
// The buffers of HTTP/2. For every stream that has a request body the client of
// net/http keeps a buffer of min(the frame size of the server, 512 KB). With
// XHTTP there are a couple of dozen such streams, 13 MB of the live heap, and the
// extension died at 130 to 260 Mbit/s. The copy makes the cap 16 KB, the frame
// size every peer accepts (a sender may always send frames smaller than the
// peer allows), by changing one line of net/http/internal/http2/transport.go.
//
// A copy and not an overlay: the toolchain that go.mod asks for lives in the
// module cache, and go refuses to replace files there. A copy and not a flag
// set from our own init: the AEADs that other packages build in their init would
// be set up without the hardware tables, and work with them later.
//
// The change covers the HTTP/2 client inside net/http. golang.org/x/net/http2,
// which the MASQUE transport of xray uses, has the same cap and is not changed.
package toolchain

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RelPath is the file of GOROOT whose body the copy replaces, for the processor.
const RelPath = "src/internal/cpu/cpu_arm64_other.go"

// ScratchRelPath is the file of GOROOT in which the copy changes one line, for
// the buffers of HTTP/2.
const ScratchRelPath = "src/net/http/internal/http2/transport.go"

const stampName = ".slipstream-ios-toolchain"

const (
	scratchOld = "const max = 512 << 10"
	scratchNew = "const max = 16 << 10 // slipstream-core: 16 KB, the frame size every peer accepts, not 512 KB per stream (toolchain/)"
)

//go:embed cpu_arm64_other.go.txt
var replacement []byte

//go:embed original.sha256
var pinned string

//go:embed scratch.sha256
var scratchPinned string

// Replacement is the text of the file that takes the place of RelPath.
func Replacement() []byte { return append([]byte(nil), replacement...) }

// PinnedSHA256 is the hash of RelPath as Go ships it, the file the replacement
// was written against. A Go that ships another one needs a look before it is
// used: it may detect the extensions itself by then.
func PinnedSHA256() string { return strings.TrimSpace(pinned) }

// ScratchPinnedSHA256 is the hash of ScratchRelPath as Go ships it, the file
// ApplyScratchCap was written against.
func ScratchPinnedSHA256() string { return strings.TrimSpace(scratchPinned) }

// ApplyScratchCap returns the file of the HTTP/2 client with the cap of the
// buffer of a stream at 16 KB: the one line that says 512 KB is changed, and
// it has to be there exactly once.
func ApplyScratchCap(orig []byte) ([]byte, error) {
	switch n := bytes.Count(orig, []byte(scratchOld)); n {
	case 1:
		return bytes.Replace(orig, []byte(scratchOld), []byte(scratchNew), 1), nil
	case 0:
		return nil, fmt.Errorf("toolchain: no %q in %s", scratchOld, ScratchRelPath)
	default:
		return nil, fmt.Errorf("toolchain: %q is %d times in %s, want once", scratchOld, n, ScratchRelPath)
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// A change: the file, the hash of it that the change was written for, a hash of
// what the change is (for the stamp), and how the new text is made.
type change struct {
	rel    string
	pinned string
	what   string
	apply  func(orig []byte) ([]byte, error)
}

func changes() []change {
	return []change{
		{RelPath, PinnedSHA256(), hashOf(replacement), func([]byte) ([]byte, error) { return Replacement(), nil }},
		{ScratchRelPath, ScratchPinnedSHA256(), hashOf([]byte(scratchOld + "\n" + scratchNew)), ApplyScratchCap},
	}
}

// Prepare makes in dest a copy of the toolchain in srcRoot (a GOROOT) with the
// changes made, and reports whether it did: a copy made before from the same
// version of Go and the same changes is left as it is. The source is not
// changed. It refuses, with nothing left behind, a source in which one of the
// files is not the one its change was written against.
func Prepare(srcRoot, dest string) (created bool, err error) {
	version, err := goVersion(srcRoot)
	if err != nil {
		return false, err
	}
	cs := changes()
	var stamp strings.Builder
	fmt.Fprintf(&stamp, "%s\n", version)
	for _, c := range cs {
		fmt.Fprintf(&stamp, "%s %s %s\n", c.rel, c.pinned, c.what)
	}
	want := stamp.String()
	if got, err := os.ReadFile(filepath.Join(dest, stampName)); err == nil && string(got) == want {
		return false, nil
	}

	results := make([][]byte, len(cs))
	for i, c := range cs {
		orig, err := os.ReadFile(filepath.Join(srcRoot, filepath.FromSlash(c.rel)))
		if err != nil {
			return false, fmt.Errorf("toolchain: %w", err)
		}
		if got := hashOf(orig); got != c.pinned {
			return false, fmt.Errorf("toolchain: %s of %s has changed (sha256 %s, the change was written for %s): "+
				"see what Go does there now, then update toolchain/", c.rel, version, got, c.pinned)
		}
		if results[i], err = c.apply(orig); err != nil {
			return false, err
		}
	}

	partial := dest + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	fail := func(err error) (bool, error) {
		_ = os.RemoveAll(partial)
		return false, err
	}
	if err := copyTree(srcRoot, partial); err != nil {
		return fail(fmt.Errorf("toolchain: copy %s: %w", srcRoot, err))
	}
	for i, c := range cs {
		file := filepath.Join(partial, filepath.FromSlash(c.rel))
		if err := os.Remove(file); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(file, results[i], 0o644); err != nil {
			return fail(err)
		}
	}
	if err := os.WriteFile(filepath.Join(partial, stampName), []byte(want), 0o644); err != nil {
		return fail(err)
	}
	if err := os.RemoveAll(dest); err != nil {
		return fail(err)
	}
	if err := os.Rename(partial, dest); err != nil {
		return fail(err)
	}
	return true, nil
}

func goVersion(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", fmt.Errorf("toolchain: %s is not a GOROOT: %w", root, err)
	}
	version, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(version), nil
}

// copyTree copies src to dst the way cp -R does: modes and times kept, symbolic
// links as links. Whatever the modes of the source, the copy can be written by
// its owner; the module cache keeps its files read-only.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(path, target, info)
		default:
			return fmt.Errorf("%s is neither a directory, a link nor a file", path)
		}
	})
}

func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}
