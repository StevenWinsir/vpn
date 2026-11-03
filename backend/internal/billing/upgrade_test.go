package billing

import (
	"math"
	"testing"
	"time"

	"vpn/backend/internal/model"
)

const testGiB = int64(1) << 30

func TestUpgradeDaysRounding(t *testing.T) {
	now := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		left              time.Duration
		charged, credited int
	}{
		{19*24*time.Hour + 14*time.Hour, 20, 19},
		{24 * time.Hour, 1, 1},
		{20 * time.Hour, 1, 0},
		{time.Nanosecond, 1, 0},
		{0, 0, 0},
		{-time.Hour, 0, 0},
		{35 * 24 * time.Hour, 35, 35},
	} {
		charged, credited := UpgradeDays(now, now.Add(c.left))
		if charged != c.charged || credited != c.credited {
			t.Fatalf("%v: got %d/%d want %d/%d", c.left, charged, credited, c.charged, c.credited)
		}
	}
}

func TestProrateAndTrafficRounding(t *testing.T) {
	if v, ok := ProrateUp(Rate{3990, 30}, 20); !ok || v != 2660 {
		t.Fatalf("value %d", v)
	}
	if v, ok := ProrateUp(Rate{7990, 30}, 10); !ok || v != 2664 { // 2663.33 rounds up
		t.Fatalf("value %d", v)
	}
	if v, ok := ProrateUp(Rate{43092, 365}, 69); !ok || v != 8147 {
		t.Fatalf("yearly value %d", v)
	}
	// 300 GiB x 20/30 = 200 GiB exactly; 100 GiB x 20/30 rounds up to the byte.
	if add, ok := TrafficAdd(Rate{300 * testGiB, 30}, Rate{100 * testGiB, 30}, 20); !ok || add != 214748364800-71582788267 {
		t.Fatalf("traffic %d", add)
	}
	// A target below the holding never takes traffic away.
	if add, ok := TrafficAdd(Rate{100 * testGiB, 30}, Rate{300 * testGiB, 30}, 20); !ok || add != 0 {
		t.Fatalf("negative traffic %d", add)
	}
	// Overflow is refused, not wrapped.
	if _, ok := ProrateUp(Rate{math.MaxInt64, 1}, 3); ok {
		t.Fatal("overflow accepted")
	}
	if _, ok := TrafficAdd(Rate{1 << 50, 1}, Rate{0, 1}, 1<<20); ok {
		t.Fatal("traffic overflow accepted")
	}
	if _, ok := ProrateUp(Rate{100, 0}, 3); ok {
		t.Fatal("zero-day rate accepted")
	}
}

func TestUpgradeCreditWindows(t *testing.T) {
	cst := time.FixedZone("UTC+8", 8*3600)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, cst)
	expires := start.AddDate(0, 0, 30)
	starter := PaidWindow{OrderID: "a", Cents: 1990, From: start, To: expires}
	// Example 1: 19 credited days of a 30-day order.
	if k, parts := UpgradeCredit([]PaidWindow{starter}, expires, 19); k != 1260 || len(parts) != 1 || parts[0].Cents != 1260 {
		t.Fatalf("example 1 credit %d %+v", k, parts)
	}
	// Example 2: plus the first upgrade's window (19 d 14 h) for 9 days.
	upgradedAt := time.Date(2026, 11, 11, 10, 0, 0, 0, cst)
	upgrade := PaidWindow{OrderID: "b", Cents: 1400, From: upgradedAt, To: expires}
	if k, _ := UpgradeCredit([]PaidWindow{starter, upgrade}, expires, 9); k != 597+643 {
		t.Fatalf("example 2 credit %d", k)
	}
	// No credited days, no credit; a window wholly in the past gives nothing.
	if k, _ := UpgradeCredit([]PaidWindow{starter}, expires, 0); k != 0 {
		t.Fatalf("zero days %d", k)
	}
	past := PaidWindow{OrderID: "c", Cents: 5000, From: start.AddDate(0, -2, 0), To: start.AddDate(0, -1, 0)}
	if k, _ := UpgradeCredit([]PaidWindow{past}, expires, 30); k != 0 {
		t.Fatalf("past window %d", k)
	}
	// A shortened subscription clips the window.
	if k, _ := UpgradeCredit([]PaidWindow{starter}, expires.AddDate(0, 0, -10), 10); k != 663 {
		t.Fatalf("clipped %d", k)
	}
}

