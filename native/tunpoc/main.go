// Command tunpoc is a TEST-ONLY proof of concept that starts the pinned Mihomo
// native TUN against one operator-supplied Shadowsocks test node, proves that an
// application with no proxy settings changes its exit address, then shuts the
// tunnel down and checks that this run left no utun, route or DNS change behind.
//
// It changes the machine's network, therefore it:
//   - refuses to run without root and the explicit --allow-network-changes flag;
//   - reads the node password only from ASTERLINK_POC_SS_PASSWORD (never argv);
//   - stops itself after --max-seconds, on SIGINT/SIGTERM, or after the probe;
//   - is a separate Go module that the application bundle build never includes.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

type snapshot struct {
	Interfaces []string `json:"interfaces"`
	RoutesV4   []string `json:"routes_v4_sha256_lines"`
	RoutesV6   []string `json:"routes_v6_sha256_lines"`
	DNSDigest  string   `json:"dns_sha256"`
}

type report struct {
	Started          string   `json:"started"`
	Machine          string   `json:"machine"`
	ServerHost       string   `json:"server_host_sha256"`
	Stack            string   `json:"tun_stack"`
	BaselineExit     string   `json:"baseline_exit"`
	TunnelExit       string   `json:"tunnel_exit"`
	ExitChanged      bool     `json:"exit_changed"`
	NewInterfaces    []string `json:"new_interfaces_while_up"`
	TunnelUpload     int64    `json:"mihomo_total_upload_bytes"`
	TunnelDownload   int64    `json:"mihomo_total_download_bytes"`
	CleanupLeftovers []string `json:"cleanup_leftovers"`
	Pass             bool     `json:"pass"`
	Problems         []string `json:"problems"`
}

func main() {
	server := flag.String("server", "", "test Shadowsocks server host")
	port := flag.Int("port", 0, "test Shadowsocks server port")
	cipher := flag.String("cipher", "aes-256-gcm", "Shadowsocks AEAD cipher")
	probe := flag.String("probe-url", "https://api.ipify.org", "URL returning the caller's public address as plain text")
	expect := flag.String("expect-exit", "", "optional exact tunnel exit address (e.g. the test node's IP)")
	stack := flag.String("stack", "system", "Mihomo TUN stack to validate (system|gvisor|mixed)")
	output := flag.String("output", "", "new directory for the sanitized JSON report")
	maxSeconds := flag.Int("max-seconds", 90, "hard upper bound for the whole run")
	allow := flag.Bool("allow-network-changes", false, "confirm this machine may have its routes/DNS changed")
	flag.Parse()

	if !*allow {
		fail("BLOCKED: pass --allow-network-changes on a machine you control; this changes routes and DNS")
	}
	if os.Geteuid() != 0 {
		fail("BLOCKED: root is required to create a utun device (run with sudo)")
	}
	password := os.Getenv("ASTERLINK_POC_SS_PASSWORD")
	if *server == "" || *port <= 0 || *port > 65535 || password == "" || *output == "" {
		fail("usage: --server HOST --port N --output NEWDIR with ASTERLINK_POC_SS_PASSWORD set")
	}
	if err := os.Mkdir(*output, 0o700); err != nil {
		fail("output directory must not exist: " + err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*maxSeconds)*time.Second)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() { <-signals; cancel() }()

	hostname, _ := os.Hostname()
	rep := &report{Started: time.Now().UTC().Format(time.RFC3339), Machine: hostname, Stack: *stack}
	sum := sha256.Sum256([]byte(*server))
	rep.ServerHost = hex.EncodeToString(sum[:8])

	before := capture()
	rep.BaselineExit = fetch(ctx, *probe)
	if rep.BaselineExit == "" {
		rep.Problems = append(rep.Problems, "baseline probe failed: check connectivity before testing the tunnel")
		finish(rep, *output)
	}

	started := runTunnel(ctx, rep, *server, *port, *cipher, password, *stack, *probe, *expect, before)
	if started {
		rep.TunnelUpload, rep.TunnelDownload = statistic.DefaultManager.Total()
	}
	shutdown()
	time.Sleep(2 * time.Second) // let the kernel retire the device and routes
	after := capture()
	rep.CleanupLeftovers = diff(before, after)
	if len(rep.CleanupLeftovers) > 0 {
		rep.Problems = append(rep.Problems, "state left behind after shutdown")
	}
	finish(rep, *output)
}

