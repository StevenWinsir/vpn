package main

import (
	"net"
	"net/netip"
	"strings"

	"asterlink/core/internal/network"
)

// Pure helpers behind the round-6 reliability cases (handoff 10, round 6). They are
// platform independent so their decisions are unit-tested on every host.

var fakeIPRange = netip.MustParsePrefix("198.18.0.0/16")

// classifyDNS53 interprets the one-line output of the "dns53" probe child
// ("addr,addr ERR(...)"):
//   - hijacked: the answer is a fake-ip, so the tunnel's resolver answered;
//   - blocked:  no address came back (the WFP block or a timeout);
//   - answered: a real, non-fake address came back, i.e. the aimed-at resolver replied
//     over the physical path. That is a DNS leak.
func classifyDNS53(out string) string {
	var real, fake int
	for _, field := range strings.FieldsFunc(out, func(r rune) bool { return r == ',' || r == ' ' }) {
		ip, err := netip.ParseAddr(field)
		if err != nil {
			continue
		}
		if fakeIPRange.Contains(ip) {
			fake++
		} else {
			real++
		}
	}
	switch {
	case real > 0:
		return "answered"
	case fake > 0:
		return "hijacked"
	default:
		return "blocked"
	}
}

// lanResolverTargets lists the IPv4 DNS servers configured on every adapter except the
// Core's own TUN: the DHCP router's DNS and any manually entered server, on all NICs, since
// the Windows DNS Client queries every adapter's servers in parallel. Loopback, unspecified
// and fake-ip addresses are skipped, duplicates removed, at most max returned.
func lanResolverTargets(adapters []network.Adapter, max int) []string {
	seen := map[netip.Addr]bool{}
	var out []string
	for _, a := range adapters {
		if a.IsOwn() || !a.Up {
			continue
		}
		for _, ip := range a.DNS {
			ip = ip.Unmap()
			if !ip.Is4() || ip.IsLoopback() || ip.IsUnspecified() || fakeIPRange.Contains(ip) || seen[ip] {
				continue
			}
			seen[ip] = true
			if out = append(out, ip.String()); len(out) == max {
				return out
			}
		}
	}
	return out
}

// firstIPv4 picks the address a socket would be bound to in order to test explicit
// physical source binding: global or private IPv4, never link-local or loopback.
func firstIPv4(addrs []net.Addr) (netip.Addr, bool) {
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := prefix.Addr().Unmap()
		if ip.Is4() && !ip.IsLinkLocalUnicast() && !ip.IsLoopback() && !ip.IsUnspecified() {
			return ip, true
		}
	}
	return netip.Addr{}, false
}
