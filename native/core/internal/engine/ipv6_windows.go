//go:build windows

package engine

import "net/netip"

// Windows IPv6 policy. There is no "reject route" on Windows, so the early refusal is
// done by the pinned sing-tun itself: with strict-route and a TUN that has NO IPv6
// address, it adds a Windows Filtering Platform filter that blocks every IPv6 connect
// except this process's own (tun_windows.go, "block ipv6"). Applications get an
// immediate WSAEACCES instead of a handshake that completes against the local stack and
// then dies, so Happy Eyeballs falls back to IPv4 at once. The filter lives in a
// FWPM_SESSION_FLAG_DYNAMIC session, which the OS deletes when the process ends, even if
// the process is killed.
//
// A node that carries IPv6 keeps its IPv6 address in the TUN and the traffic is proxied.
// The decision is taken before the TUN is created (the probe already ran), because the
// WFP rules are installed when the adapter is configured.

// prepareTunConf removes the TUN's IPv6 address when the node cannot carry IPv6.
func (e *Mihomo) prepareTunConf(tunConf tunConfig) tunConfig {
	if !e.ipv6Capable {
		tunConf.Inet6Address = nil
	}
	return tunConf
}

// applyIPv6Policy records the mode; tunConf is the configuration BEFORE prepareTunConf.
func (e *Mihomo) applyIPv6Policy(tunConf tunConfig) {
	switch {
	case len(tunConf.Inet6Address) == 0:
		e.ipv6Mode = IPv6Absent
	case e.ipv6Capable:
		e.ipv6Mode = IPv6Tunnel
	default:
		e.ipv6Mode = IPv6Rejected
	}
}

// releaseIPv6Policy: nothing to remove, the OS owns the filters.
func (e *Mihomo) releaseIPv6Policy() {
	e.ipv6Mode, e.ipv6Gateway = "", netip.Addr{}
}
