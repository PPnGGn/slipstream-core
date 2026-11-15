package core

import (
	"sync"
	"testing"
	"time"
)

// The tunnel can die under a running session: the device the packets come from
// stops delivering them, and nothing in the app knows, because xray and the
// interface are still there. The core says so through a TunnelHandler, once per
// tunnel; what to do about it (hold the traffic, alert) is the app's.

type lostRecorder struct {
	mu      sync.Mutex
	reasons []string
	got     chan struct{}
}

func (r *lostRecorder) OnTunnelLost(reason string) {
	r.mu.Lock()
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
	select {
	case r.got <- struct{}{}:
	default:
	}
}

func (r *lostRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reasons)
}

// watchLoss registers a recorder as the handler for the length of the test.
func watchLoss(t *testing.T) *lostRecorder {
	t.Helper()
	r := &lostRecorder{got: make(chan struct{}, 8)}
	SetTunnelHandler(r)
	t.Cleanup(func() { SetTunnelHandler(nil) })
	return r
}

// waitLoss waits for a loss to be reported, or fails after a while.
func (r *lostRecorder) waitLoss(t *testing.T) {
	t.Helper()
	select {
	case <-r.got:
	case <-time.After(3 * time.Second):
		t.Fatal("the loss of the tunnel was not reported")
	}
}

func TestTunnelLossIsReportedWhenTheDeviceStopsReading(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	lost := watchLoss(t)
	r, w := pipePair(t)
	if err := startTun(r); err != nil {
		t.Fatalf("startTun: %v", err)
	}
	t.Cleanup(stopTun)

	closeFd(w) // the other end of the device is gone: the next read fails

	lost.waitLoss(t)
	if got := lost.reasons[0]; got == "" {
		t.Error("the loss comes with no reason")
	}
}

func TestTunnelLossIsReportedOnce(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	lost := watchLoss(t)
	r, w := pipePair(t)
	if err := startTun(r); err != nil {
		t.Fatalf("startTun: %v", err)
	}
	closeFd(w)
	lost.waitLoss(t)

	stopTun() // cleaning up after the loss must not report another one
	time.Sleep(300 * time.Millisecond)

	if n := lost.count(); n != 1 {
		t.Errorf("%d reports, want 1", n)
	}
}

// Stop ends the tunnel on purpose: that is not a loss, and the app would take
// every switch of server for one.
func TestStoppingTheTunnelIsNotALoss(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	lost := watchLoss(t)
	a, _ := socketPair(t)
	if err := startTun(a); err != nil {
		t.Fatalf("startTun: %v", err)
	}

	stopTun()
	time.Sleep(300 * time.Millisecond)

	if n := lost.count(); n != 0 {
		t.Errorf("a stop was reported as a loss (%d)", n)
	}
}

// A server switch stops one tunnel and starts the next at once: the end of the
// old one must not be taken for a loss of the new.
func TestRestartingTheTunnelIsNotALoss(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	lost := watchLoss(t)

	for round := 0; round < 3; round++ {
		a, _ := socketPair(t)
		if err := startTun(a); err != nil {
			t.Fatalf("round %d: startTun: %v", round, err)
		}
		stopTun()
	}
	time.Sleep(300 * time.Millisecond)

	if n := lost.count(); n != 0 {
		t.Errorf("%d reports over three restarts, want none", n)
	}
}

func TestTunnelLossWithNoHandlerIsHarmless(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	SetTunnelHandler(nil)
	r, w := pipePair(t)
	if err := startTun(r); err != nil {
		t.Fatalf("startTun: %v", err)
	}
	t.Cleanup(stopTun)

	closeFd(w)

	time.Sleep(300 * time.Millisecond) // a panic in the watcher would end the test run
}

func TestTunnelLossIsReportedOnTheIOPathToo(t *testing.T) {
	startableProxy(t)
	lost := watchLoss(t)

	if err := startTunIO(eofReadWriter{}); err != nil { // reads end at once
		t.Fatalf("startTunIO: %v", err)
	}
	t.Cleanup(stopTunIO)

	lost.waitLoss(t)
}

// DropTun is how the loss is made on a phone: it pulls the device out from
// under a running session.
func TestDropTunMakesTheTunnelLossOnPurpose(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	lost := watchLoss(t)
	SetVerboseLogs(true)
	t.Cleanup(func() { SetVerboseLogs(false) })
	a, _ := socketPair(t)
	if err := startTun(a); err != nil {
		t.Fatalf("startTun: %v", err)
	}

	if err := DropTun(); err != nil {
		t.Fatalf("DropTun: %v", err)
	}

	lost.waitLoss(t)
	stopTun() // the session can still be stopped the usual way
	if n := lost.count(); n != 1 {
		t.Errorf("%d reports, want 1", n)
	}
}

func TestDropTunIsForDebugBuildsOnly(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	lost := watchLoss(t)
	SetVerboseLogs(false)
	a, _ := socketPair(t)
	if err := startTun(a); err != nil {
		t.Fatalf("startTun: %v", err)
	}
	t.Cleanup(stopTun)

	if err := DropTun(); err == nil {
		t.Fatal("DropTun worked in a release build")
	}

	time.Sleep(300 * time.Millisecond)
	if n := lost.count(); n != 0 {
		t.Errorf("a refused DropTun still reported a loss (%d)", n)
	}
}

func TestDropTunWithNoTunnelSaysSo(t *testing.T) {
	SetVerboseLogs(true)
	t.Cleanup(func() { SetVerboseLogs(false) })

	if err := DropTun(); err == nil {
		t.Error("DropTun succeeded with no tunnel")
	}
}
