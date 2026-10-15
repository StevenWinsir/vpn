// Command asterlink-testnode is the controlled proxy node of the Android TUN acceptance
// (handoff_app_android.md 10, round 2). It runs the pinned Mihomo as a server on the machine
// that hosts the emulator, so the suite needs no third-party node and no credential of the
// user's: the emulator reaches it at 10.0.2.2 over a socket the app protects, and the
// node's own byte counter proves that an app's request really crossed it (T01).
//
// Inbounds, all on --listen (default 127.0.0.1, the emulator's 10.0.2.2):
//   - Shadowsocks AEAD (aes-256-gcm) with UDP relay;
//   - VLESS without TLS;
//   - VLESS with REALITY (fresh X25519 key and short id), handshaking against --reality-dest.
//
// Every credential is generated at start and written only to --ready-file (mode 0600),
// never printed. A token-gated counter answers on --status-listen at /stats?t=<token>; the
// node itself forwards with MATCH,DIRECT and resolves example.com to 127.0.0.1, where
// the suite's traffic sink listens. It is a test tool: never part of a package.
package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
	"gopkg.in/yaml.v3"
)

// TargetName is the name the node resolves to its own loopback (see start). It is the
// reserved documentation name: it exists publicly (A and AAAA, at IANA), which matters on the
// device, where the tunnel answers A with fake-ip but forwards AAAA upstream; a name that does
// not exist publicly gets NXDOMAIN there and Android then refuses the whole name.
const TargetName = "example.com"

// ready is what the acceptance driver reads to build the client side.
type ready struct {
	Listen      string `json:"listen"`
	SSPort      int    `json:"ss_port"`
	SSCipher    string `json:"ss_cipher"`
	SSPassword  string `json:"ss_password"`
	VLESSPort   int    `json:"vless_port"`
	VLESSUUID   string `json:"vless_uuid"`
	RealityPort int    `json:"reality_port,omitempty"`
	RealitySNI  string `json:"reality_server_name,omitempty"`
	RealityPub  string `json:"reality_public_key,omitempty"`
	RealitySID  string `json:"reality_short_id,omitempty"`
	StatusAddr  string `json:"status_addr"`
	StatusToken string `json:"status_token"`
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// freePort asks the kernel for an unused TCP port on host (UDP shares the number).
func freePort(host string) int {
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func main() {
	listen := flag.String("listen", "127.0.0.1", "address the proxy inbounds listen on")
	statusListen := flag.String("status-listen", "127.0.0.1:0", "address of the token-gated /stats counter")
	realityDest := flag.String("reality-dest", "itunes.apple.com:443", "TLS server REALITY borrows its handshake from (empty: no REALITY inbound)")
	readyFile := flag.String("ready-file", "", "new file (0600) receiving ports and generated credentials")
	maxSeconds := flag.Int("max-seconds", 3600, "exit after this long")
	flag.Parse()
	if *readyFile == "" {
		log.Fatal("--ready-file is required")
	}
	r, stop, err := start(*listen, *statusListen, *realityDest)
	if err != nil {
		log.Fatal(err)
	}
	defer stop()

	data, _ := json.MarshalIndent(r, "", "  ")
	file, err := os.OpenFile(*readyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		log.Fatal(err)
	}
	file.Close()
	log.Printf("test node up: ss %d, vless %d, reality %s, status %s", r.SSPort, r.VLESSPort, strconv.Itoa(r.RealityPort), r.StatusAddr)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
	case <-time.After(time.Duration(*maxSeconds) * time.Second):
	}
}

// start runs the inbounds and the counter; stop shuts both down.
func start(listen, statusListen, realityDest string) (ready, func(), error) {
	home, err := os.MkdirTemp("", "asterlink-testnode-")
	if err != nil {
		return ready{}, nil, err
	}
	constant.SetHomeDir(home)

	r := ready{Listen: listen, SSCipher: "aes-256-gcm", SSPassword: randomHex(16), VLESSUUID: uuid(), StatusToken: randomHex(16)}
	r.SSPort, r.VLESSPort = freePort(listen), freePort(listen)
	inbounds := []any{
		map[string]any{"name": "ss-in", "type": "shadowsocks", "listen": listen, "port": r.SSPort, "cipher": r.SSCipher, "password": r.SSPassword, "udp": true},
		// Plain VLESS (the pinned core requires an explicit allow-insecure for it): the
		// emulator reaches it over the host's loopback only.
		map[string]any{"name": "vless-in", "type": "vless", "listen": listen, "port": r.VLESSPort, "allow-insecure": true,
			"users": []any{map[string]any{"username": "t", "uuid": r.VLESSUUID}}},
	}
	if realityDest != "" {
		host, _, err := net.SplitHostPort(realityDest)
		if err != nil {
			return ready{}, nil, fmt.Errorf("--reality-dest must be host:port")
		}
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return ready{}, nil, err
		}
		r.RealityPort, r.RealitySNI, r.RealitySID = freePort(listen), host, randomHex(8)
		r.RealityPub = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
		inbounds = append(inbounds, map[string]any{
			"name": "reality-in", "type": "vless", "listen": listen, "port": r.RealityPort,
			"users": []any{map[string]any{"username": "t", "uuid": r.VLESSUUID}},
			"reality-config": map[string]any{
				"dest": realityDest, "private-key": base64.RawURLEncoding.EncodeToString(key.Bytes()),
				"short-id": []string{r.RealitySID}, "server-names": []string{host},
			},
		})
	}
	doc, err := yaml.Marshal(map[string]any{
		"mode": "rule", "log-level": "warning", "ipv6": true, "allow-lan": false,
		// TargetName resolves to this host's loopback only here, at the node: an app reaches
		// the controlled target behind it by that name only through the node (directly the
		// name leads to IANA's servers), so no LAN or VPN address of the host is involved.
		"hosts":     map[string]any{TargetName: "127.0.0.1"},
		"listeners": inbounds, "rules": []string{"MATCH,DIRECT"},
	})
	if err != nil {
		return ready{}, nil, err
	}
	cfg, err := executor.ParseWithBytes(doc)
	if err != nil {
		return ready{}, nil, fmt.Errorf("pinned core rejected the node configuration: %w", err)
	}
	executor.ApplyConfig(cfg, true)
	// The pinned fork's ApplyConfig leaves listeners alone; start them explicitly.
	listener.PatchInboundListeners(cfg.Listeners, tunnel.Tunnel, true)

	statusLn, err := net.Listen("tcp", statusListen)
	if err != nil {
		return ready{}, nil, err
	}
	r.StatusAddr = statusLn.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, req *http.Request) {
		if subtle.ConstantTimeCompare([]byte(req.URL.Query().Get("t")), []byte(r.StatusToken)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		up, down := statistic.DefaultManager.Total()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"upload": up, "download": down,
			"connections": int64(len(statistic.DefaultManager.Snapshot().Connections))})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(statusLn) }()
	stop := func() {
		_ = server.Close()
		listener.PatchInboundListeners(nil, tunnel.Tunnel, true)
		executor.Shutdown()
		_ = os.RemoveAll(home)
	}
	return r, stop, nil
}
