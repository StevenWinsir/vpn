// Command asterlink-billtest is the privileged billing acceptance harness for round 5
// (handoff_app_windows.md section 10, cases A02, A04, B01-B03). Unlike asterlink-tuntest it
// drives the production state machine (managed.Manager) with the production engine, against
// the REAL API and database: login -> select a line -> connect (first metering ack, then a
// real TUN) -> move controlled bytes -> disconnect (tail settled) -> compare the server's
// ledger with the formula, to the unit.
//
// The bytes come from asterlink-trafficsink, a target you run on a machine the test node can
// reach, so the amount that crossed the node is known independently of the client counters.
// The transfer itself runs in a child process: on Windows the Core's own process is exempt
// from the TUN firewall rules, and an ordinary application is what must be metered.
//
// It changes the machine's routes and DNS and spends plan quota of the test account, so it:
//   - refuses to run without administrator/root and the explicit --allow-network-changes flag;
//   - takes every secret from the environment only (never argv), and writes only a redacted
//     JSON report (no token, password, node address or URL);
//   - stops itself after --max-seconds and on Ctrl+C, always closing the tunnel.
//
// Environment (all required unless noted):
//
//	ASTERLINK_BILL_API_BASE    https://<host>/api/v1/client
//	ASTERLINK_BILL_EMAIL / ASTERLINK_BILL_PASSWORD   a test account with an active plan
//	ASTERLINK_BILL_SINK        http://<host>:<port> of asterlink-trafficsink
//	ASTERLINK_BILL_SINK_TOKEN  the sink's ASTERLINK_SINK_TOKEN
//	ASTERLINK_BILL_DEVICE_ID   optional UUID; default is derived from the host name
//
// --presence (presence.go) instead checks online leases with several devices of the account:
// device limit, a full line and a server withdrawn under a live connection. It needs no sink.
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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == childFlag {
		os.Exit(runChild(os.Args[2:]))
	}
	allow := flag.Bool("allow-network-changes", false, "confirm this machine may have its routes/DNS changed and the test account's quota spent")
	output := flag.String("output", "", "new directory for the redacted JSON report")
	uploadMiB := flag.Int64("upload-mib", 20, "controlled upload per measured cycle, MiB")
	downloadMiB := flag.Int64("download-mib", 80, "controlled download per measured cycle, MiB")
	nodes := flag.String("nodes", "", "comma-separated node ids or exact names to measure (default: one per distinct rate, at most two)")
	backgroundKiB := flag.Int64("background-kib", 2048, "foreign traffic tolerated per direction during a cycle, KiB")
	ackLoss := flag.Bool("ack-loss", false, "also run B02: lose one real acknowledgement and require an identical retry charged once")
	watchSeconds := flag.Int("watch-stop-seconds", 0, "instead of B01: hold a connection for N seconds and require it to close by itself after you change the node/plan/quota in the admin panel")
	watchDrive := flag.Bool("watch-drive", false, "with --watch-stop-seconds: keep downloading so a low quota is exhausted")
	expectStop := flag.String("expect-stop", "", "with --watch-stop-seconds: the stable reason code expected (e.g. node_unavailable, profile_changed, subscription_expired, quota_exhausted)")
	maxSeconds := flag.Int("max-seconds", 1800, "hard upper bound for the whole run")
	grants := flag.Bool("grants", false, "round 7 instead of B01: verify the node enforces billing, revocation and expiry with an independent client (no client report)")
	spend := flag.Bool("spend-test-quota", false, "with --grants: confirm the test account's quota may be spent and device slots used")
	tun := flag.Bool("tun", false, "with --grants: also run B04 through the production Manager and the real TUN (elevated Windows; needs --allow-network-changes)")
	cutWithin := flag.Int("cut-within-seconds", 15, "with --grants: a revoked grant must close established flows within this long")
	holdSeconds := flag.Int("hold-seconds", 0, "with --grants: open flows and wait this long for YOU to end the plan / lower the quota / disable the node / stop the API; the node must cut them")
	holdCase := flag.String("hold-case", "B05-admin", "with --hold-seconds: the case id to record (B05-expire, B05-quota, B07-outage, ...)")
	holdDrive := flag.Bool("hold-drive", false, "with --hold-seconds: keep downloading so a low quota is exhausted")
	idleExpiry := flag.Bool("idle-expiry", false, "with --grants: let a grant lapse with no client activity and require the node to end it by itself")
	idleWait := flag.Int("idle-wait-seconds", 0, "with --idle-expiry: upper bound (default: session idle timeout + 90 s)")
	raceN := flag.Int("race-sessions", 0, "with --grants: B06, this many devices ask for a grant at once (needs spare device slots)")
	raceAt := flag.String("race-at", "", "with --race-sessions: RFC 3339 start time, to line up several machines (e.g. 2026-10-14T20:30:00+08:00)")
	raceMax := flag.Int64("race-max-mib", 2048, "with --race-sessions: refuse (BLOCKED) when the account has more quota than this left")
	udpSink := flag.String("udp-sink", "", "with --grants: host:port of the sink's UDP echo, tested with pinned Mihomo XUDP (no command-2 fallback); empty = TCP flows only")
	presence := flag.Bool("presence", false, "instead of B01: online leases through the production Manager (P01 device limit, P02 group full, P03 reassignment); several devices of the test account sign in")
	slots := flag.Bool("use-device-slots", false, "with --presence: confirm the test account's device slots may be taken (its other devices may be refused meanwhile)")
	deviceLimit := flag.Int("device-limit", 2, "with --presence: the account's effective online-device limit, min(plan max_devices, CLIENT_MAX_ONLINE_DEVICES)")
	line := flag.String("line", "", "with --presence: id or exact name of the line for P01/P03 (default: the first client-reported line)")
	fullLine := flag.String("full-line", "", "with --presence: id or exact name of a line whose total capacity is below --device-limit (P02; empty = BLOCKED)")
	reassignWait := flag.Int("reassign-wait-seconds", 0, "with --presence: P03, hold a connection this long for YOU to disable its server in /admin/nodes (0 = BLOCKED)")
	reconnectWithin := flag.Int("reconnect-within-seconds", 30, "with --reassign-wait-seconds: the automatic reconnect must complete within this long")
	flag.Parse()

	if *presence {
		os.Exit(presenceMain(*output, *slots, *tun, *allow, presenceOptions{
			limit: *deviceLimit, line: *line, fullLine: *fullLine, tun: *tun,
			reassignWait: time.Duration(*reassignWait) * time.Second, reconnectWithin: time.Duration(*reconnectWithin) * time.Second,
		}, time.Duration(*maxSeconds)*time.Second))
	}
	needTUN := !*grants || *tun
	if *grants && !*spend {
		fail("BLOCKED: --grants spends the test account's quota; pass --spend-test-quota to confirm")
	}
	if needTUN && !*allow {
		fail("BLOCKED: pass --allow-network-changes on a machine you control; this changes routes and DNS and spends plan quota")
	}
	if needTUN && !privileged() {
		fail("BLOCKED: " + privilegeHint)
	}
	if *output == "" {
		fail("usage: --output NEWDIR (and the ASTERLINK_BILL_* environment, see the file header)")
	}
	cfg, err := configFromEnv(true)
	if err != nil {
		fail("BLOCKED: " + err.Error())
	}
	cfg.upload, cfg.download = *uploadMiB<<20, *downloadMiB<<20
	cfg.background = *backgroundKiB << 10
	cfg.ackLoss = *ackLoss
	cfg.watchStop = time.Duration(*watchSeconds) * time.Second
	cfg.watchDrive, cfg.expectStop = *watchDrive, *expectStop
	cfg.settleWait = 2500 * time.Millisecond
	for _, n := range strings.Split(*nodes, ",") {
		if n = strings.TrimSpace(n); n != "" {
			cfg.nodes = append(cfg.nodes, n)
		}
	}
	if err := os.Mkdir(*output, 0o700); err != nil {
		fail("output directory must not exist: " + err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*maxSeconds)*time.Second)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() { <-signals; cancel() }()

	lossy := newLossy()
	eng := engine.New()
	if *grants {
		code := runGrants(ctx, cfg, grantOptions{
			tun: *tun, cutWithin: time.Duration(*cutWithin) * time.Second, holdFor: time.Duration(*holdSeconds) * time.Second, holdCase: *holdCase, holdDrive: *holdDrive,
			idleExpiry: *idleExpiry, idleWait: time.Duration(*idleWait) * time.Second, raceN: *raceN, raceMaxByte: *raceMax << 20, udpSink: *udpSink,
		}, *raceAt, *output, lossy, eng)
		os.Exit(code)
	}
	manager := managed.New(managed.Options{
		CoreVersion: "billtest", Engine: eng,
		NewClient: func(base *url.URL) *apiclient.Client {
			client := apiclient.New(base)
			client.WrapTransport(lossy.wrap)
			return client
		},
	})
	h := &harness{
		cfg: cfg, m: manager, lossy: lossy, rec: &recorder{},
		xfer: childTransfer(cfg.sinkBase),
		stats: func(ctx context.Context) (sinkStats, error) {
			return fetchSinkStats(ctx, cfg.sinkBase, os.Getenv(tokenEnv))
		},
	}
	h.run(ctx)
	code := finish(*output, h.rec, cfg)
	// Whatever happened, the data path is closed and the tail settled under the live token.
	stop, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	manager.Shutdown(stop)
	stopCancel()
	_ = eng.Stop()
	cleanupProbe()
	os.Exit(code)
}

