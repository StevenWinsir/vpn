package main

import (
	"context"
	"core/managed"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/updater"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	"github.com/metacubex/mihomo/dns"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/route"
	"github.com/metacubex/mihomo/tunnel"
	"gopkg.in/yaml.v3"
	"vpn/nodepolicy"
)

type managedProfileEngine struct {
	network managedNetwork
	home    string
	view    *managed.ConfigurationView
}

type preparedManagedConfiguration struct {
	engine  *managedProfileEngine
	profile managed.Profile
	config  *config.Config
	groups  []managed.ConfigurationGroup
	names   []string
}

var managedProfiles = &managedProfileEngine{}

func managedConfigError(code string) error { return &managed.APIError{Code: code} }

func validManagedName(name string) bool {
	return name != "" && len(name) <= 256 && !strings.ContainsFunc(name, unicode.IsControl)
}

func validateManagedYAML(node *yaml.Node, depth int, count *int) bool {
	*count++
	if depth > 32 || *count > 50000 || node.Kind == yaml.AliasNode || node.Anchor != "" {
		return false
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" || seen[key.Value] {
				return false
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if !validateManagedYAML(child, depth+1, count) {
			return false
		}
	}
	return true
}

func managedRawConfig(data string) (*config.RawConfig, []managed.ConfigurationGroup, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(data))
	count := 0
	if decoder.Decode(&document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode || !validateManagedYAML(&document, 0, &count) || decoder.Decode(new(yaml.Node)) != io.EOF {
		return nil, nil, managedConfigError("invalid_client_config")
	}
	var source map[string]any
	if document.Decode(&source) != nil {
		return nil, nil, managedConfigError("invalid_client_config")
	}
	routing := map[string]any{}
	for key, value := range source {
		switch key {
		case "proxies", "proxy-groups", "rules", "dns", "mode", "ipv6", "tcp-concurrent", "unified-delay":
			routing[key] = value
		case "port", "socks-port", "redir-port", "tproxy-port", "mixed-port", "allow-lan", "bind-address", "authentication", "skip-auth-prefixes", "lan-allowed-ips", "lan-disallowed-ips", "tun", "external-controller", "external-controller-tls", "external-controller-unix", "external-controller-pipe", "external-controller-cors", "external-controller-routing-mark", "external-doh-server", "external-ui", "external-ui-url", "external-ui-name", "secret", "profile", "log-level", "geo-auto-update", "geo-update-interval":
		default:
			return nil, nil, managedConfigError("unsupported_managed_configuration")
		}
	}
	encoded, err := yaml.Marshal(routing)
	if err != nil {
		return nil, nil, managedConfigError("invalid_client_config")
	}
	raw, err := config.UnmarshalRawConfig(encoded)
	if err != nil {
		return nil, nil, managedConfigError("invalid_client_config")
	}
	if len(raw.Proxy) == 0 || len(raw.Proxy) > 512 || len(raw.ProxyGroup) == 0 || len(raw.ProxyGroup) > 128 || len(raw.Rule) == 0 || len(raw.Rule) > 10000 {
		return nil, nil, managedConfigError("invalid_client_config")
	}
	for _, proxy := range raw.Proxy {
		name, _ := proxy["name"].(string)
		if !validManagedName(name) {
			return nil, nil, managedConfigError("invalid_client_config")
		}
		if err := nodepolicy.Validate(proxy); err != nil {
			if errors.Is(err, nodepolicy.ErrUnsupported) {
				return nil, nil, managedConfigError("unsupported_managed_configuration")
			}
			return nil, nil, managedConfigError("invalid_client_config")
		}
	}
	groups := make([]managed.ConfigurationGroup, 0, len(raw.ProxyGroup))
	for _, group := range raw.ProxyGroup {
		name, _ := group["name"].(string)
		if !validManagedName(name) {
			return nil, nil, managedConfigError("invalid_client_config")
		}
		if group["type"] != "select" {
			return nil, nil, managedConfigError("unsupported_managed_configuration")
		}
		for key := range group {
			switch key {
			case "name", "type", "proxies", "hidden", "icon", "disable-udp":
			default:
				return nil, nil, managedConfigError("unsupported_managed_configuration")
			}
		}
		values, ok := group["proxies"].([]any)
		if !ok || len(values) == 0 || len(values) > 512 {
			return nil, nil, managedConfigError("invalid_client_config")
		}
		members := make([]string, 0, len(values))
		for _, value := range values {
			member, ok := value.(string)
			if !ok || !validManagedName(member) {
				return nil, nil, managedConfigError("invalid_client_config")
			}
			members = append(members, member)
		}
		groups = append(groups, managed.ConfigurationGroup{Name: name, Type: "select", Selected: members[0], Proxies: members})
	}
	for _, rule := range raw.Rule {
		for _, token := range strings.Split(strings.ToUpper(rule), ",") {
			switch strings.Trim(token, " ()") {
			case "GEOIP", "GEOSITE", "IP-ASN", "RULE-SET", "SUB-RULE", "SCRIPT":
				return nil, nil, managedConfigError("unsupported_managed_configuration")
			}
		}
	}
	if raw.DNS.Enable {
		if dnsSource, ok := source["dns"].(map[string]any); ok {
			if filter, ok := dnsSource["fallback-filter"].(map[string]any); ok && filter["geoip"] == true {
				return nil, nil, managedConfigError("unsupported_managed_configuration")
			}
		}
		if len(raw.DNS.FallbackFilter.GeoSite) > 0 || strings.Contains(strings.ToLower(string(encoded)), "geosite:") || strings.Contains(strings.ToLower(string(encoded)), "geoip:") || strings.Contains(strings.ToLower(string(encoded)), "rule-set:") || strings.Contains(strings.ToUpper(string(encoded)), "GEOIP,") || strings.Contains(strings.ToUpper(string(encoded)), "GEOSITE,") || strings.Contains(strings.ToUpper(string(encoded)), "RULE-SET,") {
			return nil, nil, managedConfigError("unsupported_managed_configuration")
		}
	}
	raw.DNS.Listen = ""
	raw.DNS.FallbackFilter.GeoIP = false
	raw.Profile.StoreSelected, raw.Profile.StoreFakeIP = false, false
	raw.ExternalUIURL = ""
	return raw, groups, nil
}

