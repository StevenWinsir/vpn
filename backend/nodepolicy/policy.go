// Package nodepolicy is the inline-only proxy policy shared by the API and
// managed Core. Validation never dials, reads files, or exposes input values.
package nodepolicy

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CoreRevision is the reviewed gitlink, not the latest upstream release.
const CoreRevision = "70f0570405c3c2c47bb113b88db95006d239b346"

var (
	ErrUnsupported   = errors.New("unsupported proxy type or option")
	ErrInvalid       = errors.New("invalid proxy field or incompatible options")
	uuidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	headerPattern    = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9a-zA-Z-]+$")
	bandwidthPattern = regexp.MustCompile(`^[1-9][0-9]{0,5}(?:\.[0-9]{1,3})?\s*(?i:[kmgt]?bps)?$`)
)

type rule func(any) bool
type schema map[string]rule

func str(max int) rule {
	return func(v any) bool {
		s, ok := v.(string)
		return ok && len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
	}
}
func nonempty(v any) bool { return str(4096)(v) && v != "" }
func boolean(v any) bool  { _, ok := v.(bool); return ok }
func disabled(v any) bool { return v == false }
func integer(min, max int) rule {
	return func(v any) bool { n, ok := v.(int); return ok && n >= min && n <= max }
}
func oneOf(values ...string) rule {
	return func(v any) bool { s, ok := v.(string); return ok && slices.Contains(values, s) }
}
func list(element rule, min, max int) rule {
	return func(v any) bool {
		values, ok := v.([]any)
		if !ok {
			if ss, valid := v.([]string); valid {
				values = make([]any, len(ss))
				for i, s := range ss {
					values[i] = s
				}
			} else {
				return false
			}
		}
		if len(values) < min || len(values) > max {
			return false
		}
		for _, value := range values {
			if !element(value) {
				return false
			}
		}
		return true
	}
}
func object(fields schema) rule {
	return func(v any) bool { m, ok := v.(map[string]any); return ok && check(m, fields) == nil }
}
func merge(parts ...schema) schema {
	result := schema{}
	for _, part := range parts {
		for k, v := range part {
			result[k] = v
		}
	}
	return result
}
func check(m map[string]any, fields schema) error {
	for k, v := range m {
		validate, ok := fields[k]
		if !ok {
			return ErrUnsupported
		}
		if !validate(v) {
			return ErrInvalid
		}
	}
	return nil
}
func required(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if !nonempty(m[key]) {
			return false
		}
	}
	return true
}
func host(v any) bool {
	s, ok := v.(string)
	if !ok || s == "" || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func ip(v any) bool { s, ok := v.(string); return ok && net.ParseIP(s) != nil }
func prefix(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, err := netip.ParsePrefix(s)
	return err == nil
}
func uuid(v any) bool {
	s, ok := v.(string)
	return ok && uuidPattern.MatchString(s) && s != "00000000-0000-0000-0000-000000000000"
}
func key32(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) != 44 {
		return false
	}
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == 32 && slices.ContainsFunc(b, func(c byte) bool { return c != 0 })
}
func realityKey(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == 32 && slices.ContainsFunc(b, func(c byte) bool { return c != 0 })
}
func shortID(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) > 16 || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func pemKey(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) > 16384 || !strings.HasPrefix(s, "-----BEGIN ") {
		return false
	}
	b, rest := pem.Decode([]byte(s))
	return b != nil && len(strings.TrimSpace(string(rest))) == 0 && len(b.Bytes) > 0 && slices.Contains([]string{"OPENSSH PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "PRIVATE KEY"}, b.Type)
}
func headers(value rule) rule {
	return func(v any) bool {
		m, ok := v.(map[string]any)
		if !ok || len(m) > 32 {
			return false
		}
		for k, v := range m {
			if len(k) > 128 || !headerPattern.MatchString(k) || !value(v) {
				return false
			}
		}
		return true
	}
}
func portSet(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) == 0 || len(s) > 256 {
		return false
	}
	parts := strings.Split(s, ",")
	if len(parts) > 32 {
		return false
	}
	for _, part := range parts {
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return false
		}
		last := 0
		for _, bound := range bounds {
			n, err := strconv.Atoi(bound)
			if err != nil || n < 1 || n > 65535 || n < last {
				return false
			}
			last = n
		}
	}
	return true
}

