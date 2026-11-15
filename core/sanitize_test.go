package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/vless"
	vlessout "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet/finalmask/realm"
)

// startHardened runs a provider config through hardenConfig, builds it and
// starts a real xray instance: what startXray does, minus the global state.
func startHardened(t *testing.T, providerConfig string) {
	t.Helper()
	p, err := newProxyParams("")
	if err != nil {
		t.Fatalf("newProxyParams: %v", err)
	}
	hardened, err := hardenConfig(providerConfig, p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	built, err := buildCoreConfig(hardened, "")
	if err != nil {
		t.Fatalf("buildCoreConfig: %v", err)
	}
	inst, err := xcore.New(built)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = inst.Close() })
}

// withBlock returns hardenSample with one extra top-level block.
func withBlock(t *testing.T, key, blockJSON string) string {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(hardenSample), &cfg); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	var block any
	if err := json.Unmarshal([]byte(blockJSON), &block); err != nil {
		t.Fatalf("decode block: %v", err)
	}
	cfg[key] = block
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(out)
}

// assertNotListening fails if something accepts connections on addr within a
// short window. Most xray listeners are open when Start returns, but some
// (the browser dialer) start on their own goroutine, so one dial is not
// enough to prove a port stays closed.
func assertNotListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			t.Errorf("%s accepts connections: the provider config opened a listener", addr)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// assertListenerBlockDropped checks that a top-level block which makes xray
// listen on an address is removed: it starts a real xray instance with the
// block pointing at a free port and expects the port to stay closed. blockFor
// renders the block's JSON for a given listen address.
func assertListenerBlockDropped(t *testing.T, name string, blockFor func(addr string) string) {
	t.Helper()
	// The key may be spelled in any case: encoding/json, which xray uses to
	// read the config, matches field names case-insensitively.
	for _, key := range []string{name, strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:]} {
		t.Run(key, func(t *testing.T) {
			port, err := freePort()
			if err != nil {
				t.Fatalf("freePort: %v", err)
			}
			addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))

			startHardened(t, withBlock(t, key, blockFor(addr)))
			assertNotListening(t, addr)
		})
	}
}

