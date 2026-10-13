//go:build !windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"asterlink/core/internal/engine"
)

// privileged reports whether the process may create a utun device.
func privileged() bool { return os.Geteuid() == 0 }

const privilegeHint = "root is required to create a utun device (run with sudo -E)"

// machineFacts is recorded in the report next to every case.
func machineFacts() map[string]any { return map[string]any{"os": "unix"} }

// platformChecks runs the macOS cases in-process: the harness is root and the system
// resolver is mDNSResponder, so there is no exempt-process distinction to account for.
func platformChecks(ctx context.Context, eng *engine.Mihomo, o options, baseline6 string) {
	dnsChecks(ctx, eng.DNSMode())
	udpCheck(ctx, o.udpEcho)
	ipv6Check(ctx, o.probe6, baseline6)
	ipv6FallbackCheck(ctx, eng.IPv6Mode())
}

// platformExtras: nothing beyond the shared flow on macOS.
func platformExtras(context.Context, options, map[string]any, snapshot) {}

// runChild exists for the Windows harness only.
func runChild([]string) int { return 2 }

func dnsChecks(ctx context.Context, mode string) {
	fake := netip.MustParsePrefix("198.18.0.0/16")
	// The system resolver must point into the tunnel; otherwise a LAN resolver (router,
	// IPv6 link-local RDNSS) is reached over the on-link route and never hijacked, and a
	// public one lets browsers auto-upgrade to DoH around the hijack.
	scutil, _ := exec.Command("/usr/sbin/scutil", "--dns").Output()
	primary := ""
	for _, line := range strings.Split(string(scutil), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "nameserver[0]" {
			primary = f[2]
			break
		}
	}
	record("T03-dns-capture", "the system default resolver points into the tunnel ("+engine.TunnelDNS+")",
		fmt.Sprintf("mode=%s primary=%s", mode, primary), verdict(mode == engine.DNSCaptured && primary == engine.TunnelDNS))

	// Through mDNSResponder (Go's default resolver on darwin uses libSystem), with a
	// fresh name so no cached answer from before the tunnel can satisfy it.
	addrs, err := net.DefaultResolver.LookupHost(ctx, fmt.Sprintf("al-%d.example.com", time.Now().UnixNano()))
	system := false
	for _, a := range addrs {
		if ip, perr := netip.ParseAddr(a); perr == nil && fake.Contains(ip) {
			system = true
		}
	}
	record("T03-dns-system", "a system lookup is answered by the tunnel's resolver (fake-ip), not a LAN/ISP resolver",
		fmt.Sprintf("addrs=%v err=%v", addrs, err), verdict(system))

	// Plain DNS aimed at a public resolver must be hijacked into the tunnel as well.
	hijacked := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", "9.9.9.9:53")
	}}
	addrs, err = hijacked.LookupHost(ctx, "example.org")
	any53 := false
	for _, a := range addrs {
		if ip, perr := netip.ParseAddr(a); perr == nil && fake.Contains(ip) {
			any53 = true
		}
	}
	record("T03-dns-any53", "port-53 queries to an arbitrary resolver are hijacked", fmt.Sprintf("addrs=%v err=%v", addrs, err), verdict(any53))
}

