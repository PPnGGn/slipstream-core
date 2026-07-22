// https://pkg.go.dev/golang.org/x/mobile/cmd/gobind.
package mobile

import (
	v2core "v2net-core/core"
)

func init() {
	v2core.InstallLogForwarding()
}

type Handler interface {
	OnLog(level string, message string, source string)
}

type handlerAdapter struct{ h Handler }

func (a handlerAdapter) OnLog(level, message, source string) {
	a.h.OnLog(level, message, source)
}

func SetHandler(h Handler) {
	if h == nil {
		v2core.SetLogHandler(nil)
		return
	}
	v2core.SetLogHandler(handlerAdapter{h: h})
}

func Start(configJson string, fd int, socksPort int) error {
	if err := v2core.StartXray(configJson); err != nil {
		return err
	}
	if err := v2core.StartTun(fd, socksPort); err != nil {
		_ = v2core.StopXray()
		return err
	}
	return nil
}

func Stop() error {
	v2core.StopTun()
	return v2core.StopXray()
}

type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

func QueryTraffic() *TrafficStats {
	up, down := v2core.QueryTraffic()
	return &TrafficStats{UplinkBytes: up, DownlinkBytes: down}
}
