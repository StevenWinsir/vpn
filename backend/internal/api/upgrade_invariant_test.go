package api

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/model"
)

// R1-02 (handoff_plans.md 6.8): random histories of purchases, renewals (monthly and
// yearly, discounted), administrator extensions and cuts, orders that lost their value
// window (an older binary fulfilled them) and chained upgrades. Every upgrade the code
// offers is checked against an independent exact model of the money paid:
//
//   - the credit never exceeds what is really left of the money paid for the period;
//   - the payable amount plus that real residual covers the new plan's list value for
//     the exact remaining time (the merchant never loses);
//   - with exact windows the credit is short by at most one day per order;
//   - the traffic added is never negative nor more than the exact per-day difference;
//   - fulfilment keeps the period and expiry and grants exactly the quoted traffic.
//
// UPGRADE_TRAJECTORIES overrides the number of histories (default 2000).

type truthWindow struct {
	from, to time.Time
	cents    int64
	paidAt   time.Time
	exact    bool
}

func ratNs(d time.Duration) *big.Rat { return new(big.Rat).SetInt64(int64(d)) }

// exactShare is cents x overlap([from,to], [lo,hi]) / (to - from), exactly.
func exactShare(w truthWindow, lo, hi time.Time) *big.Rat {
	from, to := w.from, w.to
	if from.Before(lo) {
		from = lo
	}
	if to.After(hi) {
		to = hi
	}
	if !to.After(from) || !w.to.After(w.from) {
		return new(big.Rat)
	}
	r := new(big.Rat).Mul(new(big.Rat).SetInt64(w.cents), ratNs(to.Sub(from)))
	return r.Quo(r, ratNs(w.to.Sub(w.from)))
}

