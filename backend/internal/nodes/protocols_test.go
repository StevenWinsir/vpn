package nodes

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"vpn/nodepolicy"
)

func TestAllManagedProtocolsRoundTrip(t *testing.T) {
	data, err := os.ReadFile("../../../FlClash/core/nodepolicy/testdata/proxies.yaml")
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, proxy := range proxies {
		seen[proxy["type"].(string)] = true
		encoded, err := Marshal([]map[string]any{proxy})
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(string(encoded))
		if err != nil || !reflect.DeepEqual(again[0], proxy) {
			t.Fatal("nested protocol fields lost during normalization")
		}
	}
	for _, kind := range nodepolicy.Supported() {
		if !seen[kind] {
			t.Fatalf("missing fixture for %s", kind)
		}
	}
}

func TestProtocolBoundaryRejectsUntrustedOptions(t *testing.T) {
	vless := "proxies: [{name: node, type: vless, server: 127.0.0.1, port: 443, uuid: 11111111-2222-4333-8444-555555555555, network: ws, ws-opts: {path: /ws}}]"
	wg := "proxies: [{name: node, type: wireguard, server: 127.0.0.1, port: 51820, ip: 10.0.0.2, private-key: IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=, public-key: ERERERERERERERERERERERERERERERERERERERERERE=}]"
	cases := map[string]string{
		"nested file":            strings.Replace(vless, "path: /ws", "path: /ws, certificate: /private/secret.pem", 1),
		"nested bypass":          strings.Replace(vless, "path: /ws", "path: /ws, dialer-proxy: DIRECT", 1),
		"header injection":       strings.Replace(vless, "path: /ws", `path: /ws, headers: {Host: "safe\r\nInjected: x"}`, 1),
		"duplicate nested key":   strings.Replace(vless, "path: /ws", "path: /ws, path: /other", 1),
		"wrong nested shape":     strings.Replace(vless, "{path: /ws}", "[path]", 1),
		"invalid uuid":           strings.Replace(vless, "11111111-2222-4333-8444-555555555555", "credential-must-not-appear-in-errors", 1),
		"incompatible transport": strings.Replace(vless, "network: ws", "network: tcp", 1),
		"TLS bypass":             strings.Replace(vless, "network: ws", "network: ws, skip-cert-verify: true", 1),
		"global overrides":       vless + "\nexternal-controller: 0.0.0.0:9090",
		"wg file path":           strings.Replace(wg, "IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=", "/private/key", 1),
		"wg wrong key length":    strings.Replace(wg, "IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=", "aGVsbG8=", 1),
		"wg newline":             strings.Replace(wg, "10.0.0.2", `"10.0.0.2\npublic_key=malicious"`, 1),
		"wg reserved bounds":     strings.Replace(wg, "ip: 10.0.0.2", "ip: 10.0.0.2, reserved: [1, 2, 256]", 1),
		"wg keepalive":           strings.Replace(wg, "ip: 10.0.0.2", "ip: 10.0.0.2, persistent-keepalive: 25", 1),
		"wg resolver":            strings.Replace(wg, "ip: 10.0.0.2", "ip: 10.0.0.2, remote-dns-resolve: true", 1),
	}
	for _, kind := range []string{"direct", "dns", "reject", "rematch", "tailscale", "zerotier", "openvpn", "unknown"} {
		cases["excluded "+kind] = strings.Replace(vless, "type: vless", "type: "+kind, 1)
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(input)
			if err == nil {
				t.Fatal("unsafe input accepted")
			}
			if strings.Contains(err.Error(), "credential-must-not") || strings.Contains(err.Error(), "/private/") {
				t.Fatal("validation exposed secret input")
			}
		})
	}
}

func FuzzParseNodeYAML(f *testing.F) {
	f.Add(example)
	data, err := os.ReadFile("../../../FlClash/core/nodepolicy/testdata/proxies.yaml")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(data))
	f.Fuzz(func(t *testing.T, text string) {
		p, err := Parse(text)
		if err != nil {
			return
		}
		encoded, err := Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		// Marshal indentation may make a near-limit input exceed the size limit.
		if len(encoded) <= MaxYAMLBytes {
			if _, err := Parse(string(encoded)); err != nil {
				t.Fatal("accepted input failed normalization")
			}
		}
	})
}
