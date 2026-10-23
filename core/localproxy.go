package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/proxy"
)

// randHex returns n random bytes as a hex string of length 2*n.
//
// Uses crypto/rand, not math/rand: the result becomes a proxy password.
func randHex(n int) (string, error) {
	// make([]byte, n) would panic on a negative n.
	if n <= 0 {
		return "", fmt.Errorf("random hex: invalid length %d", n)
	}

	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random hex: %w", err)
	}

	return hex.EncodeToString(buf), nil
}

// proxyParams is what the app needs to send its own requests through the
// tunnel: an HTTP proxy on a Unix socket, with a password for the session.
// The socket is optional (the iOS extension has none); without it xray has no
// inbound at all and nothing listens.
type proxyParams struct {
	httpSocket string
	user       string
	pass       string
}

// newProxyParams makes the parameters of a session. A socket path brings fresh
// random credentials with it; an empty one means no HTTP proxy.
func newProxyParams(httpSocket string) (proxyParams, error) {
	if httpSocket == "" {
		return proxyParams{}, nil
	}
	user, err := randHex(8)
	if err != nil {
		return proxyParams{}, err
	}
	pass, err := randHex(16)
	if err != nil {
		return proxyParams{}, err
	}
	return proxyParams{httpSocket: httpSocket, user: user, pass: pass}, nil
}

// bridgeUserLevel is the policy level of the tunnel's traffic, the one of the
// SOCKS inbound the bridge replaced.
const bridgeUserLevel = 8

// localInbounds builds the inbounds that replace whatever inbounds a config
// came with. There are none, unless the app asked for an HTTP proxy for its own
// requests: then one, on a Unix socket in the app's private directory, which
// no other app can reach, with a password on top. The traffic of the tunnel
// itself does not come through an inbound at all, see xrayProxy: a loopback
// port can be found by every app on the phone, and what answers on it gives away
// a proxy client.
//
// The tag stays "http": provider routing rules may refer to it via inboundTag.
//
// The result uses map[string]any / []any, the same shapes json.Unmarshal
// produces, so it can be dropped straight into a decoded config.
func localInbounds(p proxyParams) []any {
	if p.httpSocket == "" {
		return []any{}
	}
	return []any{map[string]any{
		"tag":      "http",
		"listen":   p.httpSocket,
		"protocol": "http",
		"settings": map[string]any{
			"accounts":  []any{map[string]any{"user": p.user, "pass": p.pass}},
			"userLevel": bridgeUserLevel,
		},
	}}
}

// hardenConfig returns a copy of configJson in which every inbound is replaced
// by what localInbounds says, logging is turned down, every top-level block not
// listed in allowedBlocks is removed, and so are the reverse setting of
// outbounds and the router port mapping of finalmask. The traffic counters are
// switched on (ensureTrafficCounters). Everything else (outbounds, routing,
// dns, policy and the fields inside them) is passed through untouched. verbose
// (debug builds only, see SetVerboseLogs) keeps xray chatty instead.
func hardenConfig(configJson string, p proxyParams, verbose bool) (string, error) {
	// A map keeps fields we know nothing about, unlike a typed struct which
	// would silently drop them on the way back.
	var cfg map[string]any
	if err := json.Unmarshal([]byte(configJson), &cfg); err != nil {
		return "", fmt.Errorf("harden config: decode: %w", err)
	}
	// The literal JSON "null" decodes without error into a nil map, and
	// writing to a nil map panics.
	if cfg == nil {
		return "", fmt.Errorf("harden config: config is not a JSON object")
	}

	keepAllowedBlocks(cfg)
	dropOutboundReverse(cfg)
	dropFinalmaskPortMapping(cfg)
	ensureTrafficCounters(cfg)

	cfg["inbounds"] = localInbounds(p)
	cfg["log"] = xrayLog(verbose)

	out, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("harden config: encode: %w", err)
	}
	return string(out), nil
}

// xrayLog is the "log" block the core always imposes.
//
// Release: "warning" only, no access log — info logs every connection.
// Debug: info + access log (who went where, through which outbound) + DNS log
// (which resolver answered what), the three things you need to debug routing.
// None of them print the proxy credentials.
func xrayLog(verbose bool) map[string]any {
	if verbose {
		return map[string]any{"loglevel": "info", "dnsLog": true}
	}
	return map[string]any{"loglevel": "warning", "access": "none"}
}

var (
	liveMu     sync.Mutex
	liveBridge proxy.Proxy  // what tun2socks relays through; nil while xray is down
	liveHTTP   *proxyParams // the HTTP socket of the app's requests; nil without one
)

// setLive publishes the bridge and the HTTP parameters of a running xray.
func setLive(px proxy.Proxy, p proxyParams) {
	liveMu.Lock()
	defer liveMu.Unlock()
	liveBridge = px
	liveHTTP = nil
	if p.httpSocket != "" {
		liveHTTP = &p
	}
}

// clearLive withdraws them when xray stops, so the password does not outlive its
// session.
func clearLive() {
	liveMu.Lock()
	defer liveMu.Unlock()
	liveBridge = nil
	liveHTTP = nil
}

// liveProxy returns the bridge of the running xray, or false when none runs.
func liveProxy() (proxy.Proxy, bool) {
	liveMu.Lock()
	defer liveMu.Unlock()
	return liveBridge, liveBridge != nil
}

// ActiveHTTPProxy reports the HTTP proxy of the running session, on a Unix
// socket, so the app can send its own requests (geo downloads) through the
// tunnel it is excluded from. ok is false when no session is active or the
// session was started without a socket.
func ActiveHTTPProxy() (socket, user, pass string, ok bool) {
	liveMu.Lock()
	defer liveMu.Unlock()
	if liveHTTP == nil {
		return "", "", "", false
	}
	return liveHTTP.httpSocket, liveHTTP.user, liveHTTP.pass, true
}
