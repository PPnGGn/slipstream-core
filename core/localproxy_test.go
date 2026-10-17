package core

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestFreePort(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error: %v", err)
	}
	if port < 1 || port > 65535 {
		t.Fatalf("freePort() = %d, want a valid TCP port", port)
	}

	// The port must really be free: we can bind it right away.
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d returned by freePort() is not bindable: %v", port, err)
	}
	l.Close()
}

func TestFreePortDiffersWhileHeld(t *testing.T) {
	first, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error: %v", err)
	}

	// Hold the first port; the OS must not hand it out again.
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(first)))
	if err != nil {
		t.Fatalf("cannot hold port %d: %v", first, err)
	}
	defer l.Close()

	second, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error: %v", err)
	}
	if second == first {
		t.Fatalf("freePort() returned the busy port %d again", first)
	}
}

func TestRandHexLength(t *testing.T) {
	for _, n := range []int{1, 8, 16, 32} {
		got, err := randHex(n)
		if err != nil {
			t.Fatalf("randHex(%d) error: %v", n, err)
		}
		if len(got) != 2*n {
			t.Errorf("randHex(%d) length = %d, want %d", n, len(got), 2*n)
		}
		// DecodeString fails on any non-hex character.
		if _, err := hex.DecodeString(got); err != nil {
			t.Errorf("randHex(%d) = %q is not valid hex: %v", n, got, err)
		}
	}
}

func TestRandHexDiffers(t *testing.T) {
	a, err := randHex(16)
	if err != nil {
		t.Fatalf("randHex(16) error: %v", err)
	}
	b, err := randHex(16)
	if err != nil {
		t.Fatalf("randHex(16) error: %v", err)
	}
	if a == b {
		t.Fatalf("two randHex(16) calls returned the same value %q", a)
	}
}

func TestRandHexInvalidLength(t *testing.T) {
	for _, n := range []int{0, -1} {
		if got, err := randHex(n); err == nil {
			t.Errorf("randHex(%d) = %q, want an error", n, got)
		}
	}
}

func TestNewProxyParams(t *testing.T) {
	p, err := newProxyParams()
	if err != nil {
		t.Fatalf("newProxyParams() error: %v", err)
	}
	if p.socksPort == p.httpPort {
		t.Errorf("socks and http share port %d", p.socksPort)
	}
	if p.socksPort < 1 || p.httpPort < 1 {
		t.Errorf("invalid ports: socks=%d http=%d", p.socksPort, p.httpPort)
	}
	if len(p.user) != 16 || len(p.pass) != 32 {
		t.Errorf("credential lengths: user=%d pass=%d, want 16 and 32", len(p.user), len(p.pass))
	}

	other, err := newProxyParams()
	if err != nil {
		t.Fatalf("newProxyParams() error: %v", err)
	}
	if p.pass == other.pass {
		t.Errorf("two sessions got the same password")
	}
}

func TestLocalInbounds(t *testing.T) {
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}

	// Round-trip through JSON: this is what xray will actually see, and it
	// turns every number into float64.
	raw, err := json.Marshal(localInbounds(p))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d inbounds, want 2", len(got))
	}

	socks, httpIn := got[0], got[1]
	for _, c := range []struct {
		name string
		in   map[string]any
		tag  string
		port float64
	}{
		{"socks", socks, "socks", 1111},
		{"http", httpIn, "http", 2222},
	} {
		if c.in["tag"] != c.tag || c.in["protocol"] != c.tag {
			t.Errorf("%s: tag/protocol = %v/%v", c.name, c.in["tag"], c.in["protocol"])
		}
		if c.in["listen"] != "127.0.0.1" {
			t.Errorf("%s: listen = %v, want 127.0.0.1", c.name, c.in["listen"])
		}
		if c.in["port"] != c.port {
			t.Errorf("%s: port = %v, want %v", c.name, c.in["port"], c.port)
		}

		settings, ok := c.in["settings"].(map[string]any)
		if !ok {
			t.Fatalf("%s: settings is not an object", c.name)
		}
		accounts, ok := settings["accounts"].([]any)
		if !ok || len(accounts) != 1 {
			t.Fatalf("%s: accounts = %v, want one entry", c.name, settings["accounts"])
		}
		acc, _ := accounts[0].(map[string]any)
		if acc["user"] != "u" || acc["pass"] != "p" {
			t.Errorf("%s: account = %v", c.name, acc)
		}
	}

	socksSettings := socks["settings"].(map[string]any)
	if socksSettings["auth"] != "password" || socksSettings["udp"] != true {
		t.Errorf("socks settings = %v", socksSettings)
	}
	sniffing := socks["sniffing"].(map[string]any)
	if sniffing["enabled"] != true || sniffing["routeOnly"] != true {
		t.Errorf("socks sniffing = %v", sniffing)
	}
}

