package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	xcore "github.com/xtls/xray-core/core"
)

// measureMu lets one measurement run at a time. They are started by a tap,
// the app queues them itself; this only keeps two of them from juggling the
// process-wide log handler at once.
var measureMu sync.Mutex

// MeasureDelay is the "Real delay" of one server: the time one HTTP request to
// rawURL takes through the `proxy` outbound of configJson, in milliseconds.
//
// A connect to the port (what the ping does) shows that something listens
// there. This goes the whole way a real connection goes: the transport, TLS or
// REALITY, the account on the server and the exit to the internet, so a server
// that accepts connections but carries nothing fails here.
//
// It runs its own xray, which lives for the duration of the call and which the
// session knows nothing about: the session's instance, its traffic counters and
// its tunnel are not touched, and the call may be made while a session is up.
// The app is outside its own tunnel (split tunnel), so the request leaves the
// phone the same way the ping does and the number does not depend on the VPN
// being on.
//
// The request is sent twice on one connection: the first pays for the
// handshakes with the server and is thrown away, the second is the number.
// timeoutMs covers everything, the start of xray included.
//
// assetDir is where xray finds geosite.dat and geoip.dat for the rules of the
// config; "" leaves the directory a running session set as it is.
func MeasureDelay(configJson, assetDir, rawURL string, timeoutMs int) (int64, error) {
	if timeoutMs <= 0 {
		return 0, errors.New("measure: the timeout must be positive")
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("measure: bad url: %w", err)
	}
	if (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return 0, errors.New("measure: the url must be http or https and name a host")
	}

	forced, err := forceThroughProxy(configJson, target.Hostname())
	if err != nil {
		return 0, err
	}
	// No inbounds, no HTTP proxy socket: the request is made in this process.
	hardened, err := hardenConfig(forced, proxyParams{}, false)
	if err != nil {
		return 0, err
	}

	measureMu.Lock()
	defer measureMu.Unlock()

	if assetDir != "" {
		setAssetDir(assetDir)
	}
	coreConfig, err := decodeCoreConfig(hardened)
	if err != nil {
		return 0, err
	}
	if !hasOutboundTag(coreConfig, OutboundTag) {
		return 0, fmt.Errorf("measure: the config has no outbound tagged %q", OutboundTag)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	inst, err := xcore.New(coreConfig)
	if err != nil {
		return 0, fmt.Errorf("measure: create xray: %w", err)
	}
	// Starting and closing an instance replaces the log handler of the whole
	// process; a session that runs beside us would stop logging. So the app's
	// forwarder goes back on after both.
	defer InstallLogForwarding()
	defer func() { _ = inst.Close() }()
	if err := inst.Start(); err != nil {
		return 0, fmt.Errorf("measure: start xray: %w", err)
	}
	InstallLogForwarding()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				host, portText, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				port, err := strconv.ParseUint(portText, 10, 16)
				if err != nil {
					return nil, err
				}
				return xcore.Dial(ctx, inst, xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(port)))
			},
			// A proxy of the system is not for this request.
			Proxy: nil,
		},
		// A redirect is an answer too, but not the one the server of a test
		// URL gives; following it would measure some other host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()

	if _, err := measureRequest(ctx, client, rawURL); err != nil {
		return 0, err
	}
	elapsed, err := measureRequest(ctx, client, rawURL)
	if err != nil {
		return 0, err
	}
	return elapsed.Milliseconds(), nil
}

// measureRequest sends one GET and returns how long until the answer began.
// Only a 2xx answer counts: the server of the test URL answers 204.
func measureRequest(ctx context.Context, client *http.Client, rawURL string) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("measure: %w", err)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return 0, errors.New("measure: timed out")
		}
		return 0, fmt.Errorf("measure: %w", err)
	}
	elapsed := time.Since(start)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("measure: the server answered %d", resp.StatusCode)
	}
	return elapsed, nil
}

// forceThroughProxy puts a rule in front of the routing of configJson that sends
// the one host of the test URL to the `proxy` outbound. The rules of the app
// (RU bypass, custom ones, block lists) would otherwise decide where the request
// goes, and a measurement that came out of `direct` would say nothing about
// the server. The rest of the config, the dns block included, stays as it
// is: the server's own name is resolved the way a real connection resolves it.
func forceThroughProxy(configJson, host string) (string, error) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(configJson), &cfg); err != nil {
		return "", fmt.Errorf("measure: decode config: %w", err)
	}
	if cfg == nil {
		return "", errors.New("measure: config is not a JSON object")
	}

	rule := map[string]any{"type": "field", "outboundTag": OutboundTag}
	if _, err := netip.ParseAddr(host); err == nil {
		rule["ip"] = []any{host}
	} else {
		rule["domain"] = []any{"full:" + host}
	}

	routing, ok := cfg["routing"].(map[string]any)
	if !ok {
		if cfg["routing"] != nil {
			return "", errors.New("measure: routing is not an object")
		}
		routing = map[string]any{}
	}
	rules, _ := routing["rules"].([]any)
	routing["rules"] = append([]any{rule}, rules...)
	cfg["routing"] = routing

	out, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("measure: encode config: %w", err)
	}
	return string(out), nil
}
