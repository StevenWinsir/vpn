package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// handoff_shop.md D2 (revised): a yearly purchase grants the monthly allowance again
// every 30 days from the purchase. Leftover traffic is not carried over, and an
// allowance used up early stays exhausted until the next refresh.

const day = 24 * time.Hour

// buyYearly pays one yearly order of plan for u through the fake gateway.
func (e *consoleEnv) buyYearly(u payUser, plan string) {
	e.t.Helper()
	var p placed
	_ = json.Unmarshal(e.want2(e.placeCycle(u, plan, "yearly", "", nil, ""), 201), &p)
	gw, status := e.gw.open(e.t, p.PayURL)
	if status != 200 {
		e.t.Fatalf("gateway %d", status)
	}
	e.want(e.pay(gw), 200)
}

func (e *consoleEnv) stored(userID string) model.Subscription {
	e.t.Helper()
	var sub model.Subscription
	if err := e.db.First(&sub, "user_id = ?", userID).Error; err != nil {
		e.t.Fatal(err)
	}
	return sub
}

func (e *consoleEnv) setUsed(userID string, units int64) {
	e.t.Helper()
	if err := e.db.Model(&model.Subscription{}).Where("user_id = ?", userID).Update("used_units", units).Error; err != nil {
		e.t.Fatal(err)
	}
}

func TestQuotaCycleRoll(t *testing.T) {
	start := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	fresh := func() model.Subscription {
		anchor := start
		return model.Subscription{StartsAt: start, ExpiresAt: start.AddDate(0, 0, 365), TrafficLimitBytes: 100, UsedUnits: 90_000, UploadBytes: 50, DownloadBytes: 40,
			QuotaCycleDays: 30, QuotaCycleBytes: 100, QuotaCycleStartsAt: &anchor}
	}
	s := fresh()
	if billing.RollQuotaCycle(&s, start.Add(30*day-time.Nanosecond)) || s.UsedUnits != 90_000 {
		t.Fatal("rolled before the cycle ended")
	}
	if next := billing.NextQuotaReset(&s); next == nil || !next.Equal(start.Add(30*day)) {
		t.Fatalf("next reset %v", next)
	}
	if !billing.RollQuotaCycle(&s, start.Add(30*day)) || s.UsedUnits != 0 || s.TrafficLimitBytes != 100 || s.UploadBytes != 0 || s.DownloadBytes != 0 || !s.QuotaCycleStartsAt.Equal(start.Add(30*day)) {
		t.Fatalf("first refresh %+v", s)
	}
	// Several cycles without any access land on the cycle that contains now.
	s = fresh()
	if !billing.RollQuotaCycle(&s, start.Add(95*day)) || !s.QuotaCycleStartsAt.Equal(start.Add(90*day)) {
		t.Fatalf("skipped cycles %+v", s.QuotaCycleStartsAt)
	}
	// The last cycle (day 360) is cut short by the expiry: no refresh after it.
	s = fresh()
	billing.RollQuotaCycle(&s, start.Add(362*day))
	if !s.QuotaCycleStartsAt.Equal(start.Add(360*day)) || billing.NextQuotaReset(&s) != nil {
		t.Fatalf("last cycle %v next %v", s.QuotaCycleStartsAt, billing.NextQuotaReset(&s))
	}
	// Expired: left as it is.
	s = fresh()
	if billing.RollQuotaCycle(&s, start.AddDate(0, 0, 400)) || s.UsedUnits != 90_000 {
		t.Fatal("an expired subscription was refreshed")
	}
	// One-shot allowance, or an anchor left over from an earlier period: never refreshed.
	s = fresh()
	s.QuotaCycleDays = 0
	if billing.RollQuotaCycle(&s, start.Add(40*day)) || billing.NextQuotaReset(&s) != nil {
		t.Fatal("a one-shot allowance was refreshed")
	}
	s = fresh()
	s.StartsAt = start.Add(time.Hour)
	if billing.RollQuotaCycle(&s, start.Add(40*day)) || billing.QuotaCycleActive(&s) {
		t.Fatal("a stale anchor was used")
	}
}

