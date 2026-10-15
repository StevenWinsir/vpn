//go:build android

package engine

import "asterlink/core/internal/network"

// System DNS on Android needs no capture and leaves nothing to restore: the VpnService
// Builder's addDnsServer(TunnelDNS) makes the tunnel the system resolver for as long as
// the VPN exists, and Android removes it with the VPN, including when the process is
// killed. The Core only records the default network's own resolvers so its protected
// control-plane lookups do not ask the tunnel (which would answer with fake-ip).

// bootstrapFallback is used when the default network reports no usable DNS server.
const bootstrapFallback = "1.1.1.1"

func captureSystemDNS(string) error {
	resolvers := network.HostResolvers()
	if len(resolvers) == 0 {
		resolvers = []string{bootstrapFallback}
	}
	network.Default.SetResolvers(resolvers)
	return nil
}

// RestoreSystemDNS forgets the bootstrap resolvers. Safe at any time.
func RestoreSystemDNS() error {
	network.Default.SetResolvers(nil)
	return nil
}
