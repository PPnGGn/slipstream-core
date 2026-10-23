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
	"io"
	"time"

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

// Handler is implemented by the app and receives the log lines of the core, see
// slipcore.LogHandler. It is called from one goroutine of the core, in order, and
// should return fast: lines are dropped when it falls far behind.
type Handler interface {
	OnLog(level string, message string, source string)
}

type handlerAdapter struct{ h Handler }

func (a handlerAdapter) OnLog(level, message, source string) {
	a.h.OnLog(level, message, source)
}

// SetHandler sets the receiver of the core's log lines; nil removes it. Set it
// before Start to see the lines of the start itself.
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

// Start starts xray and tun2socks. fd is the TUN descriptor, already detached
// from the app's ParcelFileDescriptor: Start owns it from the call on and
// closes it when it fails, so the app must not close it either way. It blocks
// for as long as building the config takes (seconds with a large geo config),
// so it does not belong on the main thread. A failed Start leaves nothing
// running; a Start while a session runs is refused.
//
// httpSocket is the path of a Unix socket in the app's private directory where
// the HTTP proxy for the app's own requests listens (see LocalHTTPProxy), or ""
// for none. The traffic of the tunnel needs no socket at all: nothing else
// listens, so no other app on the phone can find a port to scan.
func Start(configJson string, fd int, assetDir string, httpSocket string) error {
	return slipcore.StartFd(configJson, fd, assetDir, httpSocket)
}