var tlsVerification = schema{"skip-cert-verify": disabled, "fingerprint": str(256), "name-cert-verify": str(256)}
var tls = merge(tlsVerification, schema{"sni": host, "alpn": list(nonempty, 1, 16)})
var fingerprint = oneOf("", "chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized")
var ws = object(schema{"path": str(4096), "headers": headers(str(4096)), "max-early-data": integer(0, 65536), "early-data-header-name": str(128), "v2ray-http-upgrade": boolean, "v2ray-http-upgrade-fast-open": boolean})
var grpc = object(schema{"grpc-service-name": str(1024), "grpc-user-agent": str(1024), "ping-interval": integer(0, 3600), "max-connections": integer(1, 32), "min-streams": integer(0, 1024), "max-streams": integer(1, 1024)})
var reality = object(schema{"public-key": realityKey, "short-id": shortID})
var transport = schema{
	"network": oneOf("", "tcp", "ws", "http", "h2", "grpc"), "ws-opts": ws, "grpc-opts": grpc,
	"http-opts": object(schema{"method": oneOf("GET", "POST", "PUT", "HEAD"), "path": list(str(4096), 1, 32), "headers": headers(list(str(4096), 1, 16))}),
	"h2-opts":   object(schema{"host": list(host, 1, 16), "path": str(4096)}),
}
var vmTLS = merge(tlsVerification, schema{"alpn": list(nonempty, 1, 16), "servername": host, "tls": boolean, "client-fingerprint": fingerprint, "reality-opts": reality})
var smux = object(schema{"enabled": boolean, "protocol": oneOf("smux", "yamux", "h2mux"), "max-connections": integer(1, 32), "min-streams": integer(0, 1024), "max-streams": integer(1, 1024), "padding": boolean, "statistic": boolean, "only-tcp": boolean})
var peer = schema{"server": host, "port": integer(1, 65535), "public-key": key32, "pre-shared-key": key32, "reserved": list(integer(0, 255), 3, 3), "allowed-ips": list(prefix, 1, 64)}

