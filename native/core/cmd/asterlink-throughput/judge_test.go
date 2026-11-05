package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The tool starts copies of itself in "burn" mode; under go test that copy is the test binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "burn" {
		os.Exit(runBurn(2*time.Second, 1))
	}
	os.Exit(m.Run())
}

// series builds a run from per-second Mbit/s values: 10 s windows, contention in [cStart, cEnd).
func series(mbps []float64, warmup, cStart, cEnd int) ([]window, []int64, time.Duration) {
	per := make([]int64, len(mbps))
	for i, v := range mbps {
		per[i] = int64(v * 1e6 / 8)
	}
	var windows []window
	for from := 0; from+10 <= len(per); from += 10 {
		var b int64
		for _, x := range per[from : from+10] {
			b += x
		}
		windows = append(windows, window{Start: time.Duration(from) * time.Second, Length: 10 * time.Second, Bytes: b,
			Warmup: from < warmup, Contention: cEnd > 0 && from >= cStart && from+10 <= cEnd})
	}
	end := time.Duration(0)
	if cEnd > 0 {
		end = time.Duration(cEnd) * time.Second
	}
	return windows, per, end
}

func flat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func concat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestJudgePassesAHealthyRun(t *testing.T) {
	// 30 s warm-up, 60 s idle at 60, 60 s contention at 55, 60 s after at 60.
	w, per, end := series(concat(flat(90, 60), flat(60, 55), flat(60, 60)), 30, 90, 150)
	v := judge(w, per, end, defaultThresholds())
	if v.Status != "PASS" || v.BaselineMbps != 60 || v.RecoverySeconds != 0 {
		t.Fatalf("healthy run: %+v", v)
	}
}

func TestJudgeFailsTheF1Collapse(t *testing.T) {
	// The utility clamp of section 2.2: throughput falls to a few percent while the CPU is full.
	w, per, end := series(concat(flat(90, 60), flat(60, 2), flat(60, 60)), 30, 90, 150)
	v := judge(w, per, end, defaultThresholds())
	if v.Status != "FAIL" || !strings.Contains(strings.Join(v.Reasons, "|"), "under contention") {
		t.Fatalf("collapse not caught: %+v", v)
	}
	if v.ContentionRatio > 0.05 {
		t.Fatalf("ratio %v", v.ContentionRatio)
	}
}

func TestJudgeFailsASlowRecovery(t *testing.T) {
	// Fine under contention, but it ramps back over 30 s instead of 10.
	ramp := make([]float64, 30)
	for i := range ramp {
		ramp[i] = 20 + float64(i)*1.2
	}
	w, per, end := series(concat(flat(90, 60), flat(60, 50), ramp, flat(30, 60)), 30, 90, 150)
	v := judge(w, per, end, defaultThresholds())
	if v.Status != "FAIL" || v.RecoverySeconds <= 10 {
		t.Fatalf("slow recovery not caught: %+v", v)
	}
}

func TestJudgeFailsThePlateau(t *testing.T) {
	// The user's report: fine, then a drop that lasts minutes, unrelated to the contention phase.
	w, per, end := series(concat(flat(90, 60), flat(60, 58), flat(30, 60), flat(200, 20), flat(30, 60)), 30, 90, 150)
	v := judge(w, per, end, defaultThresholds())
	if v.Status != "FAIL" || v.LongestLowSeconds != 200 {
		t.Fatalf("plateau not caught: %+v", v)
	}
	// A dip shorter than the rule is not a plateau.
	w, per, end = series(concat(flat(90, 60), flat(60, 58), flat(30, 60), flat(60, 20), flat(30, 60)), 30, 90, 150)
	if v = judge(w, per, end, defaultThresholds()); v.Status != "PASS" || v.LongestLowSeconds != 60 {
		t.Fatalf("short dip judged as plateau: %+v", v)
	}
}

func TestJudgeBlocksWhatCannotBeJudged(t *testing.T) {
	w, per, end := series(flat(60, 60), 30, 40, 50)
	if v := judge(w, per, end, defaultThresholds()); v.Status != "BLOCKED" {
		t.Fatalf("too short: %+v", v)
	}
	w, per, end = series(concat(flat(90, 0), flat(60, 0)), 30, 90, 150)
	if v := judge(w, per, end, defaultThresholds()); v.Status != "BLOCKED" {
		t.Fatalf("nothing downloaded: %+v", v)
	}
}

