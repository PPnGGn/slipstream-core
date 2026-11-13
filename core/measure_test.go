package core

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	xraylog "github.com/xtls/xray-core/common/log"
)

// Real delay: the app asks for the time one request takes through one server.
// The measurement starts its own xray next to the session, so these tests
// check two things: that the request goes through the `proxy` outbound
// whatever the routing of the config says, and that the session which may be
// running at the same time is left as it was.

// measureConfig has a working `proxy` outbound and a routing that sends
// everything to a blackhole: the measurement has to get through anyway.
const measureConfig = `{
	"outbounds": [
		{"protocol": "freedom", "tag": "proxy"},
		{"protocol": "blackhole", "tag": "block"}
	],
	"routing": {"rules": [{"type": "field", "network": "tcp,udp", "outboundTag": "block"}]}
}`

// rulesOf decodes the routing rules of a config, in order.
func rulesOf(t *testing.T, configJson string) []map[string]any {
	t.Helper()
	var cfg struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(configJson), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return cfg.Routing.Rules
}

func TestForceThroughProxyPutsItsRuleFirst(t *testing.T) {
	out, err := forceThroughProxy(measureConfig, "www.gstatic.com")
	if err != nil {
		t.Fatal(err)
	}
	rules := rulesOf(t, out)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want ours and the one the config had: %v", len(rules), rules)
	}
	if rules[0]["outboundTag"] != "proxy" {
		t.Fatalf("first rule goes to %v, want proxy", rules[0]["outboundTag"])
	}
	domains, _ := rules[0]["domain"].([]any)
	if len(domains) != 1 || domains[0] != "full:www.gstatic.com" {
		t.Fatalf("first rule matches %v, want the one host", rules[0])
	}
	if rules[1]["outboundTag"] != "block" {
		t.Fatal("the rules the config brought must stay, behind ours")
	}
}

