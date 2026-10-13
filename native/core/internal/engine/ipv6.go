package engine

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os/exec"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
)

// IPv6 handling (handoff_app.md section 6.2: "IPv6 完整代理，或对未支持的 IPv6 明确阻断").
//
// The template captures IPv6 into the TUN. That is only correct when the selected node
// can carry IPv6. Many nodes are IPv4-only, and the system stack completes every TCP
// handshake locally before the node is dialed, so an app that learned a real AAAA
// (Chrome's automatic DoH, cached answers, literals) sees its IPv6 connection succeed,
// then die, and Happy Eyeballs never falls back to IPv4: the tunnel looks "connected"
// while browsing fails. For such nodes the Core adds reject routes that are more
// specific than the TUN's IPv6 routes, so IPv6 connects fail at once with
// EHOSTUNREACH and apps use IPv4. Nothing leaves through the physical interface, and
// the routes use the utun's own address as gateway, so the kernel removes them with
// the device even if the Core is killed.

// IPv6Probes are IPv6 literals answering plain HTTP on port 80; one real HTTP response
// through the node proves IPv6 egress. No DNS is involved.
var IPv6Probes = []string{"[2606:4700:4700::1111]:80", "[2001:4860:4860::8888]:80"}

// IPv6 modes reported for diagnostics and the privileged acceptance harness.
const (
	IPv6Tunnel       = "tunnel"        // the node carries IPv6; v6 goes through the TUN
	IPv6Rejected     = "rejected"      // the node has no IPv6; v6 is refused locally
	IPv6RejectFailed = "reject_failed" // refusal routes could not be installed (no leak: v6 stays in the TUN)
	IPv6Absent       = "absent"        // the host has no IPv6 to handle
)

// singTunIPv6Ranges mirrors the darwin auto-route ranges of the pinned sing-tun
// (tun_rules.go, autoRouteUseSubRanges). Each reject route is one bit longer than one
// of these, so it wins the longest-prefix match while the pair covers the same space.
var singTunIPv6Ranges = []netip.Prefix{
	netip.MustParsePrefix("100::/8"),
	netip.MustParsePrefix("200::/7"),
	netip.MustParsePrefix("400::/6"),
	netip.MustParsePrefix("800::/5"),
	netip.MustParsePrefix("1000::/4"),
	netip.MustParsePrefix("2000::/3"),
	netip.MustParsePrefix("4000::/2"),
	netip.MustParsePrefix("8000::/1"),
}

// rejectPrefixes splits every TUN IPv6 range into its two halves.
func rejectPrefixes() []netip.Prefix {
	out := make([]netip.Prefix, 0, 2*len(singTunIPv6Ranges))
	for _, p := range singTunIPv6Ranges {
		bits := p.Bits() + 1
		low := netip.PrefixFrom(p.Addr(), bits)
		raw := p.Addr().As16()
		raw[(bits-1)/8] |= 0x80 >> ((bits - 1) % 8)
		out = append(out, low, netip.PrefixFrom(netip.AddrFrom16(raw), bits))
	}
	return out
}

// nodeCarriesIPv6 asks the configured node for a real HTTP answer from an IPv6
// literal. A VLESS/SS dial "succeeds" before the server connects anywhere, so only
// response bytes count. Any probe answering is enough; all failing means IPv4-only.
func nodeCarriesIPv6(ctx context.Context, timeout time.Duration) bool {
	proxy, ok := tunnel.Proxies()[ProxyName]
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answered := make(chan bool, len(IPv6Probes))
	var wg sync.WaitGroup
	for _, target := range IPv6Probes {
		addr, err := netip.ParseAddrPort(target)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			answered <- httpAnswers(ctx, func(ctx context.Context, md *C.Metadata) (net.Conn, error) { return proxy.DialContext(ctx, md) }, addr)
		}()
	}
	go func() { wg.Wait(); close(answered) }()
	for ok := range answered {
		if ok {
			return true
		}
	}
	return false
}

type dialFunc func(context.Context, *C.Metadata) (net.Conn, error)

func httpAnswers(ctx context.Context, dial dialFunc, addr netip.AddrPort) bool {
	conn, err := dial(ctx, &C.Metadata{NetWork: C.TCP, DstIP: addr.Addr(), DstPort: addr.Port()})
	if err != nil {
		return false
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: [" + addr.Addr().String() + "]\r\nConnection: close\r\n\r\n")); err != nil {
		return false
	}
	head := make([]byte, 5)
	read := 0
	for read < len(head) {
		n, err := conn.Read(head[read:])
		read += n
		if err != nil {
			break
		}
	}
	return bytes.Equal(head[:read], []byte("HTTP/"))
}

// route runs /sbin/route with a fixed argument vector (no shell).
var route = func(args ...string) error {
	return exec.Command("/sbin/route", append([]string{"-n"}, args...)...).Run()
}

// addIPv6Reject installs the refusal routes via the utun gateway; on any failure the
// ones already added are removed again so the result is all or nothing.
func addIPv6Reject(gateway netip.Addr) error {
	var added []netip.Prefix
	for _, p := range rejectPrefixes() {
		if err := route("add", "-inet6", "-net", p.String(), gateway.String(), "-reject"); err != nil {
			removeIPv6Reject(gateway, added)
			return err
		}
		added = append(added, p)
	}
	return nil
}

func removeIPv6Reject(gateway netip.Addr, prefixes []netip.Prefix) {
	for _, p := range prefixes {
		_ = route("delete", "-inet6", "-net", p.String(), gateway.String())
	}
}
