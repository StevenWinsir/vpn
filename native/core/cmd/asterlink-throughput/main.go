// Command asterlink-throughput is the long-run throughput regression of handoff_fix.md
// section 6.1 (round 3). It measures the tunnel the way an ordinary app uses it: it never
// starts, stops or configures the Core, so it sees the production path (the Core as the
// installed Helper or Windows service starts it), which is the only path where F1 and F4
// appear.
//
//	asterlink-throughput --url <large file through the tunnel> [--duration 10m] [--out dir]
//
// It downloads --url on --streams parallel connections for --duration, writes one CSV row
// every 10 s (throughput, HTTP time to first byte, and what it can read of the Core from
// outside: thread priorities on macOS; priority class, throttling policy and power source on
// Windows), runs a CPU contention phase in the middle (one busy process per core) and judges
// the run by section 6.1: under contention at least 70% of the idle baseline, back to 90%
// within 10 s after it, and no plateau of 3 minutes below 50% anywhere. Exit status: 0 PASS,
// 1 FAIL, 3 BLOCKED (cannot be judged), 2 usage.
//
// Two helper modes exist for the R3-01 self-check on one machine:
//
//	asterlink-throughput relay --listen 127.0.0.1:18090 --to 127.0.0.1:18088 --rate 2000
//	asterlink-throughput burn --for 2m
//
// relay is the AEAD forwarder stand-in of section 2.2 (run it under `taskpolicy -c utility`
// to reproduce the pre-fix clamp); burn is one contention process (started by the tool itself).
// This is a development and acceptance tool: it is never part of a package.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "relay":
			fs := flag.NewFlagSet("relay", flag.ExitOnError)
			listen := fs.String("listen", "127.0.0.1:18090", "address to accept on (loopback)")
			to := fs.String("to", "127.0.0.1:18088", "address every connection is forwarded to")
			rate := fs.Float64("rate", 0, "pace the relay to this many Mbit/s (0: unpaced, CPU-bound on loopback; see relay.go)")
			_ = fs.Parse(os.Args[2:])
			os.Exit(runRelay(*listen, *to, *rate))
		case "burn":
			fs := flag.NewFlagSet("burn", flag.ExitOnError)
			d := fs.Duration("for", 2*time.Minute, "how long to spin before exiting on its own")
			threads := fs.Int("threads", 1, "busy threads in this process")
			_ = fs.Parse(os.Args[2:])
			os.Exit(runBurn(*d, *threads))
		}
	}
	os.Exit(runMeasure(os.Args[1:]))
}

