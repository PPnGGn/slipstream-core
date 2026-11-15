package core

import (
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// The iOS extension lives under 50 MB, and the TCP buffers of every connection
// the stack terminates count against it: its tunnel sets small ranges (see
// tcpBufferOptions). A change of gVisor that dropped the option would show only
// as a jetsam on a phone nobody checks (iOS is frozen), so this pins it.
//
// The Android tunnel keeps gVisor's defaults, measured: tcpBufferOptions.
func TestTheIOTunnelHasTheSmallTCPBuffers(t *testing.T) {
	startableProxy(t)
	if err := startTunIO(eofReadWriter{}); err != nil {
		t.Fatalf("startTunIO: %v", err)
	}
	t.Cleanup(stopTunIO)
	ioMu.Lock()
	st := ioActive.stack
	ioMu.Unlock()

	var send tcpip.TCPSendBufferSizeRangeOption
	var recv tcpip.TCPReceiveBufferSizeRangeOption
	if err := st.TransportProtocolOption(tcp.ProtocolNumber, &send); err != nil {
		t.Fatalf("send buffer option: %v", err)
	}
	if err := st.TransportProtocolOption(tcp.ProtocolNumber, &recv); err != nil {
		t.Fatalf("receive buffer option: %v", err)
	}

	for what, got := range map[string][3]int{
		"send":    {send.Min, send.Default, send.Max},
		"receive": {recv.Min, recv.Default, recv.Max},
	} {
		if want := [3]int{tcpBufferMin, tcpBufferDefault, tcpBufferMax}; got != want {
			t.Errorf("%s buffers are %v, want %v", what, got, want)
		}
	}
}
