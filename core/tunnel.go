package core

import (
	"fmt"
	"github.com/xjasonlyu/tun2socks/v2/engine"
)

func StartTun(fd int, socksPort int) error {
	engine.Insert(&engine.Key{
		Proxy:    fmt.Sprintf("socks5://127.0.0.1:%d", socksPort),
		Device:   fmt.Sprintf("fd://%d", fd),
		LogLevel: "info",
	})
	engine.Start()

	InstallLogForwarding()

	return nil
}

func StopTun() {
	engine.Stop()
}