func runMeasure(args []string) int {
	fs := flag.NewFlagSet("asterlink-throughput", flag.ContinueOnError)
	t := defaultThresholds()
	o := options{Thresholds: t}
	fs.StringVar(&o.URL, "url", "", "file to download repeatedly through the tunnel (required; a large file or the traffic sink's /down)")
	fs.StringVar(&o.RTTURL, "rtt-url", "", "URL for the 1 s time-to-first-byte probe (default --url with Range 0-0)")
	fs.IntVar(&o.Streams, "streams", 4, "parallel download connections")
	fs.DurationVar(&o.Duration, "duration", 10*time.Minute, "length of the run")
	fs.DurationVar(&o.Warmup, "warmup", 30*time.Second, "first part that is recorded but not judged")
	fs.DurationVar(&o.Window, "window", 10*time.Second, "length of one CSV row")
	fs.DurationVar(&o.ContentionAt, "contention-at", 4*time.Minute, "when the CPU contention phase starts")
	fs.DurationVar(&o.ContentionFor, "contention-for", 2*time.Minute, "how long it lasts (0: no contention phase, plateau rule only)")
	fs.IntVar(&o.Burners, "burners", runtime.NumCPU(), "contention processes (one busy thread each)")
	fs.StringVar(&o.CoreName, "core-name", defaultCoreName, "process name of the Core to sample")
	fs.IntVar(&o.CorePID, "core-pid", 0, "sample this process instead of looking the name up (for example the relay)")
	fs.DurationVar(&o.StallAfter, "stall-after", 30*time.Second, "cut and retry a download that moved no byte for this long")
	fs.Float64Var(&o.LimitMbps, "limit-mbps", 0, "read at most this many Mbit/s in total, like one video stream (0: as fast as the path goes); "+
		"a capped run costs less of the account's traffic, but only shows drops below the cap")
	fs.Float64Var(&o.Thresholds.ContentionRatio, "min-contention-ratio", t.ContentionRatio, "rule: median under contention / baseline")
	fs.Float64Var(&o.Thresholds.RecoverRatio, "recover-ratio", t.RecoverRatio, "rule: share of the baseline to get back to")
	fs.DurationVar(&o.Thresholds.RecoverWithin, "recover-within", t.RecoverWithin, "rule: time allowed to get back")
	fs.Float64Var(&o.Thresholds.PlateauRatio, "plateau-ratio", t.PlateauRatio, "rule: a window below this share of the baseline is low")
	fs.DurationVar(&o.Thresholds.PlateauFor, "plateau-for", t.PlateauFor, "rule: this long of low windows in a row is a plateau")
	outDir := fs.String("out", "", "directory for throughput.csv and report.json (default artifacts/throughput/<time>)")
	label := fs.String("label", "", "free text stored in the report (build, node, power state)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := validate(&o); err != nil {
		log.Print(err)
		return 2
	}
	if *outDir == "" {
		*outDir = filepath.Join("artifacts", "throughput", time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Print(err)
		return 2
	}
	csvFile, err := os.Create(filepath.Join(*outDir, "throughput.csv"))
	if err != nil {
		log.Print(err)
		return 2
	}
	defer csvFile.Close()
	writer := csv.NewWriter(csvFile)
	_ = writer.Write(csvHeader)
	writer.Flush()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := time.Now()
	log.Printf("measuring %s for %v (%d streams; contention %v from %v with %d burners); writing %s",
		redact(o.URL), o.Duration, o.Streams, o.ContentionFor, o.ContentionAt, o.Burners, *outDir)
	res, runErr := measure(ctx, o, func(rw row) {
		_ = writer.Write(rw.csv())
		writer.Flush()
		log.Printf("%4ds-%4ds %-10s %8.2f Mbit/s  ttfb p50 %6.1f ms  core %s", int(rw.Start.Seconds()), int((rw.Start + rw.Length).Seconds()),
			rw.Phase, rw.mbps(), rw.TTFBP50, coreText(rw.Core))
	})
	windows := make([]window, len(res.Rows))
	for i, rw := range res.Rows {
		windows[i] = rw.window
	}
	v := judge(windows, res.PerSecond, res.ContentionEnd, o.Thresholds)
	if runErr != nil {
		v.Reasons = append(v.Reasons, "interrupted after "+time.Since(started).Round(time.Second).String()+": judged on the part that ran")
	}
	report := map[string]any{
		"tool": "asterlink-throughput", "label": *label, "started_at": started.UTC().Format(time.RFC3339),
		"host": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU()},
		"url":  redact(o.URL), "streams": o.Streams, "duration_s": o.Duration.Seconds(), "warmup_s": o.Warmup.Seconds(), "limit_mbps": o.LimitMbps,
		"contention": map[string]any{"at_s": o.ContentionAt.Seconds(), "for_s": o.ContentionFor.Seconds(), "burners": o.Burners},
		"thresholds": map[string]any{"min_contention_ratio": o.Thresholds.ContentionRatio, "recover_ratio": o.Thresholds.RecoverRatio,
			"recover_within_s": o.Thresholds.RecoverWithin.Seconds(), "plateau_ratio": o.Thresholds.PlateauRatio, "plateau_for_s": o.Thresholds.PlateauFor.Seconds()},
		"verdict": v, "core": summarizeCores(res.Cores),
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(*outDir, "report.json"), data, 0o644); err != nil {
		log.Print(err)
	}
	log.Printf("%s: baseline %.2f Mbit/s, contention %.2f Mbit/s (%.0f%%), recovery %.0f s, longest low run %.0f s",
		v.Status, v.BaselineMbps, v.ContentionMbps, v.ContentionRatio*100, v.RecoverySeconds, v.LongestLowSeconds)
	log.Printf("core: %s", coreSummaryText(summarizeCores(res.Cores)))
	for _, reason := range v.Reasons {
		log.Printf("  %s", reason)
	}
	switch v.Status {
	case "PASS":
		return 0
	case "FAIL":
		return 1
	}
	return 3
}