// assertBlockRemovedFromOutput checks that no spelling of the key survives in
// the JSON hardenConfig produces.
func assertBlockRemovedFromOutput(t *testing.T, name string) {
	t.Helper()
	p := proxyParams{}
	out, err := hardenConfig(withBlock(t, strings.ToUpper(name), `{}`), p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	for key := range after {
		if strings.EqualFold(key, name) {
			t.Errorf("key %q survived hardenConfig", key)
		}
	}
}

// A provider config must not be able to start xray's gRPC management API:
// it has no authentication, and HandlerService can add inbounds and list
// outbounds (server addresses and keys).
func TestHardenConfigDropsAPIBlock(t *testing.T) {
	assertListenerBlockDropped(t, "api", func(addr string) string {
		return fmt.Sprintf(`{"tag":"api","listen":%q,"services":["HandlerService"]}`, addr)
	})
	assertBlockRemovedFromOutput(t, "api")
}

// The metrics block starts an HTTP server with /debug/pprof/* and expvar:
// goroutine stacks, heap contents and command line, no authentication.
func TestHardenConfigDropsMetricsBlock(t *testing.T) {
	assertListenerBlockDropped(t, "metrics", func(addr string) string {
		return fmt.Sprintf(`{"tag":"metrics","listen":%q}`, addr)
	})
	assertBlockRemovedFromOutput(t, "metrics")
}

// Only the blocks the client needs pass: outbounds, routing, dns, policy,
// stats and fakeDns. Everything else, known or not yet invented, is removed,
// so a block that a later xray release adds does not become a new hole.
func TestHardenConfigKeepsOnlyAllowedBlocks(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(hardenSample), &cfg); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	kept := map[string]any{
		// Both flags already set: hardenConfig adds them (see traffic_test.go) and
		// this test is about what passes through unchanged.
		"policy":  map[string]any{"system": map[string]any{"statsOutboundUplink": true, "statsOutboundDownlink": true}},
		"stats":   map[string]any{},
		"fakeDns": map[string]any{"ipPool": "198.18.0.0/15", "poolSize": float64(128)},
	}
	for key, value := range kept {
		cfg[key] = value
	}
	removed := []string{
		"someFutureBlock", "remarks", "observatory", "burstObservatory", "version", "transport",
		"api", "metrics", "env", "geodata", "reverse",
	}
	for _, key := range removed {
		cfg[key] = map[string]any{"x": "y"}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	p := proxyParams{}
	out, err := hardenConfig(string(raw), p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	for _, key := range []string{"outbounds", "routing", "dns", "policy", "stats", "fakeDns"} {
		want := cfg[key]
		if got, ok := after[key]; !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v (present: %v), want %v", key, got, ok, want)
		}
	}
	for _, key := range removed {
		if _, ok := after[key]; ok {
			t.Errorf("block %q survived hardenConfig", key)
		}
	}
	// inbounds and log are ours, not the provider's. The sample has an inbound
	// of its own; with no socket asked for, none is left.
	if inbounds, ok := after["inbounds"].([]any); !ok || len(inbounds) != 0 {
		t.Errorf("inbounds = %v, want an empty list", after["inbounds"])
	}
	if logCfg, _ := after["log"].(map[string]any); logCfg["loglevel"] != "warning" {
		t.Errorf("log = %v, want ours", after["log"])
	}
}

// The one inbound the core adds is the HTTP proxy of the app on a Unix socket:
// no port, so nothing a scan of the loopback can find.
func TestHardenConfigAddsOnlyTheSocketInbound(t *testing.T) {
	p, err := newProxyParams("/tmp/x/h.sock")
	if err != nil {
		t.Fatalf("newProxyParams: %v", err)
	}
	out, err := hardenConfig(hardenSample, p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	var after struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(after.Inbounds) != 1 {
		t.Fatalf("inbounds = %v, want just the HTTP proxy", after.Inbounds)
	}
	in := after.Inbounds[0]
	if in["protocol"] != "http" || in["listen"] != "/tmp/x/h.sock" {
		t.Errorf("inbound = %v, want http on the socket", in)
	}
	if _, hasPort := in["port"]; hasPort {
		t.Errorf("inbound has a port: %v", in)
	}
}

// hardenConfig replaces the inbounds of a provider config with its own. The
// replacement must not depend on how the provider spells the key: encoding/json
// maps a key onto a field by simple case folding, and folding goes beyond
// ASCII ("ſ", U+017F, folds to "s"). Such a key sorts after our "inbounds" when
// the config is encoded again, so xray reads it last, and an inbound that
// listens without a password replaces ours.
func TestHardenConfigReplacesInboundsInEverySpelling(t *testing.T) {
	for _, key := range []string{"inbounds", "INBOUNDS", "Inbounds", "inboundſ", "INBOUNDſ"} {
		t.Run(key, func(t *testing.T) {
			port, err := freePort()
			if err != nil {
				t.Fatalf("freePort: %v", err)
			}
			addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
			cfg := withBlock(t, key, fmt.Sprintf(
				`[{"tag":"open","listen":"127.0.0.1","port":%d,"protocol":"socks","settings":{"auth":"noauth","udp":true}}]`, port))

			startHardened(t, cfg)
			assertNotListening(t, addr)
		})
	}
}

// hardenConfig replaces the log block with one that writes nowhere. As with
// inbounds, a provider key spelled differently must not survive: the two log
// blocks are decoded into the same field and merged, so a log file path from
// the provider would stay in the result.
func TestHardenConfigReplacesLogInEverySpelling(t *testing.T) {
	for _, key := range []string{"log", "LOG", "Log"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			errorLog := filepath.Join(dir, "error.log")
			accessLog := filepath.Join(dir, "access.log")
			cfg := withBlock(t, key, fmt.Sprintf(`{"error":%q,"access":%q}`, errorLog, accessLog))

			startHardened(t, cfg)

			for _, path := range []string{errorLog, accessLog} {
				if _, err := os.Stat(path); err == nil {
					t.Errorf("%s exists: the provider config made xray open a file of its choice", filepath.Base(path))
				}
			}
		})
	}
}

