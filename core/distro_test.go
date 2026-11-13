package core

import (
	"bytes"
	"go/parser"
	"go/token"
	"io"
	"sort"
	"strconv"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
)

// distroImports reads the blank imports of distro.go.
func distroImports(t *testing.T) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "distro.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse distro.go: %v", err)
	}
	imports := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("import %s: %v", spec.Path.Value, err)
		}
		imports[path] = true
	}
	return imports
}

// distro.go is the list of the parts of xray the client relies on. The parser of
// the JSON config (infra/conf/serial) links every protocol and transport of xray
// whatever this file says, so a part that is missing here still works today, and
// no test that builds a config can notice: it would pass with an empty
// distro.go. That is how the DNS outbound the app's DNS hijack uses, the
// hysteria2 transport and the kcp transport came to be missing from the list
// while working. This test reads the list itself, so that it can fail.
func TestDistroListsWhatTheClientUses(t *testing.T) {
	const xray = "github.com/xtls/xray-core/"
	required := []struct{ pkg, why string }{
		{"app/dispatcher", "routes every connection"},
		{"app/dns", "the DNS client of the config"},
		{"app/dns/fakedns", "fakeDns, an allowed block, referenced from dns servers"},
		{"app/log", "the log block the core imposes"},
		{"app/policy", "levels, timeouts and the traffic counters"},
		{"app/proxyman/inbound", "the HTTP inbound for the app's own requests"},
		{"app/proxyman/outbound", "every outbound"},
		{"app/router", "routing rules"},
		{"app/stats", "QueryTraffic"},
		{"proxy/blackhole", "block rules"},
		{"proxy/dns", "the dns outbound of the DNS hijack"},
		{"proxy/freedom", "direct"},
		{"proxy/http", "the HTTP inbound on the Unix socket"},
		{"proxy/hysteria", "hysteria2 servers"},
		{"proxy/shadowsocks", "shadowsocks servers"},
		{"proxy/socks", "socks outbounds in provider configs"},
		{"proxy/trojan", "trojan servers"},
		{"proxy/vless/outbound", "vless servers"},
		{"proxy/vmess/outbound", "vmess servers"},
		{"transport/internet/finalmask/salamander", "hysteria2 obfuscation"},
		{"transport/internet/grpc", "grpc transport"},
		{"transport/internet/httpupgrade", "httpupgrade transport"},
		{"transport/internet/hysteria", "hysteria2 transport"},
		{"transport/internet/kcp", "mKCP transport"},
		{"transport/internet/reality", "reality"},
		{"transport/internet/splithttp", "xhttp transport"},
		{"transport/internet/tcp", "tcp transport"},
		{"transport/internet/tls", "tls"},
		{"transport/internet/udp", "udp transport"},
		{"transport/internet/websocket", "websocket transport"},
	}
	imports := distroImports(t)
	var missing []string
	for _, r := range required {
		if !imports[xray+r.pkg] {
			missing = append(missing, r.pkg+" ("+r.why+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("distro.go does not import %s", m)
	}
}

// Every import of distro.go is a blank one that exists in the module: a typo
// or a package xray dropped is a build error, but only on the day it is added.
func TestDistroImportsAreBlank(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "distro.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range file.Imports {
		if spec.Name == nil || spec.Name.Name != "_" {
			t.Errorf("import %s is not a blank import", spec.Path.Value)
		}
	}
}

// How a provider chains servers is sockopt.dialerProxy: an outbound that dials
// through another one. The xray this module is pinned to does it in its dialer,
// without the tagged package (that one is for the geodata downloads and the
// observatory probes, which the core removes from provider configs), so nothing
// has to be imported for it. This one dials through a second outbound to an
// echo server, to keep it so when xray changes.
func TestDialerProxyChainCarriesTraffic(t *testing.T) {
	const chain = `{"outbounds":[
		{"protocol":"freedom","tag":"proxy","streamSettings":{"sockopt":{"dialerProxy":"hop"}}},
		{"protocol":"freedom","tag":"hop"}
	]}`
	if err := startXray(chain, "", ""); err != nil {
		t.Fatalf("startXray: %v", err)
	}
	t.Cleanup(func() { _ = stopXray() })
	px, ok := liveProxy()
	if !ok {
		t.Fatal("no bridge to xray")
	}

	conn, err := px.DialContext(t.Context(), metadata(M.TCP, echoTCP(t)))
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo through the chain = %q, %v; want ping", got, err)
	}
}

