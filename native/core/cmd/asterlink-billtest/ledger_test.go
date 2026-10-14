package main

import (
	"math"
	"strings"
	"testing"
)

const mib = 1 << 20

func TestExpectedUnitsMatchesTheContractExamples(t *testing.T) {
	// 20 MiB up + 80 MiB down = 104,857,600 bytes (handoff section 8.1).
	for rate, want := range map[int64]int64{1000: 104_857_600_000, 500: 52_428_800_000} {
		got, ok := expectedUnits(20*mib, 80*mib, rate)
		if !ok || got != want {
			t.Fatalf("rate %d: got %d ok=%v, want %d", rate, got, ok, want)
		}
		if quotaBytes(got) != want/1000 {
			t.Fatalf("quota bytes %d", quotaBytes(got))
		}
	}
	if _, ok := expectedUnits(math.MaxInt64/2, math.MaxInt64/2, 1000); ok {
		t.Fatal("overflow was not reported")
	}
	if _, ok := expectedUnits(-1, 0, 1000); ok {
		t.Fatal("negative counters accepted")
	}
}

func TestRemainingFollowsTheFloorFormula(t *testing.T) {
	for _, c := range []struct{ total, used, want int64 }{
		{100, 0, 100}, {100, 99_999, 0}, {100, 100_000, 0}, {100, 100_001, 0}, {100, 50_500, 49}, {100, 50_000, 50}, {math.MaxInt64/1000 + 1, 0, 0},
	} {
		if got := remainingBytes(c.total, c.used); got != c.want {
			t.Fatalf("remaining(%d,%d) = %d, want %d", c.total, c.used, got, c.want)
		}
	}
}

func goodSegment(rate int64) segment {
	s := segment{RatePermille: rate, UploadBefore: 5 * mib, DownloadBefore: 9 * mib, UnitsBefore: 7_000_000, TotalBytes: 10 << 30,
		UploadBody: 20 * mib, DownloadBody: 80 * mib}
	s.UploadAfter, s.DownloadAfter = s.UploadBefore+20*mib+4096, s.DownloadBefore+80*mib+9000
	s.UnitsAfter = s.UnitsBefore + (s.deltaUp()+s.deltaDown())*rate
	s.RemainingAfter = remainingBytes(s.TotalBytes, s.UnitsAfter)
	return s
}

func TestLedgerAcceptsAnExactChargeAtBothRates(t *testing.T) {
	for _, rate := range []int64{1000, 500, 1500, 0} {
		if p := ledgerProblems(goodSegment(rate)); len(p) != 0 {
			t.Fatalf("rate %d: %v", rate, p)
		}
	}
}

func TestLedgerRejectsEveryWayOfBeingOneUnitOff(t *testing.T) {
	for name, mutate := range map[string]func(*segment){
		"double charge":       func(s *segment) { s.UnitsAfter += (s.deltaUp() + s.deltaDown()) * s.RatePermille },
		"one unit over":       func(s *segment) { s.UnitsAfter++ },
		"one unit under":      func(s *segment) { s.UnitsAfter-- },
		"wrong rate applied":  func(s *segment) { s.UnitsAfter = s.UnitsBefore + (s.deltaUp()+s.deltaDown())*(s.RatePermille/2) },
		"download only":       func(s *segment) { s.UnitsAfter = s.UnitsBefore + s.deltaDown()*s.RatePermille },
		"counter went back":   func(s *segment) { s.DownloadAfter = s.DownloadBefore - 1 },
		"remaining not floor": func(s *segment) { s.RemainingAfter++ },
	} {
		s := goodSegment(1000)
		mutate(&s)
		if len(ledgerProblems(s)) == 0 {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestBodyCheckBoundsFramingAndBackground(t *testing.T) {
	background := int64(2048 << 10)
	if p := bodyProblems(goodSegment(1000), background); len(p) != 0 {
		t.Fatal(p)
	}
	short := goodSegment(1000)
	short.DownloadAfter = short.DownloadBefore + 80*mib - 1
	if p := bodyProblems(short, background); len(p) != 1 || !strings.Contains(p[0], "below") {
		t.Fatalf("an undercount was not caught: %v", p)
	}
	loud := goodSegment(1000)
	loud.UploadAfter = loud.UploadBefore + 40*mib
	if p := bodyProblems(loud, background); len(p) != 1 || !strings.Contains(p[0], "exceeds") {
		t.Fatalf("a double count was not caught: %v", p)
	}
}

func TestOverheadIsInformationalAndSafeOnEmptyBodies(t *testing.T) {
	if overhead(10, 0) != 0 {
		t.Fatal("division by zero not guarded")
	}
	if got := overhead(110, 100); math.Abs(got-0.1) > 1e-9 {
		t.Fatal(got)
	}
}
