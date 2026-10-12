package engine

import (
	"strings"
	"testing"

	"github.com/metacubex/mihomo/hub/executor"

	"asterlink/core/internal/managed"
)

func sampleProfile(name string, extra map[string]any) managed.Profile {
	proxy := map[string]any{"name": name, "type": "ss", "server": "192.0.2.10", "port": 8388, "cipher": "aes-256-gcm", "password": "p@ss,word: 1", "udp": true}
	for k, v := range extra {
		proxy[k] = v
	}
	return managed.Profile{NodeID: "n", Version: "v", Group: "VPN", Proxy: proxy}
}

// The generated document must be accepted by the pinned core, and must come out
// exactly as the template promises: TUN on, nothing listening, one proxy, MATCH only.
func TestTemplateIsParsedByPinnedCoreAndStaysLocked(t *testing.T) {
	// A display name full of rule syntax must not reach the rules.
	data, err := BuildConfig(sampleProfile("🇭🇰 香港 | IEPL-01 »,DIRECT", nil))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatalf("pinned core rejected the template: %v\n%s", err, redact(string(data)))
	}
	general := cfg.General
	if !general.Tun.Enable || general.Tun.Stack.String() != "System" || !general.Tun.AutoRoute || !general.Tun.AutoDetectInterface {
		t.Fatalf("tun not as designed: %+v", general.Tun)
	}
	if len(general.Tun.DNSHijack) != 1 {
		t.Fatalf("dns-hijack: %v", general.Tun.DNSHijack)
	}
	if general.Port != 0 || general.SocksPort != 0 || general.MixedPort != 0 || general.RedirPort != 0 || general.TProxyPort != 0 {
		t.Fatalf("a proxy listener is configured: %+v", general.Inbound)
	}
	if general.AllowLan {
		t.Fatal("allow-lan must be off")
	}
	if !general.IPv6 {
		t.Fatal("IPv6 must be captured by the tunnel rather than bypass it")
	}
	if cfg.Controller.ExternalController != "" || cfg.Controller.ExternalControllerUnix != "" || cfg.Controller.ExternalUI != "" {
		t.Fatalf("a controller is exposed: %+v", cfg.Controller)
	}
	for name := range cfg.Proxies { // the node plus the core's built-in adapters, nothing else
		if name != ProxyName && !isBuiltin(name) {
			t.Fatalf("unexpected adapter %q", name)
		}
	}
	if _, ok := cfg.Proxies[ProxyName]; !ok {
		t.Fatal("the node adapter is missing")
	}
	if len(cfg.Rules) != 1 || !strings.EqualFold(cfg.Rules[0].RuleType().String(), "MATCH") || cfg.Rules[0].Adapter() != ProxyName {
		t.Fatalf("routing is not a single MATCH to the node: %d rules, %s -> %s", len(cfg.Rules), cfg.Rules[0].RuleType(), cfg.Rules[0].Adapter())
	}
	for name := range cfg.Providers { // only the core-internal "default" holder may exist
		if name != "default" {
			t.Fatalf("unexpected provider %q", name)
		}
	}
	if len(cfg.RuleProviders) != 0 {
		t.Fatal("providers must not exist")
	}
	if strings.Contains(string(data), "DIRECT") {
		t.Fatal("the document must not reference DIRECT")
	}
}

func isBuiltin(name string) bool {
	switch name {
	case "DIRECT", "REJECT", "REJECT-DROP", "COMPATIBLE", "PASS", "PASS-RULE", "GLOBAL":
		return true
	}
	return false
}

// Values containing YAML metacharacters survive a round trip untouched.
func TestSecretsWithYAMLSyntaxRoundTrip(t *testing.T) {
	data, err := BuildConfig(sampleProfile("x", map[string]any{"password": "a: b #c\n- d"}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(data)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if cfg.Proxies[ProxyName] == nil {
		t.Fatal("node missing")
	}
}

func TestVlessTemplateParses(t *testing.T) {
	profile := managed.Profile{NodeID: "n", Version: "v", Proxy: map[string]any{
		"name": "jp", "type": "vless", "server": "example.com", "port": 443, "uuid": "9b17fe2c-6070-41ca-908b-02d69ab01efc",
		"tls": true, "servername": "example.com", "network": "tcp", "udp": true,
	}}
	data, err := BuildConfig(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = executor.ParseWithBytes(data); err != nil {
		t.Fatalf("pinned core rejected the VLESS node: %v", err)
	}
}

func TestEmptyProfileIsRefused(t *testing.T) {
	if _, err := BuildConfig(managed.Profile{}); err == nil {
		t.Fatal("empty profile accepted")
	}
}

func TestOriginalProfileIsNotMutated(t *testing.T) {
	profile := sampleProfile("Display Name", nil)
	if _, err := BuildConfig(profile); err != nil {
		t.Fatal(err)
	}
	if profile.Proxy["name"] != "Display Name" {
		t.Fatal("BuildConfig renamed the caller's proxy")
	}
}

func redact(s string) string {
	return strings.ReplaceAll(s, "p@ss,word: 1", "<redacted>")
}
