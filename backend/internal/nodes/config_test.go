package nodes

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

const example = "proxies:\n  - {name: '香港测试', type: ss, server: 127.0.0.1, port: 18443, cipher: aes-256-gcm, password: fixture-only-secret, udp: true}\n"

func TestNodeYAML(t *testing.T) {
	for _, input := range []string{example, "proxies: [{name: test, type: http, server: localhost, port: 8080}]"} {
		p, err := Parse(input)
		if err != nil || len(p) != 1 {
			t.Fatal("valid inline node rejected")
		}
		encoded, err := Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Parse(string(encoded)); err != nil {
			t.Fatal("normalized node cannot be read")
		}
	}
	invalid := []string{
		"", "proxies: []", example + "---\nproxies: []", example + "rules: ['MATCH,DIRECT']",
		strings.Replace(example, "port: 18443", "port: -1", 1),
		strings.Replace(example, "port: 18443", "port: '18443'", 1),
		strings.Replace(example, "aes-256-gcm", "unknown", 1),
		strings.Replace(example, "127.0.0.1", `tw\.example.invalid`, 1),
		strings.Replace(example, "udp: true", "udp: true, plugin: arbitrary", 1),
		strings.Replace(example, "udp: true", "udp: true, password: duplicate", 1),
		"proxies: [&node {name: test, type: http, server: localhost, port: 8080}, *node]",
		"proxies: [{name: VPN, type: http, server: localhost, port: 8080}]",
		strings.Repeat("x", MaxYAMLBytes+1),
	}
	for i, input := range invalid {
		if _, err := Parse(input); err == nil {
			t.Fatalf("invalid YAML accepted, case %d", i)
		}
	}
}

func TestNodeEncryption(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	first, err := Encrypt(key, "node-a", []byte(example))
	if err != nil || strings.Contains(first, "fixture-only-secret") {
		t.Fatal("configuration was not encrypted")
	}
	second, err := Encrypt(key, "node-a", []byte(example))
	if err != nil || first == second {
		t.Fatal("nonce reuse")
	}
	data, err := Decrypt(key, "node-a", first)
	if err != nil || string(data) != example {
		t.Fatal("encrypted configuration did not round trip")
	}
	if _, err = Decrypt(key, "node-b", first); err == nil {
		t.Fatal("ciphertext could be substituted between nodes")
	}
	encoded, _ := base64.StdEncoding.DecodeString(first)
	encoded[len(encoded)-1] ^= 1
	if _, err = Decrypt(key, "node-a", base64.StdEncoding.EncodeToString(encoded)); err == nil {
		t.Fatal("tampered configuration accepted")
	}
	if _, err = Encrypt("invalid", "node-a", data); err == nil {
		t.Fatal("invalid key accepted")
	}
}
