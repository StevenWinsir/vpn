//go:build android

package engine

import "net/netip"

// Android IPv6 policy (handoff_app_android.md 7.3). When the node cannot carry IPv6 the
// VpnService is built without any IPv6 address or route, and Android then blocks the
// address family for every app while the VPN is up: an IPv6 connect fails immediately and
// Happy Eyeballs falls back to IPv4. Nothing leaks and no handshake completes locally
// against a node that cannot finish it. There is no route or filter for the Core to
// install or remove; the TUN simply carries no IPv6 address.

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

// releaseIPv6Policy: nothing to remove, the VPN's address families go with it.
func (e *Mihomo) releaseIPv6Policy() {
	e.ipv6Mode, e.ipv6Gateway = "", netip.Addr{}
}