func TestUpgradeChargeMinimum(t *testing.T) {
	if a, applied := UpgradeCharge(2660, 1260, 100); a != 1400 || applied {
		t.Fatalf("%d %v", a, applied)
	}
	if a, applied := UpgradeCharge(500, 450, 100); a != 100 || !applied {
		t.Fatalf("%d %v", a, applied)
	}
	if a, applied := UpgradeCharge(500, 900, 100); a != 100 || !applied {
		t.Fatalf("negative %d %v", a, applied)
	}
}

func TestUpgradeRelation(t *testing.T) {
	starter := Holding{PriceCents: 1990, Traffic: Rate{100 * testGiB, 30}, MaxDevices: 2}
	target := func(price, traffic int64, devices int, dedicated bool) Target {
		return Target{PriceCents: price, Traffic: Rate{traffic * testGiB, 30}, MaxDevices: devices, AllowDedicated: dedicated}
	}
	for name, c := range map[string]struct {
		h    Holding
		t    Target
		want string
	}{
		"pro":              {starter, target(3990, 300, 5, true), RelationUpgrade},
		"cheaper":          {starter, target(990, 300, 5, true), RelationDowngrade},
		"same price":       {starter, target(1990, 300, 5, true), RelationLateral},
		"less traffic":     {starter, target(3990, 50, 5, true), RelationLateral},
		"fewer devices":    {starter, target(3990, 300, 1, true), RelationLateral},
		"loses dedicated":  {Holding{PriceCents: 3990, Traffic: Rate{300 * testGiB, 30}, MaxDevices: 5, AllowDedicated: true}, target(7990, 800, 10, false), RelationLateral},
		"equal traffic ok": {starter, target(2990, 100, 2, false), RelationUpgrade},
		// Per day: 100 GiB / 30 d against 1000 GiB / 365 d (less per day).
		"longer term":  {starter, Target{PriceCents: 2990, Traffic: Rate{1000 * testGiB, 365}, MaxDevices: 2}, RelationLateral},
		"invalid rate": {starter, Target{PriceCents: 2990, Traffic: Rate{100 * testGiB, 0}, MaxDevices: 2}, RelationLateral},
	} {
		if got := UpgradeRelation(c.h, c.t); got != c.want {
			t.Fatalf("%s: %s want %s", name, got, c.want)
		}
	}
}

func TestGroupVisibleTo(t *testing.T) {
	for _, c := range []struct {
		g         model.NodeGroup
		plan      string
		dedicated bool
		want      bool
	}{
		{model.NodeGroup{PlanIDs: `[]`, Enabled: true, LineType: "direct"}, "starter", false, true},
		{model.NodeGroup{PlanIDs: `["starter"]`, Enabled: true, LineType: "direct"}, "pro", true, false},
		{model.NodeGroup{PlanIDs: `[]`, Enabled: true, LineType: "dedicated"}, "starter", false, false},
		{model.NodeGroup{PlanIDs: `[]`, Enabled: false, LineType: "direct"}, "starter", false, false},
	} {
		if got, err := GroupVisibleTo(c.g, c.plan, c.dedicated); err != nil || got != c.want {
			t.Fatalf("%+v %s: %v %v", c.g, c.plan, got, err)
		}
	}
	if _, err := GroupVisibleTo(model.NodeGroup{PlanIDs: `{`}, "starter", false); err == nil {
		t.Fatal("invalid plan list accepted")
	}
}
