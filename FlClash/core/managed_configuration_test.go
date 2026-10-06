//go:build !cgo

package main

import (
	"context"
	"core/managed"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
)

const managedFixtureYAML = `mixed-port: 17991
allow-lan: true
external-controller: 127.0.0.1:17992
external-ui-url: https://must-not-download.invalid/archive.zip
tun: {enable: true}
profile: {store-selected: true}
proxies:
  - {name: Server-A, type: socks5, server: 127.0.0.1, port: 9, username: fixture, password: private-fixture-password}
  - {name: Server-B, type: socks5, server: 127.0.0.1, port: 10}
proxy-groups:
  - {name: VIP, type: select, proxies: [Server-A, Server-B]}
rules: ['MATCH,VIP']
`

func managedProfileFixture(data string) managed.Profile {
	digest := sha256.Sum256([]byte(data))
	version := hex.EncodeToString(digest[:])
	return managed.Profile{YAML: data, Version: version, Session: managed.Status{SessionID: "session-a", ProfileVersion: version}}
}

func newManagedTestEngine(t *testing.T) *managedProfileEngine {
	t.Helper()
	engine := &managedProfileEngine{home: t.TempDir()}
	previousHome := C.Path.HomeDir()
	C.SetHomeDir(engine.home)
	wasInit := isInit.Swap(true)
	t.Cleanup(func() {
		handleStopListener()
		handleCloseConnections()
		if err := engine.Clear(); err != nil {
			t.Error(err)
		}
		isInit.Store(wasInit)
		settleMessageBatcher()
		C.SetHomeDir(previousHome)
	})
	return engine
}

func applyManagedFixture(t *testing.T, engine *managedProfileEngine) managed.ConfigurationView {
	t.Helper()
	prepared, err := engine.Prepare(context.Background(), managedProfileFixture(managedFixtureYAML))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "user-a", SessionID: "session-a"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return view
}

