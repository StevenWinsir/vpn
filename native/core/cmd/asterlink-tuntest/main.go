// Command asterlink-tuntest is the privileged acceptance harness for the real TUN
// (handoff_app.md stage 3, cases T01-T05 and the 20-cycle resource check). It drives the
// production engine package, not a copy of it, against ONE controlled test node, so the
// template that ships is the template that is tested.
//
// It changes the machine's routes and DNS, therefore it:
//   - refuses to run without root and the explicit --allow-network-changes flag;
//   - refuses to run while another VPN owns the routes (the Core would refuse too);
//   - takes the node only from the environment (never argv): either
//     ASTERLINK_TUN_PROXY_JSON (a full single-proxy object, any supported protocol, e.g. VLESS
//     Reality) or ASTERLINK_POC_SS_SERVER / _PORT / _PASSWORD [/ _CIPHER];
//   - stops itself after --max-seconds and on SIGINT/SIGTERM, always closing the tunnel;
//   - writes only a redacted JSON report (the node host appears as a hash prefix).
//
// It is never part of the application bundle.
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
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

type caseResult struct {
	ID       string `json:"case_id"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	Result   string `json:"result"` // pass | fail | blocked
}

var results []caseResult

func record(id, expected, observed, result string) {
	results = append(results, caseResult{id, expected, observed, result})
	fmt.Printf("%-7s %-7s %s\n", id, strings.ToUpper(result), observed)
}

func main() {
	allow := flag.Bool("allow-network-changes", false, "confirm this machine may have its routes/DNS changed")
	output := flag.String("output", "", "new directory for the redacted JSON report")
	cycles := flag.Int("cycles", 20, "connect/disconnect cycles for the resource check")
	maxSeconds := flag.Int("max-seconds", 900, "hard upper bound for the whole run")
	probe := flag.String("probe-url", "https://ipv4.icanhazip.com,https://ipinfo.io/ip,https://v4.ident.me", "IPv4 exit probes (plain-text address)")
	probe6 := flag.String("probe-url6", "https://ipv6.icanhazip.com,https://v6.ident.me", "IPv6 exit probes (plain-text address)")
	expect := flag.String("expect-exit", "", "optional exact tunnel exit address (the test node's IP)")
	downloadURL := flag.String("download-url", "", "controlled HTTP(S) URL serving a body of known size, for the counter reconciliation")
	downloadBytes := flag.Int64("download-bytes", 0, "exact body size of --download-url")
	udpEcho := flag.String("udp-echo", "", "controlled UDP echo server host:port reachable from the node")
	apiHost := flag.String("api-host", "", "host:port of the API (control plane must stay reachable during the tunnel)")
	flag.Parse()

	if !*allow {
		fail("BLOCKED: pass --allow-network-changes on a machine you control; this changes routes and DNS")
	}
	if os.Geteuid() != 0 {
		fail("BLOCKED: root is required to create a utun device (run with sudo -E)")
	}
	proxy, hostForReport, err := proxyFromEnv()
	if err != nil || *output == "" {
		fail("usage: --output NEWDIR with a node in the environment: " + fmt.Sprint(err))
	}
	if err := os.Mkdir(*output, 0o700); err != nil {
		fail("output directory must not exist: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*maxSeconds)*time.Second)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() { <-signals; cancel() }()

	eng := engine.New()
	profile := managed.Profile{NodeID: "tuntest", Version: "tuntest", Proxy: proxy}
	// Whatever happens below, the tunnel is closed and the control-plane binding released.
	defer func() { _ = eng.Stop(); network.Default.Clear() }()

	iface, err := eng.Preflight(ctx)
	if err != nil {
		record("T-PRE", "no competing VPN and a physical default route", err.Error()+" (disconnect other VPNs, e.g. a company VPN, and retry)", "blocked")
		finish(*output, hostForReport)
	}
	before := capture()
	baseline := fetch(ctx, *probe, "tcp4")
	baseline6 := fetch(ctx, *probe6, "tcp6")
	if baseline == "" {
		record("T-PRE", "baseline connectivity", "baseline probe failed: check connectivity before testing the tunnel", "blocked")
		finish(*output, hostForReport)
	}

	// ---- one full session -------------------------------------------------------------
	network.Default.Set(iface)
	started := time.Now()
	if err = eng.Start(ctx, profile); err != nil {
		record("T01", "TUN device and routes appear", "start failed: "+err.Error(), "fail")
		finish(*output, hostForReport)
	}
	record("T01a", "utun device and its routes exist", fmt.Sprintf("up in %v; new devices %v", time.Since(started).Round(time.Millisecond), newDevices(before)), "pass")
	if err = eng.Verify(ctx); err != nil {
		record("T02", "real handshake and forwarding through the node (not just a parsed YAML)", err.Error(), "fail")
		finish(*output, hostForReport)
	}
	record("T02", "real handshake and forwarding through the node", "controlled probe answered through the tunnel", "pass")

	exit := fetch(ctx, *probe, "tcp4")
	switch {
	case exit == "" || exit == baseline:
		record("T01", "a no-proxy request leaves from a different address", fmt.Sprintf("baseline=%s tunnel=%s", baseline, exit), "fail")
	case *expect != "" && exit != *expect:
		record("T01", "exit equals the test node", "exit differs from --expect-exit", "fail")
	default:
		record("T01", "a no-proxy request leaves from a different address", "exit changed ("+hash(exit)+" vs "+hash(baseline)+")", "pass")
	}

	reconcile(ctx, eng, *downloadURL, *downloadBytes)
	dnsChecks(ctx)
	udpCheck(ctx, *udpEcho)
	ipv6Check(ctx, *probe6, baseline6)
	apiCheck(ctx, *apiHost)
	idleLoopCheck(ctx, eng)

	_ = eng.Stop()
	network.Default.Clear()
	time.Sleep(2 * time.Second)
	if left := diff(before, capture()); len(left) > 0 {
		record("T07", "nothing of this run remains after Stop", strings.Join(left, "; "), "fail")
	} else {
		record("T07", "nothing of this run remains after Stop", "no utun, route or DNS change left", "pass")
	}

	cycleCheck(ctx, eng, profile, iface, before, *cycles)
	finish(*output, hostForReport)
}

// reconcile compares Mihomo's proxy-only counters with a body of known size fetched
// through the tunnel (handoff 7.2: the counting layer and its overhead are recorded).
func reconcile(ctx context.Context, eng *engine.Mihomo, url string, size int64) {
	if url == "" || size <= 0 {
		record("B-COUNT", "counter delta reconciles with a controlled download", "--download-url/--download-bytes not provided", "blocked")
		return
	}
	up0, down0, _ := eng.Totals()
	body, err := download(ctx, url)
	if err != nil || body != size {
		record("B-COUNT", "controlled download completes", fmt.Sprintf("got %d of %d bytes: %v", body, size, err), "fail")
		return
	}
	time.Sleep(1500 * time.Millisecond) // trackers push their last chunk asynchronously
	up1, down1, _ := eng.Totals()
	down, up := down1-down0, up1-up0
	ratio := float64(down) / float64(size)
	ok := down >= size && float64(down) <= float64(size)*1.10+65536 && up > 0
	record("B-COUNT", "download counter covers the body with only protocol overhead",
		fmt.Sprintf("body=%d counted_down=%d (x%.4f) counted_up=%d", size, down, ratio, up), verdict(ok))
}

func dnsChecks(ctx context.Context) {
	fake := netip.MustParsePrefix("198.18.0.0/16")
	resolver := &net.Resolver{PreferGo: true}
	addrs, err := resolver.LookupHost(ctx, "example.com")
	system := false
	for _, a := range addrs {
		if ip, perr := netip.ParseAddr(a); perr == nil && fake.Contains(ip) {
			system = true
		}
	}
	record("T03-dns-system", "a system lookup is answered by the tunnel's resolver (fake-ip), not a LAN/ISP resolver",
		fmt.Sprintf("addrs=%v err=%v", addrs, err), verdict(system))

	// Plain DNS aimed at a public resolver must be hijacked into the tunnel as well.
	hijacked := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", "9.9.9.9:53")
	}}
	addrs, err = hijacked.LookupHost(ctx, "example.org")
	any53 := false
	for _, a := range addrs {
		if ip, perr := netip.ParseAddr(a); perr == nil && fake.Contains(ip) {
			any53 = true
		}
	}
	record("T03-dns-any53", "port-53 queries to an arbitrary resolver are hijacked", fmt.Sprintf("addrs=%v err=%v", addrs, err), verdict(any53))
}

func udpCheck(ctx context.Context, target string) {
	if target == "" {
		record("T03-udp", "UDP echo round trip through the node", "--udp-echo not provided", "blocked")
		return
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", target)
	if err != nil {
		record("T03-udp", "UDP echo round trip through the node", err.Error(), "fail")
		return
	}
	defer conn.Close()
	payload := []byte("asterlink-udp-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	buffer := make([]byte, 512)
	for range 3 {
		if _, err = conn.Write(payload); err != nil {
			break
		}
		if n, rerr := conn.Read(buffer); rerr == nil && string(buffer[:n]) == string(payload) {
			record("T03-udp", "UDP echo round trip through the node", "echo matched", "pass")
			return
		}
	}
	record("T03-udp", "UDP echo round trip through the node", "no echo (UDP unsupported by this node or path): must be a controlled refusal, never a direct send", "fail")
}

func ipv6Check(ctx context.Context, probes, baseline6 string) {
	exit6 := fetch(ctx, probes, "tcp6")
	switch {
	case exit6 == "" && baseline6 == "":
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "this network has no IPv6 (nothing to leak)", "pass")
	case exit6 == "":
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 requests fail while the tunnel is up (blocked, not leaked)", "pass")
	case exit6 == baseline6:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 exit unchanged: v6 traffic bypasses the tunnel", "fail")
	default:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 exit changed ("+hash(exit6)+")", "pass")
	}
}

// apiCheck proves the control plane does not depend on the paid node: a socket bound to
// the physical interface (exactly what the Core's API client uses) still connects.
func apiCheck(ctx context.Context, host string) {
	if host == "" {
		record("T05", "API stays reachable on the physical interface while the tunnel is up", "--api-host not provided", "blocked")
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, err := network.Default.DialContext(dctx, "tcp", host)
	if err != nil {
		record("T05", "API stays reachable on the physical interface while the tunnel is up", err.Error(), "fail")
		return
	}
	remote := conn.RemoteAddr().String()
	conn.Close()
	record("T05", "API stays reachable on the physical interface while the tunnel is up", "bound dial to "+hash(remote)+" succeeded", "pass")
}

// idleLoopCheck: a routing loop shows up as counters that keep growing with no traffic.
// A system-wide TUN also carries traffic from other applications. We therefore use
// a strict budget when no flows are active, and a bounded background-traffic budget
// when the host keeps legitimate flows open. A self-routing loop normally grows far
// beyond either budget; the observed active-flow counts are recorded as evidence.
func idleLoopCheck(ctx context.Context, eng *engine.Mihomo) {
	time.Sleep(2 * time.Second)
	active0 := eng.ActiveConnections()
	up0, down0, _ := eng.Totals()
	select {
	case <-ctx.Done():
		return
	case <-time.After(6 * time.Second):
	}
	up1, down1, _ := eng.Totals()
	active1 := eng.ActiveConnections()
	grown := (up1 - up0) + (down1 - down0)
	budget := int64(64 << 10)
	if active0 > 0 || active1 > 0 {
		budget = 256 << 10
	}
	ok := grown < budget
	record("T05-loop", "an idle tunnel does not feed itself (no self-routing loop)",
		fmt.Sprintf("%d bytes counted in 6s idle (active flows %d->%d, budget %d)", grown, active0, active1, budget), verdict(ok))
}

// cycleCheck: N start/verify/stop cycles in one process must not accumulate resources.
func cycleCheck(ctx context.Context, eng *engine.Mihomo, profile managed.Profile, iface network.Interface, before snapshot, n int) {
	var goroutines0, fds0 int
	var failures []string
	for i := 1; i <= n && ctx.Err() == nil; i++ {
		network.Default.Set(iface)
		if err := eng.Start(ctx, profile); err != nil {
			failures = append(failures, fmt.Sprintf("cycle %d start: %v", i, err))
			break
		}
		if err := eng.Verify(ctx); err != nil {
			failures = append(failures, fmt.Sprintf("cycle %d verify: %v", i, err))
		}
		_ = eng.Stop()
		network.Default.Clear()
		time.Sleep(500 * time.Millisecond)
		if added := newDevices(before); len(added) > 0 {
			failures = append(failures, fmt.Sprintf("cycle %d left %v", i, added))
		}
		if i == 1 {
			goroutines0, fds0 = runtime.NumGoroutine(), openFDs()
		}
	}
	goroutines, fds := runtime.NumGoroutine(), openFDs()
	if goroutines-goroutines0 > 20 || fds-fds0 > 8 {
		failures = append(failures, fmt.Sprintf("growth: goroutines %d->%d, fds %d->%d", goroutines0, goroutines, fds0, fds))
	}
	if left := diff(before, capture()); len(left) > 0 {
		failures = append(failures, strings.Join(left, "; "))
	}
	record("T-CYCLES", fmt.Sprintf("%d connect/disconnect cycles leave no accumulation", n),
		fmt.Sprintf("goroutines %d->%d fds %d->%d failures=%v", goroutines0, goroutines, fds0, fds, failures), verdict(len(failures) == 0))
}

// ---- helpers ----------------------------------------------------------------------------

func proxyFromEnv() (map[string]any, string, error) {
	if raw := os.Getenv("ASTERLINK_TUN_PROXY_JSON"); raw != "" {
		var proxy map[string]any
		if err := json.Unmarshal([]byte(raw), &proxy); err != nil {
			return nil, "", fmt.Errorf("ASTERLINK_TUN_PROXY_JSON is not a JSON object")
		}
		server, _ := proxy["server"].(string)
		return proxy, hash(server), nil
	}
	server, port, password := os.Getenv("ASTERLINK_POC_SS_SERVER"), os.Getenv("ASTERLINK_POC_SS_PORT"), os.Getenv("ASTERLINK_POC_SS_PASSWORD")
	number, err := strconv.Atoi(port)
	if server == "" || password == "" || err != nil || number <= 0 || number > 65535 {
		return nil, "", fmt.Errorf("set ASTERLINK_TUN_PROXY_JSON or ASTERLINK_POC_SS_SERVER/_PORT/_PASSWORD")
	}
	cipher := os.Getenv("ASTERLINK_POC_SS_CIPHER")
	if cipher == "" {
		cipher = "aes-256-gcm"
	}
	return map[string]any{"name": "tuntest", "type": "ss", "server": server, "port": number, "cipher": cipher, "password": password, "udp": true}, hash(server), nil
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

func verdict(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func fetch(ctx context.Context, urls, network string) string {
	for _, u := range strings.Split(urls, ",") {
		client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
			},
		}}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(u), nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 256))
		response.Body.Close()
		if text := strings.TrimSpace(string(body)); net.ParseIP(text) != nil {
			return text
		}
	}
	return ""
}

func download(ctx context.Context, url string) (int64, error) {
	client := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return io.Copy(io.Discard, response.Body)
}

func openFDs() int {
	entries, _ := os.ReadDir("/dev/fd")
	return len(entries)
}

type snapshot struct {
	Interfaces []string
	RoutesV4   []string
	RoutesV6   []string
	DNSDigest  string
}

func capture() snapshot {
	var s snapshot
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		s.Interfaces = append(s.Interfaces, i.Name)
	}
	sort.Strings(s.Interfaces)
	s.RoutesV4 = routeLines("-f", "inet")
	s.RoutesV6 = routeLines("-f", "inet6")
	out, _ := exec.Command("/usr/sbin/scutil", "--dns").Output()
	sum := sha256.Sum256(out)
	s.DNSDigest = hex.EncodeToString(sum[:])
	return s
}

// routeLines keeps destination/gateway/flags/interface and ignores neighbour-cache and
// temporary-address entries (flags W/L) that the LAN changes on its own.
func routeLines(args ...string) []string {
	out, _ := exec.Command("/usr/sbin/netstat", append([]string{"-rn"}, args...)...).Output()
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || strings.HasPrefix(line, "Routing") || strings.HasPrefix(line, "Internet") || strings.ContainsAny(f[2], "WL") {
			continue
		}
		lines = append(lines, strings.Join(f[:4], " "))
	}
	sort.Strings(lines)
	return lines
}

func newDevices(before snapshot) []string {
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
	if added := newDevices(before); len(added) > 0 {
		problems = append(problems, "utun still present: "+strings.Join(added, ","))
	}
	if strings.Join(before.RoutesV4, "\n") != strings.Join(after.RoutesV4, "\n") {
		problems = append(problems, "IPv4 route table differs from before the run")
	}
	if strings.Join(before.RoutesV6, "\n") != strings.Join(after.RoutesV6, "\n") {
		problems = append(problems, "IPv6 route table differs from before the run")
	}
	if before.DNSDigest != after.DNSDigest {
		problems = append(problems, "scutil --dns differs from before the run")
	}
	return problems
}

func finish(output, host string) {
	failed, blocked := 0, 0
	for _, r := range results {
		switch r.Result {
		case "fail":
			failed++
		case "blocked":
			blocked++
		}
	}
	report := map[string]any{
		"started": time.Now().UTC().Format(time.RFC3339), "node_host_sha256_prefix": host,
		"go": runtime.Version(), "cases": results, "failed": failed, "blocked": blocked,
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(output, "tun-report.json"), data, 0o600)
	fmt.Printf("%d cases: %d fail, %d blocked\n", len(results), failed, blocked)
	switch {
	case failed > 0:
		os.Exit(1)
	case blocked > 0:
		os.Exit(3)
	}
	os.Exit(0)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(3)
}
