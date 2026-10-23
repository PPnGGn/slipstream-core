// Package toolchain makes the copy of the Go toolchain that builds the iOS
// framework.
//
// Go cannot ask iOS which extensions the processor has, so internal/cpu takes
// them all as absent (cpu_arm64_other.go, which is what GOOS=ios uses) and the
// crypto packages run AES-GCM in software. Measured on an iPhone 14 during a
// speed test, that was about 40% of the CPU time of the network extension.
// For macOS Go assumes the extensions (cpu_arm64_darwin.go); the copy made here
// does the same for iOS by replacing that one file.
//
// A copy and not an overlay: the toolchain that go.mod asks for lives in the
// module cache, and go refuses to replace files there. A copy and not a flag
// set from our own init: the AEADs that other packages build in their init would
// be set up without the hardware tables, and work with them later.
package toolchain

import (
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

// RelPath is the file of GOROOT that the copy replaces.
const RelPath = "src/internal/cpu/cpu_arm64_other.go"

const stampName = ".slipstream-ios-toolchain"

//go:embed cpu_arm64_other.go.txt
var replacement []byte

//go:embed original.sha256
var pinned string

// Replacement is the text of the file that takes the place of RelPath.
func Replacement() []byte { return append([]byte(nil), replacement...) }

// PinnedSHA256 is the hash of RelPath as Go ships it, the file the replacement
// was written against. A Go that ships another one needs a look before it is
// used: it may detect the extensions itself by then.
func PinnedSHA256() string { return strings.TrimSpace(pinned) }

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Prepare makes in dest a copy of the toolchain in srcRoot (a GOROOT) with
// RelPath replaced, and reports whether it did: a copy made before from the
// same version of Go and the same replacement is left as it is. The source is
// not changed. It refuses, with nothing left behind, a source whose RelPath is
// not the file the replacement was written against.
func Prepare(srcRoot, dest string) (created bool, err error) {
	version, err := goVersion(srcRoot)
	if err != nil {
		return false, err
	}
	want := fmt.Sprintf("%s\n%s\n%s\n", version, hashOf(replacement), PinnedSHA256())
	if got, err := os.ReadFile(filepath.Join(dest, stampName)); err == nil && string(got) == want {
		return false, nil
	}

	orig, err := os.ReadFile(filepath.Join(srcRoot, filepath.FromSlash(RelPath)))
	if err != nil {
		return false, fmt.Errorf("toolchain: %w", err)
	}
	if got := hashOf(orig); got != PinnedSHA256() {
		return false, fmt.Errorf("toolchain: %s of %s has changed (sha256 %s, the replacement was written for %s): "+
			"see whether Go detects the extensions itself now, or what else changed, then update toolchain/",
			RelPath, version, got, PinnedSHA256())
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
	file := filepath.Join(partial, filepath.FromSlash(RelPath))
	if err := os.Remove(file); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(file, replacement, 0o644); err != nil {
		return fail(err)
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
