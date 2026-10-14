package main

import (
	"net"
	"net/netip"
	"reflect"
	"testing"

	"asterlink/core/internal/network"
)

func TestClassifyDNS53(t *testing.T) {
	for out, want := range map[string]string{
		"198.18.0.7":                         "hijacked",
		"198.18.0.7 ":                        "hijacked",
		"":                                   "blocked",
		" ERR(i/o timeout)":                  "blocked",
		"93.184.216.34":                      "answered",
		"198.18.0.7,93.184.216.34":           "answered", // any real answer is a leak
		"2606:2800:220:1:248:1893:25c8:1946": "answered",
	} {
		if got := classifyDNS53(out); got != want {
			t.Errorf("classifyDNS53(%q) = %s, want %s", out, got, want)
		}
	}
}

func TestLanResolverTargets(t *testing.T) {
	a := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	adapters := []network.Adapter{
		{Name: "Ethernet", Up: true, DNS: []netip.Addr{a("192.168.1.1"), a("8.8.8.8"), a("fe80::1")}},
		{Name: "Wi-Fi", Up: true, DNS: []netip.Addr{a("192.168.1.1"), a("::ffff:9.9.9.9")}}, // duplicate + mapped v4
		{Name: network.TunName, Up: true, DNS: []netip.Addr{a("198.18.0.2")}},               // our own: never a LAN resolver
		{Name: "Unplugged", Up: false, DNS: []netip.Addr{a("10.0.0.1")}},
		{Name: "Loopback", Up: true, DNS: []netip.Addr{a("127.0.0.1"), a("0.0.0.0"), a("198.18.5.5")}},
	}
	want := []string{"192.168.1.1", "8.8.8.8", "9.9.9.9"}
	if got := lanResolverTargets(adapters, 6); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := lanResolverTargets(adapters, 2); len(got) != 2 {
		t.Fatalf("limit ignored: %v", got)
	}
}

func TestFirstIPv4(t *testing.T) {
	mk := func(s string) net.Addr {
		_, n, _ := net.ParseCIDR(s)
		ip, _, _ := net.ParseCIDR(s)
		return &net.IPNet{IP: ip, Mask: n.Mask}
	}
	got, ok := firstIPv4([]net.Addr{mk("fe80::1/64"), mk("169.254.3.4/16"), mk("2001:db8::5/64"), mk("192.168.1.20/24")})
	if !ok || got.String() != "192.168.1.20" {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := firstIPv4([]net.Addr{mk("169.254.3.4/16"), mk("::1/128")}); ok {
		t.Fatal("link-local or loopback chosen")
	}
}
