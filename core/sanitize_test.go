package core

import (
	"encoding/json"
	"fmt"
	"net"
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

// assertNotListening fails if something accepts connections on addr. xray
// opens its listeners synchronously in Start, so no waiting is needed.
func assertNotListening(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		conn.Close()
		t.Errorf("%s accepts connections: the provider config opened a listener", addr)
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