func udpCheck(ctx context.Context, target string) {
	if target == "" {
		record("T03-udp", "UDP echo round trip through the node", "--udp-echo not provided", "blocked")
		return
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", target)
	if err != nil {
		record("T03-udp", "UDP echo round trip through the node", err.Error(), "fail")
		return
	}
	defer conn.Close()
	payload := []byte("asterlink-udp-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	buffer := make([]byte, 512)
	for range 3 {
		if _, err = conn.Write(payload); err != nil {
			break
		}
		if n, rerr := conn.Read(buffer); rerr == nil && string(buffer[:n]) == string(payload) {
			record("T03-udp", "UDP echo round trip through the node", "echo matched", "pass")
			return
		}
	}
	record("T03-udp", "UDP echo round trip through the node", "no echo (UDP unsupported by this node or path): must be a controlled refusal, never a direct send", "fail")
}

func ipv6Check(ctx context.Context, probes, baseline6 string) {
	exit6 := fetch(ctx, probes, "tcp6")
	switch {
	case exit6 == "" && baseline6 == "":
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "this network has no IPv6 (nothing to leak)", "pass")
	case exit6 == "":
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 requests fail while the tunnel is up (blocked, not leaked)", "pass")
	case exit6 == baseline6:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 exit unchanged: v6 traffic bypasses the tunnel", "fail")
	default:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 exit changed ("+hash(exit6)+")", "pass")
	}
}

// ipv6FallbackCheck uses an IPv6 literal, so DNS cannot hide the path: an app holding a
// real AAAA must either be carried by the node or be refused before the handshake, so
// Happy Eyeballs falls back to IPv4. A handshake that completes and then goes silent
// is the "connected but no internet" failure and must fail this case.
func ipv6FallbackCheck(ctx context.Context, mode string) {
	const id, expected = "T04-ipv6-fallback", "IPv6 is carried by the node, or refused before the handshake"
	if mode == engine.IPv6Absent {
		record(id, expected, "mode=absent: this host has no IPv6", "pass")
		return
	}
	target := engine.IPv6Probes[0]
	started := time.Now()
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp6", target)
	elapsed := time.Since(started).Round(time.Millisecond)
	if err != nil {
		ok := mode == engine.IPv6Rejected && elapsed < time.Second
		record(id, expected, fmt.Sprintf("mode=%s: connect refused after %v (%v)", mode, elapsed, err), verdict(ok))
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + target + "\r\nConnection: close\r\n\r\n"))
	head := make([]byte, 5)
	n, _ := io.ReadFull(conn, head)
	answered := string(head[:n]) == "HTTP/"
	record(id, expected, fmt.Sprintf("mode=%s: handshake in %v, HTTP answer %v", mode, elapsed, answered), verdict(mode == engine.IPv6Tunnel && answered))
}

func openFDs() int {
	entries, _ := os.ReadDir("/dev/fd")
	return len(entries)
}

func capture() snapshot {
	var s snapshot
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		s.Interfaces = append(s.Interfaces, i.Name)
	}
	sort.Strings(s.Interfaces)
	s.RoutesV4 = routeLines("-f", "inet")
	s.RoutesV6 = routeLines("-f", "inet6")
	out, _ := exec.Command("/usr/sbin/scutil", "--dns").Output()
	sum := sha256.Sum256(out)
	s.DNSDigest = hex.EncodeToString(sum[:])
	return s
}

// routeLines keeps destination/gateway/flags/interface and ignores neighbour-cache and
// temporary-address entries (flags W/L) that the LAN changes on its own.
func routeLines(args ...string) []string {
	out, _ := exec.Command("/usr/sbin/netstat", append([]string{"-rn"}, args...)...).Output()
	return parseRouteLines(string(out))
}

// parseRouteLines turns the stable four columns of macOS netstat into a sorted
// comparison set. The Expire column is deliberately omitted: its countdown is
// expected to change while a test is running. Entries with W (waiting) or L
// (link-layer/neighbor cache) flags are also omitted because they are maintained
// asynchronously by the LAN and are not tunnel-owned routes.
func parseRouteLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] == "Destination" || f[0] == "Routing" || f[0] == "Internet" {
			continue
		}
		flags := strings.ToUpper(f[2])
		if strings.ContainsAny(flags, "WL") {
			continue
		}
		lines = append(lines, strings.Join(f[:4], " "))
	}
	sort.Strings(lines)
	return lines
}

func newDevices(before snapshot) []string {
	known := map[string]bool{}
	for _, n := range before.Interfaces {
		known[n] = true
	}
	var added []string
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if !known[i.Name] && strings.HasPrefix(i.Name, "utun") {
			added = append(added, i.Name)
		}
	}
	return added
}

func diff(before, after snapshot) []string {
	var problems []string
	if added := newDevices(before); len(added) > 0 {
		problems = append(problems, "utun still present: "+strings.Join(added, ","))
	}
	if strings.Join(before.RoutesV4, "\n") != strings.Join(after.RoutesV4, "\n") {
		problems = append(problems, "IPv4 route table differs from before the run")
	}
	if strings.Join(before.RoutesV6, "\n") != strings.Join(after.RoutesV6, "\n") {
		problems = append(problems, "IPv6 route table differs from before the run")
	}
	if before.DNSDigest != after.DNSDigest {
		problems = append(problems, "scutil --dns differs from before the run")
	}
	return problems
}

// platformCleanup removes temporary probe files.
func platformCleanup() {}