func runTunnel(ctx context.Context, rep *report, server string, port int, cipher, password, stack, probe, expect string, before snapshot) bool {
	home, err := os.MkdirTemp("", "asterlink-tunpoc-")
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return false
	}
	defer os.RemoveAll(home)
	constant.SetHomeDir(home)

	// Fixed, reviewed template: one proxy, no listeners, no controller, MATCH only to it.
	config := fmt.Sprintf(`log-level: warning
mode: rule
ipv6: false
allow-lan: false
tun:
  enable: true
  stack: %s
  auto-route: true
  auto-detect-interface: true
  dns-hijack:
    - any:53
dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  nameserver:
    - 1.1.1.1
    - 8.8.8.8
  default-nameserver:
    - 1.1.1.1
  proxy-server-nameserver:
    - 1.1.1.1
proxies:
  - {name: test-node, type: ss, server: %q, port: %d, cipher: %q, password: %q, udp: true}
rules:
  - MATCH,test-node
`, stack, server, port, cipher, password)

	cfg, err := executor.ParseWithBytes([]byte(config))
	if err != nil {
		rep.Problems = append(rep.Problems, "configuration rejected by pinned core: "+err.Error())
		return false
	}
	executor.ApplyConfig(cfg, true)
	// The pinned FlClash fork's ApplyConfig deliberately leaves listeners alone,
	// so the TUN listener must be created explicitly.
	listener.ReCreateTun(cfg.General.Tun, tunnel.Tunnel)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if added := newInterfaces(before); len(added) > 0 {
			rep.NewInterfaces = added
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if len(rep.NewInterfaces) == 0 {
		rep.Problems = append(rep.Problems, "no new utun interface appeared")
		return true
	}
	time.Sleep(2 * time.Second) // routes are installed asynchronously
	rep.TunnelExit = fetch(ctx, probe)
	rep.ExitChanged = rep.TunnelExit != "" && rep.TunnelExit != rep.BaselineExit
	if !rep.ExitChanged {
		rep.Problems = append(rep.Problems, "exit address did not change through the tunnel")
	}
	if expect != "" && rep.TunnelExit != expect {
		rep.Problems = append(rep.Problems, "tunnel exit differs from --expect-exit")
	}
	return true
}

func shutdown() {
	listener.ReCreateTun(LC.Tun{}, tunnel.Tunnel)
	executor.Shutdown()
}

func fetch(ctx context.Context, url string) string {
	// Proxy: nil — the probe must not rely on any HTTP/SOCKS proxy setting.
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 256))
	text := strings.TrimSpace(string(body))
	if net.ParseIP(text) == nil {
		return ""
	}
	return text
}

func capture() snapshot {
	var s snapshot
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		s.Interfaces = append(s.Interfaces, i.Name)
	}
	sort.Strings(s.Interfaces)
	s.RoutesV4 = lines("netstat", "-rn", "-f", "inet")
	s.RoutesV6 = lines("netstat", "-rn", "-f", "inet6")
	dns := sha256.Sum256([]byte(strings.Join(lines("scutil", "--dns"), "\n")))
	s.DNSDigest = hex.EncodeToString(dns[:])
	return s
}

// lines returns stable, sorted, de-timed output; the report keeps only counts of
// differences, not the routing table itself.
func lines(name string, args ...string) []string {
	out, _ := exec.Command(name, args...).Output()
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && !strings.HasPrefix(line, "Routing") && !strings.HasPrefix(line, "Internet") {
			// destination, gateway, flags, interface; drop volatile expire column.
			result = append(result, strings.Join(fields[:4], " "))
		} else if name == "scutil" {
			result = append(result, strings.TrimSpace(line))
		}
	}
	sort.Strings(result)
	return result
}

func newInterfaces(before snapshot) []string {
	known := map[string]bool{}
	for _, n := range before.Interfaces {
		known[n] = true
	}
	var added []string
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if !known[i.Name] && strings.HasPrefix(i.Name, "utun") {
			added = append(added, i.Name)
		}
	}
	return added
}

func diff(before, after snapshot) []string {
	var problems []string
	if added := newInterfaces(before); len(added) > 0 {
		problems = append(problems, "utun still present: "+strings.Join(added, ","))
	}
	if !equal(before.RoutesV4, after.RoutesV4) {
		problems = append(problems, "IPv4 route table differs from before the run")
	}
	if !equal(before.RoutesV6, after.RoutesV6) {
		problems = append(problems, "IPv6 route table differs from before the run")
	}
	if before.DNSDigest != after.DNSDigest {
		problems = append(problems, "scutil --dns differs from before the run")
	}
	return problems
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func finish(rep *report, output string) {
	rep.Pass = len(rep.Problems) == 0
	data, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "tun-poc-report.json"), data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	fmt.Println(string(data))
	if !rep.Pass {
		os.Exit(1)
	}
	os.Exit(0)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(3)
}