func TestUpgradeInvariantTrajectories(t *testing.T) {
	trajectories := 2000
	if v, err := strconv.Atoi(os.Getenv("UPGRADE_TRAJECTORIES")); err == nil && v > 0 {
		trajectories = v
	}
	e := newPayEnv(t, nil)
	var plans []model.Plan
	if err := e.db.Order("price_cents").Find(&plans, "id IN ?", []string{"starter", "pro", "max"}).Error; err != nil || len(plans) != 3 {
		t.Fatalf("seed plans %v", err)
	}
	day := 24 * time.Hour
	checked, minimums, legacy, chained := 0, 0, 0, 0
	for i := range trajectories {
		rng := rand.New(rand.NewPCG(uint64(i), 0x9e3779b97f4a7c15))
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rng.Int64N(int64(300 * day)))).Truncate(time.Second)
		srv := &Server{db: e.db, cfg: e.cfg, clock: func() time.Time { return now }}
		user := model.User{ID: uuid.NewString(), Email: fmt.Sprintf("inv%d@example.com", i), Name: "inv", PasswordHash: payHash, Role: "user", Status: "active", AuthVersion: 1}
		if err := e.db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		truth := map[string]*truthWindow{}
		upgrades := 0
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("trajectory %d at %v: %s", i, now, fmt.Sprintf(format, args...))
		}
		fulfil := func(order *model.Order, paid int64) bool {
			ok := false
			err := e.db.Transaction(func(tx *gorm.DB) error {
				if _, err := lockUserRow(tx, user.ID); err != nil {
					return err
				}
				locked, err := lockOrder(tx, order.ID)
				if err != nil {
					return err
				}
				ok, err = srv.fulfilOrder(tx, locked, paid, now)
				return err
			})
			if err != nil {
				fail("fulfil: %v", err)
			}
			return ok
		}
		for range 3 + rng.IntN(8) {
			now = now.Add(time.Hour + time.Duration(rng.Int64N(int64(25*day)))).Truncate(time.Second)
			sub, err := readSubscription(e.db, user.ID, now)
			if err != nil {
				fail("%v", err)
			}
			active := sub != nil && now.Before(sub.ExpiresAt)
			switch roll := rng.IntN(10); {
			case !active || roll < 3:
				// A purchase: a new period, or a renewal of the plan held (monthly or yearly,
				// sometimes discounted).
				plan := plans[rng.IntN(3)]
				if active {
					for _, p := range plans {
						if p.ID == sub.PlanID {
							plan = p
						}
					}
				}
				cycle := model.CycleMonthly
				if rng.IntN(4) == 0 {
					cycle = model.CycleYearly
				}
				terms, err := termsFor(plan, cycle, model.DefaultYearlyDiscountPercent)
				if err != nil {
					fail("%v", err)
				}
				amount := max(1, terms.PriceCents*int64(50+rng.IntN(51))/100)
				order := model.Order{ID: uuid.NewString(), UserID: user.ID, IdempotencyKey: uuid.NewString(), PlanID: plan.ID, PlanName: plan.Name, PriceCents: amount, DurationDays: terms.DurationDays,
					TrafficBytes: terms.TrafficBytes, Status: model.OrderPending, Provider: "gateway", CreatedAt: now, ListPriceCents: terms.PriceCents, AmountCents: amount, PriceSource: priceList, Kind: model.OrderKindPurchase}
				snapshotCycle(&order, terms, plan)
				if err := e.db.Omit("User").Create(&order).Error; err != nil {
					fail("%v", err)
				}
				from := now
				if active {
					from = sub.ExpiresAt
				}
				if !fulfil(&order, amount) {
					fail("purchase refused")
				}
				after, _ := readSubscription(e.db, user.ID, now)
				truth[order.ID] = &truthWindow{from: from, to: after.ExpiresAt, cents: amount, paidAt: now, exact: true}
			case roll < 5:
				// An administrator extends or cuts the subscription.
				next := sub.ExpiresAt.Add(time.Duration(rng.IntN(31)-10) * day)
				if next.Before(now.Add(time.Hour)) {
					continue
				}
				if err := e.db.Model(&model.Subscription{}).Where("id = ?", sub.ID).Updates(map[string]any{"expires_at": next, "version": sub.Version + 1}).Error; err != nil {
					fail("%v", err)
				}
			case roll < 6:
				// An older binary fulfilled one of the orders: no value window.
				var ids []string
				e.db.Model(&model.Order{}).Where("user_id = ? AND status = ?", user.ID, model.OrderPaid).Order("paid_at, id").Pluck("id", &ids)
				if len(ids) > 0 {
					id := ids[rng.IntN(len(ids))]
					e.db.Model(&model.Order{}).Where("id = ?", id).Updates(map[string]any{"covers_from": nil, "covers_to": nil})
					truth[id].exact = false
				}
			default:
				target := plans[1+rng.IntN(2)]
				settings := upgradeSettings{Enabled: true, MinChargeCents: []int64{1, 100, 2000}[rng.IntN(3)]}
				if err := e.db.Model(&model.ShopSettings{}).Where("id = 1").Update("upgrade_min_charge_cents", settings.MinChargeCents).Error; err != nil {
					fail("%v", err)
				}
				ev, err := srv.evaluateUpgrade(e.db, user, sub, target, settings, model.DefaultYearlyDiscountPercent, now, false)
				if err != nil {
					fail("evaluate: %v", err)
				}
				if ev.Relation != billing.RelationUpgrade || ev.Problem != nil {
					continue
				}
				snap := ev.Snap
				E := sub.ExpiresAt
				remaining := E.Sub(now)
				// The real residual of this period's money, and the slack one day per order allows.
				residual, slack, allExact := new(big.Rat), new(big.Rat), true
				for _, w := range truth {
					if w.paidAt.Before(sub.StartsAt) {
						continue
					}
					residual.Add(residual, exactShare(*w, now, E))
					perDay := new(big.Rat).Mul(new(big.Rat).SetInt64(w.cents), ratNs(day))
					slack.Add(slack, perDay.Quo(perDay, ratNs(w.to.Sub(w.from))))
					slack.Add(slack, big.NewRat(1, 1))
					allExact = allExact && w.exact
				}
				credit := new(big.Rat).SetInt64(snap.CreditCents)
				if credit.Cmp(residual) > 0 {
					fail("credit %d over the real residual %s", snap.CreditCents, residual.FloatString(3))
				}
				if allExact && new(big.Rat).Sub(residual, credit).Cmp(slack) > 0 {
					fail("credit %d short of %s by more than %s", snap.CreditCents, residual.FloatString(3), slack.FloatString(3))
				}
				exact := new(big.Rat).Mul(new(big.Rat).SetInt64(snap.ValueRate.Amount), ratNs(remaining))
				exact.Quo(exact, new(big.Rat).Mul(new(big.Rat).SetInt64(int64(snap.ValueRate.Days)), ratNs(day)))
				value := new(big.Rat).SetInt64(snap.ValueCents)
				oneDay := big.NewRat(snap.ValueRate.Amount+int64(snap.ValueRate.Days), int64(snap.ValueRate.Days))
				if value.Cmp(exact) < 0 || new(big.Rat).Sub(value, exact).Cmp(oneDay) > 0 {
					fail("value %d against the exact %s", snap.ValueCents, exact.FloatString(3))
				}
				want, applied := max(snap.ValueCents-snap.CreditCents, settings.MinChargeCents), snap.ValueCents-snap.CreditCents < settings.MinChargeCents
				if ev.AmountCents != want || snap.MinChargeApplied != applied {
					fail("amount %d want %d", ev.AmountCents, want)
				}
				if new(big.Rat).Add(new(big.Rat).SetInt64(ev.AmountCents), residual).Cmp(exact) < 0 {
					fail("merchant short: %d + %s < %s", ev.AmountCents, residual.FloatString(3), exact.FloatString(3))
				}
				// Traffic: the plan's own terms bound what is held from below (merchant side).
				days := snap.ChargeDays
				if snap.Refreshing {
					end := E
					if next := sub.QuotaCycleStartsAt.Add(time.Duration(sub.QuotaCycleDays) * day); next.Before(end) {
						end = next
					}
					days = int((end.Sub(now) + day - 1) / day)
				} else if !snap.FromTraffic.AtLeast(billing.Rate{Amount: ev.From.TrafficBytes, Days: ev.From.DurationDays}) {
					fail("held traffic %+v below the plan's", snap.FromTraffic)
				}
				bound := new(big.Rat).Sub(big.NewRat(snap.ToTraffic.Amount*int64(days), int64(snap.ToTraffic.Days)), big.NewRat(snap.FromTraffic.Amount*int64(days), int64(snap.FromTraffic.Days)))
				if snap.TrafficAddBytes < 0 || new(big.Rat).SetInt64(snap.TrafficAddBytes).Cmp(bound) > 0 && snap.TrafficAddBytes > 0 {
					fail("traffic %d over %s", snap.TrafficAddBytes, bound.FloatString(1))
				}
				// Place and pay it; the period, the expiry and the quoted traffic hold.
				var order model.Order
				err = e.db.Transaction(func(tx *gorm.DB) error {
					var e2 error
					order, _, _, e2 = srv.placeUpgradeOrder(tx, user.ID, upgradeOrderInput{PlanID: target.ID, PayType: "wxpay", ExpectedAmountCents: &ev.AmountCents}, uuid.NewString(), now)
					return e2
				})
				if err != nil {
					fail("place: %v", err)
				}
				if !fulfil(&order, order.AmountCents) {
					fail("upgrade refused")
				}
				after, _ := readSubscription(e.db, user.ID, now)
				paid := e.order(order.ID)
				if after.PlanID != target.ID || !after.StartsAt.Equal(sub.StartsAt) || !after.ExpiresAt.Equal(E) || paid.UpgradeTrafficBytes != snap.TrafficAddBytes || after.TrafficLimitBytes != sub.TrafficLimitBytes+snap.TrafficAddBytes {
					fail("after upgrade %+v (before %+v, order %+v)", after, sub, paid)
				}
				truth[order.ID] = &truthWindow{from: now, to: E, cents: order.AmountCents, paidAt: now, exact: true}
				checked++
				upgrades++
				if applied {
					minimums++
				}
				if !allExact {
					legacy++
				}
				if upgrades > 1 {
					chained++
				}
			}
		}
	}
	t.Logf("%d trajectories, %d upgrades checked (%d at the minimum charge, %d with legacy windows, %d chained)", trajectories, checked, minimums, legacy, chained)
	if checked < trajectories/10 || chained == 0 || legacy == 0 || minimums == 0 {
		t.Fatalf("too little coverage: %d upgrades, %d chained, %d legacy, %d minimum", checked, chained, legacy, minimums)
	}
}
