package core

import "strings"

// allowedBlocks are the only top-level blocks of a provider config that reach
// xray. Everything else is removed, whether xray knows it today or only learns
// it in a later release: a block we have not looked at must not be a hole.
//
// Left out on purpose, each of them was one:
//   - api: gRPC management API with no authentication; HandlerService adds
//     inbounds (an open proxy on the LAN) and lists outbounds (server keys).
//   - metrics: HTTP server with /debug/pprof/* and expvar, no authentication.
//   - env: os.Setenv in the long-lived process; xray re-reads its settings
//     from the environment, so the config can switch on the browser dialer
//     (a listener), move the geo directory or set GODEBUG, for every later
//     session too.
//   - geodata: a cron job that downloads URLs and replaces the files of the
//     geo directory, shared by every config the app runs.
//   - reverse: removed from xray (the build fails); the VLESS reverse bridge
//     inside outbounds is handled by dropOutboundReverse.
//   - inbounds, log: hardenConfig sets its own. A provider key spelled
//     differently maps onto the same field in encoding/json and is merged into
//     ours, so every spelling must go first (an "inboundſ" inbound that
//     listens without a password, a log file path of the provider's choice).
//   - observatory, burstObservatory: background probes nobody asked for; the
//     app removes them too.
//   - version, transport: nothing the client needs.
var allowedBlocks = []string{
	"outbounds",
	"routing",
	"dns",
	"policy", // the app turns on the traffic counters through it
	"stats",
	"fakedns", // referenced from dns servers
}

// keepAllowedBlocks removes every key of cfg that is not an allowed block.
//
// Keys are compared case-insensitively: xray reads the config with
// encoding/json, which matches field names that way, so "API" would otherwise
// reach the same field as "api".
func keepAllowedBlocks(cfg map[string]any) {
	for key := range cfg {
		allowed := false
		for _, name := range allowedBlocks {
			if strings.EqualFold(key, name) {
				allowed = true
				break
			}
		}
		if !allowed {
			delete(cfg, key)
		}
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
