package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

// writeGeoFixtures drops a minimal geosite.dat + geoip.dat into dir, each with a
// single "TEST" category, so a config that references geosite:test / geoip:test
// can be built.
func writeGeoFixtures(t *testing.T, dir string) {
	t.Helper()

	site, err := proto.Marshal(&geodata.GeoSiteList{
		Entry: []*geodata.GeoSite{{
			Code: "TEST",
			Domain: []*geodata.Domain{
				{Type: geodata.Domain_Full, Value: "example.com"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal geosite: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), site, 0o644); err != nil {
		t.Fatalf("write geosite.dat: %v", err)
	}

	ip, err := proto.Marshal(&geodata.GeoIPList{
		Entry: []*geodata.GeoIP{{
			Code: "TEST",
			Cidr: []*geodata.CIDR{{Ip: []byte{192, 0, 2, 0}, Prefix: 24}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal geoip: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), ip, 0o644); err != nil {
		t.Fatalf("write geoip.dat: %v", err)
	}
}

// geoConfig is a client config whose routing needs both geosite.dat and
// geoip.dat to build. The proxy outbound points at a private IP: xray-core
// (>= mid-2026) rejects plaintext VLESS to any non-private address ("vless
// without TLS or other encryption is prohibited unless the server address is a
// private IP or domain" — "domain" there means localhost, not any hostname).
// This test only cares about geo asset loading, not transport.
const geoConfig = `{
	"inbounds":[{"listen":"127.0.0.1","port":19999,"protocol":"socks","settings":{"auth":"noauth","udp":true},"tag":"socks"}],
	"outbounds":[
		{"protocol":"vless","settings":{"vnext":[{"address":"10.0.0.1","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"none"},"tag":"proxy"},
		{"protocol":"freedom","settings":{},"tag":"direct"}
	],
	"routing":{"rules":[
		{"type":"field","domain":["geosite:test"],"outboundTag":"direct"},
		{"type":"field","ip":["geoip:test"],"outboundTag":"direct"}
	]}
}`

func TestBuildCoreConfig_GeoAssetsFromAssetDir(t *testing.T) {
	// GetAssetLocation is process-global env; keep the suite hermetic.
	t.Cleanup(func() {
		os.Unsetenv("xray.location.asset")
		os.Unsetenv("XRAY_LOCATION_ASSET")
	})

	t.Run("builds when the data files are in assetDir", func(t *testing.T) {
		dir := t.TempDir()
		writeGeoFixtures(t, dir)

		if _, err := buildCoreConfig(geoConfig, dir); err != nil {
			t.Fatalf("buildCoreConfig with assetDir=%q: %v", dir, err)
		}
	})

	t.Run("fails with a clear error when the data files are missing", func(t *testing.T) {
		empty := t.TempDir()

		_, err := buildCoreConfig(geoConfig, empty)
		if err == nil {
			t.Fatal("expected an error when geosite.dat is absent, got nil")
		}
		if !strings.Contains(err.Error(), "geosite.dat") {
			t.Fatalf("error should name the missing file, got: %v", err)
		}
	})
}
