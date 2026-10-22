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

// A provider config must not be able to start xray's gRPC management API:
// it has no authentication, and HandlerService can add inbounds and list
// outbounds (server addresses and keys).
func TestHardenConfigDropsAPIBlock(t *testing.T) {
	// The key may be spelled in any case: encoding/json, which xray uses to
	// read the config, matches field names case-insensitively.
	for _, key := range []string{"api", "API", "Api"} {
		t.Run(key, func(t *testing.T) {
			port, err := freePort()
			if err != nil {
				t.Fatalf("freePort: %v", err)
			}
			addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
			cfg := withBlock(t, key, fmt.Sprintf(
				`{"tag":"api","listen":%q,"services":["HandlerService"]}`, addr))

			startHardened(t, cfg)
			assertNotListening(t, addr)
		})
	}
}

func TestHardenConfigDropsAPIBlockFromOutput(t *testing.T) {
	p := proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"}
	cfg := withBlock(t, "API", `{"tag":"api","services":["HandlerService"]}`)

	out, err := hardenConfig(cfg, p, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	for key := range after {
		if strings.EqualFold(key, "api") {
			t.Errorf("key %q survived hardenConfig", key)
		}
	}
}
