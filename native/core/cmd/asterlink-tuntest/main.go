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
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/leakcheck"
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

// options are the command-line inputs the platform checks need.
type options struct {
	probe, probe6 string
	udpEcho       string
	apiHost       string
	ntpHost       string
	udpReject     bool
	killTest      bool
}

// Reference values taken before the tunnel exists, used by the round-6 cases.
var baselineExit, baselineSignature string

// childFlag re-executes the harness as a separate process. On Windows the harness IS the
// Core process, and sing-tun's Windows Filtering Platform rules exempt the Core's own
// process; a probe that must behave like an ordinary application therefore runs in a child.
const childFlag = "--child"

func main() {
	if len(os.Args) > 1 && os.Args[1] == childFlag {
		os.Exit(runChild(os.Args[2:]))
	}
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
	ntpHost := flag.String("ntp-host", "time.cloudflare.com:123", "Windows: public SNTP server used as a UDP target (no server of your own needed)")
	udpReject := flag.Bool("udp-reject-test", false, "Windows: also run a session whose node has udp:false and require UDP to be refused, never sent direct")
	soakSeconds := flag.Int("soak-seconds", 0, "hold one connected tunnel this long, sampling CPU/memory/handles/threads and probing the exit (round 6; 0 = off)")
	soakInterval := flag.Int("soak-interval", 30, "seconds between soak samples")
	killTest := flag.Bool("kill-test", false, "Windows: also hard-kill a connected child process and require the adapter, routes and filters to vanish")
	hold := flag.Bool("hold", false, "internal: connect, print READY, wait to be killed (used by --kill-test)")
	flag.Parse()
	opts := options{probe: *probe, probe6: *probe6, udpEcho: *udpEcho, apiHost: *apiHost, ntpHost: *ntpHost, udpReject: *udpReject, killTest: *killTest}

	if !*allow {
		fail("BLOCKED: pass --allow-network-changes on a machine you control; this changes routes and DNS")
	}
	if !privileged() {
		fail("BLOCKED: " + privilegeHint)
	}
	proxy, hostForReport, err := proxyFromEnv()
	if err != nil || (*output == "" && !*hold) {
		fail("usage: --output NEWDIR with a node in the environment: " + fmt.Sprint(err))
	}
	if !*hold {
		if err := os.Mkdir(*output, 0o700); err != nil {
			fail("output directory must not exist: " + err.Error())
		}
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
	if *hold {
		holdSession(ctx, eng, profile, iface, err)
		return
	}
	if err != nil {
		record("T-PRE", "no competing VPN, administrator rights and a physical default route", err.Error()+" (disconnect other VPNs, e.g. a company VPN, and retry)", "blocked")
		finish(*output, hostForReport)
	}
	before := capture()
	baselineSnapshot = before
	baseline := fetch(ctx, *probe, "tcp4")
	baseline6 := fetch(ctx, *probe6, "tcp6")
	baselineExit, baselineSignature = baseline, network.Signature()
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
	record("T01a", "TUN device and its routes exist", fmt.Sprintf("up in %v; new devices %v", time.Since(started).Round(time.Millisecond), newDevices(before)), "pass")
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
	platformChecks(ctx, eng, opts, baseline6)
	apiCheck(ctx, *apiHost)
	idleLoopCheck(ctx, eng)
	if *soakSeconds > 0 {
		soakCheck(ctx, eng, *probe, baseline, time.Duration(*soakSeconds)*time.Second, time.Duration(max(*soakInterval, 1))*time.Second, *output)
	}

	_ = eng.Stop()
	network.Default.Clear()
	time.Sleep(2 * time.Second)
	if left := diff(before, capture()); len(left) > 0 {
		record("T07", "nothing of this run remains after Stop", strings.Join(left, "; "), "fail")
	} else {
		record("T07", "nothing of this run remains after Stop", "no TUN device, route, DNS or proxy-setting change left", "pass")
	}

	platformExtras(ctx, opts, proxy, before)
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
	if n < 2 {
		record("T-CYCLES", "at least two complete cycles", "--cycles must be >= 2", "fail")
		return
	}
	// Capture before even the first connect, so its leak cannot become a baseline.
	nat0, err := leakcheck.TCPNAT()
	if err != nil {
		record("T-CYCLES", "readable goroutine profile", err.Error(), "fail")
		return
	}
	completed := 0
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
		completed++
	}
	if completed != n {
		failures = append(failures, fmt.Sprintf("only %d/%d cycles completed (context=%v)", completed, n, ctx.Err()))
	}
	goroutines, fds := runtime.NumGoroutine(), openFDs()
	nat, err := leakcheck.TCPNAT()
	if err != nil {
		failures = append(failures, err.Error())
	}
	failures = append(failures, cycleGrowth(goroutines0, goroutines, fds0, fds, nat0, nat)...)
	if left := diff(before, capture()); len(left) > 0 {
		failures = append(failures, strings.Join(left, "; "))
	}
	record("T-CYCLES", fmt.Sprintf("%d connect/disconnect cycles leave no accumulation", n),
		fmt.Sprintf("%d/%d cycles; goroutines %d->%d fds %d->%d TCPNat.loopCheckTimeout %d->%d (%+d, allowed +0) failures=%v", completed, n, goroutines0, goroutines, fds0, fds, nat0, nat, nat-nat0, failures), verdict(len(failures) == 0))
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

type snapshot struct {
	Interfaces []string
	RoutesV4   []string
	RoutesV6   []string
	DNSDigest  string
	// Proxy is the Windows per-user system proxy setting (WinINET); empty on macOS. The
	// tunnel must never touch it.
	Proxy string
	// Err is set when the state could not be read; a comparison against it is meaningless.
	Err string
}

// baselineSnapshot is the machine state before the tunnel existed.
var baselineSnapshot snapshot

// holdSession connects, announces READY on stdout and waits until it is killed. It is the
// victim of --kill-test: the parent terminates it without any chance to clean up and then
// checks that the OS removed everything the session created.
func holdSession(ctx context.Context, eng *engine.Mihomo, profile managed.Profile, iface network.Interface, preflight error) {
	if preflight != nil {
		fmt.Println("PREFLIGHT-FAILED", preflight)
		os.Exit(3)
	}
	network.Default.Set(iface)
	if err := eng.Start(ctx, profile); err != nil {
		fmt.Println("START-FAILED", err)
		os.Exit(1)
	}
	fmt.Println("READY")
	<-ctx.Done()
}

func finish(output, host string) {
	platformCleanup()
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
		"go": runtime.Version(), "machine": machineFacts(), "cases": results, "failed": failed, "blocked": blocked,
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
