package main

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"runtime"
	"syscall"

	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/listener/sing_tun"
	"github.com/metacubex/mihomo/tunnel"
)

type managedNetwork interface {
	Configure(*config.RawConfig)
	Open(context.Context, *config.Config, C.Tunnel) (io.Closer, error)
}

type loopbackManagedNetwork struct{}

func (loopbackManagedNetwork) Configure(*config.RawConfig) {}
func (loopbackManagedNetwork) Open(context.Context, *config.Config, C.Tunnel) (io.Closer, error) {
	return nil, nil
}

type macOSManagedNetwork struct{}

func (e *managedProfileEngine) networkPolicy() managedNetwork {
	if e.network != nil {
		return e.network
	}
	if runtime.GOOS == "darwin" {
		return macOSManagedNetwork{}
	}
	return loopbackManagedNetwork{}
}

func (macOSManagedNetwork) Configure(raw *config.RawConfig) {
	raw.IPv6 = true
	raw.Mode = tunnel.Rule
	raw.DNS = config.RawDNS{
		Enable: true, IPv6: true, RespectRules: true,
		EnhancedMode: C.DNSFakeIP, FakeIPRange: "198.18.0.1/16", FakeIPRange6: "fc00::/18",
		NameServer:            []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		ProxyServerNameserver: []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		DefaultNameserver:     []string{"1.1.1.1", "8.8.8.8"},
	}
}

func managedMacOSTunConfig() LC.Tun {
	return LC.Tun{
		Enable: true, Stack: C.TunGvisor, AutoRoute: true, AutoDetectInterface: true, StrictRoute: true,
		MTU: 1500, DNSHijack: []string{"any:53", "tcp://any:53"},
		Inet4Address: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/30")},
		Inet6Address: []netip.Prefix{netip.MustParsePrefix("fc00::1/126")},
		// Darwin RTM_ADD rejects an existing /0; paired /1 routes preserve full capture.
		RouteAddress: []netip.Prefix{
			netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1"),
			netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1"),
		},
		DisableICMPForwarding: true,
	}
}

func (macOSManagedNetwork) Open(ctx context.Context, cfg *config.Config, plane C.Tunnel) (io.Closer, error) {
	if ctx.Err() != nil {
		return nil, managedConfigError("operation_superseded")
	}
	if os.Geteuid() != 0 {
		return nil, managedConfigError("managed_tun_permission_required")
	}
	if err := checkManagedTunRoutes(); err != nil {
		return nil, err
	}
	tunConfig := managedMacOSTunConfig()
	listener, err := sing_tun.New(tunConfig, plane)
	if err != nil {
		managedLogError("[Managed TUN] startup failed: %v", err)
		if errors.Is(err, syscall.EEXIST) {
			return nil, managedConfigError("managed_tun_route_conflict")
		}
		return nil, managedConfigError("managed_tun_start_failed")
	}
	cfg.General.Tun = listener.Config()
	return listener, nil
}

// Owned under configMu; retain failed cleanup ownership rather than open another TUN.
var managedNetworkCloser io.Closer

func closeManagedNetworkLocked() error {
	if managedNetworkCloser == nil {
		return nil
	}
	if err := managedNetworkCloser.Close(); err != nil {
		managedLogError("[Managed TUN] cleanup failed: %v", err)
		return managedConfigError("managed_tun_cleanup_failed")
	}
	managedNetworkCloser = nil
	return nil
}
