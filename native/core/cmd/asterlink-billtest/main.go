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
	flag.Parse()

	if !*allow {
		fail("BLOCKED: pass --allow-network-changes on a machine you control; this changes routes and DNS and spends plan quota")
	}
	if !privileged() {
		fail("BLOCKED: " + privilegeHint)
	}
	if *output == "" {
		fail("usage: --output NEWDIR (and the ASTERLINK_BILL_* environment, see the file header)")
	}
	cfg, err := configFromEnv()
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

func configFromEnv() (config, error) {
	cfg := config{
		apiBase: os.Getenv("ASTERLINK_BILL_API_BASE"), channel: "development",
		email: os.Getenv("ASTERLINK_BILL_EMAIL"), password: os.Getenv("ASTERLINK_BILL_PASSWORD"),
		sinkBase: strings.TrimRight(os.Getenv("ASTERLINK_BILL_SINK"), "/"), deviceID: os.Getenv("ASTERLINK_BILL_DEVICE_ID"),
	}
	for name, value := range map[string]string{"ASTERLINK_BILL_API_BASE": cfg.apiBase, "ASTERLINK_BILL_EMAIL": cfg.email, "ASTERLINK_BILL_PASSWORD": cfg.password, "ASTERLINK_BILL_SINK": cfg.sinkBase, tokenEnv: os.Getenv(tokenEnv)} {
		if value == "" {
			return cfg, fmt.Errorf("set %s", name)
		}
	}
	if _, err := apiclient.ParseBase(cfg.apiBase, cfg.channel); err != nil {
		return cfg, fmt.Errorf("ASTERLINK_BILL_API_BASE must be https://<host>/api/v1/client")
	}
	if u, err := url.Parse(cfg.sinkBase); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
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
