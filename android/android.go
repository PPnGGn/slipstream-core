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

	slipcore "slipstream-core/core"
)

func init() {
	slipcore.InstallLogForwarding()
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
		slipcore.SetLogHandler(nil)
		return
	}
	slipcore.SetLogHandler(handlerAdapter{h: h})
}

func Start(configJson string, fd int, socksPort int, assetDir string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("android.Start: panic recovered: %v", r)
		}
	}()
	if err := slipcore.StartXray(configJson, assetDir); err != nil {
		return err
	}
	if err := slipcore.StartTun(fd, socksPort); err != nil {
		_ = slipcore.StopXray()
		return err
	}
	return nil
}

func Stop() error {
	slipcore.StopTun()
	return slipcore.StopXray()
}

// Version returns the embedded xray-core version, e.g. "26.9.9".
func Version() string {
	return slipcore.XrayVersion()
}

type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

func QueryTraffic() *TrafficStats {
	up, down := slipcore.QueryTraffic()
	return &TrafficStats{UplinkBytes: up, DownlinkBytes: down}
}

// MemoryStats is xray-core/tun2socks's own memory footprint — the Go
// runtime's, isolated from the surrounding Flutter/Android process. See
// slipcore.QueryMemory for what each field means.
type MemoryStats struct {
	HeapAllocBytes int64
	SysBytes       int64
}

func QueryMemory() *MemoryStats {
	heapAlloc, sys := slipcore.QueryMemory()
	return &MemoryStats{HeapAllocBytes: heapAlloc, SysBytes: sys}
}
