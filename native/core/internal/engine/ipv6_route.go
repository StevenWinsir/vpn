//go:build !windows && !android && !ios

package engine

import (
	"net/netip"
	"os/exec"
)

// macOS IPv6 early refusal: reject routes more specific than the TUN's own IPv6 routes
// (the kernel removes them with the utun). Windows has no "reject" route type; it
// refuses IPv6 by leaving the TUN without an IPv6 address under strict-route, which
// makes sing-tun's WFP session block every IPv6 connect (ipv6_windows.go).

// singTunIPv6Ranges mirrors the darwin auto-route ranges of the pinned sing-tun
// (tun_rules.go, autoRouteUseSubRanges). Each reject route is one bit longer than one
// of these, so it wins the longest-prefix match while the pair covers the same space.
var singTunIPv6Ranges = []netip.Prefix{
	netip.MustParsePrefix("100::/8"),
	netip.MustParsePrefix("200::/7"),
	netip.MustParsePrefix("400::/6"),
	netip.MustParsePrefix("800::/5"),
	netip.MustParsePrefix("1000::/4"),
	netip.MustParsePrefix("2000::/3"),
	netip.MustParsePrefix("4000::/2"),
	netip.MustParsePrefix("8000::/1"),
}

// rejectPrefixes splits every TUN IPv6 range into its two halves.
func rejectPrefixes() []netip.Prefix {
	out := make([]netip.Prefix, 0, 2*len(singTunIPv6Ranges))
	for _, p := range singTunIPv6Ranges {
		bits := p.Bits() + 1
		low := netip.PrefixFrom(p.Addr(), bits)
		raw := p.Addr().As16()
		raw[(bits-1)/8] |= 0x80 >> ((bits - 1) % 8)
		out = append(out, low, netip.PrefixFrom(netip.AddrFrom16(raw), bits))
	}
	return out
}

// route runs /sbin/route with a fixed argument vector (no shell).
var route = func(args ...string) error {
	return exec.Command("/sbin/route", append([]string{"-n"}, args...)...).Run()
}

// addIPv6Reject installs the refusal routes via the utun gateway; on any failure the
// ones already added are removed again so the result is all or nothing.
func addIPv6Reject(gateway netip.Addr) error {
	var added []netip.Prefix
	for _, p := range rejectPrefixes() {
		if err := route("add", "-inet6", "-net", p.String(), gateway.String(), "-reject"); err != nil {
			removeIPv6Reject(gateway, added)
			return err
		}
		added = append(added, p)
	}
	return nil
}

func removeIPv6Reject(gateway netip.Addr, prefixes []netip.Prefix) {
	for _, p := range prefixes {
		_ = route("delete", "-inet6", "-net", p.String(), gateway.String())
	}
}

// applyIPv6Policy runs after the device exists. A node that carries IPv6 keeps it in the
// TUN; otherwise the refusal routes are installed.
func (e *Mihomo) applyIPv6Policy(tunConf tunConfig) {
	switch {
	case len(tunConf.Inet6Address) == 0:
		e.ipv6Mode = IPv6Absent
	case e.ipv6Capable:
		e.ipv6Mode = IPv6Tunnel
	default:
		gateway := tunConf.Inet6Address[0].Addr()
		if err := addIPv6Reject(gateway); err != nil {
			// Still no leak: IPv6 stays routed into the TUN, it just fails slowly.
			e.ipv6Mode = IPv6RejectFailed
			return
		}
		e.ipv6Mode, e.ipv6Gateway = IPv6Rejected, gateway
	}
}

// releaseIPv6Policy removes exactly the routes this session added.
func (e *Mihomo) releaseIPv6Policy() {
	if e.ipv6Mode == IPv6Rejected {
		removeIPv6Reject(e.ipv6Gateway, rejectPrefixes())
	}
	e.ipv6Mode, e.ipv6Gateway = "", netip.Addr{}
}

// prepareTunConf is the hook where a platform adjusts the TUN before it is created.
// macOS keeps IPv6 in the TUN and refuses it with routes afterwards.
func (e *Mihomo) prepareTunConf(tunConf tunConfig) tunConfig {
	return tunConf
}
