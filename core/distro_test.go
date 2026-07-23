package core

import (
	"bytes"
	"testing"

	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
)

func TestCuratedDistroCoversClientConfigs(t *testing.T) {
	cases := map[string]string{
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