func validate(o *options) error {
	switch {
	case o.URL == "":
		return fmt.Errorf("--url is required: a large file reached through the tunnel (see docs/speed-troubleshooting.md)")
	case !strings.HasPrefix(o.URL, "http://") && !strings.HasPrefix(o.URL, "https://"):
		return fmt.Errorf("--url must be http or https")
	case o.Streams < 1 || o.Burners < 0:
		return fmt.Errorf("--streams must be at least 1 and --burners not negative")
	case o.Window < time.Second || o.Window%time.Second != 0:
		return fmt.Errorf("--window must be whole seconds")
	case o.Duration < 4*o.Window:
		return fmt.Errorf("--duration must cover at least 4 windows")
	}
	// The contention phase is aligned to whole windows, so every window is either fully idle or
	// fully under contention and the two medians compare like with like.
	o.ContentionAt = o.ContentionAt.Truncate(o.Window)
	o.ContentionFor = o.ContentionFor.Truncate(o.Window)
	if o.ContentionFor > 0 && (o.ContentionAt < o.Warmup+3*o.Window || o.ContentionAt+o.ContentionFor+2*o.Window > o.Duration) {
		return fmt.Errorf("the contention phase needs 3 idle windows after the warm-up before it and 2 windows after it inside --duration")
	}
	return nil
}

// redact drops the query string, which may carry the traffic sink's access value.
func redact(url string) string {
	if i := strings.IndexByte(url, '?'); i >= 0 {
		return url[:i] + "?…"
	}
	return url
}

func coreText(c coreSample) string {
	switch {
	case c.Err != "" && c.PID == 0:
		return c.Err
	case c.PrioMin != 0:
		return fmt.Sprintf("pid %d, %d threads, priority %d-%d", c.PID, c.Threads, c.PrioMin, c.PrioMax)
	case c.PrioClass != "" || c.Throttling != "":
		return fmt.Sprintf("pid %d, %d threads, class %s, throttling %s, power %s", c.PID, c.Threads, c.PrioClass, c.Throttling, c.Power)
	}
	return fmt.Sprintf("pid %d %s", c.PID, c.Err)
}

type coreSummary struct {
	Samples    int      `json:"samples"`
	Found      int      `json:"found"`
	PrioMin    int      `json:"prio_min,omitempty"`
	PrioMax    int      `json:"prio_max,omitempty"`
	Classes    []string `json:"priority_classes,omitempty"`
	Throttling []string `json:"throttling,omitempty"`
	Power      []string `json:"power,omitempty"`
	Errors     []string `json:"errors,omitempty"`
	// macOS: in some sample no thread was above 20, the utility clamp of F1. The minimum alone
	// says little: the scheduler lowers a busy thread's current priority below 31 by itself.
	Clamped bool `json:"utility_clamped,omitempty"`
}

func summarizeCores(samples []coreSample) coreSummary {
	s := coreSummary{Samples: len(samples)}
	sets := map[string]map[string]bool{"class": {}, "thr": {}, "power": {}, "err": {}}
	for _, c := range samples {
		if c.PID != 0 && c.Err == "" || c.PrioMin != 0 || c.PrioClass != "" {
			s.Found++
		}
		if c.PrioMin != 0 && (s.PrioMin == 0 || c.PrioMin < s.PrioMin) {
			s.PrioMin = c.PrioMin
		}
		if c.PrioMax > s.PrioMax {
			s.PrioMax = c.PrioMax
		}
		if c.PrioMax > 0 && c.PrioMax <= 20 {
			s.Clamped = true
		}
		for key, value := range map[string]string{"class": c.PrioClass, "thr": c.Throttling, "power": c.Power, "err": c.Err} {
			if value != "" {
				sets[key][value] = true
			}
		}
	}
	keys := func(m map[string]bool) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	s.Classes, s.Throttling, s.Power, s.Errors = keys(sets["class"]), keys(sets["thr"]), keys(sets["power"]), keys(sets["err"])
	return s
}

func coreSummaryText(s coreSummary) string {
	parts := []string{fmt.Sprintf("%d/%d samples found it", s.Found, s.Samples)}
	if s.PrioMin != 0 {
		parts = append(parts, fmt.Sprintf("thread priority %d-%d", s.PrioMin, s.PrioMax))
	}
	if s.Clamped {
		parts = append(parts, "UTILITY CLAMP seen (no thread above 20, handoff_fix.md F1)")
	}
	if len(s.Classes) > 0 {
		parts = append(parts, "class "+strings.Join(s.Classes, "/"))
	}
	if len(s.Throttling) > 0 {
		parts = append(parts, "throttling "+strings.Join(s.Throttling, "/"))
	}
	if len(s.Power) > 0 {
		parts = append(parts, "power "+strings.Join(s.Power, "/"))
	}
	if len(s.Errors) > 0 {
		parts = append(parts, "errors: "+strings.Join(s.Errors, "; "))
	}
	return strings.Join(parts, ", ")
}
