package billing

import (
	"testing"
	"time"

	"vpn/backend/internal/model"
)

const gib = int64(1) << 30

// refreshing is a yearly subscription in its first 30-day cycle (100 GiB per cycle).
func refreshing(start time.Time) *model.Subscription {
	anchor := start
	return &model.Subscription{StartsAt: start, ExpiresAt: start.AddDate(1, 0, 0), TrafficLimitBytes: 100 * gib,
		QuotaCycleDays: 30, QuotaCycleBytes: 100 * gib, QuotaCycleStartsAt: &anchor}
}

// R1-07 (handoff_coupon.md 9): a reward survives the 30-day refresh and is spent last.
func TestBonusSurvivesRefresh(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cycle := 30 * 24 * time.Hour
	for _, tc := range []struct {
		name     string
		usedGiB  int64
		wantKeep int64
	}{
		{"cycle allowance left over", 40, 29 * gib},
		{"cycle allowance used up, bonus untouched", 100, 29 * gib},
		{"part of the bonus used", 110, 19 * gib},
		{"everything used", 129, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := refreshing(start)
			AddBonus(s, 29*gib)
			if s.TrafficLimitBytes != 129*gib || s.BonusBytes != 29*gib || !s.BonusStartsAt.Equal(start) || CurrentBonus(s) != 29*gib {
				t.Fatalf("grant %+v", s)
			}
			s.UsedUnits = tc.usedGiB * gib * 1000
			if !RollQuotaCycle(s, start.Add(cycle+time.Hour)) {
				t.Fatal("no refresh")
			}
			if s.TrafficLimitBytes != 100*gib+tc.wantKeep || s.BonusBytes != tc.wantKeep || s.UsedUnits != 0 {
				t.Fatalf("after refresh: limit %d bonus %d used %d", s.TrafficLimitBytes, s.BonusBytes, s.UsedUnits)
			}
		})
	}
	// Several cycles skipped at once count as one refresh (nothing was used meanwhile).
	s := refreshing(start)
	AddBonus(s, 29*gib)
	s.UsedUnits = 50 * gib * 1000
	RollQuotaCycle(s, start.Add(3*cycle+time.Hour))
	if s.TrafficLimitBytes != 129*gib || s.BonusBytes != 29*gib || !s.QuotaCycleStartsAt.Equal(start.Add(3*cycle)) {
		t.Fatalf("skipped cycles %+v", s)
	}
	// The bonus then wears down over later cycles.
	s.UsedUnits = 120 * gib * 1000
	RollQuotaCycle(s, start.Add(4*cycle+time.Hour))
	if s.TrafficLimitBytes != 109*gib || s.BonusBytes != 9*gib {
		t.Fatalf("second refresh %+v", s)
	}
	// Fractional usage rounds the remaining allowance down to the byte.
	s = refreshing(start)
	AddBonus(s, 10)
	s.UsedUnits = 100*gib*1000 + 2500
	RollQuotaCycle(s, start.Add(cycle))
	if s.BonusBytes != 7 {
		t.Fatalf("fractional remainder %d", s.BonusBytes)
	}
}

// A bonus anchored to another period (an older binary started a new one without clearing
// it) is no bonus: the refresh behaves exactly as before, and a new grant re-anchors.
func TestBonusOfAnotherPeriod(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	old := start.AddDate(0, -2, 0)
	s := refreshing(start)
	s.BonusBytes, s.BonusStartsAt = 29*gib, &old
	if BonusActive(s) || CurrentBonus(s) != 0 {
		t.Fatal("a stale bonus counts")
	}
	RollQuotaCycle(s, start.Add(31*24*time.Hour))
	if s.TrafficLimitBytes != 100*gib || s.BonusBytes != 0 {
		t.Fatalf("stale bonus kept %+v", s)
	}
	s.BonusBytes, s.BonusStartsAt = 29*gib, &old
	AddBonus(s, 5*gib)
	if s.BonusBytes != 5*gib || !s.BonusStartsAt.Equal(s.StartsAt) || s.TrafficLimitBytes != 105*gib {
		t.Fatalf("re-anchored grant %+v", s)
	}
	// A one-shot (monthly) allowance is never reset, so the bonus is display only.
	m := &model.Subscription{StartsAt: start, ExpiresAt: start.AddDate(0, 0, 30), TrafficLimitBytes: 100 * gib}
	AddBonus(m, 29*gib)
	if RollQuotaCycle(m, start.AddDate(0, 0, 29)) || m.TrafficLimitBytes != 129*gib || CurrentBonus(m) != 29*gib {
		t.Fatalf("monthly %+v", m)
	}
	if CurrentBonus(nil) != 0 || BonusActive(nil) {
		t.Fatal("nil subscription")
	}
}