// The configs of the app and the shapes of provider configs, built and started
// by the real xray. They pass whatever distro.go says (see above); what they
// prove is that the xray this module is pinned to still builds and starts
// each of them, which is what an update of xray-core can break.
func TestClientConfigsBuildAndStart(t *testing.T) {
	cases := map[string]string{
		// The DNS hijack of the app: the dns outbound and a rule to it.
		"dns_outbound": `{
			"outbounds":[
				{"protocol":"freedom","tag":"proxy"},
				{"protocol":"dns","tag":"dns-out","settings":{"nonIPQuery":"skip"}}
			],
			"routing":{"rules":[{"type":"field","inboundTag":["socks"],"port":"53","outboundTag":"dns-out"}]}
		}`,
		// A private address: xray refuses plaintext vless to any other.
		"kcp": `{
			"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"10.0.0.1","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","encryption":"none"}]}]},"streamSettings":{"network":"kcp","kcpSettings":{"mtu":1350}},"tag":"proxy"}]
		}`,
		// xray_config_builder.dart buildVlessReality: VLESS + Reality over tcp,
		// plus the socks(10808)+http(10809) inbounds and freedom/blackhole.
		"vless_reality_tcp": `{
			"inbounds":[
				{"listen":"127.0.0.1","port":10808,"protocol":"socks","settings":{"auth":"noauth","udp":true},"sniffing":{"destOverride":["http","tls"],"enabled":true},"tag":"socks"},
				{"listen":"127.0.0.1","port":10809,"protocol":"http","settings":{},"tag":"http"}
			],
			"outbounds":[
				{"protocol":"vless","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","encryption":"none","flow":"xtls-rprx-vision","level":8}]}]},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"publicKey":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","serverName":"example.com","shortId":"","fingerprint":"chrome"},"tcpSettings":{"header":{"type":"none"}}},"tag":"proxy"},
				{"protocol":"freedom","settings":{"domainStrategy":"UseIP"},"tag":"direct"},
				{"protocol":"blackhole","settings":{"response":{"type":"http"}},"tag":"block"}
			]
		}`,
		// buildShadowsocks: Shadowsocks over tcp.
		"shadowsocks_tcp": `{
			"inbounds":[{"listen":"127.0.0.1","port":10818,"protocol":"socks","settings":{"auth":"noauth","udp":true},"tag":"socks"}],
			"outbounds":[
				{"protocol":"shadowsocks","settings":{"servers":[{"address":"1.2.3.4","port":8388,"method":"aes-256-gcm","password":"pw","level":8}]},"streamSettings":{"network":"tcp"},"tag":"proxy"},
				{"protocol":"freedom","settings":{},"tag":"direct"}
			]
		}`,
		// The Fragment setting of the app: a tcp mask on the proxy outbound, here
		// over REALITY, where it cuts the ClientHello before REALITY writes it.
		"vless_reality_fragment": `{
			"outbounds":[
				{"protocol":"vless","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","encryption":"none","flow":"xtls-rprx-vision","level":8}]}]},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"publicKey":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","serverName":"example.com","shortId":"","fingerprint":"chrome"},"tcpSettings":{"header":{"type":"none"}},"finalmask":{"tcp":[{"type":"fragment","settings":{"packets":"tlshello","length":"100-200","delay":"10-20"}}]}},"tag":"proxy"}
			]
		}`,
		// The Noise setting of the app: a udp mask, next to the salamander mask
		// buildHysteria2 writes. Noise comes first, nearest to the wire.
		"hysteria2_noise": `{
			"outbounds":[
				{"protocol":"hysteria","settings":{"version":2,"address":"1.2.3.4","port":443},"streamSettings":{"network":"hysteria","security":"tls","tlsSettings":{"serverName":"example.com","alpn":["h3"]},"hysteriaSettings":{"version":2,"auth":"pw"},"finalmask":{"udp":[{"type":"noise","settings":{"reset":"0-0","noise":[{"rand":"10-20","delay":"10-16"}]}},{"type":"salamander","settings":{"password":"obfs"}}]}},"tag":"proxy"}
			]
		}`,
		// JSON-sub shapes the custom parser passes through: exercise the
		// remaining transports/security the curated set claims to support.
		"vmess_ws": `{
			"inbounds":[{"listen":"127.0.0.1","port":10828,"protocol":"socks","settings":{"auth":"noauth"},"tag":"socks"}],
			"outbounds":[{"protocol":"vmess","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","alterId":0,"security":"auto"}]}]},"streamSettings":{"network":"ws","security":"tls","wsSettings":{"path":"/ray"},"tlsSettings":{"serverName":"example.com"}},"tag":"proxy"}]
		}`,
		"trojan_grpc": `{
			"inbounds":[{"listen":"127.0.0.1","port":10838,"protocol":"socks","settings":{"auth":"noauth"},"tag":"socks"}],
			"outbounds":[{"protocol":"trojan","settings":{"servers":[{"address":"1.2.3.4","port":443,"password":"pw"}]},"streamSettings":{"network":"grpc","security":"tls","grpcSettings":{"serviceName":"gun"},"tlsSettings":{"serverName":"example.com"}},"tag":"proxy"}]
		}`,
		"vless_httpupgrade": `{
			"inbounds":[{"listen":"127.0.0.1","port":10848,"protocol":"socks","settings":{"auth":"noauth"},"tag":"socks"}],
			"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","encryption":"none"}]}]},"streamSettings":{"network":"httpupgrade","security":"tls","httpupgradeSettings":{"path":"/up"},"tlsSettings":{"serverName":"example.com"}},"tag":"proxy"}]
		}`,
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			pb, err := serial.DecodeJSONConfig(bytes.NewReader([]byte(cfg)))
			if err != nil {
				t.Fatalf("DecodeJSONConfig: %v", err)
			}
			coreCfg, err := pb.Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			inst, err := xcore.New(coreCfg)
			if err != nil {
				t.Fatalf("core.New (likely a missing blank-import in distro.go): %v", err)
			}
			_ = inst.Close()
		})
	}
}
