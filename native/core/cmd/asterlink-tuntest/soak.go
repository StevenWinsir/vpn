package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"

	"asterlink/core/internal/engine"
)

// Long-running connected sampling (handoff 10 round 6, R03). It records what the Core
// process really consumes while one tunnel stays up and probes the exit now and then, so
// a leak that appears after hours (a drifted route, a rebuilt adapter) is caught. The
// verdict criteria below only separate "bounded" from "growing without limit"; they are
// not performance promises, and the CSV next to the report is the evidence to read.

type soakSample struct {
	At         time.Duration
	Goroutines int
	Threads    int
	Handles    int
	HeapMiB    float64
	CPUSeconds float64
	Active     int
	Probed     bool
	ExitOK     bool
}

// Growth limits measured from the third sample (the first two are warm-up).
const (
	soakMaxGoroutineGrowth = 50
	soakMaxHandleGrowth    = 64
	soakMaxThreadGrowth    = 32
	soakMaxHeapGrowthMiB   = 64.0
)

func judgeSoak(samples []soakSample) (bool, string) {
	if len(samples) < 4 {
		return false, fmt.Sprintf("only %d samples; the run was too short to judge growth", len(samples))
	}
	for _, s := range samples {
		if s.Probed && !s.ExitOK {
			return false, fmt.Sprintf("the exit address left the tunnel at +%v (direct or failed)", s.At.Round(time.Second))
		}
	}
	base, last := samples[2], samples[len(samples)-1]
	var problems []string
	if d := last.Goroutines - base.Goroutines; d > soakMaxGoroutineGrowth {
		problems = append(problems, fmt.Sprintf("goroutines +%d", d))
	}
	if d := last.Handles - base.Handles; d > soakMaxHandleGrowth {
		problems = append(problems, fmt.Sprintf("handles +%d", d))
	}
	if d := last.Threads - base.Threads; d > soakMaxThreadGrowth {
		problems = append(problems, fmt.Sprintf("threads +%d", d))
	}
	if d := last.HeapMiB - base.HeapMiB; d > soakMaxHeapGrowthMiB {
		problems = append(problems, fmt.Sprintf("heap +%.0f MiB", d))
	}
	detail := fmt.Sprintf("%d samples over %v: goroutines %d->%d handles %d->%d threads %d->%d heap %.1f->%.1f MiB cpu %.1fs",
		len(samples), last.At.Round(time.Second), base.Goroutines, last.Goroutines, base.Handles, last.Handles,
		base.Threads, last.Threads, base.HeapMiB, last.HeapMiB, last.CPUSeconds)
	if len(problems) > 0 {
		return false, detail + "; unbounded growth: " + fmt.Sprint(problems)
	}
	return true, detail
}

func takeSample(start time.Time, eng *engine.Mihomo) soakSample {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return soakSample{
		At: time.Since(start), Goroutines: runtime.NumGoroutine(),
		Threads: pprof.Lookup("threadcreate").Count(), Handles: openFDs(),
		HeapMiB: float64(mem.HeapAlloc) / (1 << 20), CPUSeconds: cpuSeconds(), Active: eng.ActiveConnections(),
	}
}

// soakCheck keeps the tunnel up for d, sampling every interval and probing the exit
// every probeEvery samples. outputDir may be empty (no CSV).
func soakCheck(ctx context.Context, eng *engine.Mihomo, probe, baseline string, d, interval time.Duration, outputDir string) {
	start := time.Now()
	var samples []soakSample
	tick := time.NewTicker(interval)
	defer tick.Stop()
	const probeEvery = 5
loop:
	for n := 0; ; n++ {
		s := takeSample(start, eng)
		if n%probeEvery == 0 {
			exit := fetch(ctx, probe, "tcp4")
			s.Probed, s.ExitOK = true, exit != "" && exit != baseline
		}
		samples = append(samples, s)
		if time.Since(start) >= d {
			break
		}
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
		}
	}
	if outputDir != "" {
		writeSoakCSV(filepath.Join(outputDir, "soak.csv"), samples)
	}
	ok, detail := judgeSoak(samples)
	record("R03-soak", fmt.Sprintf("a tunnel held for %v keeps its exit and shows no unbounded resource growth", d), detail, verdict(ok))
}

func writeSoakCSV(path string, samples []soakSample) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"seconds", "goroutines", "threads", "handles", "heap_mib", "cpu_seconds", "active_flows", "probed", "exit_ok"})
	for _, s := range samples {
		_ = w.Write([]string{
			strconv.Itoa(int(s.At.Seconds())), strconv.Itoa(s.Goroutines), strconv.Itoa(s.Threads), strconv.Itoa(s.Handles),
			strconv.FormatFloat(s.HeapMiB, 'f', 1, 64), strconv.FormatFloat(s.CPUSeconds, 'f', 2, 64),
			strconv.Itoa(s.Active), strconv.FormatBool(s.Probed), strconv.FormatBool(s.ExitOK),
		})
	}
	w.Flush()
}