func TestJudgeWithoutContentionUsesThePlateauRuleOnly(t *testing.T) {
	w, per, end := series(flat(600, 40), 30, 0, 0)
	v := judge(w, per, end, defaultThresholds())
	if v.Status != "PASS" || v.ContentionRatio != 0 || !strings.Contains(strings.Join(v.Reasons, "|"), "only the plateau rule") {
		t.Fatalf("long watch: %+v", v)
	}
	w, per, end = series(concat(flat(120, 40), flat(240, 10), flat(240, 40)), 30, 0, 0)
	if v = judge(w, per, end, defaultThresholds()); v.Status != "FAIL" {
		t.Fatalf("plateau in a long watch: %+v", v)
	}
}

func TestParsePSThreads(t *testing.T) {
	out := "USER   PID   TT   %CPU STAT PRI     STIME     UTIME COMMAND\n" +
		"root  3261   ??    0.0 S    31T   0:00.01   0:00.01 /Library/Application Support/AsterLink/Workers/0.1.1/asterlink-core\n" +
		"      3261         0.0 S    20T   0:00.00   0:00.00 \n" +
		"      3261         4.1 R    47    0:00.00   0:00.00 \n"
	threads, lo, hi, err := parsePSThreads(out)
	if err != nil || threads != 3 || lo != 20 || hi != 47 {
		t.Fatalf("got %d threads, %d-%d, %v", threads, lo, hi, err)
	}
	if _, _, _, err = parsePSThreads("USER PID\n"); err == nil {
		t.Fatal("no thread line accepted")
	}
}

func TestCoreSummaryFlagsTheClampOnlyWhenNoThreadIsAbove20(t *testing.T) {
	// A busy unclamped Core: the scheduler's decay lowers some threads to 21-22, none is clamped.
	busy := summarizeCores([]coreSample{{PID: 1, Threads: 9, PrioMin: 21, PrioMax: 31}, {PID: 1, Threads: 9, PrioMin: 31, PrioMax: 31}})
	if busy.Clamped || busy.PrioMin != 21 || busy.PrioMax != 31 || busy.Found != 2 {
		t.Fatalf("unclamped: %+v", busy)
	}
	clamped := summarizeCores([]coreSample{{PID: 1, Threads: 9, PrioMin: 20, PrioMax: 20}, {Err: "not running"}})
	if !clamped.Clamped || clamped.Found != 1 || !strings.Contains(coreSummaryText(clamped), "UTILITY CLAMP") {
		t.Fatalf("clamped: %+v %s", clamped, coreSummaryText(clamped))
	}
	win := summarizeCores([]coreSample{{PID: 7, Threads: 20, PrioClass: "normal", Throttling: "opted_out", Power: "battery"}})
	if win.Found != 1 || win.Clamped || win.Throttling[0] != "opted_out" || win.Power[0] != "battery" {
		t.Fatalf("windows: %+v", win)
	}
}

func TestValidateAlignsAndRejects(t *testing.T) {
	o := options{URL: "http://127.0.0.1/x", Streams: 1, Duration: 10 * time.Minute, Warmup: 30 * time.Second, Window: 10 * time.Second,
		ContentionAt: 4*time.Minute + 3*time.Second, ContentionFor: 2*time.Minute + 7*time.Second}
	if err := validate(&o); err != nil || o.ContentionAt != 4*time.Minute || o.ContentionFor != 2*time.Minute {
		t.Fatalf("alignment: %v %v %v", err, o.ContentionAt, o.ContentionFor)
	}
	bad := o
	bad.ContentionAt = 9 * time.Minute
	if validate(&bad) == nil {
		t.Fatal("contention at the very end accepted")
	}
	bad = o
	bad.URL = "ftp://x"
	if validate(&bad) == nil {
		t.Fatal("non-http URL accepted")
	}
	if redact("http://h/down?bytes=1&t=secret") != "http://h/down?…" {
		t.Fatal("query not redacted")
	}
}
