package core

import (
	"testing"

	xraylog "github.com/xtls/xray-core/common/log"
)

type captureHandler struct{ levels []string }

func (c *captureHandler) OnLog(level, _, _ string) { c.levels = append(c.levels, level) }

// forwarded runs one message of every kind through the xray forwarder and
// returns the levels that reached the app.
func forwarded(t *testing.T, verbose bool) []string {
	t.Helper()
	capture := &captureHandler{}
	SetLogHandler(capture)
	SetVerboseLogs(verbose)
	t.Cleanup(func() {
		SetLogHandler(nil)
		SetVerboseLogs(false)
	})

	f := &xrayLogForwarder{}
	for _, s := range []xraylog.Severity{
		xraylog.Severity_Debug,
		xraylog.Severity_Info,
		xraylog.Severity_Warning,
		xraylog.Severity_Error,
	} {
		f.Handle(&xraylog.GeneralMessage{Severity: s, Content: "x"})
	}
	f.Handle(&xraylog.AccessMessage{From: "a", To: "b", Status: xraylog.AccessAccepted})
	return capture.levels
}

func TestXrayForwarderFiltersRelease(t *testing.T) {
	got := forwarded(t, false)
	want := []string{"warning", "error"} // no debug, no info, no access record
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("release forwarded %v, want %v", got, want)
	}
}

func TestXrayForwarderFiltersDebugBuild(t *testing.T) {
	got := forwarded(t, true)
	// info + warning + error + the access record (reported as info); never debug
	want := []string{"info", "warning", "error", "info"}
	if len(got) != len(want) {
		t.Fatalf("debug build forwarded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("debug build forwarded %v, want %v", got, want)
			break
		}
	}
}
