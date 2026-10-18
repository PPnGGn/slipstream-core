package core

import (
	"sync/atomic"
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
