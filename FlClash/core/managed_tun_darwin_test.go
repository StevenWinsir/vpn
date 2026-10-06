//go:build darwin && !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/sing_tun"
	"github.com/metacubex/mihomo/tunnel"
)

type managedTunProbeResult struct {
	metadata C.Metadata
	err      error
}

type managedTunProbe struct {
	C.Tunnel
	managedProviderAccess
	received chan managedTunProbeResult
}

func (p *managedTunProbe) HandleUDPPacket(packet C.UDPPacket, metadata *C.Metadata) {
	defer packet.Drop()
	if metadata.DstPort != 3478 || (metadata.DstIP != netip.MustParseAddr("198.19.253.2") && metadata.DstIP != netip.MustParseAddr("2001:db8:ffff::2")) {
		return
	}
	_, err := packet.WriteBack(packet.Data(), &net.UDPAddr{IP: metadata.DstIP.AsSlice(), Port: int(metadata.DstPort)})
	select {
	case p.received <- managedTunProbeResult{metadata: *metadata, err: err}:
	default:
	}
}

func TestManagedMacOSRealTUNUDPBothFamilies(t *testing.T) {
	if os.Getenv("RUN_MANAGED_TUN_TEST") != "1" {
		t.Skip("requires explicit opt-in on an isolated privileged macOS runner")
	}
	runManagedMacOSTUNUDP(t, false)
}

func TestManagedMacOSProductionTUNRoutes(t *testing.T) {
	if os.Getenv("RUN_MANAGED_TUN_FULL_ROUTE_TEST") != "1" {
		t.Skip("requires explicit opt-in: temporarily captures all routed traffic on an isolated macOS runner")
	}
	if os.Geteuid() != 0 {
		t.Fatal("production-route test requires root")
	}
	if os.Getenv("RUN_MANAGED_TUN_SETUID_TEST") == "1" {
		if os.Getuid() == 0 {
			t.Fatal("setuid regression must be launched by a non-root user, not sudo")
		}
		t.Log("verified installed-app identity: real UID is non-root, effective UID is root")
	}
	if err := exec.Command("/sbin/route", "-n", "get", "default").Run(); err != nil {
		t.Fatalf("regression requires an existing default route: %v", err)
	}
	t.Run("legacy default route collision", func(t *testing.T) {
		legacy := managedMacOSTunConfig()
		legacy.RouteAddress = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
		listener, err := sing_tun.New(legacy, tunnel.Tunnel)
		if listener != nil {
			if closeErr := listener.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		if !errors.Is(err, syscall.EEXIST) {
			t.Fatalf("expected the legacy default route collision, got: %v", err)
		}
		t.Logf("reproduced legacy startup failure: %v", err)
	})
	t.Run("competing VPN routes", func(t *testing.T) {
		occupied := managedMacOSTunConfig()
		occupied.Inet4Address = []netip.Prefix{netip.MustParsePrefix("198.19.252.1/30")}
		occupied.Inet6Address = []netip.Prefix{netip.MustParsePrefix("fdfe:252::1/126")}
		other, err := sing_tun.New(occupied, tunnel.Tunnel)
		if err != nil {
			t.Fatalf("start competing VPN fixture: %v", err)
		}
		defer func() {
			if err := other.Close(); err != nil {
				t.Errorf("close competing VPN fixture: %v", err)
			}
		}()
		legacy, err := sing_tun.New(managedMacOSTunConfig(), tunnel.Tunnel)
		if legacy != nil {
			_ = legacy.Close()
		}
		if !errors.Is(err, syscall.EEXIST) {
			t.Fatalf("expected paired /1 route collision, got %v", err)
		}
		t.Logf("reproduced occupied-route startup failure: %v", err)
		for attempt := 0; attempt < 2; attempt++ {
			listener, err := (macOSManagedNetwork{}).Open(context.Background(), nil, nil)
			if listener != nil || managed.PublicError(err) != "managed_tun_route_conflict" {
				t.Fatalf("preflight did not preserve the competing VPN: %v", err)
			}
		}
	})
	for _, name := range []string{"connect after other VPN disconnects", "reconnect after cleanup"} {
		t.Run(name, func(t *testing.T) { runManagedMacOSTUNUDP(t, true) })
	}
}

func runManagedMacOSTUNUDP(t *testing.T, productionRoutes bool) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("explicit real-TUN test requires root")
	}
	engine := newManagedTestEngine(t)
	if productionRoutes {
		engine.network = macOSManagedNetwork{}
		_ = applyManagedFixture(t, engine)
	}
	probe := &managedTunProbe{Tunnel: tunnel.Tunnel, managedProviderAccess: tunnel.Tunnel, received: make(chan managedTunProbeResult, 8)}
	plane := newManagedTrafficTunnel(probe)
	plane.managedProviderAccess = probe
	defer plane.stop()
	var listener io.Closer
	var err error
	if productionRoutes {
		listener, err = engine.networkPolicy().Open(context.Background(), currentConfig, plane)
	} else {
		cfg := managedMacOSTunConfig()
		cfg.RouteAddress = []netip.Prefix{netip.MustParsePrefix("198.19.253.2/32"), netip.MustParsePrefix("2001:db8:ffff::2/128")}
		cfg.DNSHijack = nil
		listener, err = sing_tun.New(cfg, plane)
	}
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
			select {
			case result := <-probe.received:
				if result.err != nil {
					t.Fatalf("TUN fixture could not send a UDP reply: %v", result.err)
				}
				metadata := result.metadata
				if metadata.NetWork != C.UDP || metadata.Type != C.TUN || metadata.DstPort != 3478 {
					t.Fatalf("unexpected tunnel metadata: %+v", metadata)
				}
			case <-ctx.Done():
				t.Fatal("packet bypassed managed data plane")
			}
			buffer := make([]byte, 256)
			n, err := conn.Read(buffer)
			if err != nil || !bytes.Equal(buffer[:n], payload) {
				t.Fatalf("UDP did not roundtrip through real TUN: %v", err)
			}
		})
	}
}
