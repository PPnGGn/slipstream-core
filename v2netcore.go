package v2netcore

import (
	"bytes"
	"fmt"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"

	// blank import: регистрация протоколов и модулей xray-core
	_ "github.com/xtls/xray-core/main/distro/all"
)

var xrayInst *core.Instance

// StartXray — JSON от Flutter, decode в protobuf, старт instance.
func StartXray(configJson string) error {
	if xrayInst != nil {
		return fmt.Errorf("xray is already running")
	}

	pbConfig, err := serial.DecodeJSONConfig(bytes.NewReader([]byte(configJson)))
	if err != nil {
		return fmt.Errorf("failed to decode config: %v", err)
	}

	coreConfig, err := pbConfig.Build()
	if err != nil {
		return fmt.Errorf("failed to build core config: %v", err)
	}

	inst, err := core.New(coreConfig)
	if err != nil {
		return fmt.Errorf("failed to create instance: %v", err)
	}

	if err := inst.Start(); err != nil {
		return fmt.Errorf("failed to start xray: %v", err)
	}

	xrayInst = inst
	return nil
}

// StartTun — fd Android TUN -> tun2socks -> socks inbound на socksPort.
func StartTun(fd int, socksPort int) error {
	engine.Insert(&engine.Key{
		Proxy:    fmt.Sprintf("socks5://127.0.0.1:%d", socksPort),
		Device:   fmt.Sprintf("fd://%d", fd),
		LogLevel: "info",
	})
	engine.Start()
	return nil
}

// StopAll — tun2socks + xray instance.
func StopAll() error {
	engine.Stop()

	if xrayInst != nil {
		err := xrayInst.Close()
		xrayInst = nil
		return err
	}
	return nil
}