func (e *managedProfileEngine) Prepare(ctx context.Context, profile managed.Profile) (prepared managed.PreparedConfiguration, err error) {
	defer func() {
		if recover() != nil {
			prepared, err = nil, managedConfigError("invalid_client_config")
		}
	}()
	digest := sha256.Sum256([]byte(profile.YAML))
	if len(profile.YAML) == 0 || len(profile.YAML) > 1<<20 || hex.EncodeToString(digest[:]) != profile.Version || profile.Version != profile.Session.ProfileVersion {
		return nil, managedConfigError("invalid_client_config")
	}
	if ctx.Err() != nil {
		return nil, managedConfigError("operation_superseded")
	}
	raw, groups, err := managedRawConfig(profile.YAML)
	if err != nil {
		return nil, err
	}
	if !profile.ValidCatalog() {
		return nil, managedConfigError("invalid_client_config")
	}
	if len(profile.Nodes) != 0 {
		selected := ""
		names := make([]string, 0, len(profile.Nodes))
		for _, node := range profile.Nodes {
			names = append(names, node.Name)
			if node.ID == profile.NodeID {
				selected = node.Name
			}
		}
		if len(raw.Proxy) != 1 || raw.Proxy[0]["name"] != selected || len(groups) != 1 || groups[0].Name != "VPN" || len(groups[0].Proxies) != 1 || groups[0].Selected != selected || len(raw.Rule) != 1 || raw.Rule[0] != "MATCH,VPN" {
			return nil, managedConfigError("invalid_client_config")
		}
		groups[0].Proxies = names
	}
	configMu.Lock()
	defer configMu.Unlock()
	if e.home == "" || !isInit.Load() {
		return nil, managedConfigError("managed_configuration_required")
	}
	previousNames := config.GetProxyNameList()
	defer config.SetProxyNameList(previousNames)
	e.networkPolicy().Configure(raw)
	raw.Rule = append(raw.Rule, "MATCH,REJECT")
	cfg, err := config.ParseRawConfig(raw)
	if err != nil {
		return nil, managedConfigError("invalid_client_config")
	}
	if ctx.Err() != nil {
		closeManagedAdapters(cfg)
		return nil, managedConfigError("operation_superseded")
	}
	return &preparedManagedConfiguration{e, profile, cfg, groups, append([]string(nil), config.GetProxyNameList()...)}, nil
}

