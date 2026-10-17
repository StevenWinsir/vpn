//go:build ios

package engine

import (
	"net/netip"

	"github.com/metacubex/mihomo/component/resolver"
)

// iOS IPv6 policy (handoff_app_ios.md 8.4). Unlike Android, an iOS tunnel without IPv6
// settings lets IPv6 leave through the physical interface, so the tunnel always claims ::/0
// (the template fixes the address). A node that carries IPv6 gets it proxied; for any other
// node the ingress relay refuses IPv6 before the local stack completes a handshake, and the
// fake-ip resolver stops answering AAAA so apps are not offered IPv6 in the first place.
// There is no "absent" mode on iOS: IPv6 is always either carried or refused.

// prepareTunConf puts the IPv6 address back when the pinned core dropped it (it does on a
// host without global IPv6), because the NE settings must always claim IPv6, and records
// whether the ingress must refuse IPv6. ipv6Capable was probed before the TUN existed.
func (e *Mihomo) prepareTunConf(tunConf tunConfig) tunConfig {
	rejectIPv6 = !e.ipv6Capable
	return iosTunConf(tunConf)
}

func (e *Mihomo) applyIPv6Policy(tunConfig) {
	if e.ipv6Capable {
		e.ipv6Mode = IPv6Tunnel
		return
	}
	e.ipv6Mode = IPv6Rejected
	resolver.DisableIPv6 = true
}

func (e *Mihomo) releaseIPv6Policy() {
	rejectIPv6 = false
	e.ipv6Mode, e.ipv6Gateway = "", netip.Addr{}
}
