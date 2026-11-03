package billing

import (
	"encoding/json"
	"errors"
	"math"
	"math/bits"
	"slices"
	"time"

	"vpn/backend/internal/model"
)

// Mid-period upgrades (handoff_plans.md 5, 6): the subscription keeps its period and
// expiry; the customer pays the new plan's list price for the remaining days less what
// is left of the money actually paid for them, and gains the per-day traffic difference.
// Every rounding goes the merchant's way. Everything here is pure integer arithmetic;
// the API reads the rows and stores the result.

// Relations of a plan to the current subscription.
const (
	RelationNew       = "new"
	RelationCurrent   = "current"
	RelationUpgrade   = "upgrade"
	RelationDowngrade = "downgrade"
	RelationLateral   = "lateral"
)

const upgradeDay = 24 * time.Hour

// Rate is Amount (cents or bytes) per Days days.
type Rate struct {
	Amount int64 `json:"amount"`
	Days   int   `json:"days"`
}

// Valid: a non-negative amount over at least one day.
func (r Rate) Valid() bool { return r.Amount >= 0 && r.Days > 0 }

// AtLeast compares two rates per day exactly (cross-multiplied in 128 bits).
func (r Rate) AtLeast(o Rate) bool {
	return cmpProducts(uint64(r.Amount), uint64(o.Days), uint64(o.Amount), uint64(r.Days)) >= 0
}

// cmpProducts compares a*b with c*d.
func cmpProducts(a, b, c, d uint64) int {
	h1, l1 := bits.Mul64(a, b)
	h2, l2 := bits.Mul64(c, d)
	switch {
	case h1 != h2:
		return cmpUint(h1, h2)
	default:
		return cmpUint(l1, l2)
	}
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// mulDiv is a*b/c rounded down or up, or false when the result does not fit an int64.
func mulDiv(a, b, c int64, up bool) (int64, bool) {
	if a < 0 || b < 0 || c <= 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return 0, false
	}
	q, r := bits.Div64(hi, lo, uint64(c))
	if up && r > 0 {
		q++
	}
	if q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}

// UpgradeDays is the remaining time in whole days: charged rounds up, credited rounds
// down (D3). Both are 0 once the subscription has ended.
func UpgradeDays(now, expires time.Time) (charged, credited int) {
	left := expires.Sub(now)
	if left <= 0 {
		return 0, 0
	}
	credited = int(left / upgradeDay)
	charged = credited
	if left%upgradeDay != 0 {
		charged++
	}
	return charged, credited
}

// CeilDays is a duration in days rounded up (0 for none).
func CeilDays(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	days := int(d / upgradeDay)
	if d%upgradeDay != 0 {
		days++
	}
	return days
}

// ProrateUp is rate x days rounded up: the new plan's value for the charged days.
func ProrateUp(r Rate, days int) (int64, bool) {
	if !r.Valid() || days < 0 {
		return 0, false
	}
	return mulDiv(r.Amount, int64(days), int64(r.Days), true)
}

// TrafficAdd is what an upgrade grants for days: the new plan's traffic rounded down
// less the old plan's rounded up, never negative.
func TrafficAdd(to, from Rate, days int) (int64, bool) {
	if !to.Valid() || !from.Valid() || days < 0 {
		return 0, false
	}
	gain, ok1 := mulDiv(to.Amount, int64(days), int64(to.Days), false)
	loss, ok2 := mulDiv(from.Amount, int64(days), int64(from.Days), true)
	if !ok1 || !ok2 {
		return 0, false
	}
	return max(0, gain-loss), true
}

// PaidWindow is one paid order of the current period: the cents it brought in and the
// time its value covers.
type PaidWindow struct {
	OrderID  string
	Cents    int64
	From, To time.Time
}

// Contribution is one order's share of the credit.
type Contribution struct {
	OrderID string `json:"order_id"`
	Cents   int64  `json:"cents"`
}

// UpgradeCredit is what is left of the money paid for the period's last credited days:
// for every window, cents x (its overlap with [expires - credited days, expires]) / its
// length, rounded down (handoff_plans.md 6.4).
func UpgradeCredit(windows []PaidWindow, expires time.Time, credited int) (int64, []Contribution) {
	contributions := []Contribution{}
	if credited <= 0 {
		return 0, contributions
	}
	start := expires.Add(-time.Duration(credited) * upgradeDay)
	var total int64
	for _, w := range windows {
		length := w.To.Sub(w.From)
		if w.Cents <= 0 || length <= 0 {
			continue
		}
		from, to := w.From, w.To
		if from.Before(start) {
			from = start
		}
		if to.After(expires) {
			to = expires
		}
		overlap := to.Sub(from)
		if overlap <= 0 {
			continue
		}
		cents, ok := mulDiv(w.Cents, int64(overlap), int64(length), false)
		if !ok || cents <= 0 {
			continue
		}
		total += cents
		contributions = append(contributions, Contribution{OrderID: w.OrderID, Cents: cents})
	}
	return total, contributions
}

// UpgradeCharge is the payable amount: value less credit, never below the minimum.
func UpgradeCharge(value, credit, minimum int64) (amount int64, minimumApplied bool) {
	if value-credit < minimum {
		return minimum, true
	}
	return value - credit, false
}

// Holding is what the customer holds now; Target is the plan offered. Traffic is per
// day for a one-shot allowance and per cycle for a refreshing one (Days = cycle days).
type Holding struct {
	PriceCents     int64
	Traffic        Rate
	MaxDevices     int
	AllowDedicated bool
}

type Target struct {
	PriceCents     int64
	Traffic        Rate
	MaxDevices     int
	AllowDedicated bool
}

// UpgradeRelation is D6: a strictly higher monthly price with no lower traffic, devices
// or line access is an upgrade; a lower price a downgrade; anything else lateral.
func UpgradeRelation(h Holding, t Target) string {
	switch {
	case t.PriceCents < h.PriceCents:
		return RelationDowngrade
	case t.PriceCents > h.PriceCents && t.Traffic.Valid() && h.Traffic.Valid() && t.Traffic.AtLeast(h.Traffic) &&
		t.MaxDevices >= h.MaxDevices && (t.AllowDedicated || !h.AllowDedicated):
		return RelationUpgrade
	}
	return RelationLateral
}

// GroupVisibleTo reports whether an enabled line group is open to a plan with or without
// dedicated access (the rule of the node catalogue: an empty plan list means every plan).
func GroupVisibleTo(g model.NodeGroup, planID string, dedicated bool) (bool, error) {
	var plans []string
	if json.Unmarshal([]byte(g.PlanIDs), &plans) != nil {
		return false, errors.New("invalid group plan list")
	}
	return g.Enabled && !(g.LineType == "dedicated" && !dedicated) && (len(plans) == 0 || slices.Contains(plans, planID)), nil
}