func TestManagedConfigurationAppliesRealMihomoWithoutListenersOrLocalOverrides(t *testing.T) {
	engine := newManagedTestEngine(t)
	legacy := filepath.Join(engine.home, "config.yaml")
	if err := os.WriteFile(legacy, []byte("legacy-file-unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	view := applyManagedFixture(t, engine)
	if currentConfig == nil || tunnel.AllProxies()["Server-A"] == nil || len(currentConfig.Rules) != 1 {
		t.Fatal("configuration was not applied to Mihomo")
	}
	if isRunning.Load() || currentConfig.General.MixedPort != 0 || currentConfig.General.AllowLan || currentConfig.General.Tun.Enable || currentConfig.Controller.ExternalController != "" || currentConfig.Controller.ExternalUIURL != "" || currentConfig.Profile.StoreSelected || currentConfig.NTP.Enable || currentConfig.DNS.Listen != "" {
		t.Fatal("managed runtime policy was bypassed")
	}
	if view.Owner.UserID != "user-a" || len(view.ID) != 32 || len(view.Groups) != 1 || view.Groups[0].Selected != "Server-A" {
		t.Fatal("invalid owned metadata")
	}
	data, err := os.ReadFile(filepath.Join(engine.home, managed.ManagedDirectory, "active.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored managed.StoredConfiguration
	if json.Unmarshal(data, &stored) != nil || stored.YAML != managedFixtureYAML || stored.Owner != view.Owner || stored.ID != view.ID {
		t.Fatal("atomic profile bundle is inconsistent")
	}
	for _, target := range []struct {
		name string
		mode os.FileMode
	}{{managed.ManagedDirectory, 0700}, {filepath.Join(managed.ManagedDirectory, "active.json"), 0600}} {
		info, err := os.Stat(filepath.Join(engine.home, target.name))
		if err != nil || info.Mode().Perm() != target.mode {
			t.Fatal("managed storage permissions are not private")
		}
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "private-fixture-password") || strings.Contains(string(encoded), "yaml") {
		t.Fatal("configuration leaked to UI metadata")
	}
	if data, _ := os.ReadFile(legacy); string(data) != "legacy-file-unchanged" {
		t.Fatal("legacy profile overwritten")
	}
	if err := engine.Clear(); err != nil {
		t.Fatal(err)
	}
	if currentConfig != nil || len(tunnel.AllProxies()) != 0 {
		t.Fatal("cleared config retained kernel proxies")
	}
	if _, err := os.Stat(filepath.Join(engine.home, managed.ManagedDirectory, "active.json")); !os.IsNotExist(err) {
		t.Fatal("managed bytes survived cleanup")
	}
}

func TestManagedConfigurationSemanticFailuresNeverApply(t *testing.T) {
	cases := map[string]string{
		"missing proxy":       strings.ReplaceAll(managedFixtureYAML, "[Server-A, Server-B]", "[MISSING]"),
		"missing rule target": strings.ReplaceAll(managedFixtureYAML, "MATCH,VIP", "MATCH,MISSING"),
		"invalid node":        strings.ReplaceAll(managedFixtureYAML, "port: 9,", "port: invalid,"),
		"cycle":               strings.ReplaceAll(managedFixtureYAML, "[Server-A, Server-B]", "[VIP]"),
		"empty nodes":         "proxies: []\nproxy-groups: []\nrules: ['MATCH,DIRECT']\n",
		"duplicate mapping":   managedFixtureYAML + "proxies: []\n",
		"multiple documents":  managedFixtureYAML + "---\nproxies: []\n",
		"alias":               strings.ReplaceAll(managedFixtureYAML, "proxies: [Server-A, Server-B]", "proxies: &members [Server-A, Server-B]"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			engine := newManagedTestEngine(t)
			if _, err := engine.Prepare(context.Background(), managedProfileFixture(data)); managed.PublicError(err) != "invalid_client_config" {
				t.Fatalf("invalid config accepted: %v", err)
			}
			if engine.view != nil {
				t.Fatal("invalid config became active")
			}
			if _, err := os.Stat(filepath.Join(engine.home, managed.ManagedDirectory)); !os.IsNotExist(err) {
				t.Fatal("validation wrote a partial configuration")
			}
		})
	}
	if _, err := config.UnmarshalRawConfig([]byte(cases["missing proxy"])); err != nil {
		t.Fatal("test must distinguish YAML decoding from full semantic validation")
	}
}

func TestManagedConfigurationRejectsSecondarySourcesAndBackgroundDialers(t *testing.T) {
	for _, extra := range []string{"proxy-providers: {Remote: {type: http, url: 'https://unused.invalid/nodes'}}\n", "rule-providers: {}\n", "script: {code: unsafe}\n", "listeners: [{type: socks, port: 18993}]\n", "tunnels: ['tcp,127.0.0.1:18994,127.0.0.1:9,DIRECT']\n", "tls: {certificate: /private/secret.pem}\n"} {
		t.Run(strings.Split(extra, ":")[0], func(t *testing.T) {
			engine := newManagedTestEngine(t)
			if _, err := engine.Prepare(context.Background(), managedProfileFixture(managedFixtureYAML+extra)); managed.PublicError(err) != "unsupported_managed_configuration" {
				t.Fatal("secondary source accepted")
			}
		})
	}
	for _, data := range []string{strings.ReplaceAll(managedFixtureYAML, "type: select", "type: url-test"), strings.ReplaceAll(managedFixtureYAML, "type: socks5", "type: direct"), strings.ReplaceAll(managedFixtureYAML, "MATCH,VIP", "GEOIP,CN,VIP"), managedFixtureYAML + "dns: {enable: true, fallback-filter: {geoip: true}}\n"} {
		engine := newManagedTestEngine(t)
		if _, err := engine.Prepare(context.Background(), managedProfileFixture(data)); managed.PublicError(err) != "unsupported_managed_configuration" {
			t.Fatal("unmetered background source accepted")
		}
	}
}

func TestManagedSelectionRequiresExactOwnerAndServerMembership(t *testing.T) {
	engine := newManagedTestEngine(t)
	view := applyManagedFixture(t, engine)
	for _, pair := range [][2]string{{"GLOBAL", "Server-B"}, {"VIP", "DIRECT"}, {"VIP", ""}, {"VIP", "imported-node"}} {
		if _, err := engine.Select(context.Background(), view.Owner, view.ID, pair[0], pair[1]); managed.PublicError(err) != "invalid_managed_selection" {
			t.Fatal("unregistered selection accepted")
		}
	}
	owner := view.Owner
	owner.Generation++
	if _, err := engine.Select(context.Background(), owner, view.ID, "VIP", "Server-B"); managed.PublicError(err) != "operation_superseded" {
		t.Fatal("stale owner accepted")
	}
	if _, err := engine.Select(context.Background(), view.Owner, "old-id", "VIP", "Server-B"); managed.PublicError(err) != "operation_superseded" {
		t.Fatal("stale configuration accepted")
	}
	groups, err := engine.Select(context.Background(), view.Owner, view.ID, "VIP", "Server-B")
	if err != nil || groups[0].Selected != "Server-B" || isRunning.Load() {
		t.Fatal("server-owned selection failed or opened listener")
	}
	proxy, err := selectableGroup("VIP")
	if err != nil {
		t.Fatal(err)
	}
	if selected, ok := proxy.(interface{ Now() string }); !ok || selected.Now() != "Server-B" {
		t.Fatal("selection did not reach the actual kernel")
	}
}

func TestManagedPrepareAndApplyHonorCancellation(t *testing.T) {
	engine := newManagedTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Prepare(ctx, managedProfileFixture(managedFixtureYAML)); managed.PublicError(err) != "operation_superseded" {
		t.Fatal("cancelled prepare ran")
	}
	prepared, err := engine.Prepare(context.Background(), managedProfileFixture(managedFixtureYAML))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Apply(ctx, managed.ConfigurationOwner{Generation: 1, UserID: "user-a", SessionID: "session-a"}); managed.PublicError(err) != "operation_superseded" {
		t.Fatal("cancelled apply ran")
	}
	if engine.view != nil {
		t.Fatal("cancelled configuration became active")
	}
}
