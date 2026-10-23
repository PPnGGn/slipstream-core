package core

import "regexp"

// The patterns of an address in a line of the log, tried in this order: a
// destination xray names with its network ("tcp:example.com:443"), an IPv6
// address in brackets with a port, an IPv4 address with or without a port, and a
// name with a port. A bare IPv6 address is not matched: its shape is that of a
// time ("21:06:33"), and tun2socks always writes it in brackets.
var destinationPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`\b(tcp|udp):(?:\[[0-9A-Fa-f:.]+\]|[^\s:,>)\]]+)(?::\d{1,5})?`), "${1}:<addr>"},
	{regexp.MustCompile(`\[[0-9A-Fa-f]*:[0-9A-Fa-f:.]*\](?::\d{1,5})?`), "<addr>"},
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`), "<addr>"},
	{regexp.MustCompile(`\b[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+:\d{1,5}\b`), "<addr>"},
}

// redactDestinations replaces the addresses in a log line by "<addr>".
//
// The warnings of tun2socks and xray say where a connection was going
// ("[TCP] dial 93.184.216.34:443: refused", "dial tcp:example.com:443"). A
// release build logs warnings and errors and the app shows them in its log
// page, and where the user has been is not something the log of a VPN keeps.
// The rest of the line stays, so that the reason of a failure is still there.
// Debug builds are not redacted: they are for finding out what went wrong.
func redactDestinations(line string) string {
	for _, p := range destinationPatterns {
		line = p.re.ReplaceAllString(line, p.with)
	}
	return line
}

// releaseSafe is what a line looks like to the app: as it is in a debug build,
// without addresses otherwise.
func releaseSafe(line string, verbose bool) string {
	if verbose {
		return line
	}
	return redactDestinations(line)
}