func (p *preparedManagedConfiguration) Apply(ctx context.Context, owner managed.ConfigurationOwner) (view managed.ConfigurationView, err error) {
	configMu.Lock()
	defer configMu.Unlock()
	defer func() {
		if recover() != nil {
			err = managedConfigError("configuration_apply_failed")
		}
		if err != nil {
			closeManagedAdapters(p.config)
			_ = p.engine.clearLocked()
		}
	}()
	if ctx.Err() != nil || !isInit.Load() {
		return view, managedConfigError("operation_superseded")
	}
	id, err := managed.StoreConfiguration(p.engine.home, owner, p.profile)
	if err != nil {
		return view, err
	}
	if ctx.Err() != nil {
		return view, managedConfigError("operation_superseded")
	}
	view = managed.ConfigurationView{ID: id, Version: p.profile.Version, Owner: owner, Groups: p.groups}
	p.engine.view = managed.CopyConfiguration(&view)
	isRunning.Store(false)
	config.SetProxyNameList(p.names)
	if currentConfig != p.config {
		closeManagedAdapters(currentConfig)
	}
	hub.ApplyConfig(p.config)
	tunnel.OnSuspend()
	currentConfig = p.config
	if ctx.Err() != nil {
		return view, managedConfigError("operation_superseded")
	}
	scheduleReclaimOwnership()
	return view, nil
}

// In particular WireGuard owns a userspace IP stack/device. Do not wait for
// the Go finalizer to release old adapters after logout or node replacement.
// The pinned Core's autoCloseProxyAdapter makes Close idempotent.
func closeManagedAdapters(cfg *config.Config) {
	if cfg == nil {
		return
	}
	for _, proxy := range cfg.Proxies {
		_ = proxy.Close()
	}
}

func (e *managedProfileEngine) clearLocked() error {
	if e.view != nil {
		if currentConfig != nil {
			closeManagedAdapters(currentConfig)
			for _, provider := range currentConfig.Providers {
				if closer, ok := provider.(io.Closer); ok {
					_ = closer.Close()
				}
			}
			for _, provider := range currentConfig.RuleProviders {
				if closer, ok := provider.(io.Closer); ok {
					_ = closer.Close()
				}
			}
		}
		currentConfig = nil
		config.SetProxyNameList(nil)
		tunnel.OnSuspend()
		tunnel.UpdateProxies(map[string]C.Proxy{}, map[string]P.ProxyProvider{})
		tunnel.UpdateRules(nil, nil, map[string]P.RuleProvider{})
		resolver.DefaultResolver, resolver.DefaultHostMapper, resolver.DefaultService = nil, nil, nil
		resolver.ProxyServerHostResolver, resolver.DirectHostResolver = nil, nil
		dns.ReCreateServer("", nil, nil)
		route.ReCreateServer(&route.Config{})
		updater.StopGeoUpdater()
	}
	e.view = nil
	if e.home == "" {
		return nil
	}
	return managed.ClearStoredConfiguration(e.home)
}

func (e *managedProfileEngine) Clear() error {
	configMu.Lock()
	defer configMu.Unlock()
	return e.clearLocked()
}

func (e *managedProfileEngine) Select(ctx context.Context, owner managed.ConfigurationOwner, id, groupName, proxyName string) ([]managed.ConfigurationGroup, error) {
	configMu.Lock()
	defer configMu.Unlock()
	if ctx.Err() != nil || e.view == nil || e.view.ID != id || e.view.Owner != owner {
		return nil, managedConfigError("operation_superseded")
	}
	for i, group := range e.view.Groups {
		if group.Name != groupName {
			continue
		}
		if !slices.Contains(group.Proxies, proxyName) {
			break
		}
		if handleChangeProxy(&ChangeProxyParams{GroupName: groupName, ProxyName: proxyName}) != "" {
			break
		}
		e.view.Groups[i].Selected = proxyName
		return managed.CopyConfiguration(e.view).Groups, nil
	}
	return nil, managedConfigError("invalid_managed_selection")
}
