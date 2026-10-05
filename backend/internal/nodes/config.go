package nodes

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxYAMLBytes = 64 << 10
const MaxNodes = 128
const GroupName = "VPN"

var ErrInvalid = errors.New("invalid node configuration")
var hostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func ValidateKey(key string) bool {
	b, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(b) == 32
}

func aead(key string) (cipher.AEAD, error) {
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(b) != 32 {
		return nil, ErrInvalid
	}
	block, err := aes.NewCipher(b)
	if err != nil {
		return nil, ErrInvalid
	}
	return cipher.NewGCM(block)
}

func Encrypt(key, id string, data []byte) (string, error) {
	a, err := aead(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, data, []byte(id))), nil
}

func Decrypt(key, id, encoded string) ([]byte, error) {
	a, err := aead(key)
	if err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(b) < a.NonceSize()+a.Overhead() {
		return nil, ErrInvalid
	}
	return a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte(id))
}

func safeTree(n *yaml.Node, depth int, count *int) bool {
	*count++
	if depth > 8 || *count > 10000 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" || seen[key.Value] {
				return false
			}
			seen[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if !safeTree(child, depth+1, count) {
			return false
		}
	}
	return true
}

func Parse(text string) ([]map[string]any, error) {
	if len(text) == 0 || len(text) > MaxYAMLBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, ErrInvalid
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(text))
	count := 0
	if decoder.Decode(&document) != nil || len(document.Content) != 1 || !safeTree(&document, 0, &count) || decoder.Decode(new(yaml.Node)) != io.EOF {
		return nil, ErrInvalid
	}
	var source struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || len(root.Content) != 2 || root.Content[0].Value != "proxies" || document.Decode(&source) != nil || len(source.Proxies) == 0 || len(source.Proxies) > MaxNodes {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, p := range source.Proxies {
		name, _ := p["name"].(string)
		host, _ := p["server"].(string)
		port, portOK := p["port"].(int)
		kind, _ := p["type"].(string)
		if strings.TrimSpace(name) != name || name == "" || len(name) > 256 || utf8.RuneCountInString(name) > 120 || strings.ContainsFunc(name, unicode.IsControl) || seen[name] || name == GroupName || name == "DIRECT" || name == "REJECT" || name == "GLOBAL" || (!hostname.MatchString(host) && net.ParseIP(host) == nil) || strings.Contains(host, "..") || !portOK || port < 1 || port > 65535 {
			return nil, ErrInvalid
		}
		seen[name] = true
		if kind != "ss" && kind != "http" && kind != "socks5" {
			return nil, ErrInvalid
		}
		for key, value := range p {
			switch key {
			case "name", "server", "port", "type":
			case "udp", "tls":
				if _, ok := value.(bool); !ok || key == "tls" && kind == "ss" {
					return nil, ErrInvalid
				}
			case "password", "username", "sni":
				v, ok := value.(string)
				if !ok || len(v) > 1024 || strings.ContainsAny(v, "\x00\r\n") || kind == "ss" && key != "password" {
					return nil, ErrInvalid
				}
			case "cipher":
				if kind != "ss" || value != "aes-128-gcm" && value != "aes-256-gcm" && value != "chacha20-ietf-poly1305" {
					return nil, ErrInvalid
				}
			default:
				return nil, ErrInvalid
			}
		}
		if kind == "ss" && (p["cipher"] == nil || p["password"] == nil || p["password"] == "") {
			return nil, ErrInvalid
		}
	}
	return source.Proxies, nil
}

func Marshal(proxies []map[string]any) ([]byte, error) {
	return yaml.Marshal(map[string]any{"proxies": proxies})
}