// Stop ends the session. Safe to call at any time and any number of times; if
// a Start is still working, Stop waits for it and then stops what it started.
func Stop() error {
	err := slipcore.Stop()
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

// BuildCommit returns the commit of slipstream-core this library was built from
// (with "-dirty" after it when the tree had uncommitted changes), or "unknown"
// for a build that did not stamp it. For a bug report: Version says which xray is
// inside, this says which core.
func BuildCommit() string {
	return slipcore.BuildCommit()
}

// TrafficStats are the bytes counted through the proxy outbound since Start.
type TrafficStats struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

// QueryTraffic reads the counters; zeros when no session runs or the config
// does not count (see slipcore.OutboundTag). Cheap, safe to poll.
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

// QueryMemory reads the Go runtime's memory now. It does not stop the world, so
// it can be polled every second.
func QueryMemory() *MemoryStats {
	m := MemoryStats(slipcore.ReadMemorySnapshot())
	return &m
}

// FreeMemory runs a GC and hands every free page back to the OS, in the
// background: the app calls it from onTrimMemory, on its main thread.
func FreeMemory() {
	go slipcore.FreeMemory()
}

// SetMemoryLog turns the "mem:" logcat line (every 3 s) on or off. Off by
// default; debug builds switch it from an adb broadcast for a measurement,
// see V2RayVpnService. Survives Stop, so a log spans server switches.
func SetMemoryLog(on bool) {
	slipcore.SetMemoryLog(on)
}

// SetGCTuning changes the GC settings at once: percent is GOGC (> 0), limitMB
// a soft memory limit in MB (0 = none). Debug builds call it from an adb
// broadcast to compare settings on a live session, see V2RayVpnService. It
// refuses to run unless SetVerboseLogs(true) was called.
func SetGCTuning(percent int, limitMB int) error {
	if err := slipcore.RequireDebug(); err != nil {
		return err
	}
	return slipcore.SetGCTuning(percent, int64(limitMB)<<20)
}

// SetUDPTimeout sets, in seconds, how long an idle UDP session of the tunnel
// lives (60 by default; 0 takes the setting back). Every session holds a relay
// buffer while it lives. For measuring memory; applies to sessions that start
// after the call. Debug builds only, with the same refusal as SetGCTuning.
func SetUDPTimeout(seconds int) error {
	if err := slipcore.RequireDebug(); err != nil {
		return err
	}
	return slipcore.SetUDPTimeout(time.Duration(seconds) * time.Second)
}

// WriteHeapProfile writes the core's heap profile into dir (the app's
// filesDir) as heap[-name].pprof and returns the path. Debug builds only: the
// app wires it to an adb broadcast, see V2RayVpnService, and the call refuses
// to run unless SetVerboseLogs(true) was called.
func WriteHeapProfile(dir string, name string) (string, error) {
	if err := slipcore.RequireDebug(); err != nil {
		return "", err
	}
	return slipcore.WriteHeapProfile(dir, name)
}

// WriteGoroutineDump writes every goroutine's stack into dir (the app's
// filesDir) as goroutines[-name].txt and returns the path. Debug builds only,
// on the same adb broadcast as WriteHeapProfile and with the same refusal.
func WriteGoroutineDump(dir string, name string) (string, error) {
	if err := slipcore.RequireDebug(); err != nil {
		return "", err
	}
	return slipcore.WriteGoroutineDump(dir, name)
}

// MeasureAESGCM encrypts for a third of a second with AES-GCM and returns the
// speed, "2330 MB/s": tens of MB/s means Go runs it in software, several GB/s
// that it uses the instructions of the processor. Debug builds only, with the
// same refusal as SetGCTuning.
func MeasureAESGCM() (string, error) {
	if err := slipcore.RequireDebug(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%.0f MB/s", slipcore.MeasureAESGCM(300*time.Millisecond)), nil
}

// TunnelNetwork is what the VpnService gives the virtual interface of one
// session: addresses, the DNS address the system's queries go to when the
// client answers them itself (an address of the same /24, nothing answers
// there) and the MTU. Take them from NewTunnelNetwork for every session and do
// not use fixed ones: an app that reads the addresses of the interfaces could
// look for them. The MTU is the one the core's stack is built for, use it as it
// is.
type TunnelNetwork struct {
	Address4 string
	Prefix4  int64
	Mask4    string
	DNS4     string
	Address6 string
	Prefix6  int64
	MTU      int64
}

// NewTunnelNetwork draws the network of a new session from the system's
// randomness. An error means there was none, and the session must not start
// with a made-up one.
func NewTunnelNetwork() (*TunnelNetwork, error) {
	n, err := slipcore.NewTunnelNetwork()
	if err != nil {
		return nil, err
	}
	return &TunnelNetwork{
		Address4: n.Address4, Prefix4: int64(n.Prefix4), Mask4: n.Mask4, DNS4: n.DNS4,
		Address6: n.Address6, Prefix6: int64(n.Prefix6), MTU: int64(n.MTU),
	}, nil
}

// LocalProxy is the HTTP proxy of the running session, on a Unix socket.
//
// The credentials are behind methods and out of every text form of the object.
// With exported fields gomobile would bind them as getters and setters and
// print all of them in toString, and the password would go wherever the app
// logs the object. The object prints its socket and hides the rest, with every
// format verb and as JSON (which is empty).
type LocalProxy struct {
	socket, user, pass string
}

func newLocalProxy(socket, user, pass string) *LocalProxy {
	return &LocalProxy{socket: socket, user: user, pass: pass}
}

// Socket returns the path of the Unix socket to connect to.
func (p LocalProxy) Socket() string { return p.socket }

// User returns the user name of the proxy for this session.
func (p LocalProxy) User() string { return p.user }

// Pass returns the password of the proxy for this session. Never log it.
func (p LocalProxy) Pass() string { return p.pass }

// String names the socket and hides the credentials. gomobile makes it the
// toString of the Java and Kotlin object.
func (p LocalProxy) String() string {
	return "LocalProxy{socket: " + p.socket + ", user: ***, pass: ***}"
}

// GoString is the %#v form, the same text.
func (p LocalProxy) GoString() string { return p.String() }

// Format is every other way fmt prints the object (%d, %x and the rest print the
// fields of a struct, unexported ones too, when it has no Format): all of them
// give the same text. gomobile does not bind it, as fmt.State is not a type it
// can carry.
func (p LocalProxy) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, p.String()) }

// LocalHTTPProxy returns the HTTP proxy the app should use for its own
// requests while the tunnel is up, or nil when it is down or Start was given no
// socket. The app is excluded from the tunnel, so this is its only way through
// it. Connect to the Unix socket and speak HTTP proxy to it, with the user and
// password; a TCP address is not available, on purpose.
//
// Never log the result: it carries the proxy password.
func LocalHTTPProxy() *LocalProxy {
	socket, user, pass, ok := slipcore.ActiveHTTPProxy()
	if !ok {
		return nil
	}
	return newLocalProxy(socket, user, pass)
}
