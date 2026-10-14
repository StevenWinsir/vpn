// Package network holds the Core's view of the physical network: which interface
// carries the machine's real traffic, whether another VPN already owns the routes,
// and the dialer that keeps the Core's own API calls off the paid tunnel
// (handoff_app.md section 6.2).
package network

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	// ErrNoNetwork means no physical default route exists (offline).
	ErrNoNetwork = errors.New("no physical default route")
	// ErrUnsupported is returned on platforms the Core cannot tunnel on.
	ErrUnsupported = errors.New("platform not supported")
)

// TunName is the Windows adapter name of the Core's own TUN (macOS names its device
// utunN, chosen by the kernel). It lets the Core tell its own adapter from other VPNs.
const TunName = "AsterLink"

// Interface is the physical egress the tunnel's outer connections must use.
type Interface struct {
	Name  string
	Index int
}

// Egress is the interface control-plane sockets are bound to. While it is unset
// (no tunnel) dialing is untouched; while the tunnel is up every API socket is
// pinned to the physical interface, so the Core never depends on the paid node
// to reach its own backend and can never route into its own TUN.
type Egress struct {
	mu    sync.RWMutex
	iface Interface
	// resolvers are the machine's own DNS servers from before the tunnel pointed the
	// system resolver at itself. While set, control-plane lookups go to them (over
	// the bound interface) instead of the tunnel address /etc/resolv.conf now names.
	resolvers []string
}

// Default is the process-wide egress shared by the engine and the API client.
var Default = &Egress{}

func (e *Egress) Set(i Interface) { e.mu.Lock(); e.iface = i; e.mu.Unlock() }
func (e *Egress) Clear()          { e.mu.Lock(); e.iface = Interface{}; e.mu.Unlock() }
func (e *Egress) Get() Interface  { e.mu.RLock(); defer e.mu.RUnlock(); return e.iface }

// SetResolvers records the bootstrap DNS servers (bare IPs) the control plane keeps
// using while the system resolver points into the tunnel; nil restores the default.
func (e *Egress) SetResolvers(servers []string) {
	e.mu.Lock()
	e.resolvers = append([]string(nil), servers...)
	e.mu.Unlock()
}

// Resolvers returns the recorded bootstrap DNS servers, nil when none are set.
func (e *Egress) Resolvers() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.resolvers
}

// control binds a new socket to the egress interface, except for loopback targets
// (test servers, local services) which never leave the machine.
func (e *Egress) control(network, address string, raw syscall.RawConn) error {
	iface := e.Get()
	if iface.Index == 0 {
		return nil
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		if ip, perr := netip.ParseAddr(host); perr == nil && ip.IsLoopback() {
			return nil
		}
	}
	return bindToInterface(raw, network, iface.Index)
}

// Dialer is the only dialer the API client uses.
func (e *Egress) Dialer() *net.Dialer {
	return &net.Dialer{Timeout: 10 * time.Second, Control: e.control}
}

// DialContext resolves names through the same bound path, so DNS lookups for the
// API host cannot be hijacked into the tunnel either. While the system resolver points
// into the tunnel, /etc/resolv.conf names the tunnel address, which is unreachable
// over the physical interface, so lookups go to the recorded bootstrap servers.
func (e *Egress) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d := e.Dialer()
	d.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return e.dialResolver(ctx, network, address)
	}}
	return d.DialContext(ctx, network, address)
}

func (e *Egress) dialResolver(ctx context.Context, network, address string) (net.Conn, error) {
	servers := e.Resolvers()
	if len(servers) == 0 {
		return e.Dialer().DialContext(ctx, network, address)
	}
	var last error
	for _, server := range servers {
		conn, err := e.Dialer().DialContext(ctx, network, net.JoinHostPort(server, "53"))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// Signature identifies the current physical connection: default interface plus its
// non-link-local addresses. A change means Wi-Fi/Ethernet/IP moved underneath a
// running tunnel and the old authorization must be re-verified.
func Signature() string {
	iface, err := PhysicalDefault()
	if err != nil {
		return "none"
	}
	return signatureOf(iface.Name)
}

func signatureOf(name string) string {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return name
	}
	if i.Flags&net.FlagUp == 0 {
		return name + "|down"
	}
	addrs, _ := i.Addrs()
	var ips []netip.Addr
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			ips = append(ips, p.Addr())
		}
	}
	return signatureFrom(name, ips)
}

// signatureFrom builds the signature from an adapter's addresses. IPv4 addresses count
// individually. IPv6 counts only as the /64 prefix: SLAAC privacy (temporary) addresses
// are regenerated about daily and a DHCPv6/RA renewal may add or drop addresses with the
// same prefix; keying on the full address would disconnect a healthy tunnel in the
// middle of a long session ("network_changed") although nothing moved. A different
// prefix is a different network and still changes the signature.
func signatureFrom(name string, addrs []netip.Addr) string {
	seen := map[string]bool{}
	var list []string
	for _, a := range addrs {
		a = a.Unmap()
		if a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsUnspecified() {
			continue
		}
		key := a.String()
		if a.Is6() {
			key = netip.PrefixFrom(a, 64).Masked().String()
		}
		if !seen[key] {
			seen[key] = true
			list = append(list, key)
		}
	}
	sort.Strings(list)
	return strings.Join(append([]string{name}, list...), "|")
}

// Conflicts lists other VPNs/tunnels that already own the routes. The first release
// does not stack on top of them (handoff_app.md section 6.2, "竞争软件"). On Windows the
// Core's own adapter (TunName) is never a competitor.
func Conflicts() []string {
	found := otherTunnels()
	if via := ViaTunnel(); via != "" && via != TunName && !contains(found, via) {
		found = append(found, via)
	}
	return found
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
