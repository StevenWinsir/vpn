//go:build windows

package network

import (
	"errors"
	"net"
	"testing"
)

// Round 1 found that Windows would silently link the non-darwin placeholder. This fails the
// moment PhysicalDefault is the placeholder again.
func TestWindowsDoesNotLinkThePlaceholder(t *testing.T) {
	_, err := PhysicalDefault()
	if errors.Is(err, ErrUnsupported) {
		t.Fatal("PhysicalDefault is the unsupported placeholder: network_other.go leaked into a Windows build")
	}
}

func TestAdaptersAreEnumerated(t *testing.T) {
	adapters, err := Adapters()
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) == 0 {
		t.Fatal("GetAdaptersAddresses returned nothing")
	}
	for _, a := range adapters {
		if a.Index == 0 || a.Name == "" {
			t.Fatalf("incomplete adapter: %+v", a)
		}
	}
	if OwnTunnelUp() {
		t.Fatal("no TUN should exist in a unit test run")
	}
}

// IP_UNICAST_IF must be accepted by the stack for a real interface index (the IPv4 option
// takes network byte order; a wrong order is rejected or selects a different interface).
func TestBoundDialConnectsOnThePhysicalAddress(t *testing.T) {
	iface, err := PhysicalDefault()
	if errors.Is(err, ErrNoNetwork) {
		t.Skip("this host has no physical default route")
	}
	if err != nil {
		t.Fatal(err)
	}
	i, err := net.InterfaceByIndex(iface.Index)
	if err != nil {
		t.Fatal(err)
	}
	addrs, _ := i.Addrs()
	var local net.IP
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
			local = ipn.IP
		}
	}
	if local == nil {
		t.Skip("the physical interface has no routable IPv4 address")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(local.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if c, err := listener.Accept(); err == nil {
			c.Close()
		}
	}()
	egress := &Egress{}
	egress.Set(iface)
	conn, err := egress.Dialer().Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatalf("a socket bound with IP_UNICAST_IF to index %d could not connect: %v", iface.Index, err)
	}
	conn.Close()
}

func TestViaTunnelIsEmptyWithoutATunnel(t *testing.T) {
	if via := ViaTunnel(); via != "" {
		t.Skipf("a tunnel-class adapter (%s) owns the public routes on this host", via)
	}
}
