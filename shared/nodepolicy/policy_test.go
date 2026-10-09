package nodepolicy

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validVLESS() map[string]any {
	return map[string]any{"name": "fixture", "type": "vless", "server": "localhost", "port": 443, "uuid": "11111111-2222-4333-8444-555555555555"}
}

func TestStrictTypesAndUnknownFields(t *testing.T) {
	for key, values := range map[string][]any{
		"port":        {"443", 443.0, -1, 0, 65536, nil},
		"uuid":        {"", "00000000-0000-0000-0000-000000000000", "bad", 42},
		"server":      {"/path", "https://example.invalid", "host..invalid", "-host.invalid", "127.0.0.1\nextra"},
		"name":        {"DIRECT", "direct", "VPN", "bad\nname", " bad"},
		"tls":         {"true", 1, nil},
		"certificate": {"/private/key"}, "dialer-proxy": {"DIRECT"}, "interface-name": {"en0"},
	} {
		for _, value := range values {
			p := validVLESS()
			p[key] = value
			if Validate(p) == nil {
				t.Fatalf("accepted invalid field %s", key)
			}
		}
	}
}

func TestRealityAndVisionBindings(t *testing.T) {
	p := validVLESS()
	p["tls"], p["network"], p["flow"], p["client-fingerprint"] = true, "tcp", "xtls-rprx-vision", "chrome"
	r := map[string]any{"public-key": base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("r", 32))), "short-id": "0123456789abcdef"}
	p["reality-opts"] = r
	if err := Validate(p); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){
		func(p map[string]any) { p["tls"] = false },
		func(p map[string]any) { p["network"] = "ws" },
		func(p map[string]any) { p["client-fingerprint"] = "" },
		func(p map[string]any) { p["reality-opts"] = map[string]any{"public-key": "bad", "short-id": "00"} },
		func(p map[string]any) {
			p["reality-opts"] = map[string]any{"public-key": r["public-key"], "short-id": "abc"}
		},
		func(p map[string]any) {
			p["reality-opts"] = map[string]any{"public-key": r["public-key"], "short-id": 0}
		},
	} {
		copy := map[string]any{}
		for k, v := range p {
			copy[k] = v
		}
		mutate(copy)
		if Validate(copy) == nil {
			t.Fatal("accepted invalid Reality/vision binding")
		}
	}
}

func TestSS2022KeysAndPluginOptions(t *testing.T) {
	p := map[string]any{"name": "fixture", "type": "ss", "server": "localhost", "port": 443, "cipher": "2022-blake3-aes-128-gcm", "password": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 16)))}
	if err := Validate(p); err != nil {
		t.Fatal(err)
	}
	for _, password := range []any{"not-base64", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), "", 123} {
		p["password"] = password
		if Validate(p) == nil {
			t.Fatal("accepted bad SS2022 key")
		}
	}
	p["cipher"], p["password"], p["plugin"] = "aes-128-gcm", "fixture-secret", "v2ray-plugin"
	p["plugin-opts"] = map[string]any{"mode": "websocket", "path": "/ws", "tls": true}
	if err := Validate(p); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"certificate", "private-key", "path-to-key", "exec"} {
		p["plugin-opts"] = map[string]any{"mode": "websocket", key: "/private/key"}
		if Validate(p) == nil {
			t.Fatal("accepted nested file/execution option")
		}
	}
}

func TestSupportedReturnsOwnedList(t *testing.T) {
	a := Supported()
	a[0] = "mutated"
	if Supported()[0] == "mutated" || len(Supported()) != 15 {
		t.Fatal("capabilities mutable or unreviewed")
	}
}
