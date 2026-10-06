package nodes

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	"vpn/nodepolicy"
)

const MaxYAMLBytes = 64 << 10
const MaxNodes = 128
const GroupName = "VPN"

var ErrInvalid = errors.New("invalid node configuration")

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
	if depth > 12 || *count > 10000 || n.Kind == yaml.AliasNode || n.Anchor != "" {
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
	for index, p := range source.Proxies {
		if err := nodepolicy.Validate(p); err != nil {
			// Only fixed policy errors and the ordinal, never YAML values or keys.
			return nil, fmt.Errorf("%w: node %d: %v", ErrInvalid, index+1, err)
		}
		name, _ := p["name"].(string)
		if seen[name] {
			return nil, ErrInvalid
		}
		seen[name] = true
	}
	return source.Proxies, nil
}

func Marshal(proxies []map[string]any) ([]byte, error) {
	return yaml.Marshal(map[string]any{"proxies": proxies})
}
