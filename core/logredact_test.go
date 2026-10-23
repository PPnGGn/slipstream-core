package core

import (
	"strings"
	"testing"

	xraylog "github.com/xtls/xray-core/common/log"
)

// The warnings of tun2socks and xray name the address a connection was going
// to ("[TCP] dial 93.184.216.34:443: ...", "failed to dial tcp:example.com:443").
// A release build logs warnings and errors, and the app shows them in its log
// page: where a user has been is not something the log of a VPN should keep.
func TestRedactDestinations(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"tun2socks tcp dial", "[TCP] dial 93.184.216.34:443: connect: connection refused", "[TCP] dial <addr>: connect: connection refused"},
		{"tun2socks udp dial v6", "[UDP] dial [2001:db8::1]:443: i/o timeout", "[UDP] dial <addr>: i/o timeout"},
		{"symmetric nat", "[UDP] symmetric NAT 10.1.2.3:5000->8.8.8.8:53: drop packet from 1.1.1.1:53", "[UDP] symmetric NAT <addr>-><addr>: drop packet from <addr>"},
		{"xray destination with a domain", "proxy/vless/outbound: connection ends > dial tcp:example.com:443 > failed", "proxy/vless/outbound: connection ends > dial tcp:<addr> > failed"},
		{"xray destination with a bracketed v6", "failed to open connection to udp:[2001:db8::7]:53 > timeout", "failed to open connection to udp:<addr> > timeout"},
		{"a name and a port", "failed to dial example.org:8443: refused", "failed to dial <addr>: refused"},
		{"an address with no port", "route to 203.0.113.9 failed", "route to <addr> failed"},
		{"a line with no address", "no outbound is tagged \"proxy\": the traffic counters will stay at 0", "no outbound is tagged \"proxy\": the traffic counters will stay at 0"},
		{"a version is not an address", "Xray 26.10.17 started", "Xray 26.10.17 started"},
		{"a time is not an address", "stopped at 21:06:33", "stopped at 21:06:33"},
		{"two on a line", "udp:a.example:53 and tcp:b.example:443", "udp:<addr> and tcp:<addr>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactDestinations(tc.in); got != tc.want {
				t.Errorf("redactDestinations(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// capture collects the whole lines (the handler of logging_test.go keeps only
// the levels).
type lines struct{ got []string }

func (l *lines) OnLog(level, message, source string) {
	l.got = append(l.got, level+" "+source+": "+message)
}

func (l *lines) all() string { return strings.Join(l.got, "\n") }

func through(t *testing.T, verbose bool, feed func()) *lines {
	t.Helper()
	got := &lines{}
	SetLogHandler(got)
	SetVerboseLogs(verbose)
	t.Cleanup(func() { SetLogHandler(nil); SetVerboseLogs(false) })
	feed()
	flushLogs() // delivery is asynchronous
	return got
}

func feedBothForwarders() {
	(&xrayLogForwarder{}).Handle(&xraylog.GeneralMessage{
		Severity: xraylog.Severity_Warning,
		Content:  "proxy/freedom: dial tcp:secret.example:443 failed",
	})
	t2s := newTun2socksForwardingLogger()
	t2s.Warn("[TCP] dial 93.184.216.34:443: connection refused")
	t2s.Info("[TCP] 10.0.0.2:50000 <-> 93.184.216.34:443")
}

// In a release build the lines that reach the app carry no address, and the
// info line that is only about a connection does not reach it at all.
func TestReleaseLogsCarryNoDestination(t *testing.T) {
	got := through(t, false, feedBothForwarders)

	out := got.all()
	for _, secret := range []string{"secret.example", "93.184.216.34", "10.0.0.2"} {
		if strings.Contains(out, secret) {
			t.Errorf("release log has %q:\n%s", secret, out)
		}
	}
	if len(got.got) != 2 {
		t.Errorf("%d lines reached the app, want the 2 warnings:\n%s", len(got.got), out)
	}
	for _, want := range []string{"tcp:<addr>", "dial <addr>:"} {
		if !strings.Contains(out, want) {
			t.Errorf("release log lacks %q (the lines should stay readable):\n%s", want, out)
		}
	}
}

// A debug build is for finding out what went wrong: it keeps the addresses.
func TestDebugLogsKeepTheDestinations(t *testing.T) {
	got := through(t, true, feedBothForwarders)

	out := got.all()
	for _, want := range []string{"secret.example:443", "93.184.216.34:443", "10.0.0.2:50000"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug log lacks %q:\n%s", want, out)
		}
	}
}