func configFromEnv(needSink bool) (config, error) {
	cfg := config{
		apiBase: os.Getenv("ASTERLINK_BILL_API_BASE"), channel: "development",
		email: os.Getenv("ASTERLINK_BILL_EMAIL"), password: os.Getenv("ASTERLINK_BILL_PASSWORD"),
		sinkBase: strings.TrimRight(os.Getenv("ASTERLINK_BILL_SINK"), "/"), deviceID: os.Getenv("ASTERLINK_BILL_DEVICE_ID"),
	}
	required := map[string]string{"ASTERLINK_BILL_API_BASE": cfg.apiBase, "ASTERLINK_BILL_EMAIL": cfg.email, "ASTERLINK_BILL_PASSWORD": cfg.password}
	if needSink {
		required["ASTERLINK_BILL_SINK"], required[tokenEnv] = cfg.sinkBase, os.Getenv(tokenEnv)
	}
	for name, value := range required {
		if value == "" {
			return cfg, fmt.Errorf("set %s", name)
		}
	}
	if _, err := apiclient.ParseBase(cfg.apiBase, cfg.channel); err != nil {
		return cfg, fmt.Errorf("ASTERLINK_BILL_API_BASE must be https://<host>/api/v1/client")
	}
	if u, err := url.Parse(cfg.sinkBase); needSink && (err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https")) {
		return cfg, fmt.Errorf("ASTERLINK_BILL_SINK must be http(s)://host:port")
	}
	if cfg.deviceID == "" {
		cfg.deviceID = deviceIDFromHost()
	}
	return cfg, nil
}

