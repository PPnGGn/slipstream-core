package core

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/infra/conf/serial"
)

// OutboundTag is the tag of the outbound whose traffic QueryTraffic counts: the
// config must call its proxy outbound that, or the counters stay at 0 (the core
// warns at the start when no outbound has the tag). The stats and the
// policy.system.statsOutboundUplink/Downlink flags are switched on by the core
// itself, see ensureTrafficCounters.
const OutboundTag = "proxy"

var (
	mu       sync.Mutex
	xrayInst *core.Instance
)

// buildXrayConfig is buildCoreConfig, a variable only so that a test can hold
// startXray inside the slow step.
var buildXrayConfig = buildCoreConfig

// startXray builds and starts xray-core from configJson.
//
// assetDir, if non-empty, is where xray looks for geosite.dat / geoip.dat when
// the config references geosite:/geoip: categories. It is exported as the
// xray.location.asset env flag (both the dotted and XRAY_LOCATION_ASSET spellings
// are set — some libc reject env names with dots). GetAssetLocation re-reads the
// env on every asset open and is not cached, so setting it here, before Build(),
// is enough. Empty assetDir leaves xray's default (the executable's directory).
//
// mu guards only the xrayInst pointer and is never held while building or
// starting: that takes seconds on a large geo config, and QueryTraffic, which
// the app polls, needs mu. Callers are serialized by the session lock.
//
// httpSocket, if non-empty, is the path of a Unix socket for an HTTP proxy for the
// app's own requests (see ActiveHTTPProxy); with "" xray has no inbound and
// nothing listens at all.
func startXray(configJson string, assetDir string, httpSocket string) error {
	mu.Lock()
	running := xrayInst != nil
	mu.Unlock()
	if running {
		return fmt.Errorf("xray is already running")
	}

	// Fresh credentials for every session; the config's own inbounds are
	// replaced, so nothing listens but what we ask for.
	params, err := newProxyParams(httpSocket)
	if err != nil {
		return err
	}
	hardened, err := hardenConfig(configJson, params, verboseLogs.Load())
	if err != nil {
		return err
	}
	if httpSocket != "" {
		// A socket file left by a process that was killed would make the bind fail.
		removeSocketFile(httpSocket)
	}

	coreConfig, err := buildXrayConfig(hardened, assetDir)
	if err != nil {
		return err
	}

	inst, err := core.New(coreConfig)
	if err != nil {
		return fmt.Errorf("failed to create instance: %w", err)
	}

	if err := inst.Start(); err != nil {
		// Start stops at the first failing feature and leaves the earlier ones
		// running.
		_ = inst.Close()
		return fmt.Errorf("failed to start xray: %w", err)
	}

	InstallLogForwarding()
	if !hasOutboundTag(coreConfig, OutboundTag) {
		// Not an error of the session, but the app would show no traffic and nobody
		// would know why.
		emitLog("warning", `no outbound is tagged "`+OutboundTag+`": the traffic counters will stay at 0`, "core")
	}

	mu.Lock()
	if xrayInst != nil { // another startXray won; callers are meant to be serialized
		mu.Unlock()
		_ = inst.Close()
		return fmt.Errorf("xray is already running")
	}
	xrayInst = inst
	mu.Unlock()
	// Published only once xray is really up: startTun reads it next.
	setLive(newXrayProxy(inst, bridgeUserLevel), params)
	return nil
}

// hasOutboundTag reports whether one of the outbounds of the built config has
// the tag.
func hasOutboundTag(c *core.Config, tag string) bool {
	for _, outbound := range c.Outbound {
		if outbound.Tag == tag {
			return true
		}
	}
	return false
}

// assetEnvSet is true while the asset variables hold a directory this package
// put there.
var assetEnvSet atomic.Bool

// setAssetDir exports assetDir for xray, or, when it is empty, takes back what
// an earlier session exported so that this one gets xray's default instead of
// that session's directory. The variables are process-global and the process
// outlives sessions. An empty assetDir does not touch variables this package
// never set. Values that were in the environment before the first export are
// not restored; the app does not set them.
func setAssetDir(assetDir string) {
	if assetDir != "" {
		_ = os.Setenv("xray.location.asset", assetDir)
		_ = os.Setenv("XRAY_LOCATION_ASSET", assetDir)
		assetEnvSet.Store(true)
		return
	}
	if assetEnvSet.Swap(false) {
		_ = os.Unsetenv("xray.location.asset")
		_ = os.Unsetenv("XRAY_LOCATION_ASSET")
	}
}

// buildCoreConfig points xray at assetDir (for geosite.dat / geoip.dat) and
// turns configJson into a built *core.Config. Geo databases are read here, at
// Build() time — a config that names a geosite:/geoip: category with no data
// file present fails at this step, before anything starts.
func buildCoreConfig(configJson string, assetDir string) (*core.Config, error) {
	setAssetDir(assetDir)

	pbConfig, err := serial.DecodeJSONConfig(bytes.NewReader([]byte(configJson)))
	if err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	coreConfig, err := pbConfig.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build core config: %w", err)
	}

	return coreConfig, nil
}

// XrayVersion returns the embedded xray-core version, e.g. "26.9.9".
func XrayVersion() string {
	return core.Version()
}

// stopXray stops xray and withdraws the local proxy parameters. Closing the
// instance happens outside mu, for the same reason startXray does not hold it.
func stopXray() error {
	// Unconditional: the credentials must never outlive the session.
	socket, _, _, hadSocket := ActiveHTTPProxy()
	clearLive()

	mu.Lock()
	inst := xrayInst
	xrayInst = nil
	mu.Unlock()

	if inst == nil {
		return nil
	}
	err := inst.Close()
	if hadSocket {
		removeSocketFile(socket) // closing the listener leaves the file behind
	}
	return err
}

// removeSocketFile deletes path if it is a socket and does nothing otherwise:
// the path comes from the app, and a wrong one must not cost the user a file.
func removeSocketFile(path string) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode().Type() == os.ModeSocket {
		_ = os.Remove(path)
	}
}

// QueryTraffic returns the bytes sent and received through the outbound tagged
// OutboundTag since xray started, or 0, 0 when xray is not running or the
// config does not count them. It does not wait for a start or stop in progress.
func QueryTraffic() (uplink int64, downlink int64) {
	uplink = trafficCounter("outbound>>>" + OutboundTag + ">>>traffic>>>uplink")
	downlink = trafficCounter("outbound>>>" + OutboundTag + ">>>traffic>>>downlink")
	return
}

func trafficCounter(name string) int64 {
	mu.Lock()
	inst := xrayInst
	mu.Unlock()
	if inst == nil {
		return 0
	}

	manager, ok := inst.GetFeature(stats.ManagerType()).(stats.Manager)
	if !ok || manager == nil {
		return 0
	}
	counter := manager.GetCounter(name)
	if counter == nil {
		return 0
	}
	return counter.Value()
}
