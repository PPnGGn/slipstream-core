package core

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	xcore "github.com/xtls/xray-core/core"
)

// startHardened runs a provider config through hardenConfig, builds it and
// starts a real xray instance: what StartXray does, minus the global state.
func startHardened(t *testing.T, providerConfig string) {
	t.Helper()
	p, err := newProxyParams()
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
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}
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

	p, err := newProxyParams()
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
