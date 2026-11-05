package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// thresholds are the pass rules of handoff_fix.md section 6.1. They are flags, because the
// handoff allows them to be calibrated against real nodes; any calibration must be recorded
// with the run (the report carries the values that were used).
type thresholds struct {
	ContentionRatio float64       // median throughput during contention / baseline, at least
	RecoverRatio    float64       // after contention ends, the 3 s mean must reach this share of the baseline ...
	RecoverWithin   time.Duration // ... within this long
	PlateauRatio    float64       // a window below this share of the baseline counts as "low" ...
	PlateauFor      time.Duration // ... and this long a run of low windows is a plateau
}

func defaultThresholds() thresholds {
	return thresholds{ContentionRatio: 0.70, RecoverRatio: 0.90, RecoverWithin: 10 * time.Second, PlateauRatio: 0.50, PlateauFor: 3 * time.Minute}
}

// window is one CSV row: a fixed-length slice of the run.
type window struct {
	Start      time.Duration // from the start of the run
	Length     time.Duration
	Bytes      int64
	Contention bool // burners were running for the whole window
	Warmup     bool
}

func (w window) mbps() float64 {
	if w.Length <= 0 {
		return 0
	}
	return float64(w.Bytes) * 8 / w.Length.Seconds() / 1e6
}

// verdict is the machine-readable outcome. Status is PASS, FAIL or BLOCKED (the run cannot be
// judged: too short, nothing downloaded, or no contention phase inside it).
type verdict struct {
	Status            string   `json:"status"`
	Reasons           []string `json:"reasons"`
	BaselineMbps      float64  `json:"baseline_mbps"`
	ContentionMbps    float64  `json:"contention_mbps"`
	ContentionRatio   float64  `json:"contention_ratio"`
	RecoverySeconds   float64  `json:"recovery_seconds"` // -1: never recovered inside the run
	LongestLowSeconds float64  `json:"longest_low_seconds"`
	LowestWindowMbps  float64  `json:"lowest_window_mbps"`
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// judge applies section 6.1 to a run. windows are the 10 s rows; perSecond are the bytes of
// every second of the run (index = second since start); contentionEnd is when the burners were
// removed (zero if the run had no contention phase).
func judge(windows []window, perSecond []int64, contentionEnd time.Duration, t thresholds) verdict {
	v := verdict{Status: "PASS", RecoverySeconds: -1}
	var idle, busy []float64
	for _, w := range windows {
		if w.Warmup {
			continue
		}
		if w.Contention {
			busy = append(busy, w.mbps())
		} else if w.Start+w.Length <= contentionEnd || contentionEnd == 0 {
			// The baseline is the idle part before contention: after it the path may still be
			// recovering, and a plateau there must not lower the bar it is judged against.
			idle = append(idle, w.mbps())
		}
	}
	if len(idle) < 3 {
		return verdict{Status: "BLOCKED", Reasons: []string{fmt.Sprintf("only %d idle windows before the contention phase; at least 3 are needed for a baseline", len(idle))}, RecoverySeconds: -1}
	}
	v.BaselineMbps = median(idle)
	if v.BaselineMbps <= 0 {
		return verdict{Status: "BLOCKED", Reasons: []string{"nothing was downloaded before the contention phase (no baseline): check the URL and that the tunnel is connected"}, RecoverySeconds: -1}
	}
	if contentionEnd == 0 {
		// A long watch without contention (R3-03): only the plateau rule applies.
		v.Reasons = append(v.Reasons, "no contention phase in this run: only the plateau rule was judged")
	} else if len(busy) == 0 {
		v.Status = "BLOCKED"
		v.Reasons = append(v.Reasons, "the run has no full window under contention; make it longer or move the contention phase")
	} else {
		v.ContentionMbps = median(busy)
		v.ContentionRatio = v.ContentionMbps / v.BaselineMbps
		if v.ContentionRatio < t.ContentionRatio {
			v.Status = "FAIL"
			v.Reasons = append(v.Reasons, fmt.Sprintf("under contention the median was %.0f%% of the baseline (rule: at least %.0f%%)", v.ContentionRatio*100, t.ContentionRatio*100))
		}
		v.RecoverySeconds = recovery(perSecond, contentionEnd, v.BaselineMbps, t.RecoverRatio)
		switch {
		case v.RecoverySeconds < 0:
			v.Status = "FAIL"
			v.Reasons = append(v.Reasons, fmt.Sprintf("never got back to %.0f%% of the baseline after the contention phase", t.RecoverRatio*100))
		case v.RecoverySeconds > t.RecoverWithin.Seconds():
			v.Status = "FAIL"
			v.Reasons = append(v.Reasons, fmt.Sprintf("took %.0f s to get back to %.0f%% of the baseline (rule: within %.0f s)", v.RecoverySeconds, t.RecoverRatio*100, t.RecoverWithin.Seconds()))
		}
	}
	// Plateau: the longest run of consecutive low windows anywhere after warm-up, contention included.
	var run time.Duration
	v.LowestWindowMbps = math.Inf(1)
	for _, w := range windows {
		if w.Warmup {
			continue
		}
		v.LowestWindowMbps = math.Min(v.LowestWindowMbps, w.mbps())
		if w.mbps() < v.BaselineMbps*t.PlateauRatio {
			run += w.Length
			v.LongestLowSeconds = math.Max(v.LongestLowSeconds, run.Seconds())
		} else {
			run = 0
		}
	}
	if math.IsInf(v.LowestWindowMbps, 1) {
		v.LowestWindowMbps = 0
	}
	if v.LongestLowSeconds >= t.PlateauFor.Seconds() {
		v.Status = "FAIL"
		v.Reasons = append(v.Reasons, fmt.Sprintf("%.0f s in a row below %.0f%% of the baseline (rule: no plateau of %.0f s or more)", v.LongestLowSeconds, t.PlateauRatio*100, t.PlateauFor.Seconds()))
	}
	if v.Status == "PASS" {
		v.Reasons = append(v.Reasons, "all section 6.1 rules that apply hold")
	}
	return v
}

// recovery returns how many seconds after end the 3-second mean first reached ratio x baseline,
// or -1. Three seconds smooths the per-second noise of a real path without hiding a slow ramp.
func recovery(perSecond []int64, end time.Duration, baselineMbps, ratio float64) float64 {
	const span = 3
	start := int(end / time.Second)
	target := baselineMbps * ratio
	for s := start; s+span <= len(perSecond); s++ {
		var sum int64
		for _, b := range perSecond[s : s+span] {
			sum += b
		}
		if float64(sum)*8/span/1e6 >= target {
			return float64(s - start)
		}
	}
	return -1
}