// The whole rule against the real router: refresh at day 30 whatever was left,
// exhaustion until the next refresh, read-only views, persistence under the lock, the
// ledger row, renewals and the administrator's view.
func TestShopYearlyQuotaRefresh(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("refresh", "user")
	e.buyYearly(u, "starter")
	first := e.stored(u.ID)
	anchor := *first.QuotaCycleStartsAt
	if first.TrafficLimitBytes != 100*gib || first.QuotaCycleBytes != 100*gib || first.QuotaCycleDays != 30 || !anchor.Equal(first.StartsAt) {
		t.Fatalf("yearly subscription %+v", first)
	}

	// The node ledger row of the current period, holding a reservation.
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		sub, err := readNativeSubscription(tx, u.ID, e.clock.Now())
		if err != nil {
			return err
		}
		p, err := ensurePeriod(tx, sub)
		if err != nil {
			return err
		}
		return tx.Model(p).Update("reserved_units", 7000).Error
	}); err != nil {
		t.Fatal(err)
	}

	// 90 of 100 GiB used; one second before the end nothing changes.
	e.setUsed(u.ID, 90*gib*1000)
	e.clock.Advance(30*day - time.Second)
	if s := e.subscription(u.ID); s.UsedUnits != 90*gib*1000 || s.TrafficLimitBytes != 100*gib {
		t.Fatalf("refreshed early %+v", s)
	}
	e.clock.Advance(time.Second)

	// Read-only views show the fresh 100 GiB (not 110: the leftover is gone) and write nothing.
	b := e.loginAgain(u)
	var dash struct {
		Subscription      model.Subscription `json:"subscription"`
		EntitlementActive bool               `json:"entitlement_active"`
		QuotaResetsAt     *time.Time         `json:"quota_resets_at"`
	}
	e.decode(b.get("/me/dashboard"), 200, &dash)
	if dash.Subscription.UsedUnits != 0 || dash.Subscription.TrafficLimitBytes != 100*gib || !dash.EntitlementActive || dash.QuotaResetsAt == nil || !dash.QuotaResetsAt.Equal(anchor.Add(60*day)) ||
		dash.Subscription.QuotaCycleStartsAt == nil || !dash.Subscription.QuotaCycleStartsAt.Equal(anchor.Add(30*day)) {
		t.Fatalf("dashboard %+v resets %v", dash.Subscription, dash.QuotaResetsAt)
	}
	if s := e.stored(u.ID); s.UsedUnits != 90*gib*1000 || !s.QuotaCycleStartsAt.Equal(anchor) {
		t.Fatalf("a read-only view wrote the refresh %+v", s)
	}

	// The client path persists it, with the ledger row; reservations held by nodes stay.
	token := e.clientLogin(u.Email)
	s := e.stored(u.ID)
	if s.UsedUnits != 0 || s.TrafficLimitBytes != 100*gib || !s.QuotaCycleStartsAt.Equal(anchor.Add(30*day)) || !s.StartsAt.Equal(first.StartsAt) || s.Version != first.Version {
		t.Fatalf("persisted refresh %+v", s)
	}
	var period model.SubscriptionPeriod
	if err := e.db.First(&period, "subscription_id = ? AND starts_at = ?", s.ID, s.StartsAt).Error; err != nil {
		t.Fatal(err)
	}
	if period.LimitBytes != 100*gib || period.UsedUnits != 0 || period.ReservedUnits != 7000 {
		t.Fatalf("ledger row %+v", period)
	}
	if st, code := e.clientSession(token); code != 200 || st.Reason == "quota_exhausted" || st.TotalBytes != 100*gib {
		t.Fatalf("client after refresh %d %+v", code, st)
	}

	// Used up within the cycle: refused until the next refresh, then allowed again.
	e.setUsed(u.ID, 100*gib*1000)
	if st, _ := e.clientSession(token); st.Reason != "quota_exhausted" || st.CanConnect {
		t.Fatalf("exhausted %+v", st)
	}
	e.clock.Advance(29 * day)
	token = e.clientLogin(u.Email)
	if st, _ := e.clientSession(token); st.Reason != "quota_exhausted" {
		t.Fatalf("exhausted allowance came back early %+v", st)
	}
	e.clock.Advance(day)
	token = e.clientLogin(u.Email)
	if st, _ := e.clientSession(token); st.Reason == "quota_exhausted" || st.TotalBytes != 100*gib {
		t.Fatalf("second refresh %+v", st)
	}
	if s = e.stored(u.ID); s.UsedUnits != 0 || !s.QuotaCycleStartsAt.Equal(anchor.Add(60*day)) {
		t.Fatalf("second refresh stored %+v", s)
	}

	// A yearly renewal keeps the reset dates and the current balance and extends a year;
	// a monthly one extends 30 days. Neither adds to the current cycle.
	e.setUsed(u.ID, 40*gib*1000)
	before := e.stored(u.ID)
	e.buyYearly(e.loginAgainUser(u), "starter")
	after := e.stored(u.ID)
	if !after.ExpiresAt.Equal(before.ExpiresAt.AddDate(0, 0, 365)) || after.TrafficLimitBytes != 100*gib || after.UsedUnits != 40*gib*1000 || !after.QuotaCycleStartsAt.Equal(*before.QuotaCycleStartsAt) {
		t.Fatalf("yearly renewal %+v", after)
	}
	m, _ := e.gw.open(t, e.place(e.loginAgainUser(u), "starter", payment.TypeAlipay, "").PayURL)
	e.want(e.pay(m), 200)
	if again := e.stored(u.ID); !again.ExpiresAt.Equal(after.ExpiresAt.AddDate(0, 0, 30)) || again.TrafficLimitBytes != 100*gib || again.UsedUnits != 40*gib*1000 {
		t.Fatalf("monthly renewal %+v", again)
	}

	// The administrator sees the cycle; changing the plan by hand makes it one-shot.
	admin := e.loginAgain(e.admin)
	var detail struct {
		User consoleUser `json:"user"`
	}
	e.decode(admin.get("/admin/console/users/"+u.ID), 200, &detail)
	sub := detail.User.Subscription
	if sub == nil || sub.QuotaCycleDays != 30 || sub.QuotaCycleBytes != 100*gib || sub.QuotaResetsAt == nil || !sub.QuotaResetsAt.Equal(anchor.Add(90*day)) {
		t.Fatalf("console view %+v", sub)
	}
	e.decode(admin.post("/admin/console/users/"+u.ID+"/subscription", map[string]any{"version": sub.Version, "plan_id": "pro"}), 200, nil)
	if s = e.stored(u.ID); s.QuotaCycleDays != 0 || s.QuotaCycleStartsAt != nil || billing.QuotaCycleActive(&s) {
		t.Fatalf("hand-made plan change kept the cycle %+v", s)
	}
}

