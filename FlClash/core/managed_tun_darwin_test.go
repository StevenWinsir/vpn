//go:build darwin && !cgo

package main

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/sing_tun"
	"github.com/metacubex/mihomo/tunnel"
)

type managedTunProbe struct {
	C.Tunnel
	managedProviderAccess
	received chan C.Metadata
}

func (p *managedTunProbe) HandleUDPPacket(packet C.UDPPacket, metadata *C.Metadata) {
	defer packet.Drop()
	p.received <- *metadata
	_, _ = packet.WriteBack(packet.Data(), nil)
}

func TestManagedMacOSRealTUNUDPBothFamilies(t *testing.T) {
	if os.Getenv("RUN_MANAGED_TUN_TEST") != "1" {
		t.Skip("requires explicit opt-in on an isolated privileged macOS runner")
	}
	if os.Geteuid() != 0 {
		t.Fatal("explicit real-TUN test requires root")
	}
	_ = newManagedTestEngine(t)
	probe := &managedTunProbe{Tunnel: tunnel.Tunnel, managedProviderAccess: tunnel.Tunnel, received: make(chan C.Metadata, 8)}
	plane := newManagedTrafficTunnel(probe)
	plane.managedProviderAccess = probe
	defer plane.stop()
	cfg := managedMacOSTunConfig()
	cfg.RouteAddress = []netip.Prefix{netip.MustParsePrefix("198.19.253.2/32"), netip.MustParsePrefix("2001:db8:ffff::2/128")}
	cfg.DNSHijack = nil
	listener, err := sing_tun.New(cfg, plane)
	if err != nil {
		t.Fatalf("actual macOS TUN startup failed: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("actual TUN cleanup failed: %v", err)
		}
	}()
	for _, endpoint := range []struct{ network, address string }{{"udp4", "198.19.253.2:3478"}, {"udp6", "[2001:db8:ffff::2]:3478"}} {
		t.Run(endpoint.network, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, err := (&net.Dialer{}).DialContext(ctx, endpoint.network, endpoint.address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			payload := []byte("managed-utun-udp-roundtrip")
			if _, err := conn.Write(payload); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 256)
			n, err := conn.Read(buffer)
			if err != nil || !bytes.Equal(buffer[:n], payload) {
				t.Fatalf("UDP did not roundtrip through real TUN: %v", err)
			}
			select {
			case metadata := <-probe.received:
				if metadata.NetWork != C.UDP || metadata.Type != C.TUN || metadata.DstPort != 3478 {
					t.Fatalf("unexpected tunnel metadata: %+v", metadata)
				}
			case <-ctx.Done():
				t.Fatal("packet bypassed managed data plane")
			}
		})
	}
}
