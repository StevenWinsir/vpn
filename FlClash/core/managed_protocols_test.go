//go:build !cgo

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"core/managed"
	"github.com/metacubex/mihomo/config"
	"gopkg.in/yaml.v3"
	"vpn/nodepolicy"
)

func TestManagedProtocolCoreRevision(t *testing.T) {
	got, err := exec.Command("git", "-C", "Clash.Meta", "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(got)) != nodepolicy.CoreRevision {
		t.Fatal("review node policy and fixtures whenever the pinned Core changes")
	}
}

func TestManagedProtocolsUseRealPinnedCore(t *testing.T) {
	data, err := os.ReadFile("nodepolicy/testdata/proxies.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, proxy := range source.Proxies {
		name := proxy["name"].(string)
		seen[proxy["type"].(string)] = true
		t.Run(name, func(t *testing.T) {
			engine := newManagedTestEngine(t)
			encoded, err := yaml.Marshal(map[string]any{"proxies": []map[string]any{proxy}, "proxy-groups": []map[string]any{{"name": "VIP", "type": "select", "proxies": []string{name}}}, "rules": []string{"MATCH,VIP"}})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := engine.Prepare(context.Background(), managedProfileFixture(string(encoded)))
			if err != nil {
				// Only synthetic fixtures are allowed in diagnostic Core parser output.
				raw, _, _ := managedRawConfig(string(encoded))
				if raw != nil {
					_, diagnostic := config.ParseRawConfig(raw)
					t.Fatalf("prepare: %v; fixture parser: %v", err, diagnostic)
				}
				t.Fatalf("prepare: %v", err)
			}
			view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "fixture-user", SessionID: "session-a"})
			if err != nil || len(view.Groups) != 1 || view.Groups[0].Selected != name {
				t.Fatalf("apply: %v", err)
			}
			if isRunning.Load() || currentConfig.General.Tun.Enable || currentConfig.General.MixedPort != 0 {
				t.Fatal("preparing a node activated networking")
			}
		})
	}
	for _, kind := range nodepolicy.Supported() {
		if !seen[kind] {
			t.Fatalf("no real Core fixture for %s", kind)
		}
	}
}

func TestManagedProtocolRejectsNestedFileAndRoutingOptions(t *testing.T) {
	for _, extra := range []string{
		"dialer-proxy: DIRECT", "certificate: /private/secret.pem", "routing-mark: 7",
		"network: ws, ws-opts: {path: /, certificate: /private/key}",
		"network: ws, ws-opts: {path: /, download-settings: {server: metadata.internal}}",
	} {
		yaml := "proxies: [{name: safe-node, type: vless, server: 127.0.0.1, port: 9, uuid: 11111111-2222-4333-8444-555555555555, " + extra + "}]\nproxy-groups: [{name: VIP, type: select, proxies: [safe-node]}]\nrules: ['MATCH,VIP']\n"
		if _, _, err := managedRawConfig(yaml); err == nil {
			t.Fatal("Core accepted unsafe options")
		}
	}
}