// loginAgainUser signs u in again (the clock moved past its web session).
func (e *consoleEnv) loginAgainUser(u payUser) payUser {
	e.t.Helper()
	w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": payPassword}, "")
	if w.Code != 200 {
		e.t.Fatalf("login %d", w.Code)
	}
	u.cookies = w.Result().Cookies()
	return u
}

// A refresh while a node holds a live grant: the grant keeps running (same period, same
// session binding), its later usage counts against the fresh cycle only, and the
// balance the node budgets from is the new one.
func TestQuotaCycleRefreshUnderLiveGrantPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	u := e.newUser(1<<30, 2)
	now := time.Now().UTC().Truncate(time.Microsecond)
	set := func(values map[string]any) {
		if err := e.db.Model(&model.Subscription{}).Where("id = ?", u.subID).Updates(values).Error; err != nil {
			t.Fatal(err)
		}
	}
	set(map[string]any{"starts_at": now.Add(-40 * day), "expires_at": now.Add(300 * day), "quota_cycle_days": 30, "quota_cycle_bytes": 1 << 30, "quota_cycle_starts_at": now.Add(-20 * day)})
	node := e.addNode("Cycle", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, node.ID, "key-cycle-00001"))
	e.sync(agent, nil, nil)
	e.sync(agent, []string{id}, nil)
	e.report(agent, usage{id, 1, 1, mib, mib, false}, 200)
	if sub := e.subscription(u.subID); sub.UsedUnits != 2*mib*1000 {
		t.Fatalf("before the refresh %+v", sub)
	}

	// The 30 days run out (the anchor moves back instead of the wall clock).
	set(map[string]any{"quota_cycle_starts_at": now.Add(-31 * day)})
	if o := e.sync(agent, []string{id}, nil)[id]; o.Action != "keep" {
		t.Fatalf("a refresh revoked the live grant: %+v", o)
	}
	sub := e.subscription(u.subID)
	if sub.UsedUnits != 0 || sub.TrafficLimitBytes != 1<<30 || !sub.QuotaCycleStartsAt.Equal(now.Add(-day)) || !sub.StartsAt.Equal(now.Add(-40*day)) {
		t.Fatalf("refresh on the node path %+v", sub)
	}
	e.report(agent, usage{id, 1, 2, 3 * mib, 3 * mib, false}, 200)
	sub = e.subscription(u.subID)
	period := e.period(u.subID)
	if sub.UsedUnits != 4*mib*1000 || period.UsedUnits != 4*mib*1000 || period.LimitBytes != 1<<30 || e.grant(id).PeriodID != period.ID {
		t.Fatalf("usage after the refresh: sub %+v period %+v", sub, period)
	}
	w := e.client("GET", "/session", token, nil, nil)
	var st nativeState
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st.Reason == "subscription_changed" || st.TotalBytes != 1<<30 || st.RemainingBytes != 1<<30-4*mib {
		t.Fatalf("client session after the refresh %d %+v", w.Code, st)
	}
}

