package managed

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	"vpn/nodepolicy"
)

// maxProfileBytes matches the server's 1 MiB profile ceiling.
const maxProfileBytes = 1 << 20

var errConfigRejected = errors.New("config rejected")

// Profile is the only thing the Core keeps from a node download: one validated
// proxy definition. The raw YAML is dropped as soon as it has been parsed, and no
// field of this type is ever serialised into a Snapshot or a log line.
type Profile struct {
	NodeID  string
	Version string
	Group   string
	Proxy   map[string]any
}

// ParseProfile enforces, in order: size, SHA-256 equality with the advertised
// version, strict single-node structure, then the shared nodepolicy allowlist.
// The hash is a consistency check; transport security remains HTTPS (section 6.1).
func ParseProfile(nodeID, wantName, version, document string) (Profile, error) {
	if len(document) == 0 || len(document) > maxProfileBytes {
		return Profile{}, errConfigRejected
	}
	digest := sha256.Sum256([]byte(document))
	got := hex.EncodeToString(digest[:])
	if len(version) != len(got) || subtle.ConstantTimeCompare([]byte(got), []byte(version)) != 1 {
		return Profile{}, fmt.Errorf("%w: version mismatch", errConfigRejected)
	}
	var parsed struct {
		Mode   string           `yaml:"mode"`
		Proxy  []map[string]any `yaml:"proxies"`
		Groups []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(document))
	decoder.KnownFields(true)
	if err := decoder.Decode(&parsed); err != nil {
		return Profile{}, fmt.Errorf("%w: yaml", errConfigRejected)
	}
	if len(parsed.Proxy) != 1 || len(parsed.Groups) != 1 || len(parsed.Rules) != 1 {
		return Profile{}, fmt.Errorf("%w: shape", errConfigRejected)
	}
	proxy := parsed.Proxy[0]
	name, _ := proxy["name"].(string)
	group := parsed.Groups[0]
	if name == "" || group.Name == "" || group.Type != "select" || len(group.Proxies) != 1 || group.Proxies[0] != name ||
		parsed.Rules[0] != "MATCH,"+group.Name || (parsed.Mode != "" && parsed.Mode != "rule") {
		return Profile{}, fmt.Errorf("%w: routing", errConfigRejected)
	}
	if wantName != "" && name != wantName {
		return Profile{}, fmt.Errorf("%w: node mismatch", errConfigRejected)
	}
	if err := nodepolicy.Validate(proxy); err != nil {
		return Profile{}, fmt.Errorf("%w: policy", errConfigRejected)
	}
	return Profile{NodeID: nodeID, Version: version, Group: group.Name, Proxy: proxy}, nil
}

// String deliberately prints nothing sensitive, so an accidental %v in a log is harmless.
func (p Profile) String() string { return "profile(" + p.NodeID + ")" }

// GoString guards %#v the same way.
func (p Profile) GoString() string { return p.String() }