func TestForceThroughProxyMatchesAnIPByAddress(t *testing.T) {
	out, err := forceThroughProxy(measureConfig, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ips, _ := rulesOf(t, out)[0]["ip"].([]any)
	if len(ips) != 1 || ips[0] != "127.0.0.1" {
		t.Fatalf("first rule is %v, want an ip rule for the address", rulesOf(t, out)[0])
	}
}

func TestForceThroughProxyCreatesTheRoutingWhenThereIsNone(t *testing.T) {
	out, err := forceThroughProxy(`{"outbounds": [{"protocol": "freedom", "tag": "proxy"}]}`, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if rules := rulesOf(t, out); len(rules) != 1 {
		t.Fatalf("got %v, want just our rule", rules)
	}
}

func TestForceThroughProxyKeepsTheRestOfTheConfig(t *testing.T) {
	in := `{"dns": {"servers": ["1.1.1.1"]}, "outbounds": [{"protocol": "freedom", "tag": "proxy"}], "routing": {"domainStrategy": "IPIfNonMatch", "rules": []}}`
	out, err := forceThroughProxy(in, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(out), &cfg)
	if cfg["dns"] == nil || cfg["routing"].(map[string]any)["domainStrategy"] != "IPIfNonMatch" {
		t.Fatalf("dns or the routing settings got lost: %s", out)
	}
}

func TestForceThroughProxyRefusesWhatIsNotAConfig(t *testing.T) {
	for _, in := range []string{"", "not json", "null", "[]"} {
		if _, err := forceThroughProxy(in, "example.com"); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

// target is a server that answers 204 and counts the requests it got.
func target(t *testing.T) (url string, requests *atomic.Int32) {
	t.Helper()
	requests = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/generate_204", requests
}

func TestMeasureDelayGoesThroughProxyWhateverTheRoutingSays(t *testing.T) {
	url, _ := target(t)

	ms, err := MeasureDelay(measureConfig, "", url, 5000)
	if err != nil {
		t.Fatalf("MeasureDelay: %v", err)
	}
	if ms < 0 || ms > 5000 {
		t.Fatalf("got %d ms", ms)
	}
}

// The first request pays for the handshake of the server, the number the user
// sees is the second one.
func TestMeasureDelayAsksTwice(t *testing.T) {
	url, requests := target(t)

	if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("the target got %d requests, want a warm-up and a measured one", got)
	}
}

func TestMeasureDelayFailsWithinTheTimeoutWhenNothingAnswers(t *testing.T) {
	port, err := freePort() // closed again: nobody listens there
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = MeasureDelay(measureConfig, "", "http://127.0.0.1:"+strconv.Itoa(port)+"/", 1500)
	if err == nil {
		t.Fatal("no error for a port nobody listens on")
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("took %v for a 1.5 s timeout", took)
	}
}

// What the ping cannot see: the port is open and takes the connection, and then
// nothing comes back. A connect to it would show a good time.
func TestMeasureDelayFailsForAServerThatAcceptsAndSaysNothing(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	url, _ := target(t)
	config := fmt.Sprintf(`{"outbounds": [{
		"protocol": "shadowsocks",
		"settings": {"servers": [{"address": "127.0.0.1", "port": %d, "method": "aes-256-gcm", "password": "pw"}]},
		"tag": "proxy"
	}]}`, l.Addr().(*net.TCPAddr).Port)

	start := time.Now()
	_, err = MeasureDelay(config, "", url, 1500)
	if err == nil {
		t.Fatal("a server that carries nothing was measured")
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("took %v for a 1.5 s timeout", took)
	}
}

func TestMeasureDelayFailsWhenTheServerAnswersWithAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	if _, err := MeasureDelay(measureConfig, "", srv.URL, 3000); err == nil {
		t.Fatal("a 502 is not a working server, but there was no error")
	}
}

func TestMeasureDelayRefusesWhatCannotBeMeasured(t *testing.T) {
	url, _ := target(t)
	cases := map[string]struct {
		config, url string
		timeoutMs   int
	}{
		"no config":    {"", url, 1000},
		"not a config": {"not json", url, 1000},
		"no proxy":     {`{"outbounds": [{"protocol": "freedom", "tag": "direct"}]}`, url, 1000},
		"bad url":      {measureConfig, "://nope", 1000},
		"no host":      {measureConfig, "http:///x", 1000},
		"not http":     {measureConfig, "ftp://example.com/", 1000},
		"zero timeout": {measureConfig, url, 0},
	}
	for name, c := range cases {
		if _, err := MeasureDelay(c.config, "", c.url, c.timeoutMs); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// An empty asset directory means "xray's default" for a session, and
// setAssetDir takes the variables of the session before back. A measurement
// runs beside a session, so it must not do that.
func TestMeasureDelayLeavesTheAssetDirOfTheSession(t *testing.T) {
	dir := t.TempDir()
	setAssetDir(dir)
	t.Cleanup(func() { setAssetDir("") })
	url, _ := target(t)

	if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("XRAY_LOCATION_ASSET"); got != dir {
		t.Fatalf("the asset dir of the session is now %q, want %q", got, dir)
	}
}

// Starting an xray replaces the log handler of the whole process, which is how
// the logs of a running session would stop reaching the app.
func TestMeasureDelayKeepsTheLogsOfTheSession(t *testing.T) {
	capture := &captureHandler{}
	SetLogHandler(capture)
	t.Cleanup(func() { SetLogHandler(nil) })
	InstallLogForwarding()
	url, _ := target(t)

	if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
		t.Fatal(err)
	}
	xraylog.Record(&xraylog.GeneralMessage{Severity: xraylog.Severity_Warning, Content: "after the measurement"})
	flushLogs()
	if len(capture.levels) == 0 {
		t.Fatal("a line logged after the measurement did not reach the app")
	}
}

// The measurement is not the session: it must not become `xrayInst`, must not
// be stopped by Stop and must not stop a session that is running.
func TestMeasureDelayDoesNotTouchARunningSession(t *testing.T) {
	t.Cleanup(func() { _ = stopXray() })
	if err := startXray(measureConfig, "", ""); err != nil {
		t.Fatalf("startXray: %v", err)
	}
	mu.Lock()
	before := xrayInst
	mu.Unlock()
	url, _ := target(t)

	if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	after := xrayInst
	mu.Unlock()
	if before == nil || after != before {
		t.Fatal("the session instance changed")
	}
	if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
		t.Fatalf("a second measurement beside the session: %v", err)
	}
}

func TestMeasureDelayClosesItsXray(t *testing.T) {
	url, _ := target(t)
	if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
		t.Fatal(err)
	}
	// A call that left its xray running would hold state the next one trips
	// over; several in a row show it.
	for i := 0; i < 3; i++ {
		if _, err := MeasureDelay(measureConfig, "", url, 5000); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}
