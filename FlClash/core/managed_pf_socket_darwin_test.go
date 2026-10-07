//go:build darwin && !cgo

package main

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// Use the same Darwin socket-level binding as Mihomo's physical dialer.
// LocalAddr alone is not sufficient once unscoped split-default routes exist.
func managedPhysicalSocketControl(index int) func(string, string, syscall.RawConn) error {
	return func(network, _ string, connection syscall.RawConn) error {
		var bindErr error
		err := connection.Control(func(fd uintptr) {
			level, option := unix.IPPROTO_IP, unix.IP_BOUND_IF
			if strings.HasSuffix(network, "6") {
				level, option = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
			}
			bindErr = unix.SetsockoptInt(int(fd), level, option, index)
		})
		if err != nil {
			return err
		}
		return bindErr
	}
}

// A reachable adjacent gateway avoids creating negative route-cache entries
// for deliberately unroutable documentation addresses. No public STUN service
// is used, and only a single small UDP probe is sent per privilege assertion.
func managedPhysicalProbeTarget(t *testing.T, device net.Interface, network string) string {
	t.Helper()
	rib, err := route.FetchRIB(syscall.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		entry, ok := message.(*route.RouteMessage)
		if !ok || entry.Index != device.Index || entry.Flags&syscall.RTF_GATEWAY == 0 {
			continue
		}
		prefix, valid := managedRoutePrefix(entry)
		if !valid || prefix.Bits() != 0 || prefix.Addr().Is4() != (network == "udp4") {
			continue
		}
		var address netip.Addr
		switch gateway := entry.Addrs[syscall.RTAX_GATEWAY].(type) {
		case *route.Inet4Addr:
			address = netip.AddrFrom4(gateway.IP)
		case *route.Inet6Addr:
			address = netip.AddrFrom16(gateway.IP)
			if address.IsLinkLocalUnicast() {
				address = address.WithZone(device.Name)
			}
		}
		if address.IsValid() {
			return net.JoinHostPort(address.String(), "32123")
		}
	}
	t.Fatal(fmt.Errorf("no %s physical gateway on %s for the isolated probe", network, device.Name))
	return ""
}
