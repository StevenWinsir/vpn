//go:build darwin && !cgo

package main

import (
	"core/managed"
	"net"
	"net/netip"
	"os"
	"syscall"
	"testing"

	"golang.org/x/net/route"
)

func managedRouteFixture(prefix string, flags int) *route.RouteMessage {
	p := netip.MustParsePrefix(prefix)
	addresses := make([]route.Addr, syscall.RTAX_MAX)
	mask := net.IPMask(net.CIDRMask(p.Bits(), p.Addr().BitLen()))
	if p.Addr().Is4() {
		addresses[syscall.RTAX_DST] = &route.Inet4Addr{IP: p.Addr().As4()}
		addresses[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte(mask)}
	} else {
		addresses[syscall.RTAX_DST] = &route.Inet6Addr{IP: p.Addr().As16()}
		addresses[syscall.RTAX_NETMASK] = &route.Inet6Addr{IP: [16]byte(mask)}
	}
	return &route.RouteMessage{Flags: flags, Addrs: addresses}
}

func TestManagedMacOSRouteConflictClassification(t *testing.T) {
	for _, fixture := range []struct {
		name, prefix, device string
		flags                int
		conflict             bool
	}{
		{"physical IPv4 default", "0.0.0.0/0", "en1", syscall.RTF_UP, false},
		{"physical IPv6 default", "::/0", "en1", syscall.RTF_UP, false},
		{"VPN IPv4 default", "0.0.0.0/0", "utun8", syscall.RTF_UP, true},
		{"VPN IPv6 default", "::/0", "utun8", syscall.RTF_UP, true},
		{"macOS scoped tunnel default", "::/0", "utun0", syscall.RTF_UP | syscall.RTF_IFSCOPE, false},
		{"scoped route", "0.0.0.0/1", "utun0", syscall.RTF_UP | syscall.RTF_IFSCOPE, false},
		{"VPN lower IPv4 half", "0.0.0.0/1", "utun8", syscall.RTF_UP, true},
		{"VPN upper IPv4 half", "128.0.0.0/1", "utun8", syscall.RTF_UP, true},
		{"VPN lower IPv6 half", "::/1", "utun8", syscall.RTF_UP, true},
		{"VPN upper IPv6 half", "8000::/1", "utun8", syscall.RTF_UP, true},
		{"non-VPN occupied route", "128.0.0.0/1", "en1", syscall.RTF_UP, true},
		{"unknown interface occupied route", "128.0.0.0/1", "", syscall.RTF_UP, true},
		{"inactive route", "128.0.0.0/1", "utun8", 0, false},
		{"host cache", "128.0.0.0/1", "utun8", syscall.RTF_UP | syscall.RTF_HOST, false},
		{"neighbor cache", "::/1", "utun8", syscall.RTF_UP | syscall.RTF_LLINFO, false},
		{"ordinary link-local tunnel", "fe80::/64", "utun0", syscall.RTF_UP, false},
		{"LAN", "192.168.1.0/24", "en1", syscall.RTF_UP, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if got := managedRouteConflicts(managedRouteFixture(fixture.prefix, fixture.flags), fixture.device); got != fixture.conflict {
				t.Fatalf("conflict = %v, want %v", got, fixture.conflict)
			}
		})
	}
}

func TestManagedMacOSRoutePrefixDecoding(t *testing.T) {
	for _, prefix := range []string{"0.0.0.0/0", "128.0.0.0/1", "::/0", "8000::/1", "fe80::/64"} {
		got, ok := managedRoutePrefix(managedRouteFixture(prefix, syscall.RTF_UP))
		if !ok || got != netip.MustParsePrefix(prefix) {
			t.Fatalf("route prefix %s decoded as %v / %v", prefix, got, ok)
		}
	}
	if _, ok := managedRoutePrefix(&route.RouteMessage{}); ok {
		t.Fatal("empty route accepted")
	}
	entry := managedRouteFixture("128.0.0.0/1", syscall.RTF_UP)
	entry.Addrs[syscall.RTAX_NETMASK] = nil
	if _, ok := managedRoutePrefix(entry); ok {
		t.Fatal("non-default network without a mask accepted")
	}
	entry.Addrs[syscall.RTAX_DST] = &route.Inet4Addr{}
	if prefix, ok := managedRoutePrefix(entry); !ok || prefix.Bits() != 0 {
		t.Fatal("kernel default with omitted mask was not recognized")
	}
	entry.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 0, 255, 0}}
	if _, ok := managedRoutePrefix(entry); ok {
		t.Fatal("non-contiguous mask accepted")
	}
}

func TestManagedMacOSActiveVPNPreflight(t *testing.T) {
	if os.Getenv("RUN_MANAGED_ROUTE_INSPECTION_TEST") != "1" {
		t.Skip("read-only diagnostic requires an explicitly known active competing VPN")
	}
	if code := managed.PublicError(checkManagedTunRoutes()); code != "managed_tun_route_conflict" {
		t.Fatalf("active VPN preflight returned %s", code)
	}
	t.Log("active VPN detected without privilege, opening a TUN, or changing routes")
}
