package engine

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/hub/executor"
)

// androidTun parses the Android template with the pinned core, as Start does.
func androidTun(t *testing.T) tunConfig {
	t.Helper()
	data, err := buildConfig(sampleProfile("n", nil), "android")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatalf("pinned core rejected the Android template: %v", err)
	}
	return cfg.General.Tun
}

// The Android document differs from macOS in exactly the keys the VpnService takes over
// (routes, interface detection) plus a fixed MTU the Builder must use too.
func TestAndroidTemplateHandsRoutingToTheVpnService(t *testing.T) {
	android := androidTun(t)
	data, err := buildConfig(sampleProfile("n", nil), "darwin")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	darwin := cfg.General.Tun
	if android.AutoRoute || android.AutoDetectInterface || android.StrictRoute || android.Device != "" {
		t.Fatalf("Android must not program routes, read netlink or name a device: %+v", android)
	}
	if android.MTU != AndroidMTU || android.FileDescriptor != 0 {
		t.Fatalf("Android TUN mtu=%d fd=%d: the MTU is fixed and the descriptor comes only from the VpnService", android.MTU, android.FileDescriptor)
	}
	if !android.Enable || android.Stack != darwin.Stack || !darwin.AutoRoute ||
		strings.Join(android.DNSHijack, ",") != strings.Join(darwin.DNSHijack, ",") ||
		len(android.Inet4Address) != 1 || android.Inet4Address[0] != darwin.Inet4Address[0] {
		t.Fatalf("Android diverged beyond routing/detection/mtu:\nandroid %+v\ndarwin  %+v", android, darwin)
	}
	if android.Inet4Address[0] != netip.MustParsePrefix("198.18.0.1/30") {
		t.Fatalf("tunnel address %v", android.Inet4Address)
	}
	// The trailing REJECT guard is shared: a node without UDP never sends UDP DIRECT.
	if want := []string{"MATCH," + ProxyName, "MATCH,REJECT"}; len(cfg.Rules) != len(want) {
		t.Fatalf("rules %v", cfg.Rules)
	}
}

func contractProduced(t *testing.T) map[bool]string {
	t.Helper()
	raw, err := os.ReadFile("../../../../shared/contracts/tun-spec-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Fields   []string `json:"fields"`
		Produced []struct {
			IPv6 bool   `json:"ipv6"`
			Spec string `json:"spec"`
		} `json:"produced"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if strings.Join(contract.Fields, ",") != "address4,prefix4,dns,mtu,ipv6,address6,prefix6" {
		t.Fatalf("contract fields %v", contract.Fields)
	}
	out := map[bool]string{}
	for _, p := range contract.Produced {
		out[p.IPv6] = p.Spec
	}
	return out
}

// The spec the Builder receives is derived from the parsed section the stack runs, and is
// byte-identical to the shared contract both sides test against.
func TestTunSpecComesFromTheParsedAndroidSection(t *testing.T) {
	produced := contractProduced(t)
	conf := androidTun(t)
	conf.Inet6Address = []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/126")} // host-independent (parseIPV6)

	spec, err := tunSpecFor(conf)
	if err != nil {
		t.Fatal(err)
	}
	if doc, _ := spec.encode(); doc != produced[true] {
		t.Fatalf("IPv6 node:\n got %s\nwant %s", doc, produced[true])
	}

	// ipv6_android.go's prepareTunConf for a node without IPv6 (this host runs its own).
	withoutIPv6 := conf
	withoutIPv6.Inet6Address = nil
	spec, err = tunSpecFor(withoutIPv6)
	if err != nil {
		t.Fatal(err)
	}
	if doc, _ := spec.encode(); doc != produced[false] {
		t.Fatalf("IPv4-only node:\n got %s\nwant %s", doc, produced[false])
	}
	if strings.Contains(produced[false], "192.0.2.10") || strings.Contains(produced[true], "8388") {
		t.Fatal("the spec must never carry node data")
	}
}

func TestTunSpecRefusesSectionsItCannotExpress(t *testing.T) {
	base := androidTun(t)
	for name, mutate := range map[string]func(*tunConfig){
		"no ipv4":       func(c *tunConfig) { c.Inet4Address = nil },
		"two ipv4":      func(c *tunConfig) { c.Inet4Address = append(c.Inet4Address, netip.MustParsePrefix("10.0.0.1/30")) },
		"ipv4 /31":      func(c *tunConfig) { c.Inet4Address = []netip.Prefix{netip.MustParsePrefix("198.18.0.1/31")} },
		"mtu unset":     func(c *tunConfig) { c.MTU = 0 },
		"mtu too small": func(c *tunConfig) { c.MTU = 576 },
		"ipv6 /48":      func(c *tunConfig) { c.Inet6Address = []netip.Prefix{netip.MustParsePrefix("fdfe:dcba::1/48")} },
		"ipv4 as ipv6":  func(c *tunConfig) { c.Inet6Address = []netip.Prefix{netip.MustParsePrefix("10.0.0.1/30")} },
		"two ipv6 address": func(c *tunConfig) {
			c.Inet6Address = []netip.Prefix{netip.MustParsePrefix("fd00::1/126"), netip.MustParsePrefix("fd01::1/126")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			conf := base
			conf.Inet4Address = append([]netip.Prefix(nil), base.Inet4Address...)
			mutate(&conf)
			if _, err := tunSpecFor(conf); !errors.Is(err, errTunSpec) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

type recordingHost struct{ specs []string }

func (h *recordingHost) OpenTun(spec string) int { h.specs = append(h.specs, spec); return -1 }

func TestTunHostRegistration(t *testing.T) {
	if currentTunHost() != nil {
		t.Fatal("no host before SetTunHost")
	}
	h := &recordingHost{}
	SetTunHost(h)
	if currentTunHost() != h {
		t.Fatal("host not installed")
	}
	SetTunHost(nil)
	if currentTunHost() != nil {
		t.Fatal("host not removed")
	}
}
