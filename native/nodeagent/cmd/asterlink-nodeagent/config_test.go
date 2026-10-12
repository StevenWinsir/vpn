package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimal = `# comment
NODEAGENT_API_URL=https://api.example.invalid:8443
NODEAGENT_CLIENT_CERT_FILE=/etc/agent/client.pem
NODEAGENT_CLIENT_KEY_FILE=/etc/agent/client.key
NODEAGENT_STATE_FILE=/var/lib/agent/state.json
NODEAGENT_VLESS_TLS_CERT_FILE=/etc/agent/vless.pem
NODEAGENT_VLESS_TLS_KEY_FILE=/etc/agent/vless.key
`

func TestConfigDefaultsAndOverrides(t *testing.T) {
	file, err := parseEnvFile(write(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(file, func(string) string { return "" })
	if err != nil || c.SyncEvery.Seconds() != 2 || c.ReportEvery.Seconds() != 5 || c.VLESSListen != "0.0.0.0:443" || c.AllowPrivateTargets {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = loadConfig(file, func(k string) string {
		return map[string]string{"NODEAGENT_SYNC_SECONDS": "3", "NODEAGENT_VLESS_LISTEN": "127.0.0.1:9443"}[k]
	})
	if err != nil || c.SyncEvery.Seconds() != 3 || c.VLESSListen != "127.0.0.1:9443" {
		t.Fatalf("environment must override the file: %+v %v", c, err)
	}
}

func TestConfigRejectsUnsafeInput(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":   minimal + "NODEAGENT_DEBUG=1\n",
		"duplicate":     minimal + "NODEAGENT_STATE_FILE=/x\n",
		"interpolation": minimal + "NODEAGENT_SERVER_CA_FILE=$HOME/ca.pem\n",
		"no equals":     minimal + "garbage\n",
	} {
		if _, err := parseEnvFile(write(t, body)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	file, _ := parseEnvFile(write(t, minimal))
	none := func(string) string { return "" }
	for name, env := range map[string]map[string]string{
		"bad interval":  {"NODEAGENT_SYNC_SECONDS": "0"},
		"huge interval": {"NODEAGENT_USAGE_SECONDS": "999"},
		"bad bool":      {"NODEAGENT_ALLOW_PRIVATE_TARGETS": "maybe"},
	} {
		if _, err := loadConfig(file, func(k string) string { return env[k] }); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// Required settings.
	if _, err := loadConfig(map[string]string{}, none); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty config: %v", err)
	}
	// Plaintext VLESS needs an explicit acknowledgement.
	plain := map[string]string{"NODEAGENT_API_URL": "https://a.example.invalid", "NODEAGENT_CLIENT_CERT_FILE": "c", "NODEAGENT_CLIENT_KEY_FILE": "k", "NODEAGENT_STATE_FILE": "s"}
	if _, err := loadConfig(plain, none); err == nil {
		t.Fatal("plaintext VLESS started without acknowledgement")
	}
	plain["NODEAGENT_ALLOW_PLAINTEXT"] = "true"
	if _, err := loadConfig(plain, none); err != nil {
		t.Fatal(err)
	}
	half := map[string]string{"NODEAGENT_API_URL": "https://a.example.invalid", "NODEAGENT_CLIENT_CERT_FILE": "c", "NODEAGENT_CLIENT_KEY_FILE": "k", "NODEAGENT_STATE_FILE": "s", "NODEAGENT_VLESS_TLS_CERT_FILE": "x"}
	if _, err := loadConfig(half, none); err == nil {
		t.Fatal("certificate without key accepted")
	}
}
