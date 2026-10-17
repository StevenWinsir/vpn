//go:build ios

package engine

import "net/netip"

// iOS IPv6 policy (handoff_app_ios.md 8.4). Unlike Android, an iOS tunnel without IPv6
// settings lets IPv6 leave through the physical interface, so ::/0 always goes into the
// tunnel. A node that cannot carry IPv6 is handled by an ingress filter (round 2) that
// refuses IPv6 before the local stack completes a handshake. Round 1 keeps the address and
// only records the mode; no tunnel can be opened yet.

func (e *Mihomo) prepareTunConf(tunConf tunConfig) tunConfig { return tunConf }

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

func (e *Mihomo) releaseIPv6Policy() {
	e.ipv6Mode, e.ipv6Gateway = "", netip.Addr{}
}