// A monthly subscription renewed as yearly starts its cycles at the purchase, with the
// rest of the monthly allowance carried into the first cycle only.
func TestShopMonthlyToYearlyCycles(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("convert", "user")
	m, _ := e.gw.open(t, e.place(u, "starter", payment.TypeWeChat, "").PayURL)
	e.want(e.pay(m), 200)
	e.setUsed(u.ID, 70*gib*1000)
	e.clock.Advance(10 * day)
	u = e.loginAgainUser(u)
	e.buyYearly(u, "starter")
	bought := e.clock.Now()
	s := e.stored(u.ID)
	if s.TrafficLimitBytes != 200*gib || s.UsedUnits != 70*gib*1000 || s.QuotaCycleStartsAt == nil || s.QuotaCycleStartsAt.Before(bought.Add(-time.Minute)) {
		t.Fatalf("converted %+v", s)
	}
	e.clock.Advance(30 * day)
	if v := e.subscription(u.ID); v.TrafficLimitBytes != 100*gib || v.UsedUnits != 0 {
		t.Fatalf("first refresh after conversion %+v", v)
	}
}

// A yearly order written before refreshing allowances (no cycle snapshot) is fulfilled
// as it was sold: the whole year at once.
func TestShopLegacyYearlyOrderFulfilsOneShot(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("legacy", "user")
	var p placed
	_ = json.Unmarshal(e.want2(e.placeCycle(u, "starter", "yearly", "", nil, uuid.NewString()), 201), &p)
	e.db.Model(&model.Order{}).Where("id = ?", p.Order.ID).Updates(map[string]any{"quota_cycle_days": 0, "quota_cycle_bytes": 0})
	gw, _ := e.gw.open(t, p.PayURL)
	e.want(e.pay(gw), 200)
	s := e.stored(u.ID)
	if s.TrafficLimitBytes != 1200*gib || s.QuotaCycleDays != 0 || billing.QuotaCycleActive(&s) {
		t.Fatalf("legacy yearly %+v", s)
	}
}
