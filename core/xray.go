package core

import (
	"bytes"
	"fmt"
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

func StartXray(configJson string) error {
	mu.Lock()
	defer mu.Unlock()

	if xrayInst != nil {
		return fmt.Errorf("xray is already running")
	}

	pbConfig, err := serial.DecodeJSONConfig(bytes.NewReader([]byte(configJson)))
	if err != nil {
		return fmt.Errorf("failed to decode config: %w", err)
	}

	coreConfig, err := pbConfig.Build()
	if err != nil {
		return fmt.Errorf("failed to build core config: %w", err)
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
