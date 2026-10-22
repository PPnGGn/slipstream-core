package core

import "strings"

// forbiddenBlocks are top-level config blocks a provider config must not
// carry. Each one lets the config open a listener, touch the process
// environment or reach beyond the tunnel; the app needs none of them.
var forbiddenBlocks = []string{
	// gRPC management API with no authentication: HandlerService adds inbounds
	// (an open proxy on the LAN) and lists outbounds (server addresses, keys).
	"api",
	// HTTP server with /debug/pprof/* and expvar, no authentication: goroutine
	// stacks, heap contents, the process command line.
	"metrics",
	// os.Setenv for every entry, in the long-lived process. xray re-reads its
	// settings from the environment when an instance is created, so the config
	// can switch on the browser dialer (an HTTP/WebSocket server), move the
	// geo asset directory or set GODEBUG, and the values outlive the session.
	"env",
	// A cron schedule that downloads the listed URLs while the tunnel is up and
	// replaces the named files in the geo directory. That directory is shared
	// by every config, so one provider could swap the routing data of the rest.
	"geodata",
}

// dropForbiddenBlocks removes every forbidden block from cfg.
//
// Keys are compared case-insensitively: xray reads the config with
// encoding/json, which matches field names that way, so "API" would otherwise
// reach the same field as "api".
func dropForbiddenBlocks(cfg map[string]any) {
	for _, name := range forbiddenBlocks {
		deleteKey(cfg, name)
	}
}

// deleteKey removes every key of m that equals name, ignoring case. Deleting
// while ranging over a map is allowed in Go.
func deleteKey(m map[string]any, name string) {
	for key := range m {
		if strings.EqualFold(key, name) {
			delete(m, key)
		}
	}
}

// dropOutboundReverse removes "reverse" from the settings of every outbound.
//
// Since xray 26 the reverse proxy lives in the VLESS outbound ("settings":
// {"address": ..., "reverse": {"tag": ...}}); the old top-level "reverse"
// block only makes the build fail, so it needs no handling. With it the
// outbound turns into a bridge: a few seconds after Start it dials the server
// on its own, and every request the server sends back through that connection
// is dispatched from the phone to an address the server chooses, routed by
// the inbound tag the config gave it (a rule to "direct" reaches the user's
// LAN and localhost). No outbound the app uses needs it.
//
// Shapes that are not what xray expects (outbounds not a list, settings not an
// object) are left alone: xray rejects them anyway.
func dropOutboundReverse(cfg map[string]any) {
	for key, value := range cfg {
		if !strings.EqualFold(key, "outbounds") {
			continue
		}
		outbounds, _ := value.([]any)
		for _, o := range outbounds {
			outbound, _ := o.(map[string]any)
			for field, s := range outbound {
				if !strings.EqualFold(field, "settings") {
					continue
				}
				settings, _ := s.(map[string]any)
				deleteKey(settings, "reverse")
			}
		}
	}
}
