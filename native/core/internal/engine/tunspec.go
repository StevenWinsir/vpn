package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sync"
)

// Android VPN interface hand-off (handoff_app_android.md 3.3, 6, 7.2). On Android the TUN
// is not created by sing-tun: the app's VpnService.Builder establishes it from a TunSpec
// the Core derives from the same parsed template Mihomo runs, and hands back the file
// descriptor, which becomes tun.file-descriptor. Address, prefix, MTU and the resolver
// therefore have one source and cannot drift between the Builder and the stack. The spec
// carries no node or account data. It is declared here, not in the Android file, so the
// document and its derivation are tested on every host.

// AndroidMTU is the template's tun.mtu and the Builder's setMtu. It is the value sing-tun
// uses on the desktops when the template leaves the MTU unset.
const AndroidMTU = 9000

// iOS interface constants (shared/contracts/tun-spec-ios-v1.json). IOSMTU is the initial
// value for both NEPacketTunnelNetworkSettings.mtu and tun.mtu; rounds 2 and 5 settle it from
// device throughput and memory. The addresses are the pinned core's defaults, written out
// because the iOS tunnel must always claim IPv6 (config.go).
const (
	IOSMTU   = 1500
	IOSInet4 = "198.18.0.1/30"
	IOSInet6 = "fdfe:dcba:9876::1/126"
)

// TunSpec is the VPN interface the host must establish (shared/contracts/tun-spec-v1.json).
type TunSpec struct {
	// Address4/Prefix4 is the interface's IPv4 address (Builder.addAddress); the IPv4
	// default route always accompanies it.
	Address4 string `json:"address4"`
	Prefix4  int    `json:"prefix4"`
	// DNS is the tunnel resolver the system resolver is pointed at (Builder.addDnsServer).
	DNS string `json:"dns"`
	MTU int    `json:"mtu"`
	// IPv6 is true only when the node carries IPv6: then Address6/Prefix6 and the IPv6
	// default route are added. Without it the VPN has no IPv6 at all and Android blocks the
	// address family for every app (connect fails at once, apps fall back to IPv4).
	IPv6     bool   `json:"ipv6"`
	Address6 string `json:"address6,omitempty"`
	Prefix6  int    `json:"prefix6,omitempty"`
}

// TunHost establishes the VPN interface. OpenTun returns the detached file descriptor,
// owned by the Core from then on, or a negative value when the system refused (no VPN
// consent, the service is not running, another VPN took over).
type TunHost interface {
	OpenTun(specJSON string) int
}

var (
	tunHostMu sync.RWMutex
	tunHost   TunHost
)

// SetTunHost installs (or, with nil, removes) the host. Only Android and iOS builds consult it.
func SetTunHost(h TunHost) {
	tunHostMu.Lock()
	tunHost = h
	tunHostMu.Unlock()
}

func currentTunHost() TunHost {
	tunHostMu.RLock()
	defer tunHostMu.RUnlock()
	return tunHost
}

// iosTunConf is the iOS TUN section as the session runs it: the IPv6 address is restored
// when the pinned core dropped it on a host without global IPv6, because an iOS tunnel must
// always claim IPv6 (tun-spec-ios-v1). Declared here so every host tests it.
func iosTunConf(conf tunConfig) tunConfig {
	if len(conf.Inet6Address) == 0 {
		conf.Inet6Address = []netip.Prefix{netip.MustParsePrefix(IOSInet6)}
	}
	return conf
}

var errTunSpec = errors.New("the parsed TUN section cannot be expressed as a VPN interface")

// tunSpecFor derives the spec from the TUN section as the session will run it, i.e. after
// prepareTunConf removed IPv6 for a node without it. The resolver is the address after the
// interface's own, which is where the pinned stack answers DNS (sing_tun.New: dnsAdds).
func tunSpecFor(conf tunConfig) (TunSpec, error) {
	if len(conf.Inet4Address) != 1 || !conf.Inet4Address[0].Addr().Is4() || len(conf.Inet6Address) > 1 {
		return TunSpec{}, errTunSpec
	}
	v4 := conf.Inet4Address[0]
	spec := TunSpec{Address4: v4.Addr().String(), Prefix4: v4.Bits(), DNS: v4.Addr().Next().String(), MTU: int(conf.MTU)}
	if spec.Prefix4 < 8 || spec.Prefix4 > 30 || spec.MTU < 1280 || spec.MTU > 65535 {
		return TunSpec{}, errTunSpec
	}
	if len(conf.Inet6Address) == 1 {
		v6 := conf.Inet6Address[0]
		if !v6.Addr().Is6() || v6.Addr().Is4In6() || v6.Bits() < 64 || v6.Bits() > 126 {
			return TunSpec{}, errTunSpec
		}
		spec.IPv6, spec.Address6, spec.Prefix6 = true, v6.Addr().String(), v6.Bits()
	}
	return spec, nil
}

func (s TunSpec) encode() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errTunSpec, err)
	}
	return string(data), nil
}
