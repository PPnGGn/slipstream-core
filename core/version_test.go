package core

import (
	"os/exec"
	"strings"
	"testing"
)

// A build that did not stamp the commit (go test, go build) says so instead of
// pretending to be one.
func TestBuildCommitIsUnknownWithoutAStamp(t *testing.T) {
	if got := BuildCommit(); got != "unknown" {
		t.Errorf("BuildCommit() = %q in a build with no stamp, want %q", got, "unknown")
	}
}

// The artifact says which commit of the core it was made from, so that a bug
// reported from a phone can be tied to a source. The commit gets there through
// the linker (-X sets a string variable of a package by its import path), and
// nothing reports a typo in that path: the variable just stays "unknown". So
// this test reads what make would run for both bindings.
func TestMakefileStampsTheCommit(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	const stamp = "-X slipstream-core/core.buildCommit="
	for _, target := range []string{"bind-android", "bind-ios"} {
		t.Run(target, func(t *testing.T) {
			out, err := exec.Command("make", "-n", "-C", "..", target).CombinedOutput()
			if err != nil {
				t.Fatalf("make -n %s: %v\n%s", target, err, out)
			}
			line := firstLineWith(string(out), "gomobile bind")
			i := strings.Index(line, stamp)
			if i < 0 {
				t.Fatalf("the gomobile command of %s has no %q:\n%s", target, stamp, line)
			}
			value := line[i+len(stamp):]
			if end := strings.IndexAny(value, " \""); end >= 0 {
				value = value[:end]
			}
			if value == "" || value == "unknown" {
				t.Errorf("%s stamps %q, want the commit (or -dirty after it)", target, value)
			}
		})
	}
}

func firstLineWith(text, part string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, part) {
			return line
		}
	}
	return ""
}
