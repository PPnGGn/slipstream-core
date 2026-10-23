package core

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
)

// nothingRuns fails the test unless every part of a session is down.
func nothingRuns(t *testing.T) {
	t.Helper()
	if _, up := liveProxy(); up {
		t.Error("the bridge to xray is still published")
	}
	mu.Lock()
	xray := xrayInst
	mu.Unlock()
	if xray != nil {
		t.Error("xray is still running")
	}
	fdMu.Lock()
	tun := fdActive
	fdMu.Unlock()
	if tun != nil {
		t.Error("the fd tunnel is still running")
	}
	ioMu.Lock()
	ioTun := ioActive
	ioMu.Unlock()
	if ioTun != nil {
		t.Error("the io tunnel is still running")
	}
}

func memoryReturnRuns() bool {
	memReturnMu.Lock()
	defer memReturnMu.Unlock()
	return memReturn != nil
}

// Both platforms hand free memory back to the OS while a session runs, and
// stop doing it with the session. iOS, where the extension lives under a hard
// memory limit, needs it at least as much as Android.
func TestSessionReturnsMemoryOnBothPaths(t *testing.T) {
	useFileDevices(t)
	t.Cleanup(func() { _ = Stop() })
	a, _ := socketPair(t)

	for name, start := range map[string]func() error{
		"fd": func() error { return StartFd(hardenSample, a, "", "") },
		"io": func() error { return StartIO(hardenSample, eofReadWriter{}, "") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			if !memoryReturnRuns() {
				t.Error("no memory return loop while the session runs")
			}
			if err := Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if memoryReturnRuns() {
				t.Error("the memory return loop outlived the session")
			}
		})
	}
}

func TestSessionFdStartsAndStops(t *testing.T) {
	useFileDevices(t)
	t.Cleanup(func() { _ = Stop() })
	a, _ := socketPair(t)

	if err := StartFd(hardenSample, a, "", ""); err != nil {
		t.Fatalf("StartFd: %v", err)
	}
	if _, ok := liveProxy(); !ok {
		t.Error("no bridge to xray while the session runs")
	}
	if !fdIsOpen(a) {
		t.Error("the descriptor is closed while the session runs")
	}

	if err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	nothingRuns(t)
	if fdIsOpen(a) {
		syscall.Close(a)
		t.Errorf("descriptor %d is still open after Stop", a)
	}
	if err := Stop(); err != nil {
		t.Errorf("a second Stop failed: %v", err)
	}
}

func TestSessionIOStartsAndStops(t *testing.T) {
	t.Cleanup(func() { _ = Stop() })

	if err := StartIO(hardenSample, eofReadWriter{}, ""); err != nil {
		t.Fatalf("StartIO: %v", err)
	}
	if err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	nothingRuns(t)
}

// While a session runs, a second Start is refused and leaves the first alone;
// the descriptor the refused call was given is closed, it is the core's.
func TestSessionRefusesASecondStart(t *testing.T) {
	useFileDevices(t)
	t.Cleanup(func() { _ = Stop() })
	a, _ := socketPair(t)
	if err := StartFd(hardenSample, a, "", ""); err != nil {
		t.Fatalf("StartFd: %v", err)
	}

	c, _ := socketPair(t)
	err := StartFd(hardenSample, c, "", "")
	if err == nil {
		t.Fatal("a second StartFd succeeded while a session runs")
	}
	if fdIsOpen(c) {
		syscall.Close(c)
		t.Errorf("descriptor %d of the refused start is still open", c)
	}
	if _, ok := liveProxy(); !ok || !fdIsOpen(a) {
		t.Error("the refused start disturbed the running session")
	}
}

// A Start that fails halfway takes down the part that did start: xray must not
// be left running for a tunnel that never came up.
func TestSessionRollsBackWhenTheTunnelFails(t *testing.T) {
	t.Cleanup(func() { _ = Stop() })

	if err := StartFd(hardenSample, -1, "", ""); err == nil {
		t.Fatal("StartFd with an invalid descriptor succeeded")
	}
	nothingRuns(t)

	// And the next start works.
	useFileDevices(t)
	a, _ := socketPair(t)
	if err := StartFd(hardenSample, a, "", ""); err != nil {
		t.Fatalf("StartFd after a failed start: %v", err)
	}
}

func TestSessionFailedXrayStartClosesTheDescriptor(t *testing.T) {
	a, _ := socketPair(t)

	if err := StartFd("not json", a, "", ""); err == nil {
		t.Fatal("StartFd with a broken config succeeded")
	}
	nothingRuns(t)
	if fdIsOpen(a) {
		syscall.Close(a)
		t.Errorf("descriptor %d is still open after a failed start", a)
	}
}

// A panic anywhere in Start comes back as an error, with everything that had
// started taken down again.
func TestSessionRecoversFromAPanicInStart(t *testing.T) {
	t.Cleanup(func() { _ = Stop() })
	old := openTunDevice
	openTunDevice = func(int) (device.Device, error) { panic("boom") }
	t.Cleanup(func() { openTunDevice = old })
	a, _ := socketPair(t)

	err := StartFd(hardenSample, a, "", "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("StartFd = %v, want an error that carries the panic", err)
	}
	nothingRuns(t)
}

// Stop that arrives while Start is still working waits for it, then takes
// everything down. Without that, Stop could run between Start's steps and the
// tunnel would come up on top of a stopped xray.
func TestSessionStopWaitsForStart(t *testing.T) {
	useFileDevices(t)
	wrapped := openTunDevice
	entered, release := make(chan struct{}), make(chan struct{})
	openTunDevice = func(fd int) (device.Device, error) {
		close(entered)
		<-release
		return wrapped(fd)
	}
	t.Cleanup(func() { _ = Stop() })
	a, _ := socketPair(t)

	startDone := make(chan error, 1)
	go func() { startDone <- StartFd(hardenSample, a, "", "") }()
	<-entered // Start has xray up and is opening the device

	stopDone := make(chan error, 1)
	go func() { stopDone <- Stop() }()
	select {
	case <-stopDone:
		t.Fatal("Stop returned while Start was still opening the device")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-startDone; err != nil {
		t.Fatalf("StartFd: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	nothingRuns(t)
}
