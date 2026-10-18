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
	// Let the heap grow to 1.25× live data between GCs instead of Go's 2×.
	// Measured on a phone (2026-10-19): the GC costs ~0.2% of the core's CPU,
	// so collecting about 4× as often is cheap, while the default let a config
	// with ~60 MB of live data (RU geo rules) peak at 120–145 MB. No memory
	// limit: one below a heavy config's live data keeps the GC running non-stop.
	_ = slipcore.SetGCTuning(25, 0)
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
	slipcore.StartMemoryLog() // debug builds only
	slipcore.StartMemoryReturn()
	return nil
}

func Stop() error {
	slipcore.StopMemoryLog()
	slipcore.StopMemoryReturn()
	slipcore.StopTun()
	err := slipcore.StopXray()
	// The stopped session is garbage now. Left alone it waits for the next GC
	// and then goes back to the OS bit by bit — minutes under light traffic,
	// so after a switch the monitor kept showing the previous, heavier session.
	// In a goroutine: the app calls Stop on its main thread, and a full GC on
	// a slow phone must not freeze the UI.
	go slipcore.FreeMemory()
	return err
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
// slipcore.MemorySnapshot for what each field means.
//
// gomobile binds only this package's types, hence the copy. Fields must match
// MemorySnapshot exactly (names, types, order): QueryMemory converts one into
// the other, so any drift stops the build instead of silently zeroing a field.
type MemoryStats struct {
	TotalBytes     int64
	ReleasedBytes  int64
	RetainedBytes  int64
	HeapInuseBytes int64
	StackBytes     int64
	Goroutines     int64
	GCCycles       int64

	GCPercent        int64
	MemoryLimitBytes int64
}

func QueryMemory() *MemoryStats {
	m := MemoryStats(slipcore.ReadMemorySnapshot())
	return &m
}

// FreeMemory runs a GC and hands every free page back to the OS, in the
// background: the app calls it from onTrimMemory, on its main thread.
func FreeMemory() {
	go slipcore.FreeMemory()
}

// SetGCTuning changes the GC settings at once: percent is GOGC (> 0), limitMB
// a soft memory limit in MB (0 = none). Debug builds call it from an adb
// broadcast to compare settings on a live session, see V2RayVpnService.
func SetGCTuning(percent int, limitMB int) error {
	return slipcore.SetGCTuning(percent, int64(limitMB)<<20)
}

// WriteHeapProfile writes the core's heap profile into dir (the app's
// filesDir) as heap[-name].pprof and returns the path. Debug builds only: the
// app wires it to an adb broadcast, see V2RayVpnService.
func WriteHeapProfile(dir string, name string) (string, error) {
	return slipcore.WriteHeapProfile(dir, name)
}

// WriteGoroutineDump writes every goroutine's stack into dir (the app's
// filesDir) as goroutines[-name].txt and returns the path. Debug builds only,
// on the same adb broadcast as WriteHeapProfile.
func WriteGoroutineDump(dir string, name string) (string, error) {
	return slipcore.WriteGoroutineDump(dir, name)
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
