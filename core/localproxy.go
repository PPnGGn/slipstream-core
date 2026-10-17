package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
)

// freePort asks the OS for a free TCP port on loopback and returns its number.
func freePort() (int, error) {
	// Port 0 means "pick any free port for me".
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	// Close it so xray can bind the same port later. Another process may grab
	// the port in between; accepted, the window is tiny.
	defer listener.Close()

	// Addr() is the net.Addr interface; the port lives on the concrete type.
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("find free port: unexpected address type %T", listener.Addr())
	}

	return tcpAddr.Port, nil
}

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

// proxyParams holds everything that identifies the local proxies for one
// tunnel session: where they listen and how to authenticate to them.
type proxyParams struct {
	socksPort int
	httpPort  int
	user      string
	pass      string
}

// newProxyParams picks two distinct free ports and fresh random credentials.
func newProxyParams() (proxyParams, error) {
	socksPort, err := freePort()
	if err != nil {
		return proxyParams{}, err
	}

	// freePort closes its listener before returning, so the OS is free to hand
	// the very same port out again. Retry until the HTTP port differs.
	const maxAttempts = 5
	var httpPort int
	for range maxAttempts {
		httpPort, err = freePort()
		if err != nil {
			return proxyParams{}, err
		}
		if httpPort != socksPort {
			break
		}
	}
	if httpPort == socksPort {
		return proxyParams{}, fmt.Errorf("proxy params: no distinct free port after %d attempts", maxAttempts)
	}

	user, err := randHex(8)
	if err != nil {
		return proxyParams{}, err
	}
	pass, err := randHex(16)
	if err != nil {
		return proxyParams{}, err
	}

	return proxyParams{
		socksPort: socksPort,
		httpPort:  httpPort,
		user:      user,
		pass:      pass,
	}, nil
}

const loopbackHost = "127.0.0.1"

// localInbounds builds the two authenticated loopback inbounds that replace
// whatever inbounds a config came with. Tags stay "socks" and "http": provider
// routing rules may refer to them via inboundTag.
//
// The result uses map[string]any / []any, the same shapes json.Unmarshal
// produces, so it can be dropped straight into a decoded config.
func localInbounds(p proxyParams) []any {
	accounts := []any{
		map[string]any{"user": p.user, "pass": p.pass},
	}

	socks := map[string]any{
		"tag":      "socks",
		"listen":   loopbackHost,
		"port":     p.socksPort,
		"protocol": "socks",
		"settings": map[string]any{
			"auth":      "password",
			"accounts":  accounts,
			"udp":       true,
			"userLevel": 8,
		},
		// routeOnly: the sniffed domain is used for routing only, the outbound
		// still dials the original destination address.
		"sniffing": map[string]any{
			"enabled":      true,
			"destOverride": []any{"http", "tls"},
			"routeOnly":    true,
		},
	}

	httpInbound := map[string]any{
		"tag":      "http",
		"listen":   loopbackHost,
		"port":     p.httpPort,
		"protocol": "http",
		"settings": map[string]any{
			"accounts":  accounts,
			"userLevel": 8,
		},
	}

	return []any{socks, httpInbound}
}

// hardenConfig returns a copy of configJson in which every inbound is replaced
// by the authenticated loopback pair from localInbounds and logging is turned
// down. Everything else (outbounds, routing, dns, policy, unknown fields) is
// passed through untouched.
func hardenConfig(configJson string, p proxyParams) (string, error) {
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

	cfg["inbounds"] = localInbounds(p)
	// Not "info": it logs every connection. access "none" drops the access log.
	cfg["log"] = map[string]any{
		"loglevel": "warning",
		"access":   "none",
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("harden config: encode: %w", err)
	}
	return string(out), nil
}

var (
	proxyMu     sync.Mutex
	activeProxy *proxyParams // nil while no tunnel is running
)

// setActiveProxy remembers the parameters of the running tunnel session.
func setActiveProxy(p proxyParams) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	activeProxy = &p
}

// clearActiveProxy forgets them when the tunnel stops, so the password does
// not outlive its session.
func clearActiveProxy() {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	activeProxy = nil
}

// activeProxyParams returns a copy of the current parameters, or false when no
// tunnel session is active.
func activeProxyParams() (proxyParams, bool) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	if activeProxy == nil {
		return proxyParams{}, false
	}
	return *activeProxy, true
}

// socksProxyURL is the address tun2socks dials to reach the local SOCKS
// inbound: socks5://user:pass@127.0.0.1:port.
//
// Built with url.URL rather than Sprintf so credentials are escaped correctly.
// Never log the String() of this value; use Redacted() if you must.
func socksProxyURL(p proxyParams) *url.URL {
	return &url.URL{
		Scheme: "socks5",
		User:   url.UserPassword(p.user, p.pass),
		Host:   net.JoinHostPort(loopbackHost, strconv.Itoa(p.socksPort)),
	}
}

// ActiveHTTPProxy reports the loopback HTTP proxy of the running session, so
// the app can send its own requests (geo downloads) through the tunnel it is
// excluded from. ok is false when no session is active.
//
// The SOCKS side is deliberately not exposed: only tun2socks needs it.
func ActiveHTTPProxy() (port int, user, pass string, ok bool) {
	p, ok := activeProxyParams()
	if !ok {
		return 0, "", "", false
	}
	return p.httpPort, p.user, p.pass, true
}
