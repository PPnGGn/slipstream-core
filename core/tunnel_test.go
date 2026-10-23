package core

import (
	"io"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
)

// fakeRelayedConn stands in for a connection tun2socks relays: its manager
// only needs an ID and Close.
type fakeRelayedConn struct {
	id     string
	closed atomic.Bool
}

func (c *fakeRelayedConn) ID() string { return c.id }

func (c *fakeRelayedConn) Close() error {
	c.closed.Store(true)
	statistic.DefaultManager.Leave(c) // what the real trackers do
	return nil
}

// socketPair returns two connected datagram sockets: one stands in for the TUN
// descriptor (one datagram per packet), the other for the kernel side. The
// test owns b; a belongs to the code under test.
func socketPair(t *testing.T) (a, b int) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	t.Cleanup(func() { syscall.Close(fds[1]) })
	return fds[0], fds[1]
}

func fdIsOpen(fd int) bool {
	var st syscall.Stat_t
	return syscall.Fstat(fd, &st) == nil
}

// fileDevice is a packet device over an os.File that it closes itself, once.
type fileDevice struct {
	*iobased.Endpoint
	file *os.File
}

func (d fileDevice) Name() string { return d.file.Name() }
func (d fileDevice) Type() string { return "file" }
func (d fileDevice) Close() {
	d.Endpoint.Close()
	d.file.Close()
}

// useFileDevices makes StartTun open descriptors as fileDevice, see
// openTunDevice for why the real one is not used in tests. With stuckRead the
// descriptor stays blocking, so closing the file does not wake the read that
// waits on it, like a TUN read that never returns.
func useFileDevices(t *testing.T, stuckRead ...bool) {
	t.Helper()
	old := openTunDevice
	openTunDevice = func(fd int) (device.Device, error) {
		// Non-blocking, so that closing the file wakes a read waiting on it.
		if len(stuckRead) == 0 || !stuckRead[0] {
			if err := syscall.SetNonblock(fd, true); err != nil {
				return nil, err
			}
		}
		file := os.NewFile(uintptr(fd), "tun-test")
		ep, err := iobased.New(file, defaultMTU, 0)
		if err != nil {
			return nil, err
		}
		return fileDevice{Endpoint: ep, file: file}, nil
	}
	t.Cleanup(func() { openTunDevice = old })
}

// startableProxy makes StartTun believe xray is running.
func startableProxy(t *testing.T) {
	t.Helper()
	setActiveProxy(proxyParams{socksPort: 1111, httpPort: 2222, user: "u", pass: "p"})
	t.Cleanup(clearActiveProxy)
}

// tun2socks' engine ends the whole process (log.Fatalf, exit status 1) when it
// cannot start, and StartTun used to report success regardless. Whatever the
// descriptor is, StartTun has to answer with an error the caller can handle.
func TestStartTunRejectsAnInvalidDescriptor(t *testing.T) {
	startableProxy(t)

	err := StartTun(-1)
	if err == nil {
		StopTun()
		t.Fatal("StartTun(-1) = nil, want an error")
	}
}

func TestStartTunRunsUntilStopTunReleasesTheDescriptor(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	a, _ := socketPair(t)

	if err := StartTun(a); err != nil {
		t.Fatalf("StartTun: %v", err)
	}
	if !fdIsOpen(a) {
		t.Fatal("the descriptor is closed while the tunnel runs")
	}

	StopTun()
	if fdIsOpen(a) {
		syscall.Close(a)
		t.Errorf("descriptor %d is still open after StopTun", a)
	}
}

// The app calls Stop on its main thread. A read that never returns after the
// descriptor is closed must not keep it waiting for ever.
func TestStopTunDoesNotHangOnAStuckRead(t *testing.T) {
	startableProxy(t)
	useFileDevices(t, true)
	a, _ := socketPair(t)
	if err := StartTun(a); err != nil {
		t.Fatalf("StartTun: %v", err)
	}

	done := make(chan struct{})
	go func() {
		StopTun()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("StopTun is still waiting for the read side after 6 s")
	}
}

// A second StartTun while one tunnel runs would build a second stack next to
// the first and lose track of it. It is refused, and, since StartTun owns the
// descriptor from the call on, the new descriptor is closed.
func TestStartTunRefusesASecondStart(t *testing.T) {
	startableProxy(t)
	useFileDevices(t)
	a, _ := socketPair(t)
	if err := StartTun(a); err != nil {
		t.Fatalf("first StartTun: %v", err)
	}
	t.Cleanup(StopTun)

	c, _ := socketPair(t)
	if err := StartTun(c); err == nil {
		t.Error("second StartTun succeeded while a tunnel runs")
	}
	if fdIsOpen(c) {
		syscall.Close(c)
		t.Errorf("descriptor %d of the refused start is still open", c)
	}
	if !fdIsOpen(a) {
		t.Error("the running tunnel lost its descriptor")
	}
}

// eofReadWriter ends the read side at once, which lets the stack start and stop
// without a packet source.
type eofReadWriter struct{}

func (eofReadWriter) Read([]byte) (int, error)    { return 0, io.EOF }
func (eofReadWriter) Write(b []byte) (int, error) { return len(b), nil }

func TestStartTunIOStartsStopsAndRestarts(t *testing.T) {
	startableProxy(t)

	for round := 1; round <= 2; round++ {
		if err := StartTunIO(eofReadWriter{}); err != nil {
			t.Fatalf("round %d: StartTunIO: %v", round, err)
		}
		if err := StartTunIO(eofReadWriter{}); err == nil {
			t.Errorf("round %d: second StartTunIO succeeded while a tunnel runs", round)
		}
		StopTunIO()
	}
}

func TestStartTunIOWithoutXray(t *testing.T) {
	clearActiveProxy()
	if err := StartTunIO(eofReadWriter{}); err == nil {
		StopTunIO()
		t.Error("StartTunIO succeeded with no xray running")
	}
}

// StartTun owns the descriptor from the call on, so it must close it when it
// refuses to start, too.
func TestStartTunClosesFdWhenXrayIsNotRunning(t *testing.T) {
	clearActiveProxy()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	fd, err := syscall.Dup(int(r.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}

	if err := StartTun(fd); err == nil {
		t.Fatal("StartTun() succeeded with no xray running")
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) == nil {
		syscall.Close(fd)
		t.Errorf("descriptor %d is still open after a refused StartTun", fd)
	}
}

func TestStopTunClosesRelayedConns(t *testing.T) {
	a, b := &fakeRelayedConn{id: "test-a"}, &fakeRelayedConn{id: "test-b"}
	statistic.DefaultManager.Join(a)
	statistic.DefaultManager.Join(b)
	t.Cleanup(func() {
		statistic.DefaultManager.Leave(a)
		statistic.DefaultManager.Leave(b)
	})

	StopTun()

	if !a.closed.Load() || !b.closed.Load() {
		t.Errorf("closed: a=%v b=%v, want both", a.closed.Load(), b.closed.Load())
	}
	for _, c := range statistic.DefaultManager.Snapshot().Connections {
		if c.ID() == a.id || c.ID() == b.id {
			t.Errorf("%s still tracked after StopTun", c.ID())
		}
	}
}
