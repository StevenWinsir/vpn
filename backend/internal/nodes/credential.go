package nodes

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// ErrUnsupportedCredential: the proxy type has no per-user credential scheme the
// platform can mint. Such a node can never be an authoritative (grant) node.
var ErrUnsupportedCredential = errors.New("proxy type has no per-user credential scheme")

// CredentialFields maps a proxy type to the single field a grant replaces. Shadowsocks
// is deliberately absent: a shared-port AEAD server cannot tell users apart, so it
// needs a per-grant port scheme that no agent adapter implements yet.
var CredentialFields = map[string]string{"vless": "uuid", "vmess": "uuid", "trojan": "password"}

// NewCredential mints the secret for one grant of a proxy of the given type.
func NewCredential(proxyType string) (map[string]string, error) {
	field, ok := CredentialFields[proxyType]
	if !ok {
		return nil, ErrUnsupportedCredential
	}
	if field == "uuid" {
		return map[string]string{field: uuid.NewString()}, nil
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return map[string]string{field: base64.RawURLEncoding.EncodeToString(secret)}, nil
}

// ApplyCredential returns a copy of proxy whose credential field is replaced. The
// administrator's shared secret never reaches a grant-mode client.
func ApplyCredential(proxy map[string]any, credential map[string]string) (map[string]any, error) {
	kind, _ := proxy["type"].(string)
	field, ok := CredentialFields[kind]
	value := credential[field]
	if !ok || value == "" || len(credential) != 1 {
		return nil, ErrUnsupportedCredential
	}
	out := make(map[string]any, len(proxy))
	for k, v := range proxy {
		out[k] = v
	}
	out[field] = value
	return out, nil
}

func SealCredential(key, grantID string, credential map[string]string) (string, error) {
	data, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	return Encrypt(key, "grant:"+grantID, data)
}

func OpenCredential(key, grantID, sealed string) (map[string]string, error) {
	data, err := Decrypt(key, "grant:"+grantID, sealed)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if json.Unmarshal(data, &out) != nil || len(out) != 1 {
		return nil, ErrInvalid
	}
	return out, nil
}
