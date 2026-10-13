package network

import (
	"net/netip"
	"strings"
)

// Adapter is the part of a Windows network adapter the Core's decisions depend on. It
// is filled from the IP Helper API on Windows (network_windows.go); the decisions
// themselves are plain functions so they are unit-tested on every host.
type Adapter struct {
	Name        string // friendly name, e.g. "Wi-Fi"
	Description string // driver description, e.g. "Intel(R) Wi-Fi 6 AX201"
	Index       int
	IfType      uint32
	Up          bool
	HasIPv4     bool
	Gateway4    bool // owns an IPv4 default route with a next hop
	Gateway6    bool
	Metric4     uint32
	DNS         []netip.Addr // servers configured on this adapter, in order
}

// IANA ifType values that mark a software tunnel (tunnelType.h / ipifcons.h).
const (
	ifTypePPP      = 23
	ifTypeLoopback = 24
	ifTypeTunnel   = 131
)

// vpnDrivers are driver descriptions of VPN clients that install a virtual adapter and
// route the machine's traffic through it. Matching is case-insensitive substring. A
// Hyper-V/WSL/Docker virtual switch is deliberately absent: the handoff plan forbids
// treating every virtual adapter as a competing VPN (section 7.2, "竞争 VPN").
var vpnDrivers = []string{
	"wintun", "wireguard", "tap-windows", "openvpn", "tailscale", "zerotier",
	"anyconnect", "cisco", "globalprotect", "palo alto", "fortinet", "forticlient",
	"pulse secure", "juniper", "sonicwall", "checkpoint", "check point", "zscaler",
	"softether", "nordlynx", "expressvpn", "proton", "mullvad", "clash", "mihomo", "meta tunnel",
	"sing-tun", "v2ray", "sstap", "wintap",
}

// IsOwn reports whether the adapter is the Core's own TUN.
func (a Adapter) IsOwn() bool { return strings.EqualFold(a.Name, TunName) }

// IsOtherVPN reports whether the adapter belongs to another VPN or tunnel product. Only
// adapters that are up are competitors: an installed but disconnected client owns no routes.
func (a Adapter) IsOtherVPN() bool {
	if a.IsOwn() || !a.Up {
		return false
	}
	switch a.IfType {
	case ifTypePPP, ifTypeTunnel:
		// PPP covers dial-up/L2TP/PPTP/SSTP built-in VPN; "tunnel" covers Teredo/ISATAP/6to4
		// which Windows creates itself. Those are not VPNs unless they hold a default
		// route, which an OS-created transition tunnel never does.
		return a.Gateway4 || a.Gateway6
	case ifTypeLoopback:
		return false
	}
	desc := strings.ToLower(a.Description + " " + a.Name)
	for _, d := range vpnDrivers {
		if strings.Contains(desc, d) {
			return true
		}
	}
	return false
}

// PickPhysical chooses the adapter carrying the machine's real default route: up, with an
// IPv4 default gateway, not ours and not another VPN; lowest interface metric wins, the
// lower index breaking ties so the choice is stable.
func PickPhysical(adapters []Adapter) (Adapter, bool) {
	var best Adapter
	found := false
	for _, a := range adapters {
		if !a.Up || !a.HasIPv4 || !a.Gateway4 || a.IsOwn() || a.IsOtherVPN() || a.IfType == ifTypeLoopback {
			continue
		}
		if !found || a.Metric4 < best.Metric4 || (a.Metric4 == best.Metric4 && a.Index < best.Index) {
			best, found = a, true
		}
	}
	return best, found
}

// BootstrapResolvers returns the DNS servers the control plane may query directly while the
// system resolver points into the tunnel: the physical adapter's own servers, global
// addresses only (a link-local IPv6 server needs a zone the plain dialer cannot supply), at
// most four. nil means the caller falls back to a public resolver.
func BootstrapResolvers(a Adapter) []string {
	var out []string
	for _, ip := range a.DNS {
		ip = ip.Unmap()
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
			continue
		}
		// Site-local fec0::/10 resolvers (a Windows default for unconfigured IPv6) are dead ends.
		if ip.Is6() && ip.As16()[0] == 0xfe && ip.As16()[1]&0xc0 == 0xc0 {
			continue
		}
		out = append(out, ip.String())
		if len(out) == 4 {
			break
		}
	}
	return out
}
