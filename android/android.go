// Package android is the gomobile-bound entry point for the Android app.
// gomobile binds exactly one package (see the Makefile bind-android target).
//
// Android is the fd path: VpnService.Builder().establish() hands the app an
// honest TUN file descriptor, so Start takes an fd and drives the fd-based
// tun2socks engine. iOS has no fd and uses its own package.
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

// SetVerboseLogs turns on info-level core logs (xray access + DNS log,
// tun2socks connections) from the next Start. Debug builds only: the app
// passes its own debuggable flag, so release builds never call it with true.
func SetVerboseLogs(on bool) {
	slipcore.SetVerboseLogs(on)
}

func Start(configJson string, fd int, assetDir string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("android.Start: panic recovered: %v", r)
		}
	}()
	if err := slipcore.StartXray(configJson, assetDir); err != nil {
		return err
	}
	if err := slipcore.StartTun(fd); err != nil {
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

// LocalProxy is the loopback HTTP proxy of the running session.
type LocalProxy struct {
	Port int
	User string
	Pass string
}

// LocalHTTPProxy returns the HTTP proxy the app should use for its own
// requests while the tunnel is up, or nil when it is down. The app is excluded
// from the tunnel, so this is its only way through it.
//
// Never log the result: it carries the proxy password.
func LocalHTTPProxy() *LocalProxy {
	port, user, pass, ok := slipcore.ActiveHTTPProxy()
	if !ok {
		return nil
	}
	return &LocalProxy{Port: port, User: user, Pass: pass}
}
