package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// config holds every operational constant of the agent. They live in an env file (see
// .env.example) so they can be edited without rebuilding; nothing here is a secret
// itself (secrets stay in the key files the paths point at).
type config struct {
	APIURL                 string
	ClientCertFile         string
	ClientKeyFile          string
	ServerCAFile           string
	StateFile              string
	VLESSListen            string
	VLESSTLSCertFile       string
	VLESSTLSKeyFile        string
	AllowPlaintext         bool
	AllowPrivateTargets    bool
	SyncEvery, ReportEvery time.Duration
}

var knownKeys = map[string]bool{
	"NODEAGENT_API_URL": true, "NODEAGENT_CLIENT_CERT_FILE": true, "NODEAGENT_CLIENT_KEY_FILE": true, "NODEAGENT_SERVER_CA_FILE": true,
	"NODEAGENT_STATE_FILE": true, "NODEAGENT_VLESS_LISTEN": true, "NODEAGENT_VLESS_TLS_CERT_FILE": true, "NODEAGENT_VLESS_TLS_KEY_FILE": true,
	"NODEAGENT_ALLOW_PLAINTEXT": true, "NODEAGENT_ALLOW_PRIVATE_TARGETS": true, "NODEAGENT_SYNC_SECONDS": true, "NODEAGENT_USAGE_SECONDS": true,
}

// parseEnvFile reads KEY=VALUE lines. It is not a shell: no interpolation, no export,
// no duplicate keys, only known keys.
func parseEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || !knownKeys[key] {
			return nil, fmt.Errorf("%s:%d: unknown or malformed setting", path, line)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate %s", path, line, key)
		}
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
		if strings.ContainsAny(value, "$`") {
			return nil, fmt.Errorf("%s:%d: shell interpolation is not supported", path, line)
		}
		out[key] = value
	}
	return out, scanner.Err()
}

func loadConfig(file map[string]string, getenv func(string) string) (config, error) {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		if v, ok := file[key]; ok && v != "" {
			return v
		}
		return def
	}
	flag := func(key string) (bool, error) {
		v, err := strconv.ParseBool(get(key, "false"))
		if err != nil {
			return false, fmt.Errorf("%s must be true or false", key)
		}
		return v, nil
	}
	seconds := func(key, def string, lo, hi int) (time.Duration, error) {
		n, err := strconv.Atoi(get(key, def))
		if err != nil || n < lo || n > hi {
			return 0, fmt.Errorf("%s must be %d-%d", key, lo, hi)
		}
		return time.Duration(n) * time.Second, nil
	}
	c := config{
		APIURL: get("NODEAGENT_API_URL", ""), ClientCertFile: get("NODEAGENT_CLIENT_CERT_FILE", ""), ClientKeyFile: get("NODEAGENT_CLIENT_KEY_FILE", ""),
		ServerCAFile: get("NODEAGENT_SERVER_CA_FILE", ""), StateFile: get("NODEAGENT_STATE_FILE", ""), VLESSListen: get("NODEAGENT_VLESS_LISTEN", "0.0.0.0:443"),
		VLESSTLSCertFile: get("NODEAGENT_VLESS_TLS_CERT_FILE", ""), VLESSTLSKeyFile: get("NODEAGENT_VLESS_TLS_KEY_FILE", ""),
	}
	var err error
	if c.AllowPlaintext, err = flag("NODEAGENT_ALLOW_PLAINTEXT"); err != nil {
		return c, err
	}
	if c.AllowPrivateTargets, err = flag("NODEAGENT_ALLOW_PRIVATE_TARGETS"); err != nil {
		return c, err
	}
	if c.SyncEvery, err = seconds("NODEAGENT_SYNC_SECONDS", "2", 1, 30); err != nil {
		return c, err
	}
	if c.ReportEvery, err = seconds("NODEAGENT_USAGE_SECONDS", "5", 1, 60); err != nil {
		return c, err
	}
	for name, value := range map[string]string{"NODEAGENT_API_URL": c.APIURL, "NODEAGENT_CLIENT_CERT_FILE": c.ClientCertFile, "NODEAGENT_CLIENT_KEY_FILE": c.ClientKeyFile, "NODEAGENT_STATE_FILE": c.StateFile} {
		if value == "" {
			return c, fmt.Errorf("%s is required", name)
		}
	}
	if (c.VLESSTLSCertFile == "") != (c.VLESSTLSKeyFile == "") {
		return c, errors.New("NODEAGENT_VLESS_TLS_CERT_FILE and NODEAGENT_VLESS_TLS_KEY_FILE go together")
	}
	if c.VLESSTLSCertFile == "" && !c.AllowPlaintext {
		return c, errors.New("plaintext VLESS is for tests only: set the VLESS TLS files, or NODEAGENT_ALLOW_PLAINTEXT=true to accept the risk")
	}
	return c, nil
}
