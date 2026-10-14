package main

import (
	"fmt"
	"math"
)

// segment is one connect -> transfer -> disconnect measurement, taken entirely from the
// Core's own snapshots (the same numbers the App shows) and from the sink's independent
// byte counts. All values are integers; nothing is derived through floating point except
// the reported overhead ratio, which is informational.
type segment struct {
	RatePermille int64

	// Server-confirmed cumulative proxy bytes and used units, before Connect and after the
	// final acknowledgement.
	UploadBefore, UploadAfter     int64
	DownloadBefore, DownloadAfter int64
	UnitsBefore, UnitsAfter       int64
	TotalBytes, RemainingAfter    int64

	// Bytes of body the controlled transfer moved (what the child process saw, and what the
	// sink saw, which must agree).
	UploadBody, DownloadBody int64
}

func (s segment) deltaUp() int64    { return s.UploadAfter - s.UploadBefore }
func (s segment) deltaDown() int64  { return s.DownloadAfter - s.DownloadBefore }
func (s segment) deltaUnits() int64 { return s.UnitsAfter - s.UnitsBefore }

// expectedUnits is the contract formula: (up_delta + down_delta) * rate_permille, with
// overflow reported instead of wrapped.
func expectedUnits(up, down, rate int64) (int64, bool) {
	if up < 0 || down < 0 || rate < 0 {
		return 0, false
	}
	sum := up + down
	if sum < up || (rate != 0 && sum > math.MaxInt64/rate) {
		return 0, false
	}
	return sum * rate, true
}

// remainingBytes is the contract formula floor((total*1000 - used) / 1000), floored at 0.
func remainingBytes(total, used int64) int64 {
	if total > math.MaxInt64/1000 {
		return 0
	}
	return max(0, (total*1000-used)/1000)
}

// ledgerProblems checks the ledger against itself: the server charged exactly what its own
// confirmed counter delta times the bound rate says, and the remaining balance follows.
func ledgerProblems(s segment) []string {
	var out []string
	want, ok := expectedUnits(s.deltaUp(), s.deltaDown(), s.RatePermille)
	switch {
	case !ok:
		out = append(out, fmt.Sprintf("counter delta not representable (up %d, down %d, rate %d)", s.deltaUp(), s.deltaDown(), s.RatePermille))
	case s.deltaUnits() != want:
		out = append(out, fmt.Sprintf("charged %d units, formula (up %d + down %d) x %d = %d", s.deltaUnits(), s.deltaUp(), s.deltaDown(), s.RatePermille, want))
	}
	if got := remainingBytes(s.TotalBytes, s.UnitsAfter); got != s.RemainingAfter {
		out = append(out, fmt.Sprintf("remaining %d, formula gives %d", s.RemainingAfter, got))
	}
	return out
}

// bodyProblems compares the counted proxy bytes with the known body sizes. Counters are the
// payload that crossed the node, so they must cover the body and exceed it only by protocol
// framing plus whatever the rest of the machine sent through the tunnel meanwhile
// (background, per direction).
func bodyProblems(s segment, background int64) []string {
	var out []string
	check := func(name string, counted, body int64) {
		limit := body + body/10 + background
		switch {
		case counted < body:
			out = append(out, fmt.Sprintf("%s counted %d is below the %d byte body", name, counted, body))
		case counted > limit:
			out = append(out, fmt.Sprintf("%s counted %d exceeds body %d by more than 10%% + %d", name, counted, body, background))
		}
	}
	check("upload", s.deltaUp(), s.UploadBody)
	check("download", s.deltaDown(), s.DownloadBody)
	return out
}

func overhead(counted, body int64) float64 {
	if body <= 0 {
		return 0
	}
	return float64(counted)/float64(body) - 1
}

// quotaUnits converts units to bytes of plan quota for the report (1000 units = 1 byte).
func quotaBytes(units int64) int64 { return units / 1000 }
