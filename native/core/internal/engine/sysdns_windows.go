//go:build windows

package engine

import (
	"net"

	"asterlink/core/internal/network"
)

// System DNS on Windows (handoff_app_windows.md 7.2, "DNS 与路由修改必须只有一个明确的所有者").
//
// The owner is the pinned sing-tun, not the Core, and nothing is written to disk:
//   - it gives the TUN adapter the DNS server TunnelDNS (tun_windows.go, SetDNS with the
//     address after the TUN's own) and routes the adapter with metric 0;
//   - with strict-route it adds Windows Filtering Platform filters, in a dynamic session
//     that the OS deletes when the process ends, that block port 53 on every other
//     interface for every process except this one. The Windows DNS Client queries all
//     adapters' servers in parallel, so without that block a router's DNS would still
//     answer (and the ISP would see every name).
// So there is no per-service record to restore and no way for a killed Core to leave the
// resolver pointing into a vanished tunnel; the adapter, its routes and the filters
// disappear with the process. What the Core does own is the control plane: its own
// lookups must reach the physical resolver, because the WFP block exempts only this
// process and the tunnel's resolver would hand it a fake-ip.

// bootstrapFallback is used when the physical adapter has no usable DNS server.
const bootstrapFallback = "1.1.1.1"

// captureSystemDNS records the physical adapter's resolvers for the control plane and
// verifies that the TUN adapter really carries TunnelDNS. physical is the interface name.
func captureSystemDNS(physical string) error {
	i, err := net.InterfaceByName(physical)
	if err != nil {
		return ErrDNSCapture
	}
	adapters, err := network.Adapters()
	if err != nil {
		return ErrDNSCapture
	}
	tunHasDNS := false
	for _, a := range adapters {
		if a.IsOwn() {
			for _, ip := range a.DNS {
				tunHasDNS = tunHasDNS || ip.String() == TunnelDNS
			}
		}
	}
	if !tunHasDNS {
		return ErrDNSCapture
	}
	resolvers := network.PhysicalResolvers(network.Interface{Name: i.Name, Index: i.Index})
	if len(resolvers) == 0 {
		resolvers = []string{bootstrapFallback}
	}
	network.Default.SetResolvers(resolvers)
	return nil
}

// RestoreSystemDNS forgets the bootstrap resolvers. Safe at any time; the system
// configuration itself is restored by the OS (see above).
func RestoreSystemDNS() error {
	network.Default.SetResolvers(nil)
	return nil
}
