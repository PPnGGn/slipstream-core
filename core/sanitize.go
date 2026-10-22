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
	for key := range cfg {
		for _, name := range forbiddenBlocks {
			if strings.EqualFold(key, name) {
				delete(cfg, key)
				break
			}
		}
	}
}
