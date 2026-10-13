// Package engine adapts the pinned Mihomo core to the managed state machine: it turns
// one validated Profile into a fixed, reviewed TUN/DNS configuration, owns the tunnel's
// life, exposes the proxy-only byte counters, and verifies real forwarding.
package engine

import (
	"errors"
	"maps"
	"runtime"

	"gopkg.in/yaml.v3"

	"asterlink/core/internal/managed"
)

// ProxyName is the only name the runtime config ever uses for the paid node. The
// administrator's display name never reaches the rule syntax, so a comma or any other
// character in it cannot alter routing.
const ProxyName = "node"

// TunName is the Windows adapter name. sing-tun creates the Wintun adapter under it and
// the Core recognises its own adapter by it (network.TunName); macOS lets the kernel
// pick utunN.
const TunName = "AsterLink"

// Fixed TUN/DNS template (handoff_app.md section 6.1/6.2). It is the stage 1 template
// proven on hardware (system stack, auto-route, auto-detect-interface, fake-ip DNS with
// any:53 hijack) with IPv6 enabled so v6 traffic is captured by the tunnel instead of
// silently leaving through the physical interface. Upstream lookups the fake-ip
// resolver cannot answer locally (anything but A/AAAA/HTTPS/SVCB) go over TCP through
// the node, so neither the ISP nor a public resolver sees them from the physical
// address; only the node's own hostname is resolved directly (default/proxy-server
// nameserver), the bootstrap exception of section 6.2. The system resolver itself is
// pointed into the tunnel by sysdns.go. The administrator's YAML contributes
// exactly one proxy definition and nothing else: no listeners, no controller, no
// providers, no scripts, no rules.
func BuildConfig(profile managed.Profile) ([]byte, error) {
	return buildConfig(profile, runtime.GOOS)
}

// buildConfig takes the platform explicitly so every platform's document can be tested
// on any host.
//
// The rule list is "MATCH,node" followed by "MATCH,REJECT". The second rule is not
// redundant: when the first matches an adapter without UDP support, the pinned
// tunnel.match skips it for UDP and, with no rule left, answers DIRECT (see the
// SupportUDP check and the final proxies["DIRECT"] in Clash.Meta/tunnel/tunnel.go).
// With a trailing REJECT (a UDP-capable adapter) a node that cannot carry UDP makes
// QUIC/STUN/UDP DNS fail instead of leaving through the physical interface.
func buildConfig(profile managed.Profile, goos string) ([]byte, error) {
	if len(profile.Proxy) == 0 {
		return nil, errors.New("empty profile")
	}
	proxy := maps.Clone(profile.Proxy)
	proxy["name"] = ProxyName
	config := map[string]any{
		"log-level": "warning",
		"mode":      "rule",
		"ipv6":      true,
		"allow-lan": false,
		"tun": map[string]any{
			"enable":                true,
			"stack":                 "system",
			"auto-route":            true,
			"auto-detect-interface": true,
			"dns-hijack":            []string{"any:53"},
		},
		"dns": map[string]any{
			"enable":                  true,
			"ipv6":                    true,
			"enhanced-mode":           "fake-ip",
			"fake-ip-range":           "198.18.0.1/16",
			"nameserver":              []string{"tcp://1.1.1.1#" + ProxyName, "tcp://8.8.8.8#" + ProxyName},
			"default-nameserver":      []string{"1.1.1.1"},
			"proxy-server-nameserver": []string{"1.1.1.1"},
		},
		"proxies": []any{proxy},
		"rules":   []string{"MATCH," + ProxyName, "MATCH,REJECT"},
	}
	if goos == "windows" {
		// strict-route is what makes Windows capture DNS and IPv6: sing-tun installs a
		// dynamic Windows Filtering Platform session (the OS removes it when this process
		// ends) that blocks port 53 and, when the TUN has no IPv6 address, every IPv6
		// connect from all processes except this one (docs/windows-tun-design.md).
		tun := config["tun"].(map[string]any)
		tun["strict-route"] = true
		tun["device"] = TunName
	}
	return yaml.Marshal(config)
}
