package engine

import (
	"testing"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
)

// The Windows document differs from macOS in exactly two TUN keys and nothing else.
func TestWindowsTemplateAddsStrictRouteAndDeviceOnly(t *testing.T) {
	windows, err := buildConfig(sampleProfile("n", nil), "windows")
	if err != nil {
		t.Fatal(err)
	}
	darwin, err := buildConfig(sampleProfile("n", nil), "darwin")
	if err != nil {
		t.Fatal(err)
	}
	cfgW, err := executor.ParseWithBytes(windows)
	if err != nil {
		t.Fatalf("pinned core rejected the Windows template: %v", err)
	}
	cfgD, err := executor.ParseWithBytes(darwin)
	if err != nil {
		t.Fatalf("pinned core rejected the macOS template: %v", err)
	}
	if !cfgW.General.Tun.StrictRoute || cfgW.General.Tun.Device != TunName {
		t.Fatalf("Windows TUN must be strict-route on adapter %q: %+v", TunName, cfgW.General.Tun)
	}
	if cfgD.General.Tun.StrictRoute || cfgD.General.Tun.Device != "" {
		t.Fatalf("macOS must keep its proven template (no strict-route, kernel-chosen utun): %+v", cfgD.General.Tun)
	}
	// Same stack, auto-route, hijack and addresses on both.
	w, d := cfgW.General.Tun, cfgD.General.Tun
	if w.Stack != d.Stack || w.AutoRoute != d.AutoRoute || w.AutoDetectInterface != d.AutoDetectInterface ||
		len(w.DNSHijack) != len(d.DNSHijack) || len(w.Inet4Address) != len(d.Inet4Address) || len(w.Inet6Address) != len(d.Inet6Address) {
		t.Fatalf("platform templates diverged beyond strict-route/device:\nwindows %+v\ndarwin  %+v", w, d)
	}
	// The pinned core clears the TUN's IPv6 address itself on a host without IPv6 (parseIPV6 /
	// verifyIP6), so presence is host-dependent; the engine reports that as IPv6Absent.
}

// walk mirrors the pinned tunnel.match for a MATCH-only rule list (Clash.Meta/tunnel/tunnel.go):
// a rule whose adapter lacks UDP support is skipped for UDP, and with no rule left the
// answer is DIRECT. The test uses the pinned core's own parsed rules and adapters, so the
// only duplicated piece is that loop; the real traversal is exercised on Windows (T02).
func walk(t *testing.T, cfg *configUnderTest, network C.NetWork) string {
	t.Helper()
	md := &C.Metadata{NetWork: network}
	for _, rule := range cfg.rules {
		matched, name := rule.Match(md, C.RuleMatchHelper{})
		if !matched {
			continue
		}
		adapter, ok := cfg.proxies[name]
		if !ok {
			continue
		}
		if network == C.UDP && !adapter.SupportUDP() {
			continue
		}
		return name
	}
	return "DIRECT"
}

type configUnderTest struct {
	rules   []C.Rule
	proxies map[string]C.Proxy
}

func parseForWalk(t *testing.T, udp bool) *configUnderTest {
	t.Helper()
	data, err := BuildConfig(sampleProfile("n", map[string]any{"udp": udp}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return &configUnderTest{rules: cfg.Rules, proxies: cfg.Proxies}
}

// A node without UDP must make UDP fail, never fall through to DIRECT.
func TestUDPWithoutSupportIsRejectedNeverDirect(t *testing.T) {
	noUDP := parseForWalk(t, false)
	if got := walk(t, noUDP, C.UDP); got != "REJECT" {
		t.Fatalf("UDP through a node without UDP support resolved to %q, want REJECT", got)
	}
	if got := walk(t, noUDP, C.TCP); got != ProxyName {
		t.Fatalf("TCP resolved to %q, want the node", got)
	}
	withUDP := parseForWalk(t, true)
	if got := walk(t, withUDP, C.UDP); got != ProxyName {
		t.Fatalf("UDP through a node with UDP support resolved to %q, want the node", got)
	}
	if !noUDP.proxies["REJECT"].SupportUDP() {
		t.Fatal("REJECT must accept UDP; otherwise the pinned core skips it too and falls back to DIRECT")
	}
	// Without the trailing rule the old template really would have gone DIRECT:
	// this guards the guard against being "fixed" by deleting the rule.
	only := &configUnderTest{rules: noUDP.rules[:1], proxies: noUDP.proxies}
	if got := walk(t, only, C.UDP); got != "DIRECT" {
		t.Fatalf("the single-MATCH list was expected to fall to DIRECT for UDP, got %q", got)
	}
}
