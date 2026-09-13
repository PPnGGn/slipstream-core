package core

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/infra/conf/serial"
)

const OutboundTag = "proxy"

var (
	mu       sync.Mutex
	xrayInst *core.Instance
)

// StartXray builds and starts xray-core from configJson.
//
// assetDir, if non-empty, is where xray looks for geosite.dat / geoip.dat when
// the config references geosite:/geoip: categories. It is exported as the
// xray.location.asset env flag (both the dotted and XRAY_LOCATION_ASSET spellings
// are set — some libc reject env names with dots). GetAssetLocation re-reads the
// env on every asset open and is not cached, so setting it here, before Build(),
// is enough. Empty assetDir leaves xray's default (the executable's directory).
func StartXray(configJson string, assetDir string) error {
	mu.Lock()
	defer mu.Unlock()

	if xrayInst != nil {
		return fmt.Errorf("xray is already running")
	}

	coreConfig, err := buildCoreConfig(configJson, assetDir)
	if err != nil {
		return err
	}

	inst, err := core.New(coreConfig)
	if err != nil {
		return fmt.Errorf("failed to create instance: %w", err)
	}

	if err := inst.Start(); err != nil {
		return fmt.Errorf("failed to start xray: %w", err)
	}

	InstallLogForwarding()

	xrayInst = inst
	return nil
}

// buildCoreConfig points xray at assetDir (for geosite.dat / geoip.dat) and
// turns configJson into a built *core.Config. Geo databases are read here, at
// Build() time — a config that names a geosite:/geoip: category with no data
// file present fails at this step, before anything starts.
func buildCoreConfig(configJson string, assetDir string) (*core.Config, error) {
	if assetDir != "" {
		_ = os.Setenv("xray.location.asset", assetDir)
		_ = os.Setenv("XRAY_LOCATION_ASSET", assetDir)
	}

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

func StopXray() error {
	mu.Lock()
	defer mu.Unlock()

	if xrayInst == nil {
		return nil
	}
	err := xrayInst.Close()
	xrayInst = nil
	return err
}

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