var protocols = map[string]schema{
	"ss":        {"cipher": oneOf("aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"), "password": nonempty, "udp": boolean, "udp-over-tcp": boolean, "udp-over-tcp-version": integer(1, 2), "plugin": oneOf("obfs", "v2ray-plugin", "shadow-tls"), "plugin-opts": func(v any) bool { _, ok := v.(map[string]any); return ok }, "client-fingerprint": fingerprint, "smux": smux},
	"ssr":       {"cipher": oneOf("aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "chacha20-ietf", "rc4-md5"), "password": nonempty, "obfs": oneOf("plain", "http_simple", "http_post", "random_head", "tls1.2_ticket_auth", "tls1.2_ticket_fastauth"), "obfs-param": str(1024), "protocol": oneOf("origin", "auth_sha1_v4", "auth_aes128_md5", "auth_aes128_sha1", "auth_chain_a", "auth_chain_b"), "protocol-param": str(1024), "udp": boolean},
	"http":      merge(tlsVerification, schema{"sni": host, "username": str(1024), "password": str(4096), "tls": boolean, "headers": headers(str(4096))}),
	"socks5":    merge(tlsVerification, schema{"username": str(1024), "password": str(4096), "tls": boolean, "udp": boolean}),
	"vmess":     merge(vmTLS, transport, schema{"uuid": uuid, "alterId": integer(0, 65535), "cipher": oneOf("auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero"), "udp": boolean, "packet-encoding": oneOf("", "packetaddr", "xudp"), "packet-addr": boolean, "xudp": boolean, "global-padding": boolean, "authenticated-length": boolean, "smux": smux}),
	"vless":     merge(vmTLS, transport, schema{"uuid": uuid, "flow": oneOf("", "xtls-rprx-vision"), "encryption": oneOf("", "none"), "udp": boolean, "packet-encoding": oneOf("", "packetaddr", "xudp"), "packet-addr": boolean, "xudp": boolean, "smux": smux}),
	"trojan":    merge(tls, schema{"password": nonempty, "udp": boolean, "network": oneOf("", "tcp", "ws", "grpc"), "ws-opts": ws, "grpc-opts": grpc, "client-fingerprint": fingerprint, "reality-opts": reality, "smux": smux}),
	"hysteria":  merge(tls, schema{"up": func(v any) bool { s, ok := v.(string); return ok && bandwidthPattern.MatchString(s) }, "down": func(v any) bool { s, ok := v.(string); return ok && bandwidthPattern.MatchString(s) }, "auth": nonempty, "auth-str": nonempty, "obfs": str(1024), "protocol": oneOf("udp", "faketcp", "wechat-video"), "ports": portSet, "hop-interval": integer(5, 3600), "recv-window-conn": integer(0, 64<<20), "recv-window": integer(0, 64<<20), "disable-mtu-discovery": boolean, "fast-open": boolean}),
	"hysteria2": merge(tls, schema{"password": nonempty, "up": str(64), "down": str(64), "ports": portSet, "hop-interval": str(32), "obfs": oneOf("", "salamander"), "obfs-password": nonempty, "cwnd": integer(0, 1024), "udp-mtu": integer(512, 1500), "initial-stream-receive-window": integer(0, 64<<20), "max-stream-receive-window": integer(0, 64<<20), "initial-connection-receive-window": integer(0, 64<<20), "max-connection-receive-window": integer(0, 64<<20)}),
	"tuic":      merge(tls, schema{"uuid": uuid, "password": nonempty, "token": nonempty, "ip": ip, "heartbeat-interval": integer(1000, 3600000), "reduce-rtt": boolean, "request-timeout": integer(1000, 60000), "udp-relay-mode": oneOf("native", "quic"), "congestion-controller": oneOf("cubic", "new_reno", "bbr"), "disable-sni": disabled, "fast-open": boolean, "max-open-streams": integer(1, 1024), "cwnd": integer(0, 1024), "max-udp-relay-packet-size": integer(512, 1500), "recv-window-conn": integer(0, 64<<20), "recv-window": integer(0, 64<<20), "disable-mtu-discovery": boolean, "udp-over-stream": boolean, "udp-over-stream-version": integer(1, 2)}),
	"wireguard": merge(peer, schema{"ip": ip, "ipv6": ip, "private-key": key32, "udp": boolean, "mtu": integer(576, 9000), "workers": integer(1, 16),
		// No keepalive/independent DNS traffic outside the metered lifecycle.
		"persistent-keepalive": integer(0, 0), "remote-dns-resolve": disabled, "dns": list(ip, 1, 8), "peers": list(object(peer), 1, 16), "refresh-server-ip-interval": integer(60, 86400)}),
	"snell":  {"psk": nonempty, "version": integer(1, 5), "udp": boolean, "reuse": boolean, "obfs-opts": object(schema{"mode": oneOf("http", "tls"), "host": host}), "client-fingerprint": fingerprint},
	"anytls": merge(tls, schema{"password": nonempty, "udp": boolean, "client-fingerprint": fingerprint, "idle-session-check-interval": integer(1, 3600), "idle-session-timeout": integer(1, 3600), "min-idle-session": integer(0, 16), "disable-reuse": boolean}),
	"ssh":    {"username": nonempty, "password": nonempty, "private-key": pemKey, "private-key-passphrase": str(1024), "host-key": list(nonempty, 1, 16), "host-key-algorithms": list(nonempty, 1, 16)},
	"mieru":  {"username": nonempty, "password": nonempty, "transport": oneOf("TCP", "UDP"), "udp": boolean, "multiplexing": oneOf("MULTIPLEXING_OFF", "MULTIPLEXING_LOW", "MULTIPLEXING_MIDDLE", "MULTIPLEXING_HIGH"), "port-range": portSet},
}

func Supported() []string {
	names := make([]string, 0, len(protocols))
	for name := range protocols {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Validate uses recursive allowlists, never an open-ended denylist. Mihomo's
// file-reading TLS options and auxiliary dialers are not the managed contract.
func Validate(p map[string]any) error {
	kind, _ := p["type"].(string)
	fields, ok := protocols[kind]
	if !ok {
		return ErrUnsupported
	}
	common := schema{"name": str(256), "type": nonempty, "server": host, "port": integer(1, 65535), "tfo": boolean, "mptcp": boolean}
	if err := check(p, merge(common, fields)); err != nil {
		return err
	}
	name, _ := p["name"].(string)
	if name == "" || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 120 || slices.Contains([]string{"VPN", "DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL"}, strings.ToUpper(name)) {
		return ErrInvalid
	}
	if kind != "wireguard" || p["peers"] == nil {
		if !host(p["server"]) || !integer(1, 65535)(p["port"]) {
			return ErrInvalid
		}
	}
	switch kind {
	case "ss":
		if !required(p, "cipher", "password") || !validSS(p) {
			return ErrInvalid
		}
	case "ssr":
		if !required(p, "cipher", "password", "obfs", "protocol") {
			return ErrInvalid
		}
	case "vmess", "vless":
		if !uuid(p["uuid"]) {
			return ErrInvalid
		}
		if kind == "vmess" && !required(p, "cipher") {
			return ErrInvalid
		}
		if kind == "vless" && p["flow"] == "xtls-rprx-vision" && (p["tls"] != true || p["network"] != nil && p["network"] != "" && p["network"] != "tcp") {
			return ErrInvalid
		}
	case "trojan", "anytls", "hysteria2":
		if !required(p, "password") {
			return ErrInvalid
		}
		if kind == "hysteria2" && p["obfs"] == "salamander" && !required(p, "obfs-password") {
			return ErrInvalid
		}
	case "hysteria":
		if !required(p, "up", "down") || (p["auth"] == nil) == (p["auth-str"] == nil) {
			return ErrInvalid
		}
		if p["auth"] != nil {
			if _, err := base64.StdEncoding.DecodeString(p["auth"].(string)); err != nil {
				return ErrInvalid
			}
		}
	case "tuic":
		if p["token"] != nil {
			if p["uuid"] != nil || p["password"] != nil {
				return ErrInvalid
			}
		} else if !uuid(p["uuid"]) || !required(p, "password") {
			return ErrInvalid
		}
	case "wireguard":
		if !validWireGuard(p) {
			return ErrInvalid
		}
	case "snell":
		if !required(p, "psk") {
			return ErrInvalid
		}
	case "ssh":
		if !required(p, "username") || p["password"] == nil && p["private-key"] == nil || p["host-key"] == nil {
			return ErrInvalid
		}
	case "mieru":
		if !required(p, "username", "password", "transport") {
			return ErrInvalid
		}
	}
	if r, ok := p["reality-opts"].(map[string]any); ok {
		if !realityKey(r["public-key"]) || !shortID(r["short-id"]) || p["client-fingerprint"] == nil || p["client-fingerprint"] == "" || kind != "trojan" && p["tls"] != true {
			return ErrInvalid
		}
	}
	for key, network := range map[string]string{"ws-opts": "ws", "grpc-opts": "grpc", "http-opts": "http", "h2-opts": "h2"} {
		if p[key] != nil && p["network"] != network {
			return ErrInvalid
		}
	}
	return nil
}

func validSS(p map[string]any) bool {
	cipher := p["cipher"].(string)
	if strings.HasPrefix(cipher, "2022-") {
		size := 32
		if cipher == "2022-blake3-aes-128-gcm" {
			size = 16
		}
		keys := strings.Split(p["password"].(string), ":")
		if len(keys) > 8 || cipher == "2022-blake3-chacha20-poly1305" && len(keys) != 1 {
			return false
		}
		for _, key := range keys {
			b, err := base64.StdEncoding.Strict().DecodeString(key)
			if err != nil || len(b) != size {
				return false
			}
		}
	}
	plugin, _ := p["plugin"].(string)
	opts, has := p["plugin-opts"].(map[string]any)
	if plugin == "" {
		return !has
	}
	if !has {
		return false
	}
	var fields schema
	switch plugin {
	case "obfs":
		fields = schema{"mode": oneOf("http", "tls"), "host": host}
		if !required(opts, "mode") {
			return false
		}
	case "v2ray-plugin":
		fields = schema{"mode": oneOf("websocket"), "host": host, "path": str(4096), "tls": boolean, "mux": boolean, "headers": headers(str(4096)), "skip-cert-verify": disabled, "v2ray-http-upgrade": boolean, "v2ray-http-upgrade-fast-open": boolean}
		if opts["mode"] != "websocket" {
			return false
		}
	case "shadow-tls":
		fields = schema{"host": host, "password": nonempty, "version": integer(1, 3), "fingerprint": fingerprint, "alpn": list(nonempty, 1, 16), "skip-cert-verify": disabled}
		if !host(opts["host"]) || opts["version"] == 3 && !required(opts, "password") {
			return false
		}
	default:
		return false
	}
	return check(opts, fields) == nil
}

func validWireGuard(p map[string]any) bool {
	if !key32(p["private-key"]) || p["ip"] == nil && p["ipv6"] == nil {
		return false
	}
	if p["ip"] != nil && net.ParseIP(p["ip"].(string)).To4() == nil {
		return false
	}
	if p["ipv6"] != nil && !strings.Contains(p["ipv6"].(string), ":") {
		return false
	}
	if peers, ok := p["peers"].([]any); ok {
		for _, key := range []string{"server", "port", "public-key", "pre-shared-key", "reserved", "allowed-ips"} {
			if p[key] != nil {
				return false
			}
		}
		seen := map[string]bool{}
		for _, value := range peers {
			peer := value.(map[string]any)
			if !host(peer["server"]) || !integer(1, 65535)(peer["port"]) || !key32(peer["public-key"]) || peer["allowed-ips"] == nil {
				return false
			}
			key := peer["public-key"].(string)
			if seen[key] {
				return false
			}
			seen[key] = true
		}
		return true
	}
	return key32(p["public-key"])
}