// assertBlockNotBuilt checks that a top-level block does not reach xray: the
// hardened config is built and none of the app configs in the result may be of
// protoType. For blocks that act on their own after Start (a schedule, a
// bridge) with nothing to dial, this is the observable proof they are gone.
func assertBlockNotBuilt(t *testing.T, name, blockJSON, protoType, assetDir string) {
	t.Helper()
	for _, key := range []string{name, strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:]} {
		t.Run(key, func(t *testing.T) {
			p, err := newProxyParams("")
			if err != nil {
				t.Fatalf("newProxyParams: %v", err)
			}
			hardened, err := hardenConfig(withBlock(t, key, blockJSON), p, false)
			if err != nil {
				t.Fatalf("hardenConfig: %v", err)
			}
			built, err := buildCoreConfig(hardened, assetDir)
			if err != nil {
				t.Fatalf("buildCoreConfig: %v", err)
			}
			for _, app := range built.App {
				if app.Type == protoType {
					t.Errorf("the built config contains %s: the block reached xray", protoType)
				}
			}
		})
	}
}

// The geodata block schedules a download: on a cron schedule, while the tunnel
// is up, xray fetches the listed URLs and replaces the named files in the geo
// directory, which the app shares between all configs.
func TestHardenConfigDropsGeodataBlock(t *testing.T) {
	// buildCoreConfig exports the asset directory through the environment.
	t.Cleanup(func() {
		os.Unsetenv("xray.location.asset")
		os.Unsetenv("XRAY_LOCATION_ASSET")
	})
	dir := t.TempDir()
	writeGeoFixtures(t, dir) // the block is only accepted for files that exist

	assertBlockNotBuilt(t, "geodata",
		`{"cron":"@every 1h","outbound":"direct","assets":[{"url":"https://example.com/geosite.dat","file":"geosite.dat"}]}`,
		"xray.app.geodata.Config", dir)
	assertBlockRemovedFromOutput(t, "geodata")
}

// vlessReverseTags returns the reverse tag of every built VLESS outbound that
// has one.
func vlessReverseTags(t *testing.T, built *xcore.Config) []string {
	t.Helper()
	var tags []string
	for _, h := range built.Outbound {
		msg, err := h.ProxySettings.GetInstance()
		if err != nil {
			continue
		}
		cfg, ok := msg.(*vlessout.Config)
		if !ok || cfg.Vnext == nil || cfg.Vnext.User == nil {
			continue
		}
		acc, err := cfg.Vnext.User.Account.GetInstance()
		if err != nil {
			continue
		}
		if account, ok := acc.(*vless.Account); ok && account.Reverse != nil {
			tags = append(tags, account.Reverse.Tag)
		}
	}
	return tags
}

// A VLESS outbound with "reverse" in its settings becomes a reverse bridge: a
// couple of seconds after Start it dials out to the server by itself, and then
// every request the server sends back through that connection is dispatched
// from the phone, to any address the router allows, with the reverse tag as
// the inbound tag. The server picks the targets, not the user.
func TestHardenConfigDropsVlessReverse(t *testing.T) {
	const sample = `{
		"outbounds": [
			{"protocol": "vless", "tag": "proxy",
			 "settings": {"address": "10.0.0.1", "port": 443, "id": "11111111-1111-1111-1111-111111111111",
			              "encryption": "none", %q: {"tag": "r"}},
			 "streamSettings": {"network": "tcp", "security": "none"}},
			{"protocol": "freedom", "tag": "direct"}
		],
		"routing": {"rules": [{"type": "field", "inboundTag": ["r"], "outboundTag": "direct"}]}
	}`
	p := proxyParams{}

	// The key may be spelled in any case, see assertListenerBlockDropped.
	for _, key := range []string{"reverse", "REVERSE", "Reverse"} {
		t.Run(key, func(t *testing.T) {
			cfg := fmt.Sprintf(sample, key)

			// Without hardening the test config really is a reverse bridge.
			plain, err := buildCoreConfig(cfg, "")
			if err != nil {
				t.Fatalf("buildCoreConfig(provider config): %v", err)
			}
			if got := vlessReverseTags(t, plain); len(got) != 1 {
				t.Fatalf("test config has reverse tags %v, want one: the test would prove nothing", got)
			}

			hardened, err := hardenConfig(cfg, p, false)
			if err != nil {
				t.Fatalf("hardenConfig: %v", err)
			}
			built, err := buildCoreConfig(hardened, "")
			if err != nil {
				t.Fatalf("buildCoreConfig(hardened): %v", err)
			}
			if got := vlessReverseTags(t, built); len(got) != 0 {
				t.Errorf("the hardened config still has VLESS reverse tags %v", got)
			}
			if len(built.Outbound) != 2 {
				t.Errorf("hardened config has %d outbounds, want 2: the outbound itself must stay", len(built.Outbound))
			}
		})
	}
}

