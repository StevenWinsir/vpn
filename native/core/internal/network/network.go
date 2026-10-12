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
}

// Default is the process-wide egress shared by the engine and the API client.
var Default = &Egress{}

func (e *Egress) Set(i Interface) { e.mu.Lock(); e.iface = i; e.mu.Unlock() }
func (e *Egress) Clear()          { e.mu.Lock(); e.iface = Interface{}; e.mu.Unlock() }
func (e *Egress) Get() Interface  { e.mu.RLock(); defer e.mu.RUnlock(); return e.iface }

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
// API host cannot be hijacked into the tunnel either.
func (e *Egress) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d := e.Dialer()
	d.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return e.Dialer().DialContext(ctx, network, address)
	}}
	return d.DialContext(ctx, network, address)
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
	parts := []string{name}
	if i, err := net.InterfaceByName(name); err == nil {
		if i.Flags&net.FlagUp == 0 {
			return name + "|down"
		}
		addrs, _ := i.Addrs()
		var list []string
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && !p.Addr().IsLinkLocalUnicast() {
				list = append(list, p.Addr().String())
			}
		}
		sort.Strings(list)
		parts = append(parts, list...)
	}
	return strings.Join(parts, "|")
}

// Conflicts lists other VPNs/tunnels that already own the routes. The first release
// does not stack on top of them (handoff_app.md section 6.2, "竞争软件").
func Conflicts() []string {
	var found []string
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if !strings.HasPrefix(i.Name, "utun") || i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
				found = append(found, i.Name)
				break
			}
		}
	}
	if via := ViaTunnel(); via != "" && !contains(found, via) {
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
