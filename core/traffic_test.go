package core

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// decodeHardened runs a config through hardenConfig and decodes the result.
func decodeHardened(t *testing.T, config string) map[string]any {
	t.Helper()
	out, err := hardenConfig(config, proxyParams{}, false)
	if err != nil {
		t.Fatalf("hardenConfig: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return cfg
}

// keysFold returns the keys of m that equal name, ignoring case.
func keysFold(m map[string]any, name string) []string {
	var keys []string
	for key := range m {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	return keys
}

// objectAt follows the path of keys (ignoring case) and requires exactly one
// key at each step, so that a spelling twice would be caught.
func objectAt(t *testing.T, m map[string]any, path ...string) map[string]any {
	t.Helper()
	for _, name := range path {
		keys := keysFold(m, name)
		if len(keys) != 1 {
			t.Fatalf("keys for %q in %v: %v, want exactly one", name, m, keys)
		}
		next, ok := m[keys[0]].(map[string]any)
		if !ok {
			t.Fatalf("%q is %T, want an object", keys[0], m[keys[0]])
		}
		m = next
	}
	return m
}

// The traffic shown by the app is the counters of xray, and xray counts only
// with a stats block and the two flags of the policy. A provider config
// brings neither, and used to leave the counters at zero without a word, as the
// core relied on the app to add them. The core turns them on itself.
func TestHardenConfigTurnsTheTrafficCountersOn(t *testing.T) {
	configs := map[string]string{
		"neither stats nor policy": `{"outbounds":[{"protocol":"freedom","tag":"proxy"}]}`,
		"flags off":                `{"stats":{},"policy":{"system":{"statsOutboundUplink":false,"statsOutboundDownlink":false}}}`,
		"only one flag":            `{"policy":{"system":{"statsOutboundUplink":true}}}`,
		"other spellings":          `{"STATS":{},"Policy":{"System":{"StatsOutboundUplink":false,"statsoutbounddownlink":false}}}`,
		"stats null":               `{"stats":null}`,
		"policy not an object":     `{"policy":"x"}`,
		"system not an object":     `{"policy":{"system":[]}}`,
	}
	for name, config := range configs {
		t.Run(name, func(t *testing.T) {
			cfg := decodeHardened(t, config)

			if keys := keysFold(cfg, "stats"); len(keys) != 1 {
				t.Errorf("stats keys = %v, want exactly one", keys)
			} else if _, ok := cfg[keys[0]].(map[string]any); !ok {
				t.Errorf("stats = %v, want an object (it is what switches the counters on)", cfg[keys[0]])
			}
			system := objectAt(t, cfg, "policy", "system")
			for _, flag := range []string{"statsOutboundUplink", "statsOutboundDownlink"} {
				keys := keysFold(system, flag)
				if len(keys) != 1 || system[keys[0]] != true {
					t.Errorf("%s = %v under keys %v, want true under one key", flag, system, keys)
				}
			}
		})
	}
}

// The rest of the policy of the provider stays as it is.
func TestHardenConfigKeepsTheRestOfThePolicy(t *testing.T) {
	cfg := decodeHardened(t, `{"policy":{
		"levels":{"8":{"handshake":4,"connIdle":300}},
		"system":{"statsInboundUplink":true,"statsOutboundUplink":false}
	}}`)

	policy := objectAt(t, cfg, "policy")
	levels := objectAt(t, policy, "levels", "8")
	if levels["handshake"] != float64(4) || levels["connIdle"] != float64(300) {
		t.Errorf("levels = %v, want the provider's", levels)
	}
	if system := objectAt(t, policy, "system"); system["statsInboundUplink"] != true {
		t.Errorf("system = %v, want statsInboundUplink kept", system)
	}
}

// A policy given under two spellings would be merged by encoding/json in the
// order of the keys, which is no order to rely on: one of them is kept.
func TestHardenConfigMergesNothingWhenPolicyIsSpelledTwice(t *testing.T) {
	for i := 0; i < 20; i++ { // map order is random: the outcome must not be
		cfg := decodeHardened(t, `{"policy":{"levels":{"0":{"handshake":1}}},"POLICY":{"levels":{"0":{"handshake":2}}}}`)
		objectAt(t, cfg, "policy", "system") // exactly one policy, with the flags
	}
}

// End to end: a config with no stats and no policy, run through the real xray,
// still has its traffic counted.
func TestTrafficIsCountedForAConfigWithoutStats(t *testing.T) {
	if err := startXray(`{"outbounds":[{"protocol":"freedom","tag":"proxy"}]}`, "", ""); err != nil {
		t.Fatalf("startXray: %v", err)
	}
	t.Cleanup(func() { _ = stopXray() })
	px, ok := liveProxy()
	if !ok {
		t.Fatal("no bridge to xray")
	}

	conn, err := px.DialContext(t.Context(), metadata(M.TCP, echoTCP(t)))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	payload := []byte(strings.Repeat("x", 4000))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := io.ReadFull(conn, make([]byte, len(payload))); err != nil {
		t.Fatalf("read the echo: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		up, down := QueryTraffic()
		if up >= int64(len(payload)) && down >= int64(len(payload)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("QueryTraffic = %d up, %d down after %d bytes each way, want them counted", up, down, len(payload))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// captureLog collects the lines of the core for a test.
type captureLog struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureLog) OnLog(level, message, source string) {
	c.mu.Lock()
	c.lines = append(c.lines, level+" "+source+": "+message)
	c.mu.Unlock()
}

func (c *captureLog) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// The counters are those of the outbound tagged "proxy". A config whose proxy
// has another tag is counted at zero for ever; the core says so at the start
// instead of leaving a developer to wonder why the app shows no traffic.
func TestAWarningWhenNoOutboundIsTaggedProxy(t *testing.T) {
	cases := map[string]struct {
		config string
		warns  bool
	}{
		"tagged proxy":           {`{"outbounds":[{"protocol":"freedom","tag":"proxy"}]}`, false},
		"tagged otherwise":       {`{"outbounds":[{"protocol":"freedom","tag":"direct"}]}`, true},
		"tagged proxy not first": {`{"outbounds":[{"protocol":"freedom","tag":"direct"},{"protocol":"blackhole","tag":"proxy"}]}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logs := &captureLog{}
			SetLogHandler(logs)
			t.Cleanup(func() { SetLogHandler(nil); _ = stopXray() })

			if err := startXray(tc.config, "", ""); err != nil {
				t.Fatalf("startXray: %v", err)
			}

			flushLogs() // delivery is asynchronous
			got := strings.Contains(logs.joined(), `"proxy"`)
			if got != tc.warns {
				t.Errorf("warned = %v, want %v; log:\n%s", got, tc.warns, logs.joined())
			}
		})
	}
}