// hardenSample is a provider-like config: its own inbounds (open, no auth),
// a verbose log, a private-IP outbound (see geoConfig in xray_test.go) and
// fields hardenConfig must not touch.
const hardenSample = `{
	"log": {"loglevel": "debug"},
	"inbounds": [
		{"listen": "0.0.0.0", "port": 10808, "protocol": "socks", "tag": "old-socks", "settings": {"auth": "noauth"}}
	],
	"outbounds": [
		{"protocol": "vless", "settings": {"vnext": [{"address": "10.0.0.1", "port": 443, "users": [{"id": "11111111-1111-1111-1111-111111111111", "encryption": "none"}]}]}, "streamSettings": {"network": "tcp", "security": "none"}, "tag": "proxy"},
		{"protocol": "freedom", "tag": "direct"}
	],
	"routing": {"rules": [{"type": "field", "inboundTag": ["socks"], "outboundTag": "direct"}]},
	"dns": {"servers": ["1.1.1.1"]},
	"remarks": "keep me"
}`

func TestHardenConfig(t *testing.T) {
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}

	out, err := hardenConfig(hardenSample, p, false)
	if err != nil {
		t.Fatalf("hardenConfig error: %v", err)
	}

	var before, after map[string]any
	if err := json.Unmarshal([]byte(hardenSample), &before); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	// Untouched fields are deeply equal to the input.
	for _, key := range []string{"outbounds", "routing", "dns", "remarks"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Errorf("%q changed:\n before: %v\n after:  %v", key, before[key], after[key])
		}
	}

	// Old inbounds are gone, ours are in.
	inbounds, ok := after["inbounds"].([]any)
	if !ok || len(inbounds) != 2 {
		t.Fatalf("inbounds = %v, want exactly 2", after["inbounds"])
	}
	for _, in := range inbounds {
		m := in.(map[string]any)
		if m["listen"] != "127.0.0.1" {
			t.Errorf("inbound %v listens on %v", m["tag"], m["listen"])
		}
		if m["tag"] == "old-socks" {
			t.Errorf("old inbound survived")
		}
	}

	// Log is replaced.
	logCfg := after["log"].(map[string]any)
	if logCfg["loglevel"] != "warning" || logCfg["access"] != "none" {
		t.Errorf("log = %v", logCfg)
	}
}

func TestHardenConfigInvalidInput(t *testing.T) {
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}
	for _, in := range []string{"", "not json", "[]", "null", `"text"`} {
		if out, err := hardenConfig(in, p, false); err == nil {
			t.Errorf("hardenConfig(%q) = %q, want an error", in, out)
		}
	}
}

