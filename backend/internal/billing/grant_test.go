package billing

import (
	"errors"
	"math"
	"testing"
)

func TestApplyUsage(t *testing.T) {
	cur := UsageCursor{Epoch: 2, Sequence: 3, Upload: 100, Download: 200}
	cases := []struct {
		name     string
		in       UsageReport
		kind     UsageKind
		up, down int64
		err      error
	}{
		{"next", UsageReport{2, 4, 150, 270}, UsageNext, 50, 70, nil},
		{"next-no-traffic", UsageReport{2, 4, 100, 200}, UsageNext, 0, 0, nil},
		{"replay-same", UsageReport{2, 3, 100, 200}, UsageReplay, 0, 0, nil},
		{"replay-older", UsageReport{2, 1, 1, 1}, UsageReplay, 0, 0, nil},
		{"gap", UsageReport{2, 5, 150, 270}, 0, 0, 0, ErrUsageSequence},
		{"regress-up", UsageReport{2, 4, 99, 200}, 0, 0, 0, ErrUsageRegress},
		{"regress-down", UsageReport{2, 4, 100, 199}, 0, 0, 0, ErrUsageRegress},
		{"stale-epoch", UsageReport{1, 9, 1, 1}, 0, 0, 0, ErrUsageStale},
		{"new-epoch", UsageReport{3, 1, 40, 60}, UsageNewEpoch, 40, 60, nil},
		{"new-epoch-bad-sequence", UsageReport{3, 2, 40, 60}, 0, 0, 0, ErrUsageSequence},
		{"invalid-negative", UsageReport{2, 4, -1, 0}, 0, 0, 0, ErrUsageInvalid},
		{"invalid-ceiling", UsageReport{2, 4, MaxClientCounter + 1, 0}, 0, 0, 0, ErrUsageInvalid},
		{"invalid-zero-seq", UsageReport{2, 0, 0, 0}, 0, 0, 0, ErrUsageInvalid},
		{"invalid-zero-epoch", UsageReport{0, 1, 0, 0}, 0, 0, 0, ErrUsageInvalid},
	}
	for _, tc := range cases {
		kind, up, down, err := ApplyUsage(cur, tc.in)
		if !errors.Is(err, tc.err) || err == nil && (kind != tc.kind || up != tc.up || down != tc.down) {
			t.Errorf("%s: got kind=%d up=%d down=%d err=%v", tc.name, kind, up, down, err)
		}
	}
	// A brand-new grant (cursor zero) accepts epoch >= 1, sequence 1 only.
	if kind, up, down, err := ApplyUsage(UsageCursor{}, UsageReport{1, 1, 5, 6}); err != nil || kind != UsageNewEpoch || up != 5 || down != 6 {
		t.Fatalf("first report: %d %d %d %v", kind, up, down, err)
	}
	if _, _, _, err := ApplyUsage(UsageCursor{}, UsageReport{1, 2, 5, 6}); !errors.Is(err, ErrUsageSequence) {
		t.Fatal("first report must be sequence 1")
	}
}

func TestChargeAndBudget(t *testing.T) {
	// 20 MiB up + 80 MiB down at 1x = 104,857,600,000 units; at 0.5x half of that.
	const mib = int64(1 << 20)
	for _, tc := range []struct{ rate, units int64 }{{1000, 104857600000}, {500, 52428800000}} {
		bytes, units, err := Charge(0, 0, 20*mib, 80*mib, tc.rate)
		if err != nil || bytes != 100*mib || units != tc.units {
			t.Fatalf("rate %d: %d %d %v", tc.rate, bytes, units, err)
		}
	}
	if _, _, err := Charge(math.MaxInt64-1, 0, 1, 1, 1000); err == nil {
		t.Fatal("raw-byte overflow accepted")
	}
	if _, _, err := Charge(0, math.MaxInt64-1, 1, 1, 1000); err == nil {
		t.Fatal("unit overflow accepted")
	}
	if _, _, err := Charge(0, 0, math.MaxInt64, 1, 1000); err == nil {
		t.Fatal("up+down overflow accepted")
	}
	// A pool of 10 MiB at 0.5x can fund 20 MiB of raw traffic, capped by the chunk.
	if got := BudgetBytes(10*mib*1000, 500, 512*mib); got != 20*mib {
		t.Fatalf("budget %d", got)
	}
	if got := BudgetBytes(10*mib*1000, 500, 8*mib); got != 8*mib {
		t.Fatalf("chunk cap %d", got)
	}
	if BudgetBytes(0, 500, mib) != 0 || BudgetBytes(-5, 500, mib) != 0 || BudgetBytes(5, 0, mib) != 0 {
		t.Fatal("empty pool must grant nothing")
	}
	if ReservedUnits(100, 40, 500, false) != 30000 || ReservedUnits(100, 140, 500, false) != 0 || ReservedUnits(100, 0, 500, true) != 0 {
		t.Fatal("reservation arithmetic")
	}
	if AvailableUnits(10, 4000, 3000) != 3000 || AvailableUnits(10, 9000, 5000) != 0 || AvailableUnits(0, 0, 0) != 0 {
		t.Fatal("availability arithmetic")
	}
}
