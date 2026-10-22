package core

import "strings"

// forbiddenBlocks are top-level config blocks a provider config must not
// carry. Each one lets the config open a listener, touch the process
// environment or reach beyond the tunnel; the app needs none of them.
var forbiddenBlocks = []string{
	// gRPC management API with no authentication: HandlerService adds inbounds
	// (an open proxy on the LAN) and lists outbounds (server addresses, keys).
	"api",
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
