package core

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// The client's Fragment and Noise settings reach xray as finalmask masks on the
// `proxy` outbound (see lib/features/vpn/data/obfuscation_gate.dart): `fragment`
// on the tcp side, `noise` on the udp side. xray applies a tcp mask to the plain
// connection right after dialing, before TLS or REALITY start, and a udp mask
// to the datagrams of udp transports (kcp, hysteria).
//
// These two tests start the real xray and look at what a server on loopback
// receives. They keep the shape of the masks honest when xray-core is updated:
// a renamed field would only show as a warning in the log, and the traffic
// would go out unmasked.

// readsOf accepts one connection and records the size of every read until the
// peer is quiet for idle. The sizes show how the client cut its first write.
func readsOf(t *testing.T, idle time.Duration) (addr net.Addr, reads func() []int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	var (
		mu    sync.Mutex
		sizes []int
		done  = make(chan struct{})
	)
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		for {
			_ = c.SetReadDeadline(time.Now().Add(idle))
			n, err := c.Read(buf)
			if n > 0 {
				mu.Lock()
				sizes = append(sizes, n)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return l.Addr(), func() []int {
		<-done
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), sizes...)
	}
}

func TestFinalMaskFragmentCutsTheFirstWrite(t *testing.T) {
	// A Shadowsocks client over tcp. The server here is no Shadowsocks server,
	// it only records how the bytes arrive; the client writes a 32-byte salt
	// first, which is plenty to cut into pieces.
	addr, reads := readsOf(t, 600*time.Millisecond)
	port := addr.(*net.TCPAddr).Port
	p := startBridgeConfig(t, fmt.Sprintf(`{
		"outbounds": [{
			"protocol": "shadowsocks",
			"settings": {"servers": [{"address": "127.0.0.1", "port": %d, "method": "aes-256-gcm", "password": "pw"}]},
			"streamSettings": {
				"network": "tcp",
				"finalmask": {"tcp": [{"type": "fragment", "settings": {"packets": "1-1", "length": "4-4", "delay": "30-30"}}]}
			},
			"tag": "proxy"
		}]
	}`, port))

	c, err := p.DialContext(context.Background(), metadata(M.TCP, echoTCP(t)))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := reads()
	if len(got) < 3 {
		t.Fatalf("the server got %v: the first write was not cut into pieces", got)
	}
	for _, n := range got {
		if n > 4 {
			t.Fatalf("the server got %v: a piece is longer than the 4 bytes the mask asks for", got)
		}
	}
}

func TestFinalMaskNoiseGoesAheadOfTheFirstDatagram(t *testing.T) {
	// A VLESS client over mKCP, which is udp. The 33 bytes are not a size mKCP
	// or VLESS write on their own, so a datagram of that size is the noise.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port

	first := make(chan int, 1)
	go func() {
		buf := make([]byte, 2048)
		n, _, err := pc.ReadFrom(buf)
		if err == nil {
			first <- n
		}
	}()

	p := startBridgeConfig(t, fmt.Sprintf(`{
		"outbounds": [{
			"protocol": "vless",
			"settings": {"vnext": [{"address": "127.0.0.1", "port": %d, "users": [{"id": "11111111-1111-1111-1111-111111111111", "encryption": "none"}]}]},
			"streamSettings": {
				"network": "kcp",
				"kcpSettings": {"mtu": 1350},
				"finalmask": {"udp": [{"type": "noise", "settings": {"reset": "0-0", "noise": [{"rand": "33-33", "delay": "1-1"}]}}]}
			},
			"tag": "proxy"
		}]
	}`, port))

	c, err := p.DialContext(context.Background(), metadata(M.TCP, echoTCP(t)))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case n := <-first:
		if n != 33 {
			t.Fatalf("the first datagram is %d bytes, want the 33 bytes of noise", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server got no datagram at all")
	}
}
