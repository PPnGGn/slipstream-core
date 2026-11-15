package core

import (
	"sort"
	"strings"
)

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
	"policy", // levels and timeouts of the provider; ensureTrafficCounters adds the counters
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

// ensureTrafficCounters switches on the counters QueryTraffic reads: xray
// counts the bytes of an outbound only with a "stats" block and the two flags
// policy.system.statsOutboundUplink and statsOutboundDownlink. A provider config
// has neither, and the core used to leave it to the app to add them, so a
// config that came without them was counted at zero without a word.
//
// What else the provider put in policy (levels, timeouts) is kept. Keys are
// found ignoring case, as everywhere in this file, and the flags are set under
// one key each.
func ensureTrafficCounters(cfg map[string]any) {
	objectFold(cfg, "stats")
	system := objectFold(objectFold(cfg, "policy"), "system")
	for _, flag := range []string{"statsOutboundUplink", "statsOutboundDownlink"} {
		deleteKey(system, flag)
		system[flag] = true
	}
}

// objectFold returns the object stored in m under name, ignoring case, and
// makes an empty one when there is none or the value is not an object. A key
// given in several spellings would be merged by encoding/json in the order of
// the keys; here the first of them in sorted order is kept and the others are
// dropped, so the outcome does not depend on the order of a map.
func objectFold(m map[string]any, name string) map[string]any {
	var keys []string
	for key := range m {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		if object, ok := m[keys[0]].(map[string]any); ok {
			for _, other := range keys[1:] {
				delete(m, other)
			}
			return object
		}
	}
	for _, key := range keys {
		delete(m, key)
	}
	object := map[string]any{}
	m[name] = object
	return object
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

// hasKey reports whether m has a key equal to name, ignoring case.
func hasKey(m map[string]any, name string) bool {
	for key := range m {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
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

// dropOutboundRedirect removes "redirect" from the settings of every outbound.
//
// A freedom outbound ("direct") with "redirect" set overrides the destination
// of every connection routed to it, silently sending bypassed traffic (LAN
// bypass, geo bypass, custom direct rules) to an address chosen by the
// provider — including 127.0.0.1 or the user's LAN — instead of the real
// destination. No other outbound protocol the client uses has a "redirect"
// setting.
func dropOutboundRedirect(cfg map[string]any) {
	forEachObject(cfg, []string{"outbounds", "[]", "settings"}, func(settings map[string]any) {
		deleteKey(settings, "redirect")
	})
}

// dropTLSFilePaths removes local file paths from tlsSettings and
// realitySettings of every outbound: masterKeyLog (which makes xray open and
// append TLS session keys to a file of the provider's choice) and
// certificateFile / keyFile inside tlsSettings.certificates (which make xray
// read files from the phone's disk at Build time). Certificate entries left
// without an inline "certificate" are dropped so xray does not fail on the
// emptied entry.
func dropTLSFilePaths(cfg map[string]any) {
	for _, sec := range []string{"tlsSettings", "realitySettings"} {
		forEachObject(cfg, []string{"outbounds", "[]", "streamSettings", sec}, func(s map[string]any) {
			deleteKey(s, "masterKeyLog")
		})
	}
	forEachObject(cfg, []string{"outbounds", "[]", "streamSettings", "tlsSettings"}, func(tls map[string]any) {
		for key, raw := range tls {
			if !strings.EqualFold(key, "certificates") {
				continue
			}
			certs, ok := raw.([]any)
			if !ok {
				continue
			}
			kept := make([]any, 0, len(certs))
			for _, item := range certs {
				cert, ok := item.(map[string]any)
				if !ok {
					kept = append(kept, item)
					continue
				}
				deleteKey(cert, "certificateFile")
				deleteKey(cert, "keyFile")
				if hasKey(cert, "certificate") {
					kept = append(kept, cert)
				}
			}
			tls[key] = kept
		}
	})
}

// dropOutboundSocketOverrides removes low-level socket and local-interface
// bindings from every outbound:
//   - sendThrough: binds the outbound dialer to a local IP or Unix socket path;
//   - streamSettings.dsSettings: Unix domain socket transport on the phone;
//   - streamSettings.sockopt.{interface,mark,tproxy,customSockopt,bindAddress,bindPort}:
//     binds to a specific network interface, sets SO_MARK/tproxy, or invokes
//     arbitrary setsockopt syscalls.
//
// sockopt.dialerProxy is preserved so provider configs can chain outbounds.
func dropOutboundSocketOverrides(cfg map[string]any) {
	forEachObject(cfg, []string{"outbounds", "[]"}, func(outbound map[string]any) {
		deleteKey(outbound, "sendThrough")
	})
	forEachObject(cfg, []string{"outbounds", "[]", "streamSettings"}, func(stream map[string]any) {
		deleteKey(stream, "dsSettings")
	})
	forEachObject(cfg, []string{"outbounds", "[]", "streamSettings", "sockopt"}, func(sockopt map[string]any) {
		for _, field := range []string{"interface", "mark", "tproxy", "customSockopt", "bindAddress", "bindPort"} {
			deleteKey(sockopt, field)
		}
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
