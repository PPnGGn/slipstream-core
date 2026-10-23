package core

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"go.uber.org/zap/zapcore"
)

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
	none, err := newProxyParams("")
	if err != nil {
		t.Fatalf("newProxyParams(\"\") error: %v", err)
	}
	if none != (proxyParams{}) {
		t.Errorf("without a socket the parameters are %+v, want none: nothing needs a password", none)
	}

	p, err := newProxyParams("/tmp/x.sock")
	if err != nil {
		t.Fatalf("newProxyParams error: %v", err)
	}
	if p.httpSocket != "/tmp/x.sock" || len(p.user) != 16 || len(p.pass) != 32 {
		t.Errorf("params = %+v, want the socket, a 16 and a 32 character credential", p)
	}
	other, err := newProxyParams("/tmp/x.sock")
	if err != nil {
		t.Fatalf("newProxyParams error: %v", err)
	}
	if p.pass == other.pass {
		t.Errorf("two sessions got the same password")
	}
}

// Nothing listens by default: the traffic of the tunnel goes through the
// bridge, and the only inbound there can be is the HTTP proxy on a Unix socket
// the app asked for.
func TestLocalInbounds(t *testing.T) {
	if got := localInbounds(proxyParams{}); len(got) != 0 {
		t.Errorf("localInbounds without a socket = %v, want none", got)
	}
	// JSON must say [], not null: xray reads the inbounds as a list.
	raw, _ := json.Marshal(localInbounds(proxyParams{}))
	if string(raw) != "[]" {
		t.Errorf("no inbounds encode as %s, want []", raw)
	}

	p := proxyParams{httpSocket: "/data/app/x.sock", user: "u", pass: "p"}
	raw, err := json.Marshal(localInbounds(p))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d inbounds, want 1", len(got))
	}
	in := got[0]
	if in["tag"] != "http" || in["protocol"] != "http" || in["listen"] != "/data/app/x.sock" {
		t.Errorf("inbound = %v, want the http one on the socket", in)
	}
	if _, hasPort := in["port"]; hasPort {
		t.Errorf("a Unix socket inbound has a port: %v", in)
	}
	settings := in["settings"].(map[string]any)
	accounts, _ := settings["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("accounts = %v, want one entry", settings["accounts"])
	}
	if acc := accounts[0].(map[string]any); acc["user"] != "u" || acc["pass"] != "p" {
		t.Errorf("account = %v", acc)
	}
}

// hardenSample is a provider-like config: its own inbounds (open, no auth),
// a verbose log, a private-IP outbound (see geoConfig in xray_test.go), blocks
// hardenConfig must not touch and one it drops ("remarks").
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
	out, err := hardenConfig(hardenSample, proxyParams{}, false)
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
	for _, key := range []string{"outbounds", "routing", "dns"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Errorf("%q changed:\n before: %v\n after:  %v", key, before[key], after[key])
		}
	}
	// "remarks" is a name for the user, not something xray needs: only the
	// allowed blocks pass (see TestHardenConfigKeepsOnlyAllowedBlocks).
	if _, ok := after["remarks"]; ok {
		t.Errorf("remarks survived hardenConfig")
	}

	// The provider's inbound is gone and nothing replaces it: no inbound, no port.
	inbounds, ok := after["inbounds"].([]any)
	if !ok || len(inbounds) != 0 {
		t.Fatalf("inbounds = %v, want an empty list", after["inbounds"])
	}

	// Log is replaced.
	logCfg := after["log"].(map[string]any)
	if logCfg["loglevel"] != "warning" || logCfg["access"] != "none" {
		t.Errorf("log = %v", logCfg)
	}
}

func TestHardenConfigWithHTTPSocket(t *testing.T) {
	p := proxyParams{httpSocket: "/data/app/x.sock", user: "u", pass: "p"}
	out, err := hardenConfig(hardenSample, p, false)
	if err != nil {
		t.Fatalf("hardenConfig error: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	inbounds, _ := after["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Fatalf("inbounds = %v, want exactly the HTTP one", after["inbounds"])
	}
	in := inbounds[0].(map[string]any)
	if in["tag"] != "http" || in["listen"] != "/data/app/x.sock" || in["tag"] == "old-socks" {
		t.Errorf("inbound = %v", in)
	}
}

func TestHardenConfigInvalidInput(t *testing.T) {
	for _, in := range []string{"", "not json", "[]", "null", `"text"`} {
		if out, err := hardenConfig(in, proxyParams{}, false); err == nil {
			t.Errorf("hardenConfig(%q) = %q, want an error", in, out)
		}
	}
}

func TestHardenConfigBuilds(t *testing.T) {
	for _, socket := range []string{"", "/tmp/slipstream-test.sock"} {
		p, err := newProxyParams(socket)
		if err != nil {
			t.Fatalf("newProxyParams: %v", err)
		}
		out, err := hardenConfig(hardenSample, p, false)
		if err != nil {
			t.Fatalf("hardenConfig: %v", err)
		}
		// xray itself must accept the result (no geo refs, so no asset dir needed).
		if _, err := buildCoreConfig(out, ""); err != nil {
			t.Fatalf("socket %q: buildCoreConfig rejected the hardened config: %v", socket, err)
		}
	}
}

func TestLiveProxy(t *testing.T) {
	t.Cleanup(clearLive) // do not leak state into other tests

	clearLive()
	if _, ok := liveProxy(); ok {
		t.Fatalf("liveProxy() reported a bridge before any was set")
	}

	setLive(fakeProxy{}, proxyParams{})
	if _, ok := liveProxy(); !ok {
		t.Fatalf("liveProxy() reports no bridge after setLive")
	}
	if _, _, _, ok := ActiveHTTPProxy(); ok {
		t.Errorf("ActiveHTTPProxy() reported a proxy for a session without a socket")
	}

	clearLive()
	if _, ok := liveProxy(); ok {
		t.Errorf("liveProxy() still reports a bridge after clearLive()")
	}
}

func TestActiveHTTPProxy(t *testing.T) {
	t.Cleanup(clearLive)

	clearLive()
	if _, _, _, ok := ActiveHTTPProxy(); ok {
		t.Fatalf("ActiveHTTPProxy() reported a proxy with no session")
	}

	setLive(fakeProxy{}, proxyParams{httpSocket: "/data/app/x.sock", user: "u", pass: "p"})
	socket, user, pass, ok := ActiveHTTPProxy()
	if !ok || socket != "/data/app/x.sock" || user != "u" || pass != "p" {
		t.Errorf("ActiveHTTPProxy() = %q, %q, %q, %v; want the socket and credentials", socket, user, pass, ok)
	}

	clearLive()
	if _, _, _, ok := ActiveHTTPProxy(); ok {
		t.Errorf("the credentials outlived the session")
	}
}

func TestHardenConfigVerboseLog(t *testing.T) {
	for _, c := range []struct {
		verbose   bool
		level     string
		accessOff bool
	}{
		{verbose: false, level: "warning", accessOff: true},
		{verbose: true, level: "info", accessOff: false},
	} {
		out, err := hardenConfig(hardenSample, proxyParams{}, c.verbose)
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
