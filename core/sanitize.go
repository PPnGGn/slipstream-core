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
	// hardenConfig sets its own inbounds, so the provider's must go first, in
	// every spelling. Setting ours is not enough: when two keys of the config
	// map onto the same field, encoding/json decodes both into it, in key
	// order, and the provider's key can sort after ours ("inboundſ", with the
	// long s, folds to "inbounds" but sorts later) and win, or merge with ours
	// into an inbound that listens without a password.
	"inbounds",
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
	forEachObject(cfg, []string{"outbounds", "[]", "settings"}, func(settings map[string]any) {
		deleteKey(settings, "reverse")
	})
}

// dropFinalmaskPortMapping removes "portMapping" from the settings of every
// finalmask of every outbound.
//
// The realm mask can ask the user's router for a port mapping (UPnP or
// NAT-PMP) when the transport starts. That opens a port on the router, from
// the internet, that forwards to the phone's UDP socket, and discovering the
// gateway is itself traffic on the local network. The mapping outlives the
// connection for its lifetime and is the provider's choice, not the user's.
// The realm mask keeps working without it.
func dropFinalmaskPortMapping(cfg map[string]any) {
	for _, layer := range []string{"tcp", "udp"} {
		path := []string{"outbounds", "[]", "streamSettings", "finalmask", layer, "[]", "settings"}
		forEachObject(cfg, path, func(settings map[string]any) {
			deleteKey(settings, "portMapping")
		})
	}
}

// forEachObject calls visit for every JSON object reached from node by
// following path. A path element is an object key, matched ignoring case, or
// "[]", which steps into each element of a list. A node of any other shape
// ends that branch of the walk: xray rejects such configs anyway.
func forEachObject(node any, path []string, visit func(map[string]any)) {
	if len(path) == 0 {
		if object, ok := node.(map[string]any); ok {
			visit(object)
		}
		return
	}
	step, rest := path[0], path[1:]
	if step == "[]" {
		list, _ := node.([]any)
		for _, item := range list {
			forEachObject(item, rest, visit)
		}
		return
	}
	object, _ := node.(map[string]any)
	for key, child := range object {
		if strings.EqualFold(key, step) {
			forEachObject(child, rest, visit)
		}
	}
}
