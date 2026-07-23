// Package android is the gomobile-bound entry point for the Android app.
// gomobile binds exactly one package (see the Makefile bind-android target).
//
// Android is the fd path: VpnService.Builder().establish() hands the app an
// honest TUN file descriptor, so Start takes an fd and drives the fd-based
// tun2socks engine. iOS/macOS have no fd and use their own packages.
//
// https://pkg.go.dev/golang.org/x/mobile/cmd/gobind.
package android

import (
	"fmt"

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

func Start(configJson string, fd int, socksPort int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("android.Start: panic recovered: %v", r)
		}
	}()
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
