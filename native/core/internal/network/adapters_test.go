package network

import (
	"net/netip"
	"slices"
	"testing"
)

func adapter(name, desc string, index int, ifType uint32, mut func(*Adapter)) Adapter {
	a := Adapter{Name: name, Description: desc, Index: index, IfType: ifType, Up: true, HasIPv4: true}
	if mut != nil {
		mut(&a)
	}
	return a
}

func TestPickPhysicalPrefersLowestMetricRealAdapter(t *testing.T) {
	adapters := []Adapter{
		adapter("Ethernet", "Realtek PCIe GbE", 5, 6, func(a *Adapter) { a.Gateway4, a.Metric4 = true, 25 }),
		adapter("Wi-Fi", "Intel(R) Wi-Fi 6 AX201", 9, 71, func(a *Adapter) { a.Gateway4, a.Metric4 = true, 40 }),
		// Hyper-V switch without a gateway: not a candidate, but also not a competitor.
		adapter("vEthernet (Default Switch)", "Hyper-V Virtual Ethernet Adapter", 12, 6, nil),
		// The company VPN has the lowest metric and a default route: it must never be picked.
		adapter("Corp VPN", "Cisco AnyConnect Secure Mobility Client Virtual Miniport Adapter", 20, 6, func(a *Adapter) { a.Gateway4, a.Metric4 = true, 1 }),
		adapter(TunName, "Wintun Userspace Tunnel", 30, 6, func(a *Adapter) { a.Gateway4, a.Metric4 = true, 0 }),
	}
	got, ok := PickPhysical(adapters)
	if !ok || got.Name != "Ethernet" {
		t.Fatalf("picked %+v ok=%v, want Ethernet", got, ok)
	}
}

func TestPickPhysicalFindsNothingOffline(t *testing.T) {
	down := adapter("Ethernet", "Realtek", 5, 6, func(a *Adapter) { a.Gateway4, a.Up = true, false })
	noGateway := adapter("Wi-Fi", "Intel", 9, 71, nil)
	if _, ok := PickPhysical([]Adapter{down, noGateway}); ok {
		t.Fatal("an adapter that is down or has no default gateway must not count as network")
	}
}

func TestPickPhysicalTieBreaksOnIndex(t *testing.T) {
	a := adapter("A", "x", 9, 6, func(a *Adapter) { a.Gateway4, a.Metric4 = true, 10 })
	b := adapter("B", "y", 3, 6, func(a *Adapter) { a.Gateway4, a.Metric4 = true, 10 })
	got, _ := PickPhysical([]Adapter{a, b})
	if got.Name != "B" {
		t.Fatalf("tie must resolve to the lower index, got %s", got.Name)
	}
}

func TestOtherVPNClassification(t *testing.T) {
	cases := []struct {
		name string
		a    Adapter
		want bool
	}{
		{"own tun is never a competitor", adapter(TunName, "Wintun Userspace Tunnel", 1, 6, nil), false},
		{"own tun matched case-insensitively", adapter("asterlink", "Wintun", 1, 6, nil), false},
		{"wireguard", adapter("wg0", "WireGuard Tunnel", 2, 6, nil), true},
		{"anyconnect", adapter("Ethernet 2", "Cisco AnyConnect Secure Mobility Client Virtual Miniport Adapter for Windows x64", 2, 6, nil), true},
		{"openvpn tap", adapter("Local Area Connection", "TAP-Windows Adapter V9", 2, 6, nil), true},
		{"clash verge wintun", adapter("Meta", "Wintun Userspace Tunnel", 2, 6, nil), true},
		{"disconnected vpn client owns no routes", adapter("wg0", "WireGuard Tunnel", 2, 6, func(a *Adapter) { a.Up = false }), false},
		{"hyper-v switch is not a vpn", adapter("vEthernet (WSL)", "Hyper-V Virtual Ethernet Adapter", 3, 6, nil), false},
		{"docker/vmware host-only is not a vpn", adapter("VMware Network Adapter VMnet8", "VMware Virtual Ethernet Adapter for VMnet8", 4, 6, nil), false},
		{"bluetooth pan is not a vpn", adapter("Bluetooth Network Connection", "Bluetooth Device (Personal Area Network)", 5, 6, nil), false},
		{"teredo transition tunnel without default route", adapter("Teredo Tunneling Pseudo-Interface", "Teredo Tunneling Pseudo-Interface", 6, 131, nil), false},
		{"built-in PPP/L2TP vpn with default route", adapter("Work L2TP", "WAN Miniport (L2TP)", 7, 23, func(a *Adapter) { a.Gateway4 = true }), true},
		{"loopback", adapter("Loopback Pseudo-Interface 1", "Software Loopback Interface 1", 1, 24, nil), false},
	}
	for _, c := range cases {
		if got := c.a.IsOtherVPN(); got != c.want {
			t.Errorf("%s: IsOtherVPN=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestBootstrapResolversKeepsReachableGlobalServersOnly(t *testing.T) {
	a := Adapter{DNS: []netip.Addr{
		netip.MustParseAddr("fe80::1"),          // link-local needs a zone
		netip.MustParseAddr("fec0:0:0:ffff::1"), // Windows' dead site-local default
		netip.MustParseAddr("192.168.1.1"),      // router
		netip.MustParseAddr("0.0.0.0"),          // unset
		netip.MustParseAddr("::ffff:223.5.5.5"), // v4-mapped is unmapped
		netip.MustParseAddr("2400:3200::1"),     // global v6
		netip.MustParseAddr("127.0.0.1"),        // a local stub is not a physical server
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("9.9.9.9"), // fifth valid server is dropped
	}}
	want := []string{"192.168.1.1", "223.5.5.5", "2400:3200::1", "8.8.8.8"}
	if got := BootstrapResolvers(a); !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := BootstrapResolvers(Adapter{DNS: []netip.Addr{netip.MustParseAddr("fe80::1")}}); got != nil {
		t.Fatalf("only link-local servers must yield nil so the caller falls back, got %v", got)
	}
}