// deviceIDFromHost makes a stable v4-shaped UUID, so repeated runs reuse one device slot
// of the test account instead of exhausting its device limit.
func deviceIDFromHost() string {
	host, _ := os.Hostname()
	sum := sha256.Sum256([]byte("asterlink-billtest|" + host))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// presenceMain runs --presence. It needs no sink: only the control plane is measured, unless
// --tun gives device 1 the production engine (privileged, like B01).
func presenceMain(output string, slots, tun, allow bool, o presenceOptions, maxRun time.Duration) int {
	if !slots {
		fail("BLOCKED: --presence signs in several devices of the test account and takes its slots; pass --use-device-slots to confirm")
	}
	if o.limit < 1 || o.limit > 20 {
		fail("--device-limit must be 1-20")
	}
	if tun && (!allow || !privileged()) {
		fail("BLOCKED: --tun needs --allow-network-changes and " + privilegeHint)
	}
	if output == "" {
		fail("usage: --output NEWDIR (and the ASTERLINK_BILL_* environment, see the file header)")
	}
	cfg, err := configFromEnv(false)
	if err != nil {
		fail("BLOCKED: " + err.Error())
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		fail("output directory must not exist: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxRun)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() { <-signals; cancel() }()
	var production managed.Engine
	run := &presenceRun{cfg: cfg, opts: o, rec: &recorder{}, newEngine: func(i int) managed.Engine {
		if tun && i == 0 {
			production = engine.New()
			return production
		}
		return controlEngine{}
	}}
	fmt.Printf("presence: %s\n", presenceSummary(o))
	run.run(ctx)
	if production != nil {
		_ = production.Stop()
		cleanupProbe()
	}
	return finish(output, run.rec, cfg)
}

func finish(output string, rec *recorder, cfg config) int {
	failed, blocked := rec.counts()
	report := map[string]any{
		"started": time.Now().UTC().Format(time.RFC3339), "go": runtime.Version(), "machine": machineFacts(),
		"api_host_sha256_prefix": shortHash(cfg.apiBase), "sink_host_sha256_prefix": shortHash(cfg.sinkBase),
		"upload_bytes": cfg.upload, "download_bytes": cfg.download,
		"cases": rec.cases, "failed": failed, "blocked": blocked,
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(output, "billing-report.json"), data, 0o600)
	fmt.Printf("%d cases: %d fail, %d blocked\n", len(rec.cases), failed, blocked)
	switch {
	case failed > 0:
		return 1
	case blocked > 0 || len(rec.cases) == 0:
		return 3
	}
	return 0
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(3)
}
