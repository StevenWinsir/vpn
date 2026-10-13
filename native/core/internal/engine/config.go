// Package engine adapts the pinned Mihomo core to the managed state machine: it turns
// one validated Profile into a fixed, reviewed TUN/DNS configuration, owns the tunnel's
// life, exposes the proxy-only byte counters, and verifies real forwarding.
package engine

import (
	"errors"
	"maps"

	"gopkg.in/yaml.v3"

	"asterlink/core/internal/managed"
)

// ProxyName is the only name the runtime config ever uses for the paid node. The
// administrator's display name never reaches the rule syntax, so a comma or any other
// character in it cannot alter routing.
const ProxyName = "node"

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
		"rules":   []string{"MATCH," + ProxyName},
	}
	return yaml.Marshal(config)
}