// A freedom outbound ("direct") with "redirect" in its settings silently sends
// every bypassed connection (LAN bypass, geo bypass, custom direct rules) to
// the redirect address instead of its real destination, reaching localhost or
// any host the provider chooses.
func TestHardenConfigDropsFreedomRedirect(t *testing.T) {
	realTarget := echoTCP(t)
	trap, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { trap.Close() })
	go func() {
		for {
			c, err := trap.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = c.Write([]byte("trap"))
			}()
		}
	}()
	trapAddr := trap.Addr().String()

	const sample = `{
		"outbounds": [
			{"protocol": "freedom", "tag": "direct",
			 "settings": {"domainStrategy": "UseIP", %q: %q}}
		]
	}`
	p := proxyParams{}

	for _, key := range []string{"redirect", "REDIRECT", "Redirect"} {
		t.Run(key, func(t *testing.T) {
			cfg := fmt.Sprintf(sample, key, trapAddr)

			// Without hardening, dialing realTarget is redirected to trapAddr.
			plainPx := startBridgeConfig(t, cfg)
			conn, err := plainPx.DialContext(t.Context(), metadata(M.TCP, realTarget))
			if err != nil {
				t.Fatalf("plain DialContext: %v", err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			got := make([]byte, 4)
			_, err = io.ReadFull(conn, got)
			conn.Close()
			if err != nil || string(got) != "trap" {
				t.Fatalf("unhardened freedom answered %q (%v), want trap: the test would prove nothing", got, err)
			}

			// After hardening, dialing realTarget reaches realTarget and echoes back.
			hardened, err := hardenConfig(cfg, p, false)
			if err != nil {
				t.Fatalf("hardenConfig: %v", err)
			}
			safePx := startBridgeConfig(t, hardened)
			conn, err = safePx.DialContext(t.Context(), metadata(M.TCP, realTarget))
			if err != nil {
				t.Fatalf("hardened DialContext: %v", err)
			}
			defer conn.Close()
			if _, err := conn.Write([]byte("ping")); err != nil {
				t.Fatalf("write: %v", err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
				t.Errorf("hardened freedom answered %q (%v), want ping from realTarget", got, err)
			}
		})
	}
}

// tlsSettings.certificates[].certificateFile / keyFile make xray read files
// from the phone's disk at Build() time, and masterKeyLog in tlsSettings or
// realitySettings makes xray open a file for writing TLS secrets.
func TestHardenConfigDropsTLSFilePaths(t *testing.T) {
	missingCert := filepath.Join(t.TempDir(), "missing-cert.pem")
	missingKey := filepath.Join(t.TempDir(), "missing-key.pem")
	keyLog := filepath.Join(t.TempDir(), "keylog.txt")

	const sample = `{
		"outbounds": [
			{"protocol": "vless", "tag": "proxy",
			 "settings": {"vnext": [{"address": "1.2.3.4", "port": 443, "users": [{"id": "11111111-1111-1111-1111-111111111111", "encryption": "none"}]}]},
			 "streamSettings": {"network": "tcp", "security": "tls",
			   "tlsSettings": {
			     "serverName": "example.com",
			     %q: %q,
			     "certificates": [{%q: %q, %q: %q}]
			   }}}
		]
	}`

	for _, certKey := range []string{"certificateFile", "CERTIFICATEFILE", "Certificatefile"} {
		t.Run(certKey, func(t *testing.T) {
			cfg := fmt.Sprintf(sample, "masterKeyLog", keyLog, certKey, missingCert, "keyFile", missingKey)

			// Without hardening, buildCoreConfig tries to read missingCert from disk and fails.
			if _, err := buildCoreConfig(cfg, ""); err == nil {
				t.Fatal("unhardened buildCoreConfig succeeded with a missing certificateFile: the test would prove nothing")
			}

			// After hardening, the file paths and emptied cert entry are gone, so xray starts cleanly.
			startHardened(t, cfg)

			if _, err := os.Stat(keyLog); err == nil {
				t.Errorf("masterKeyLog %s was created on disk", keyLog)
			}
		})
	}
}

// Outbound sendThrough and streamSettings.sockopt interface/mark/bind/tproxy
// let a provider bind sockets to specific network interfaces or local addresses,
// while sockopt.dialerProxy is legitimate server chaining and must stay.
func TestHardenConfigDropsOutboundSocketOverrides(t *testing.T) {
	const cfg = `{
		"outbounds": [
			{"protocol": "freedom", "tag": "proxy",
			 "SENDTHROUGH": "127.0.0.1",
			 "streamSettings": {
			   "dsSettings": {"path": "/tmp/evil.sock"},
			   "sockopt": {
			     "INTERFACE": "wlan0",
			     "MARK": 42,
			     "bindAddress": "127.0.0.1",
			     "bindPort": 12345,
			     "tproxy": "tproxy",
			     "customSockopt": [{"level": "1", "opt": "2", "value": "1", "type": "int"}],
			     "dialerProxy": "hop"
			   }
			 }},
			{"protocol": "freedom", "tag": "hop"}
		]
	}`

	plain, err := buildCoreConfig(cfg, "")
	if err != nil {
		t.Fatalf("buildCoreConfig(plain): %v", err)
	}
	msg, err := plain.Outbound[0].SenderSettings.GetInstance()
	if err != nil {
		t.Fatalf("SenderSettings: %v", err)
	}
	plainSender := msg.(*proxyman.SenderConfig)
	if plainSender.Via == nil || plainSender.StreamSettings.SocketSettings.Interface != "wlan0" || plainSender.StreamSettings.SocketSettings.Mark != 42 {
		t.Fatalf("plain sender = %+v: test config did not set Via/Interface/Mark", plainSender)
	}

	hardened, err := hardenConfig(cfg, proxyParams{}, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	built, err := buildCoreConfig(hardened, "")
	if err != nil {
		t.Fatalf("buildCoreConfig(hardened): %v", err)
	}
	msg, err = built.Outbound[0].SenderSettings.GetInstance()
	if err != nil {
		t.Fatalf("SenderSettings(hardened): %v", err)
	}
	safeSender := msg.(*proxyman.SenderConfig)
	if safeSender.Via != nil {
		t.Errorf("sendThrough survived hardening: Via = %v", safeSender.Via)
	}
	sock := safeSender.StreamSettings.SocketSettings
	if sock.Interface != "" || sock.Mark != 0 || len(sock.CustomSockopt) != 0 {
		t.Errorf("dangerous sockopt survived hardening: %+v", sock)
	}

	// Also verify JSON keys (dsSettings, bindAddress, bindPort, tproxy) were removed,
	// while dialerProxy survived.
	var decoded struct {
		Outbounds []struct {
			StreamSettings map[string]any `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(hardened), &decoded); err != nil {
		t.Fatalf("decode hardened: %v", err)
	}
	ss := decoded.Outbounds[0].StreamSettings
	if _, hasDS := ss["dsSettings"]; hasDS {
		t.Errorf("dsSettings survived hardening: %v", ss)
	}
	sockMap, _ := ss["sockopt"].(map[string]any)
	for _, forbidden := range []string{"bindAddress", "bindPort", "tproxy", "INTERFACE", "MARK", "customSockopt"} {
		if _, ok := sockMap[forbidden]; ok {
			t.Errorf("sockopt[%q] survived hardening: %v", forbidden, sockMap)
		}
	}
	if sockMap["dialerProxy"] != "hop" {
		t.Errorf("sockopt[\"dialerProxy\"] = %v, want \"hop\"", sockMap["dialerProxy"])
	}
}

// enabledPortMappings counts the built realm masks that ask the router for a
// port mapping.
func enabledPortMappings(t *testing.T, built *xcore.Config) int {
	t.Helper()
	n := 0
	for _, h := range built.Outbound {
		msg, err := h.SenderSettings.GetInstance()
		if err != nil {
			continue
		}
		sender, ok := msg.(*proxyman.SenderConfig)
		if !ok || sender.StreamSettings == nil {
			continue
		}
		for _, mask := range sender.StreamSettings.Udpmasks {
			inst, err := mask.GetInstance()
			if err != nil {
				continue
			}
			if r, ok := inst.(*realm.Config); ok && r.PortMapping.GetEnabled() {
				n++
			}
		}
	}
	return n
}

// The realm mask can ask the user's router for a port mapping (UPnP or
// NAT-PMP) when the transport starts, which opens a port on the router that
// forwards to the phone's UDP socket.
func TestHardenConfigDropsRealmPortMapping(t *testing.T) {
	const sample = `{
		"outbounds": [
			{"protocol": "vless", "tag": "proxy",
			 "settings": {"address": "10.0.0.1", "port": 443, "id": "11111111-1111-1111-1111-111111111111", "encryption": "none"},
			 "streamSettings": {"network": "kcp", "security": "none",
			   "finalmask": {"udp": [{"type": "realm", "settings": {
			     "url": "realm://token@example.com:443/room",
			     "stunServers": ["stun.example.com:3478"],
			     %q: {"enabled": true, "timeout": 5, "lifetime": 600}}}]}}},
			{"protocol": "freedom", "tag": "direct"}
		]
	}`
	p := proxyParams{}

	for _, key := range []string{"portMapping", "PORTMAPPING", "Portmapping"} {
		t.Run(key, func(t *testing.T) {
			cfg := fmt.Sprintf(sample, key)

			// Without hardening the test config really asks for a mapping.
			plain, err := buildCoreConfig(cfg, "")
			if err != nil {
				t.Fatalf("buildCoreConfig(provider config): %v", err)
			}
			if got := enabledPortMappings(t, plain); got != 1 {
				t.Fatalf("test config has %d port mappings, want 1: the test would prove nothing", got)
			}

			hardened, err := hardenConfig(cfg, p, false)
			if err != nil {
				t.Fatalf("hardenConfig: %v", err)
			}
			built, err := buildCoreConfig(hardened, "")
			if err != nil {
				t.Fatalf("buildCoreConfig(hardened): %v", err)
			}
			if got := enabledPortMappings(t, built); got != 0 {
				t.Errorf("the hardened config still asks for %d router port mappings", got)
			}
			if len(built.Outbound) != 2 {
				t.Errorf("hardened config has %d outbounds, want 2: the outbound itself must stay", len(built.Outbound))
			}
		})
	}
}

// The env block makes xray call os.Setenv for every entry, and xray re-reads
// its settings from the environment when an instance is created. Through it
// the config can switch on the browser dialer, an HTTP/WebSocket server.
func TestHardenConfigDropsEnvBlockListener(t *testing.T) {
	// Restores the variable (unset) when the test ends; xray sets it directly.
	t.Setenv("xray.browser.dialer", "")

	assertListenerBlockDropped(t, "env", func(addr string) string {
		return fmt.Sprintf(`{"xray.browser.dialer":%q}`, addr)
	})
	assertBlockRemovedFromOutput(t, "env")
}

// Whatever the config puts in env would also stay in the process after the
// session and leak into the next one (a GODEBUG value takes effect in the Go
// runtime at once, xray.location.asset redirects the geo files).
func TestBuildHardenedConfigLeavesEnvironmentAlone(t *testing.T) {
	const canary = "SLIPSTREAM_TEST_CANARY"
	t.Setenv(canary, "")

	p, err := newProxyParams("")
	if err != nil {
		t.Fatalf("newProxyParams: %v", err)
	}
	hardened, err := hardenConfig(withBlock(t, "env", fmt.Sprintf(`{%q:"set-by-provider"}`, canary)), p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	if _, err := buildCoreConfig(hardened, ""); err != nil {
		t.Fatalf("buildCoreConfig: %v", err)
	}
	if got := os.Getenv(canary); got != "" {
		t.Errorf("%s = %q after building the config: the provider changed the process environment", canary, got)
	}
}
