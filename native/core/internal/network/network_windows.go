//go:build windows

package network

import (
	"errors"
	"math/bits"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IP_UNICAST_IF and IPV6_UNICAST_IF share the value 31 (ws2ipdef.h). They pin the
// socket's egress to one interface index and bypass the routing table, and therefore
// the TUN's 0.0.0.0/1 + 128.0.0.0/1 split routes, exactly like IP_BOUND_IF on macOS.
// The IPv4 option takes the index in network byte order, the IPv6 option in host order
// (the long-standing Winsock asymmetry that also bites WireGuard and Tailscale).
const ipUnicastIF = 31

// bindToInterface pins the socket to one interface index.
func bindToInterface(c syscall.RawConn, network string, index int) error {
	var inner error
	err := c.Control(func(fd uintptr) {
		handle := windows.Handle(fd)
		if strings.HasSuffix(network, "6") {
			inner = windows.SetsockoptInt(handle, windows.IPPROTO_IPV6, ipUnicastIF, index)
		} else {
			inner = windows.SetsockoptInt(handle, windows.IPPROTO_IP, ipUnicastIF, int(bits.ReverseBytes32(uint32(index))))
		}
	})
	if err != nil {
		return err
	}
	return inner
}

// operStatusUp is IfOperStatusUp from ifdef.h.
const operStatusUp = 1

// Adapters returns the machine's network adapters as the IP Helper API reports them.
func Adapters() ([]Adapter, error) {
	const flags = windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST
	size := uint32(16 * 1024)
	var buffer []byte
	for attempt := 0; ; attempt++ {
		buffer = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0])), &size)
		if err == nil {
			break
		}
		// The table can grow between the size query and the call (a VPN comes up).
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) || attempt >= 4 {
			return nil, err
		}
	}
	var out []Adapter
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0])); aa != nil; aa = aa.Next {
		a := Adapter{
			Name:        windows.UTF16PtrToString(aa.FriendlyName),
			Description: windows.UTF16PtrToString(aa.Description),
			Index:       int(aa.IfIndex),
			IfType:      aa.IfType,
			Up:          aa.OperStatus == operStatusUp,
			Metric4:     aa.Ipv4Metric,
		}
		for u := aa.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip != nil && ip.To4() != nil {
				a.HasIPv4 = true
			}
		}
		for g := aa.FirstGatewayAddress; g != nil; g = g.Next {
			ip := g.Address.IP()
			switch {
			case ip == nil || ip.IsUnspecified():
			case ip.To4() != nil:
				a.Gateway4 = true
			default:
				a.Gateway6 = true
			}
		}
		for d := aa.FirstDnsServerAddress; d != nil; d = d.Next {
			if addr, ok := netip.AddrFromSlice(d.Address.IP()); ok {
				a.DNS = append(a.DNS, addr.Unmap())
			}
		}
		out = append(out, a)
	}
	runtime.KeepAlive(buffer)
	return out, nil
}

// PhysicalDefault is the adapter that carries the machine's real default route while the
// TUN may already be up: the TUN installs split routes (0/1 + 128/1) next to, not over,
// the original 0/0 entry, so the adapter that owns 0/0 keeps its gateway entry.
func PhysicalDefault() (Interface, error) {
	adapters, err := Adapters()
	if err != nil {
		return Interface{}, err
	}
	a, ok := PickPhysical(adapters)
	if !ok {
		return Interface{}, ErrNoNetwork
	}
	i, err := net.InterfaceByIndex(a.Index)
	if err != nil || i.Flags&net.FlagUp == 0 {
		return Interface{}, ErrNoNetwork
	}
	return Interface{Name: i.Name, Index: i.Index}, nil
}

// PhysicalResolvers returns the DNS servers configured on the physical adapter, read
// before the tunnel exists. The control plane keeps using them (bound to that adapter)
// while the system resolver points into the tunnel. Empty means "use a public default".
func PhysicalResolvers(iface Interface) []string {
	adapters, err := Adapters()
	if err != nil {
		return nil
	}
	for _, a := range adapters {
		if a.Index == iface.Index {
			return BootstrapResolvers(a)
		}
	}
	return nil
}

// ViaTunnel reports the tunnel-class adapter that Windows would use to reach well-known
// public addresses: the Core's own TUN once its split routes are installed, or another
// VPN that already owns them. GetBestInterfaceEx asks the routing table about a concrete
// destination, so it sees split default routes that the adapter list cannot.
func ViaTunnel() string {
	var adapters []Adapter
	loaded := false
	for _, probe := range []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")} {
		var index uint32
		if windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: probe.As4()}, &index) != nil {
			continue
		}
		i, err := net.InterfaceByIndex(int(index))
		if err != nil {
			continue
		}
		if strings.EqualFold(i.Name, TunName) {
			return i.Name
		}
		if !loaded {
			adapters, _ = Adapters()
			loaded = true
		}
		for _, a := range adapters {
			if a.Index == int(index) && a.IsOtherVPN() {
				return i.Name
			}
		}
	}
	return ""
}

// otherTunnels lists up adapters that belong to another VPN product.
func otherTunnels() []string {
	adapters, err := Adapters()
	if err != nil {
		return nil
	}
	var found []string
	for _, a := range adapters {
		if a.IsOtherVPN() {
			found = append(found, a.Name)
		}
	}
	return found
}

// OwnTunnelUp reports whether the Core's TUN adapter exists and is up.
func OwnTunnelUp() bool {
	i, err := net.InterfaceByName(TunName)
	return err == nil && i.Flags&net.FlagUp != 0
}

// platformSignature: the signature is read from the interface itself (network.go).
func platformSignature() (string, bool) { return "", false }
