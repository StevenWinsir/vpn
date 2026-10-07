//go:build !cgo

package main

import (
	"context"
	"core/managed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
	singtun "github.com/metacubex/sing-tun"
	"go4.org/netipx"
)

type networkFixture struct {
	loopbackManagedNetwork
	openError     error
	closeError    error
	partialOpen   bool
	opens, closes int
}

func (n *networkFixture) Open(context.Context, *config.Config, C.Tunnel) (io.Closer, error) {
	n.opens++
	if n.openError != nil {
		if n.partialOpen {
			return n, n.openError
		}
		return nil, n.openError
	}
	return n, nil
}
func (n *networkFixture) Close() error { n.closes++; return n.closeError }

func managedTestPort(t *testing.T) int {
	t.Helper()
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestManagedMacOSTUNPolicy(t *testing.T) {
	policy := macOSManagedNetwork{}
	raw := &config.RawConfig{}
	policy.Configure(raw)
	if !raw.IPv6 || raw.Mode != tunnel.Rule || !raw.DNS.Enable || !raw.DNS.IPv6 || !raw.DNS.RespectRules || raw.DNS.EnhancedMode != C.DNSFakeIP || len(raw.DNS.ProxyServerNameserver) == 0 || raw.DNS.Listen != "" {
		t.Fatal("macOS managed DNS policy does not cover both address families through proxy rules")
	}
	tun := managedMacOSTunConfig()
	if !tun.Enable || !tun.AutoRoute || !tun.AutoDetectInterface || !tun.StrictRoute || tun.Stack != C.TunGvisor || !tun.DisableICMPForwarding || len(tun.Inet4Address) == 0 || len(tun.Inet6Address) == 0 || len(tun.RouteExcludeAddress) != 0 || !slices.Contains(tun.DNSHijack, "any:53") || !slices.Contains(tun.DNSHijack, "tcp://any:53") {
		t.Fatal("macOS TUN policy omitted full TCP/UDP/IPv6 capture or allowed direct ICMP")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if closer, err := policy.Open(ctx, nil, nil); closer != nil || managed.PublicError(err) != "operation_superseded" {
		t.Fatal("cancelled TUN opening was not rejected")
	}
	if os.Geteuid() != 0 {
		if closer, err := policy.Open(context.Background(), nil, nil); closer != nil || managed.PublicError(err) != "managed_tun_permission_required" {
			t.Fatal("unprivileged TUN opening was not rejected")
		}
	}
}

func TestManagedMacOSRoutesCoverBothFamiliesWithoutReplacingDefaults(t *testing.T) {
	cfg := managedMacOSTunConfig()
	options := singtun.Options{AutoRoute: cfg.AutoRoute, Inet4Address: cfg.Inet4Address, Inet6Address: cfg.Inet6Address}
	for _, prefix := range cfg.RouteAddress {
		if prefix.Addr().Is4() {
			options.Inet4RouteAddress = append(options.Inet4RouteAddress, prefix)
		} else {
			options.Inet6RouteAddress = append(options.Inet6RouteAddress, prefix)
		}
	}
	routes, err := options.BuildAutoRouteRanges(false)
	if err != nil {
		t.Fatal(err)
	}
	var coverage netipx.IPSetBuilder
	for _, prefix := range routes {
		if prefix.Bits() == 0 {
			t.Fatalf("TUN would add an existing default route instead of more-specific routes: %s", prefix)
		}
		coverage.AddPrefix(prefix)
	}
	set, err := coverage.IPSet()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"0.0.0.0/0", "::/0"} {
		if !set.ContainsPrefix(netip.MustParsePrefix(family)) {
			t.Fatalf("TUN routes leave a capture gap in %s: %v", family, routes)
		}
	}
}

func TestManagedMacOSConfigurationAppliesWithoutOpeningTUN(t *testing.T) {
	engine := newManagedTestEngine(t)
	engine.network = macOSManagedNetwork{}
	_ = applyManagedFixture(t, engine)
	if !currentConfig.General.IPv6 || !currentConfig.DNS.Enable || !currentConfig.DNS.IPv6 || currentConfig.General.Tun.Enable || isRunning.Load() || managedNetworkCloser != nil {
		t.Fatal("prepared macOS configuration omitted DNS/IPv6 or prematurely opened TUN")
	}
}

func TestManagedTUNOwnershipAndNoSystemProxyFallback(t *testing.T) {
	for _, code := range []string{"managed_tun_permission_required", "managed_tun_start_failed", "managed_tun_route_conflict", "managed_tun_route_check_failed"} {
		t.Run(code, func(t *testing.T) {
			engine := newManagedTestEngine(t)
			view := applyManagedFixture(t, engine)
			network := &networkFixture{openError: managedConfigError(code)}
			engine.network = network
			port := managedTestPort(t)
			if err := engine.Start(context.Background(), view.Owner, view.ID, port); managed.PublicError(err) != code {
				t.Fatalf("lost TUN error: %v", err)
			}
			if isRunning.Load() || managedNetworkCloser != nil || network.opens != 1 {
				t.Fatal("failed TUN reported an active runtime")
			}
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
			if err == nil {
				conn.Close()
				t.Fatal("failed TUN silently left mixed proxy available")
			}
		})
	}
	for _, failCleanup := range []bool{false, true} {
		t.Run(fmt.Sprintf("partially opened guard cleanup failure=%t", failCleanup), func(t *testing.T) {
			engine := newManagedTestEngine(t)
			view := applyManagedFixture(t, engine)
			network := &networkFixture{openError: managedConfigError("managed_tun_start_failed"), partialOpen: true}
			if failCleanup {
				network.closeError = errors.New("synthetic guard cleanup failure")
			}
			engine.network = network
			port := managedTestPort(t)
			expected := "managed_tun_start_failed"
			if failCleanup {
				expected = "managed_tun_cleanup_failed"
			}
			if err := engine.Start(context.Background(), view.Owner, view.ID, port); managed.PublicError(err) != expected {
				t.Fatalf("lost partial-open failure: %v", err)
			}
			if isRunning.Load() || network.opens != 1 || network.closes != 1 {
				t.Fatal("partially opened guard was not rolled back")
			}
			if failCleanup {
				if managedNetworkCloser != network {
					t.Fatal("lost ownership of the partially opened guard")
				}
				if err := engine.Start(context.Background(), view.Owner, view.ID, port); managed.PublicError(err) != "managed_tun_cleanup_failed" || network.opens != 1 {
					t.Fatal("cleanup failure allowed another guard to open")
				}
				network.closeError = nil
				if !handleStopListener() {
					t.Fatal("partial guard cleanup could not be retried")
				}
			}
			if managedNetworkCloser != nil {
				t.Fatal("successful rollback retained a closed guard")
			}
		})
	}
	t.Run("stop and retained cleanup failure", func(t *testing.T) {
		engine := newManagedTestEngine(t)
		view := applyManagedFixture(t, engine)
		network := &networkFixture{}
		engine.network = network
		port := managedTestPort(t)
		if err := engine.Start(context.Background(), view.Owner, view.ID, port); err != nil {
			t.Fatal(err)
		}
		if managedNetworkCloser != network || !isRunning.Load() {
			t.Fatal("TUN ownership was not recorded")
		}
		network.closeError = errors.New("synthetic close failure")
		if handleStopListener() || managedNetworkCloser != network || isRunning.Load() {
			t.Fatal("failed cleanup forgot live resources")
		}
		if err := engine.Start(context.Background(), view.Owner, view.ID, port); managed.PublicError(err) != "managed_tun_cleanup_failed" || network.opens != 1 {
			t.Fatal("failed cleanup allowed another network listener")
		}
		network.closeError = nil
		if !handleStopListener() || managedNetworkCloser != nil {
			t.Fatal("cleanup retry did not release resources")
		}
	})
	t.Run("mixed bind failure rolls back TUN", func(t *testing.T) {
		engine := newManagedTestEngine(t)
		view := applyManagedFixture(t, engine)
		network := &networkFixture{}
		engine.network = network
		reservation, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer reservation.Close()
		port := reservation.Addr().(*net.TCPAddr).Port
		if err := engine.Start(context.Background(), view.Owner, view.ID, port); err == nil {
			t.Fatal("occupied mixed port accepted")
		}
		if network.closes != 1 || managedNetworkCloser != nil || isRunning.Load() {
			t.Fatal("mixed failure leaked TUN ownership")
		}
	})
}

func TestManagedUDPUnsupportedNodeCannotFallThroughToDirect(t *testing.T) {
	sink, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	target := sink.LocalAddr().(*net.UDPAddr)
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject=%v", blocked), func(t *testing.T) {
			engine := newManagedTestEngine(t)
			yaml := "proxies: [{name: TCP-Only, type: http, server: 127.0.0.1, port: 9}]\nproxy-groups: [{name: VIP, type: select, proxies: [TCP-Only]}]\nrules: ['MATCH,VIP']\n"
			if !blocked {
				yaml = "proxies: [{name: TCP-Only, type: http, server: 127.0.0.1, port: 9}]\nproxy-groups: [{name: VIP, type: select, proxies: [TCP-Only]}]\nrules: ['IP-CIDR,127.0.0.1/32,DIRECT', 'MATCH,VIP']\n"
			}
			prepared, err := engine.Prepare(context.Background(), managedProfileFixture(yaml))
			if err != nil {
				t.Fatal(err)
			}
			view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "udp-user", SessionID: "session-a"})
			if err != nil {
				t.Fatal(err)
			}
			port := managedTestPort(t)
			if err := engine.Start(context.Background(), view.Owner, view.ID, port); err != nil {
				t.Fatal(err)
			}
			client, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			packet := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(target.Port >> 8), byte(target.Port)}
			packet = append(packet, []byte("stun-shaped-udp-fixture")...)
			if _, err := client.Write(packet); err != nil {
				t.Fatal(err)
			}
			_ = sink.SetReadDeadline(time.Now().Add(time.Second))
			data := make([]byte, 100)
			_, _, err = sink.ReadFrom(data)
			if blocked && err == nil {
				t.Fatal("TCP-only selected node leaked UDP directly")
			}
			if !blocked && err != nil {
				t.Fatalf("positive control did not exercise real UDP: %v", err)
			}
			if blocked {
				var netErr net.Error
				if !errors.As(err, &netErr) || !netErr.Timeout() {
					t.Fatalf("unexpected UDP error: %v", err)
				}
			}
		})
	}
}
