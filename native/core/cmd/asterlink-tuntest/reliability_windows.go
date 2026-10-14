//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"asterlink/core/internal/network"
)

// Round 6 cases that need a real Windows host. They run from probe children (a copy of the
// harness under another path, see probeExecutable), because the Core's own path is exempt
// from the WFP rules and would prove nothing about ordinary applications.

// reliabilityChecks runs while the tunnel is up. baseline is the exit address seen
// before connecting (baselineExit, baselineSignature, set by main).
func reliabilityChecks(ctx context.Context, o options) {
	lanDNSCheck(ctx)
	boundSourceCheck(ctx, o, baselineExit)
	signature := network.Signature()
	record("T07-signature", "the physical network signature (adapter + addresses) does not change just because the tunnel came up",
		fmt.Sprintf("before=%s while-connected=%s", hash(baselineSignature), hash(signature)), verdict(signature == baselineSignature))
}

// lanDNSCheck aims plain DNS at every resolver configured on any other adapter (the DHCP
// router, manually entered public DNS, a second NIC's DNS). The Windows DNS Client asks all
// of them in parallel, so one reachable resolver is enough for a leak. Each must be blocked
// or hijacked; a real answer means the query left on the physical path (T03).
func lanDNSCheck(ctx context.Context) {
	const id, expected = "T03-lan-dns", "port-53 queries aimed at every adapter's configured resolver are blocked or hijacked, never answered by it"
	adapters, err := network.Adapters()
	if err != nil {
		record(id, expected, "cannot list adapters: "+err.Error(), "blocked")
		return
	}
	targets := lanResolverTargets(adapters, 6)
	if len(targets) == 0 {
		record(id, expected, "no other adapter has an IPv4 DNS server configured; nothing to aim at", "blocked")
		return
	}
	var leaks, seen []string
	for _, server := range targets {
		// example.org has a real answer, so a reply from the resolver is told apart from a
		// block (which looks like a timeout).
		out, err := child(ctx, 30*time.Second, "dns53", server+":53", "example.org")
		kind := classifyDNS53(out)
		if err != nil {
			kind = "blocked"
		}
		seen = append(seen, hash(server)+"="+kind)
		if kind == "answered" {
			leaks = append(leaks, hash(server))
		}
	}
	record(id, expected, fmt.Sprintf("%d resolvers: %s; leaking: %v", len(targets), strings.Join(seen, " "), leaks), verdict(len(leaks) == 0))
}

// boundSourceCheck: an application that binds its socket to the physical adapter's own
// source address (some torrent/VoIP/game clients, and WebRTC's host-candidate probing do)
// must not get a direct path. If this fails with route-only protection, the plan (7.2)
// calls for the smallest WFP user-mode policy; this case is the evidence that decides it.
func boundSourceCheck(ctx context.Context, o options, baseline string) {
	const id, expected = "T05-bind", "a socket bound to the physical adapter's address does not reach the internet directly"
	physical, err := network.PhysicalDefault()
	if err != nil {
		record(id, expected, "no physical default route: "+err.Error(), "blocked")
		return
	}
	iface, err := net.InterfaceByName(physical.Name)
	if err != nil {
		record(id, expected, "interface lookup failed: "+err.Error(), "blocked")
		return
	}
	addrs, _ := iface.Addrs()
	src, ok := firstIPv4(addrs)
	if !ok {
		record(id, expected, "the physical adapter has no usable IPv4 source address", "blocked")
		return
	}
	out, err := child(ctx, 60*time.Second, "bind", src.String(), o.probe)
	exit := strings.TrimSpace(out)
	switch {
	case err != nil:
		record(id, expected, "probe failed: "+err.Error(), "fail")
	case exit == "":
		record(id, expected, "bound connection failed (blocked), no direct path", "pass")
	case exit == baseline:
		record(id, expected, "LEAK: the exit equals the pre-tunnel address ("+hash(exit)+"); WFP policy needed", "fail")
	default:
		record(id, expected, "bound connection left through the tunnel (exit "+hash(exit)+")", "pass")
	}
}

// bindFetch is the body of the "bind" child: HTTP GET with the socket bound to src.
func bindFetch(ctx context.Context, src, urls string) string {
	local := &net.TCPAddr{IP: net.ParseIP(src)}
	for _, u := range strings.Split(urls, ",") {
		client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 10 * time.Second, LocalAddr: local}).DialContext(ctx, "tcp4", address)
			},
		}}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(u), nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 128))
		response.Body.Close()
		if text := strings.TrimSpace(string(body)); response.StatusCode == http.StatusOK && net.ParseIP(text) != nil {
			return text
		}
	}
	return ""
}
