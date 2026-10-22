package core

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"

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