func TestHardenConfigBuilds(t *testing.T) {
	p, err := newProxyParams()
	if err != nil {
		t.Fatalf("newProxyParams: %v", err)
	}
	out, err := hardenConfig(hardenSample, p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	// xray itself must accept the result (no geo refs, so no asset dir needed).
	if _, err := buildCoreConfig(out, ""); err != nil {
		t.Fatalf("buildCoreConfig rejected the hardened config: %v", err)
	}
}

func TestActiveProxy(t *testing.T) {
	t.Cleanup(clearActiveProxy) // do not leak state into other tests

	clearActiveProxy()
	if _, ok := activeProxyParams(); ok {
		t.Fatalf("activeProxyParams() reported a session before any was set")
	}

	want := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}
	setActiveProxy(want)
	got, ok := activeProxyParams()
	if !ok || got != want {
		t.Fatalf("activeProxyParams() = %+v, %v; want %+v, true", got, ok, want)
	}

	// The caller gets a copy: changing it must not touch the stored value.
	got.pass = "changed"
	again, _ := activeProxyParams()
	if again.pass != "p" {
		t.Errorf("stored password was modified through the returned copy")
	}

	clearActiveProxy()
	if _, ok := activeProxyParams(); ok {
		t.Errorf("activeProxyParams() still reports a session after clearActiveProxy()")
	}
}

func TestSocksProxyURL(t *testing.T) {
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}
	if got, want := socksProxyURL(p).String(), "socks5://u:p@127.0.0.1:1111"; got != want {
		t.Errorf("socksProxyURL = %q, want %q", got, want)
	}

	// Characters that would corrupt a hand-built URL must survive a round trip.
	p.pass = "p@ss:/?#"
	parsed, err := url.Parse(socksProxyURL(p).String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pass, _ := parsed.User.Password(); pass != p.pass {
		t.Errorf("password after round trip = %q, want %q", pass, p.pass)
	}
	if parsed.Host != "127.0.0.1:1111" {
		t.Errorf("host = %q", parsed.Host)
	}
	if strings.Contains(socksProxyURL(p).Redacted(), p.pass) {
		t.Errorf("Redacted() leaks the password")
	}
}

func TestActiveHTTPProxy(t *testing.T) {
	t.Cleanup(clearActiveProxy)

	clearActiveProxy()
	if _, _, _, ok := ActiveHTTPProxy(); ok {
		t.Fatalf("ActiveHTTPProxy() reported a proxy with no session")
	}

	setActiveProxy(proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"})
	port, user, pass, ok := ActiveHTTPProxy()
	if !ok || port != 2222 || user != "u" || pass != "p" {
		t.Errorf("ActiveHTTPProxy() = %d, %q, %q, %v; want the http port and credentials", port, user, pass, ok)
	}
}

func TestHardenConfigVerboseLog(t *testing.T) {
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}
	for _, c := range []struct {
		verbose   bool
		level     string
		accessOff bool
	}{
		{verbose: false, level: "warning", accessOff: true},
		{verbose: true, level: "info", accessOff: false},
	} {
		out, err := hardenConfig(hardenSample, p, c.verbose)
		if err != nil {
			t.Fatalf("verbose=%v: %v", c.verbose, err)
		}
		var cfg map[string]any
		if err := json.Unmarshal([]byte(out), &cfg); err != nil {
			t.Fatalf("decode: %v", err)
		}
		logCfg := cfg["log"].(map[string]any)
		if logCfg["loglevel"] != c.level {
			t.Errorf("verbose=%v: loglevel = %v, want %s", c.verbose, logCfg["loglevel"], c.level)
		}
		if got := logCfg["access"] == "none"; got != c.accessOff {
			t.Errorf("verbose=%v: access log off = %v, want %v", c.verbose, got, c.accessOff)
		}
		// Both variants must still build: a typo in a log key fails here.
		if _, err := buildCoreConfig(out, ""); err != nil {
			t.Errorf("verbose=%v: build: %v", c.verbose, err)
		}
	}
}

func TestTun2socksForwarderLevel(t *testing.T) {
	t.Cleanup(func() { SetVerboseLogs(false) })

	SetVerboseLogs(false)
	if newTun2socksForwardingLogger().Core().Enabled(zapcore.InfoLevel) {
		t.Errorf("release: info logs from tun2socks are let through")
	}

	SetVerboseLogs(true)
	if !newTun2socksForwardingLogger().Core().Enabled(zapcore.InfoLevel) {
		t.Errorf("debug: info logs from tun2socks are dropped")
	}
}
