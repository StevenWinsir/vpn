package main

import (
	"net"
	"net/netip"
	"strings"
	"syscall"

	"golang.org/x/net/route"
)

func managedRoutePrefix(message *route.RouteMessage) (netip.Prefix, bool) {
	if len(message.Addrs) <= syscall.RTAX_NETMASK {
		return netip.Prefix{}, false
	}
	var address netip.Addr
	var mask net.IPMask
	switch destination := message.Addrs[syscall.RTAX_DST].(type) {
	case *route.Inet4Addr:
		address = netip.AddrFrom4(destination.IP)
		if value, ok := message.Addrs[syscall.RTAX_NETMASK].(*route.Inet4Addr); ok {
			mask = net.IPMask(value.IP[:])
		}
	case *route.Inet6Addr:
		address = netip.AddrFrom16(destination.IP)
		if value, ok := message.Addrs[syscall.RTAX_NETMASK].(*route.Inet6Addr); ok {
			mask = net.IPMask(value.IP[:])
		}
	default:
		return netip.Prefix{}, false
	}
	if message.Flags&syscall.RTF_HOST != 0 {
		return netip.PrefixFrom(address, address.BitLen()), true
	}
	if mask == nil {
		return netip.PrefixFrom(address, 0), address.IsUnspecified()
	}
	ones, bits := mask.Size()
	if bits != address.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(address, ones).Masked(), true
}

func managedRouteConflicts(message *route.RouteMessage, interfaceName string) bool {
	if message.Flags&syscall.RTF_UP == 0 || message.Flags&(syscall.RTF_IFSCOPE|syscall.RTF_LLINFO|syscall.RTF_HOST) != 0 {
		return false
	}
	prefix, ok := managedRoutePrefix(message)
	if !ok {
		return false
	}
	// Scoped link-local utun defaults belong to macOS too, not just third-party VPNs.
	if prefix.Bits() == 0 && strings.HasPrefix(interfaceName, "utun") {
		return true
	}
	for _, wanted := range managedMacOSTunConfig().RouteAddress {
		if prefix == wanted {
			return true
		}
	}
	return false
}

func checkManagedTunRoutes() error {
	rib, err := route.FetchRIB(syscall.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		managedLogError("[Managed TUN] route inspection failed: %v", err)
		return managedConfigError("managed_tun_route_check_failed")
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		managedLogError("[Managed TUN] route decoding failed: %v", err)
		return managedConfigError("managed_tun_route_check_failed")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		managedLogError("[Managed TUN] interface inspection failed: %v", err)
		return managedConfigError("managed_tun_route_check_failed")
	}
	names := make(map[int]string, len(interfaces))
	for _, networkInterface := range interfaces {
		names[networkInterface.Index] = networkInterface.Name
	}
	for _, message := range messages {
		entry, ok := message.(*route.RouteMessage)
		if !ok || !managedRouteConflicts(entry, names[entry.Index]) {
			continue
		}
		prefix, _ := managedRoutePrefix(entry)
		managedLogError("[Managed TUN] route conflict: %s on %s; disconnect the other VPN before retrying", prefix, names[entry.Index])
		return managedConfigError("managed_tun_route_conflict")
	}
	return nil
}
