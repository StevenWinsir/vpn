package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/payment"
)

// Mid-period upgrades (handoff_plans.md, round 1). The examples of section 6.6 run
// through the real endpoints and the fake gateway at fixed instants (UTC+8 dates), so
// the results are exact in any process time zone.

var cst = time.FixedZone("UTC+8", 8*3600)

func at(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, cst) }

func (e *payEnv) setNow(t time.Time) {
	e.clock.mu.Lock()
	e.clock.now = t.UTC()
	e.clock.mu.Unlock()
}

// relogin signs u in again (after a long clock jump the old session may be over).
func (e *payEnv) relogin(u payUser) payUser {
	e.t.Helper()
	w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": payPassword}, "")
	if w.Code != 200 {
		e.t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	u.cookies = w.Result().Cookies()
	return u
}

// buy places a gateway order for plan (cycle "" or yearly, optional coupon) and pays it.
func (e *payEnv) buy(u payUser, plan, cycle, coupon string) model.Order {
	e.t.Helper()
	body := map[string]any{"plan_id": plan, "pay_type": "wxpay", "coupon_code": coupon}
	if cycle != "" {
		body["billing_cycle"] = cycle
	}
	w := e.call(u.cookies, "POST", "/api/v1/payments/orders", body, uuid.NewString())
	if w.Code != 201 {
		e.t.Fatalf("order %d %s", w.Code, w.Body.String())
	}
	var out placed
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	g, status := e.gw.open(e.t, out.PayURL)
	if status != 200 {
		e.t.Fatalf("gateway %d", status)
	}
	e.want(e.pay(g), 200)
	o := e.order(out.Order.ID)
	if o.Status != model.OrderPaid {
		e.t.Fatalf("order not paid: %s %s", o.Status, o.ReviewReason)
	}
	return o
}

func (e *payEnv) upgradeQuote(u payUser, plan string) (upgradeQuoteView, *httptest.ResponseRecorder) {
	w := e.call(u.cookies, "POST", "/api/v1/payments/upgrade-quote", map[string]any{"plan_id": plan}, "")
	var out struct {
		Quote upgradeQuoteView `json:"quote"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Quote, w
}

func (e *payEnv) quoteOK(u payUser, plan string) upgradeQuoteView {
	e.t.Helper()
	q, w := e.upgradeQuote(u, plan)
	if w.Code != 200 {
		e.t.Fatalf("quote %d %s", w.Code, w.Body.String())
	}
	return q
}

func (e *payEnv) placeUpgrade(u payUser, plan, payType string, expected int64, key string) *httptest.ResponseRecorder {
	return e.call(u.cookies, "POST", "/api/v1/payments/upgrade-orders", map[string]any{"plan_id": plan, "pay_type": payType, "expected_amount_cents": expected}, key)
}

// upgradeOrder places an upgrade at the quoted price and returns it with its gateway order.
func (e *payEnv) upgradeOrder(u payUser, plan string) (placed, *fakeGatewayOrder) {
	e.t.Helper()
	q := e.quoteOK(u, plan)
	if !q.Purchasable {
		e.t.Fatalf("not purchasable: %s %s", q.Reason, q.Message)
	}
	w := e.placeUpgrade(u, plan, "alipay", q.AmountCents, uuid.NewString())
	if w.Code != 201 {
		e.t.Fatalf("upgrade order %d %s", w.Code, w.Body.String())
	}
	var out placed
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	g, status := e.gw.open(e.t, out.PayURL)
	if status != 200 {
		e.t.Fatalf("gateway %d", status)
	}
	return out, g
}

func (e *payEnv) upgrade(u payUser, plan string) model.Order {
	e.t.Helper()
	out, g := e.upgradeOrder(u, plan)
	e.want(e.pay(g), 200)
	return e.order(out.Order.ID)
}

func errorOf(w *httptest.ResponseRecorder) (code, message, relation string) {
	var out struct {
		Error struct {
			Code     string `json:"code"`
			Message  string `json:"message"`
			Relation string `json:"relation"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Error.Code, out.Error.Message, out.Error.Relation
}

func wantQuote(t *testing.T, q upgradeQuoteView, days, credited int, value, credit, amount, traffic int64) {
	t.Helper()
	if q.UpgradeDays != days || q.CreditedDays != credited || q.ValueCents != value || q.CreditCents != credit || q.AmountCents != amount || q.TrafficAddBytes != traffic || !q.Purchasable {
		t.Fatalf("quote %+v, want days %d/%d value %d credit %d amount %d traffic %d", q, days, credited, value, credit, amount, traffic)
	}
}

// R1-01, examples 1, 2 and 4.
func TestUpgradeExamplesMonthly(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upga", "user")
	e.buy(u, "starter", "", "")
	before := e.subscription(u.ID)

	e.setNow(at(2026, 11, 11, 10))
	u = e.relogin(u)
	q := e.quoteOK(u, "pro")
	wantQuote(t, q, 20, 19, 2660, 1260, 1400, 214748364800-71582788267)
	if !q.ExpiresAt.Equal(before.ExpiresAt) || q.BillingCycle != model.CycleMonthly || q.FromPlanID != "starter" || !q.AllowDedicated || !q.ReauthRequired || q.MinChargeApplied || q.ListAmountCents != 1400 || q.TestPrice {
		t.Fatalf("quote details %+v", q)
	}
	if q.DeviceLimit != e.server().deviceCap(5) {
		t.Fatalf("device limit %d", q.DeviceLimit)
	}

	// Options name every plan's relation.
	var opts struct {
		Enabled bool            `json:"enabled"`
		Current *upgradeCurrent `json:"current"`
		Plans   []upgradeOption `json:"plans"`
	}
	w := e.call(u.cookies, "GET", "/api/v1/payments/upgrade-options", nil, "")
	if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil || !opts.Enabled || opts.Current == nil || opts.Current.PlanID != "starter" || opts.Current.BillingCycle != model.CycleMonthly {
		t.Fatalf("options %s", w.Body.String())
	}
	rel := map[string]upgradeOption{}
	for _, o := range opts.Plans {
		rel[o.PlanID] = o
	}
	if rel["starter"].Relation != billing.RelationCurrent || rel["pro"].Relation != billing.RelationUpgrade || rel["pro"].AmountCents != 1400 || !rel["pro"].Purchasable || rel["max"].Relation != billing.RelationUpgrade {
		t.Fatalf("relations %+v", rel)
	}

	out, g := e.upgradeOrder(u, "pro")
	if g.Name != "进阶计划·升级" || out.Order.Kind != model.OrderKindUpgrade || out.Order.DurationDays != 0 || out.Order.TrafficBytes != 0 {
		t.Fatalf("order %+v gateway name %q", out.Order, g.Name)
	}
	e.want(e.pay(g), 200)
	first := e.order(out.Order.ID)
	sub := e.subscription(u.ID)
	if first.Status != model.OrderPaid || first.CoversFrom == nil || !first.CoversFrom.Equal(e.clock.Now().Add(-time.Millisecond)) || !first.CoversTo.Equal(before.ExpiresAt) || first.UpgradeTrafficBytes != q.TrafficAddBytes {
		t.Fatalf("paid upgrade %+v", first)
	}
	if sub.PlanID != "pro" || sub.PlanName != "进阶计划" || sub.MaxDevices != 5 || !sub.AllowDedicated || !sub.StartsAt.Equal(before.StartsAt) || !sub.ExpiresAt.Equal(before.ExpiresAt) ||
		sub.TrafficLimitBytes != before.TrafficLimitBytes+q.TrafficAddBytes || sub.Version != before.Version+1 {
		t.Fatalf("subscription %+v (before %+v)", sub, before)
	}

	// Example 2: on to max ten days later; the first upgrade's money is credited too.
	e.setNow(at(2026, 11, 21, 10))
	u = e.relogin(u)
	q2 := e.quoteOK(u, "max")
	wantQuote(t, q2, 10, 9, 2664, 1240, 1424, 286331153066-107374182400)
	second := e.upgrade(u, "max")
	if second.Status != model.OrderPaid || second.AmountCents != 1424 || second.FromPlanID != "pro" {
		t.Fatalf("second upgrade %+v", second)
	}
	if sub = e.subscription(u.ID); sub.PlanID != "max" || !sub.ExpiresAt.Equal(before.ExpiresAt) || sub.TrafficLimitBytes != before.TrafficLimitBytes+q.TrafficAddBytes+q2.TrafficAddBytes {
		t.Fatalf("after max %+v", sub)
	}
	// Max has nothing above it; starter and pro are now downgrades.
	if _, w := e.upgradeQuote(u, "pro"); w.Code != 409 {
		t.Fatalf("downgrade quote %d", w.Code)
	} else if code, _, relation := errorOf(w); code != "not_upgradable" || relation != billing.RelationDowngrade {
		t.Fatalf("downgrade %s %s", code, relation)
	}

	// Example 4: the same as example 1 with a 50 % coupon on the first order.
	e.setNow(at(2026, 11, 1, 0))
	e.coupon(model.Coupon{Code: "HALF-UPG", DiscountType: model.DiscountPercent, PercentOff: 50, Enabled: true})
	v := e.user("upgcoupon", "user")
	if o := e.buy(v, "starter", "", "HALF-UPG"); o.AmountCents != 995 {
		t.Fatalf("coupon order %d", o.AmountCents)
	}
	e.setNow(at(2026, 11, 11, 10))
	v = e.relogin(v)
	wantQuote(t, e.quoteOK(v, "pro"), 20, 19, 2660, 630, 2030, 214748364800-71582788267)
}

// R1-01 example 3 and R1-06: a refreshing (yearly) allowance.
func TestUpgradeExampleYearly(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 1, 10, 0))
	u := e.user("upgb", "user")
	if o := e.buy(u, "starter", "yearly", ""); o.AmountCents != 21492 {
		t.Fatalf("yearly price %d", o.AmountCents)
	}
	e.setNow(at(2026, 11, 2, 12))
	u = e.relogin(u)
	q := e.quoteOK(u, "pro")
	// The cycle resets at 2026-11-06 00:00: 3 d 12 h left counts as 4 days.
	wantQuote(t, q, 69, 68, 8147, 4003, 4144, 42949672960-14316557654)
	if q.BillingCycle != model.CycleYearly || q.NewCycleBytes != 300*gib {
		t.Fatalf("yearly quote %+v", q)
	}
	before := e.subscription(u.ID)
	o := e.upgrade(u, "pro")
	if o.BillingCycle != model.CycleYearly || o.CycleDiscountPercent != 90 || o.OriginalCents != 8147 {
		t.Fatalf("yearly upgrade order %+v", o)
	}
	sub := e.subscription(u.ID)
	if sub.QuotaCycleBytes != 300*gib || sub.TrafficLimitBytes != before.TrafficLimitBytes+q.TrafficAddBytes || !sub.QuotaCycleStartsAt.Equal(*before.QuotaCycleStartsAt) {
		t.Fatalf("after upgrade %+v", sub)
	}
	e.setNow(at(2026, 11, 6, 0).Add(time.Second))
	if sub = e.subscription(u.ID); sub.TrafficLimitBytes != 300*gib || sub.UsedUnits != 0 {
		t.Fatalf("after the reset %+v", sub)
	}

	// Paid after the reset: the new cycle gets the whole difference.
	e.setNow(at(2026, 1, 10, 0))
	v := e.user("upgb2", "user")
	e.buy(v, "starter", "yearly", "")
	e.setNow(at(2026, 11, 5, 23))
	v = e.relogin(v)
	out, g := e.upgradeOrder(v, "pro")
	if out.Order.UpgradeTrafficBytes != 10*gib-(100*gib+29)/30 {
		t.Fatalf("one-day quote %d", out.Order.UpgradeTrafficBytes)
	}
	e.setNow(at(2026, 11, 6, 0).Add(30 * time.Minute))
	e.want(e.pay(g), 200)
	late := e.order(out.Order.ID)
	sub = e.subscription(v.ID)
	if late.Status != model.OrderPaid || late.UpgradeTrafficBytes != 200*gib || sub.TrafficLimitBytes != 300*gib || sub.QuotaCycleBytes != 300*gib {
		t.Fatalf("late across the reset: order %+v sub %+v", late, sub)
	}
}

// R1-01 example 5 and R1-09: renewals stack; an order without a value window (fulfilled
// by an older binary) is credited from its payment time, which can only credit less.
func TestUpgradeExampleRenewalsAndLegacyWindow(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upgr", "user")
	e.buy(u, "starter", "", "")
	e.setNow(at(2026, 11, 25, 0))
	u = e.relogin(u)
	renewal := e.buy(u, "starter", "", "")
	if !renewal.CoversFrom.Equal(at(2026, 12, 1, 0)) || !renewal.CoversTo.Equal(at(2026, 12, 31, 0)) {
		t.Fatalf("renewal window %v..%v", renewal.CoversFrom, renewal.CoversTo)
	}
	e.setNow(at(2026, 11, 26, 0))
	u = e.relogin(u)
	wantQuote(t, e.quoteOK(u, "pro"), 35, 35, 4655, 2321, 2334, 375809638400-125269879467)
	if err := e.db.Model(&model.Order{}).Where("id = ?", renewal.ID).Updates(map[string]any{"covers_from": nil, "covers_to": nil}).Error; err != nil {
		t.Fatal(err)
	}
	wantQuote(t, e.quoteOK(u, "pro"), 35, 35, 4655, 2254, 2401, 375809638400-125269879467)
}

// R1-01 examples 6 and 7, R1-03 and R1-08: relations and refusals.
func TestUpgradeRefusals(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	pro := e.user("upgc", "user")
	e.buy(pro, "pro", "", "")
	starter := e.user("upgs", "user")
	e.buy(starter, "starter", "", "")
	none := e.user("upgn", "user")

	// Example 6: a downgrade, by quote, upgrade order and ordinary order.
	_, w := e.upgradeQuote(pro, "starter")
	if code, msg, rel := errorOf(w); w.Code != 409 || code != "not_upgradable" || rel != billing.RelationDowngrade || !strings.Contains(msg, "不支持降级") || !strings.Contains(msg, "2026-12-01") {
		t.Fatalf("downgrade quote %d %s", w.Code, w.Body.String())
	}
	if code, _, _ := errorOf(e.placeUpgrade(pro, "starter", "wxpay", 100, uuid.NewString())); code != "not_upgradable" {
		t.Fatalf("downgrade order %s", code)
	}
	w = e.call(pro.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString())
	if code, msg, _ := errorOf(w); w.Code != 409 || code != "plan_change_not_supported" || !strings.Contains(msg, "降级") {
		t.Fatalf("downgrade purchase %s", w.Body.String())
	}
	quote := e.want(e.call(pro.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter"}, ""), 200)["quote"].(map[string]any)
	if quote["purchasable"] != false || quote["reason"] != "plan_change_not_supported" || !strings.Contains(quote["message"].(string), "2026-12-01") {
		t.Fatalf("downgrade purchase quote %+v", quote)
	}
	// An ordinary order for a higher plan points at the upgrade.
	w = e.call(starter.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay"}, uuid.NewString())
	if code, msg, _ := errorOf(w); w.Code != 409 || code != "plan_change_not_supported" || !strings.Contains(msg, "升级") {
		t.Fatalf("upgrade by purchase %s", w.Body.String())
	}

	// Lateral: dearer but with less traffic.
	lite := model.Plan{ID: "lite-plus", Name: "加量轻享", PriceCents: 2990, DurationDays: 30, TrafficBytes: 50 * gib, MaxDevices: 2, Active: true, Sort: 9, YearlyEnabled: true, YearlyDurationDays: 365, Version: 1}
	if err := e.db.Create(&lite).Error; err != nil {
		t.Fatal(err)
	}
	if _, w = e.upgradeQuote(starter, "lite-plus"); !strings.Contains(w.Body.String(), "lateral") {
		t.Fatalf("lateral %s", w.Body.String())
	}
	// The current plan, no plan, a test plan: nothing to upgrade.
	if _, w = e.upgradeQuote(starter, "starter"); !strings.Contains(w.Body.String(), `"relation":"current"`) {
		t.Fatalf("current %s", w.Body.String())
	}
	if _, w = e.upgradeQuote(none, "pro"); !strings.Contains(w.Body.String(), `"relation":"new"`) {
		t.Fatalf("none %s", w.Body.String())
	}
	opts := e.want(e.call(none.cookies, "GET", "/api/v1/payments/upgrade-options", nil, ""), 200)
	if opts["current"] != nil {
		t.Fatalf("options without a plan %+v", opts)
	}
	for _, p := range opts["plans"].([]any) {
		if o := p.(map[string]any); o["relation"] != billing.RelationNew || o["reason"] != nil {
			t.Fatalf("option %+v", o)
		}
	}
	testSub := e.user("upgt", "user")
	if err := e.db.Create(&model.Subscription{ID: uuid.NewString(), UserID: testSub.ID, PlanID: "starter", PlanName: "轻量计划", StartsAt: e.clock.Now(), ExpiresAt: e.clock.Now().AddDate(0, 0, 30), TrafficLimitBytes: gib, MaxDevices: 2, IsTest: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, w = e.upgradeQuote(testSub, "pro"); !strings.Contains(w.Body.String(), `"relation":"new"`) {
		t.Fatalf("test plan %s", w.Body.String())
	}

	// Request shape: no coupon field, an expected amount and a UUID key are required.
	e.want(e.call(starter.cookies, "POST", "/api/v1/payments/upgrade-orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay", "expected_amount_cents": 1, "coupon_code": "HALF"}, uuid.NewString()), 400)
	if code, _, _ := errorOf(e.call(starter.cookies, "POST", "/api/v1/payments/upgrade-orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay"}, uuid.NewString())); code != "invalid_expected_amount" {
		t.Fatalf("missing expected amount %s", code)
	}
	e.want(e.placeUpgrade(starter, "pro", "wxpay", 1, "not-a-uuid"), 400)
	e.want(e.placeUpgrade(starter, "pro", "bank", 1, uuid.NewString()), 400)
	q := e.quoteOK(starter, "pro")
	if code, _, _ := errorOf(e.placeUpgrade(starter, "pro", "wxpay", q.AmountCents+1, uuid.NewString())); code != "price_changed" {
		t.Fatalf("price guard %s", code)
	}

	// Idempotency: a replay returns the order; another body or endpoint is a conflict.
	key := uuid.NewString()
	first := e.want(e.placeUpgrade(starter, "pro", "wxpay", q.AmountCents, key), 201)
	replay := e.want(e.placeUpgrade(starter, "pro", "wxpay", q.AmountCents, key), 200)
	if replay["replayed"] != true || replay["order"].(map[string]any)["id"] != first["order"].(map[string]any)["id"] {
		t.Fatalf("replay %+v", replay)
	}
	if code, _, _ := errorOf(e.placeUpgrade(starter, "pro", "alipay", q.AmountCents, key)); code != "idempotency_conflict" {
		t.Fatalf("changed body %s", code)
	}
	if code, _, _ := errorOf(e.call(starter.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay"}, key)); code != "idempotency_conflict" {
		t.Fatalf("other endpoint %s", code)
	}
	// D9: the pending upgrade blocks every new order, and any pending order blocks an upgrade.
	w = e.call(starter.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString())
	if code, _, _ := errorOf(w); code != "pending_order_exists" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("purchase next to a pending upgrade %d %s", w.Code, w.Body.String())
	}
	if code, _, _ := errorOf(e.placeUpgrade(starter, "max", "wxpay", e.quoteOK(starter, "max").AmountCents, uuid.NewString())); code != "pending_order_exists" {
		t.Fatalf("second upgrade %s", code)
	}
	e.clock.Advance(e.cfg.Payment.Window() + time.Second)
	e.place(starter, "starter", "wxpay", "")
	if code, _, _ := errorOf(e.placeUpgrade(starter, "pro", "wxpay", q.AmountCents, uuid.NewString())); code != "pending_order_exists" {
		t.Fatalf("upgrade next to a pending purchase %s", code)
	}
	e.clock.Advance(e.cfg.Payment.Window() + time.Second)

	// The switch and the minimum charge (shop settings).
	if err := e.db.Model(&model.ShopSettings{}).Where("id = 1").Update("upgrade_min_charge_cents", 9000).Error; err != nil {
		t.Fatal(err)
	}
	if q = e.quoteOK(starter, "pro"); q.AmountCents != 9000 || !q.MinChargeApplied || q.MinChargeCents != 9000 {
		t.Fatalf("minimum charge %+v", q)
	}
	if err := e.db.Model(&model.ShopSettings{}).Where("id = 1").Update("upgrade_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if q = e.quoteOK(starter, "pro"); q.Purchasable || q.Reason != "upgrade_disabled" {
		t.Fatalf("disabled quote %+v", q)
	}
	if code, _, _ := errorOf(e.placeUpgrade(starter, "pro", "wxpay", q.AmountCents, uuid.NewString())); code != "upgrade_disabled" {
		t.Fatalf("disabled order %s", code)
	}
	if opts = e.want(e.call(starter.cookies, "GET", "/api/v1/payments/upgrade-options", nil, ""), 200); opts["enabled"] != false {
		t.Fatalf("disabled options %+v", opts)
	}
	if err := e.db.Model(&model.ShopSettings{}).Where("id = 1").Updates(map[string]any{"upgrade_enabled": true, "upgrade_min_charge_cents": 100}).Error; err != nil {
		t.Fatal(err)
	}

	// Example 7: 20 hours left.
	e.setNow(at(2026, 11, 30, 4))
	starter = e.relogin(starter)
	if q = e.quoteOK(starter, "pro"); q.Purchasable || q.Reason != "remaining_too_short" || q.CreditedDays != 0 {
		t.Fatalf("too short %+v", q)
	}
	if code, _, _ := errorOf(e.placeUpgrade(starter, "pro", "wxpay", q.AmountCents, uuid.NewString())); code != "remaining_too_short" {
		t.Fatalf("too short order %s", code)
	}

	// /meta announces the capability.
	if meta := e.want(e.call(nil, "GET", "/api/v1/meta", nil, ""), 200); meta["plan_upgrade_supported"] != true {
		t.Fatalf("meta %+v", meta)
	}
}

// D8: a listed test-price account pays the test price; the list amount is still shown.
func TestUpgradeTestPrice(t *testing.T) {
	e := newPayEnv(t, func(c *config.Config) {
		c.Payment.TestPrice, c.Payment.TestEmails = true, []string{"upgtp@example.com"}
	})
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upgtp", "user")
	e.buy(u, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	u = e.relogin(u)
	q := e.quoteOK(u, "pro")
	// The starter order itself cost the test price (1 cent): almost nothing to credit.
	if !q.TestPrice || q.AmountCents != 1 || q.ListAmountCents != 2660 {
		t.Fatalf("test price quote %+v", q)
	}
	if o := e.upgrade(u, "pro"); o.Status != model.OrderPaid || o.PriceSource != priceTest || o.AmountCents != 1 {
		t.Fatalf("test price order %+v", o)
	}
}

// R1-04, R1-05: the period, its ledger row and the referral bonus stay; a bound client
// session sees subscription_changed while staying on the same period.
func TestUpgradeKeepsPeriodAndChangesFingerprint(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upgl", "user")
	e.buy(u, "starter", "", "")
	sub := e.subscription(u.ID)
	start := sub.StartsAt
	if err := e.db.Model(&model.Subscription{}).Where("id = ?", sub.ID).Updates(map[string]any{"bonus_bytes": 5 * gib, "bonus_starts_at": start, "traffic_limit_bytes": sub.TrafficLimitBytes + 5*gib}).Error; err != nil {
		t.Fatal(err)
	}
	var period *model.SubscriptionPeriod
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		var err error
		period, err = ensurePeriod(tx, e.subscription(u.ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := e.subscription(u.ID)
	sess := model.NativeSession{SubscriptionID: &before.ID, SubscriptionStartsAt: &before.StartsAt, EntitlementFingerprint: nativeEntitlementFingerprint(before)}

	e.setNow(at(2026, 11, 11, 10))
	u = e.relogin(u)
	o := e.upgrade(u, "pro")
	after := e.subscription(u.ID)
	if !nativeBound(sess, after) || nativeAuthorizationBound(sess, after) {
		t.Fatal("a bound session must stay on the period but lose its authorization")
	}
	if billing.CurrentBonus(after) != 5*gib || after.TrafficLimitBytes != before.TrafficLimitBytes+o.UpgradeTrafficBytes {
		t.Fatalf("bonus or limit %+v", after)
	}
	var periods []model.SubscriptionPeriod
	if err := e.db.Where("subscription_id = ?", after.ID).Find(&periods).Error; err != nil || len(periods) != 1 {
		t.Fatalf("periods %+v %v", periods, err)
	}
	if p := periods[0]; p.ID != period.ID || !periodIsCurrent(&p, after) || p.LimitBytes != after.TrafficLimitBytes || !p.ExpiresAt.Equal(after.ExpiresAt) {
		t.Fatalf("ledger row %+v", p)
	}

	// The customer's order list shows the upgrade fields and nothing internal.
	w := e.call(u.cookies, "GET", "/api/v1/orders", nil, "")
	body := w.Body.String()
	for _, want := range []string{`"kind":"upgrade"`, `"from_plan_name":"轻量计划"`, `"credit_cents":1260`, `"upgrade_days":20`, `"upgrade_traffic_bytes":`} {
		if !strings.Contains(body, want) {
			t.Fatalf("orders lack %s: %s", want, body)
		}
	}
	for _, leak := range []string{"covers", "upgrade_snapshot", "subscription_id", "from_plan_id", "contributions"} {
		if strings.Contains(body, leak) {
			t.Fatalf("orders leak %s", leak)
		}
	}
}

// R1-07: concurrency and stale orders.
func TestUpgradeConcurrencyAndStaleOrders(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	users := map[string]payUser{}
	for _, name := range []string{"cc1", "cc2", "cc3", "cc4", "cc5", "cc6"} {
		users[name] = e.user(name, "user")
		e.buy(users[name], "starter", "", "")
	}
	e.setNow(at(2026, 11, 11, 10))
	for name, u := range users {
		users[name] = e.relogin(u)
	}

	// Two tabs: one order, one pending_order_exists.
	u := users["cc1"]
	q := e.quoteOK(u, "pro")
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.placeUpgrade(u, "pro", []string{"wxpay", "alipay"}[i], q.AmountCents, uuid.NewString()).Code
		}()
	}
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != 201 || codes[1] != 409 {
		t.Fatalf("two tabs %v", codes)
	}

	stale := func(name string, change func(sub *model.Subscription), wantPaid bool) {
		t.Helper()
		u := users[name]
		out, g := e.upgradeOrder(u, "pro")
		if change != nil {
			change(e.subscription(u.ID))
		}
		e.want(e.pay(g), 200)
		o := e.order(out.Order.ID)
		sub := e.subscription(u.ID)
		if wantPaid != (o.Status == model.OrderPaid) || wantPaid != (sub.PlanID == "pro") || !wantPaid && o.ReviewReason != model.ReviewStateConflict {
			t.Fatalf("%s: order %s %s, plan %s", name, o.Status, o.ReviewReason, sub.PlanID)
		}
	}
	// An administrator moved the expiry meanwhile: review.
	stale("cc2", func(sub *model.Subscription) {
		e.db.Model(sub).Updates(map[string]any{"expires_at": sub.ExpiresAt.AddDate(0, 0, 3), "version": sub.Version + 1})
	}, false)
	// A referral reward landed meanwhile (version moves, identity does not): fulfilled.
	stale("cc3", func(sub *model.Subscription) {
		e.db.Model(sub).Updates(map[string]any{"bonus_bytes": gib, "bonus_starts_at": sub.StartsAt, "traffic_limit_bytes": sub.TrafficLimitBytes + gib, "version": sub.Version + 1})
	}, true)
	// The subscription ended before the payment: review.
	stale("cc4", func(sub *model.Subscription) {
		e.db.Model(sub).Update("expires_at", e.clock.Now().Add(time.Second))
		e.clock.Advance(2 * time.Second)
	}, false)

	// A repeated callback fulfils once.
	u = users["cc5"]
	out, g := e.upgradeOrder(u, "pro")
	e.want(e.pay(g), 200)
	version := e.subscription(u.ID).Version
	e.want(e.notify(gatewayIP, callbackBody(g, payment.TradeSuccess)), 200)
	if n := e.lastNotification(); n.Result != model.NotifyDuplicate || e.subscription(u.ID).Version != version || e.order(out.Order.ID).Status != model.OrderPaid {
		t.Fatalf("duplicate: %s", n.Result)
	}

	// A late renewal and an upgrade paid at the same moment: exactly one applies.
	u = users["cc6"]
	renewal := e.place(u, "starter", "wxpay", "")
	rg, _ := e.gw.open(t, renewal.PayURL)
	e.clock.Advance(e.cfg.Payment.Window() + time.Second)
	up, ug := e.upgradeOrder(u, "pro")
	e.gw.markPaid(rg)
	e.gw.markPaid(ug)
	wg = sync.WaitGroup{}
	for _, g := range []*fakeGatewayOrder{rg, ug} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.notify(gatewayIP, callbackBody(g, payment.TradeSuccess))
		}()
	}
	wg.Wait()
	r, x := e.order(renewal.Order.ID), e.order(up.Order.ID)
	if (r.Status == model.OrderPaid) == (x.Status == model.OrderPaid) || r.Status != model.OrderPaid && r.Status != model.OrderReview || x.Status != model.OrderPaid && x.Status != model.OrderReview {
		t.Fatalf("race: renewal %s, upgrade %s", r.Status, x.Status)
	}
}

// R1-09: an older binary that receives an upgrade order can only refuse it, because the
// order sells 0 days (its fulfilOrder reads days and traffic from the order).
func TestUpgradeOrderRefusedByOlderFulfilment(t *testing.T) {
	e := newPayEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upgold", "user")
	e.buy(u, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	u = e.relogin(u)
	out, _ := e.upgradeOrder(u, "pro")
	order := e.order(out.Order.ID)
	var plan model.Plan
	if err := e.db.First(&plan, "id = ?", order.PlanID).Error; err != nil {
		t.Fatal(err)
	}
	plan.Name, plan.DurationDays, plan.TrafficBytes = order.PlanName, order.DurationDays, order.TrafficBytes
	err := e.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := applyPlan(tx, order.UserID, plan, order.QuotaCycleDays, e.clock.Now(), false)
		return err
	})
	var business *apiError
	if !errors.As(err, &business) || business.Code != errPlanInvalid.Code {
		t.Fatalf("an older fulfilment would not refuse: %v", err)
	}
	// Even once the subscription has ended (a new period would otherwise start).
	e.setNow(at(2026, 12, 2, 0))
	err = e.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := applyPlan(tx, order.UserID, plan, order.QuotaCycleDays, e.clock.Now(), false)
		return err
	})
	if !errors.As(err, &business) || business.Code != errPlanInvalid.Code {
		t.Fatalf("an older fulfilment would start a period: %v", err)
	}
}

// Console: order type, filter and pricing facts; the shop settings.
func TestUpgradeConsole(t *testing.T) {
	e := newConsoleEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upgcon", "user")
	e.buy(u, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	u = e.relogin(u)
	e.admin = e.relogin(e.admin)
	o := e.upgrade(u, "pro")

	var list struct {
		Orders []consoleOrder `json:"orders"`
	}
	e.decode(e.get("/orders?kind=upgrade"), 200, &list)
	if len(list.Orders) != 1 || list.Orders[0].ID != o.ID || list.Orders[0].FromPlanName != "轻量计划" || list.Orders[0].CreditCents != 1260 || list.Orders[0].CoversTo == nil {
		t.Fatalf("upgrade list %+v", list.Orders)
	}
	e.decode(e.get("/orders?kind=purchase"), 200, &list)
	if len(list.Orders) != 1 || list.Orders[0].Kind != model.OrderKindPurchase {
		t.Fatalf("purchase list %+v", list.Orders)
	}
	e.want(e.get("/orders?kind=refund"), 400)
	var detail struct {
		Upgrade *upgradeSnapshot `json:"upgrade"`
	}
	e.decode(e.get("/orders/"+o.ID), 200, &detail)
	if detail.Upgrade == nil || detail.Upgrade.ValueCents != 2660 || len(detail.Upgrade.Contributions) != 1 || detail.Upgrade.Contributions[0].Cents != 1260 {
		t.Fatalf("detail %+v", detail.Upgrade)
	}

	var settings struct {
		Settings shopSettingsView `json:"settings"`
	}
	e.decode(e.get("/shop-settings"), 200, &settings)
	if !settings.Settings.UpgradeEnabled || settings.Settings.UpgradeMinChargeCents != 100 {
		t.Fatalf("settings %+v", settings.Settings)
	}
	// An older console sends only the yearly discount: the upgrade fields stay.
	e.decode(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "version": settings.Settings.Version}), 200, &settings)
	if !settings.Settings.UpgradeEnabled || settings.Settings.UpgradeMinChargeCents != 100 || settings.Settings.YearlyDiscountPercent != 85 {
		t.Fatalf("older console %+v", settings.Settings)
	}
	e.code(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "upgrade_min_charge_cents": 0, "version": settings.Settings.Version}), 400, "invalid_plan")
	e.code(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "upgrade_min_charge_cents": 10001, "version": settings.Settings.Version}), 400, "invalid_plan")
	e.decode(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "upgrade_enabled": false, "upgrade_min_charge_cents": 250, "version": settings.Settings.Version}), 200, &settings)
	if settings.Settings.UpgradeEnabled || settings.Settings.UpgradeMinChargeCents != 250 {
		t.Fatalf("saved %+v", settings.Settings)
	}
	var audit model.AdminAudit
	if err := e.db.Where("action = ?", AuditShopSettings).Order("version desc").First(&audit).Error; err != nil || !strings.Contains(audit.Detail, "upgrade_enabled") || !strings.Contains(audit.Detail, "upgrade_min_charge_cents") || strings.Contains(audit.Detail, "yearly_discount_percent") {
		t.Fatalf("audit %+v %v", audit, err)
	}
	e.code(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "upgrade_enabled": false, "upgrade_min_charge_cents": 250, "version": settings.Settings.Version}), 400, "no_change")
	// Recreating a deleted row keeps a false switch.
	e.db.Exec(`DELETE FROM ` + e.cfg.Schema + `.shop_settings`)
	e.decode(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 90, "upgrade_enabled": false, "version": 0}), 200, &settings)
	var row model.ShopSettings
	if err := e.db.First(&row, "id = 1").Error; err != nil || row.UpgradeEnabled || row.UpgradeMinChargeCents != 100 {
		t.Fatalf("recreated %+v %v", row, err)
	}
}

// R1-10: doctor.
func TestUpgradeDoctor(t *testing.T) {
	e := newPayEnv(t, nil)
	inspect := func() operations.ReadinessReport {
		t.Helper()
		report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	report := inspect()
	for _, w := range []string{"plans_upgrade_group_gap", "upgrade_orders_need_review", "shop_upgrade_settings_invalid"} {
		if slices.Contains(report.Warnings, w) {
			t.Fatalf("seed database warns %s", w)
		}
	}
	if !report.Shop.UpgradeEnabled || report.Shop.UpgradeMinChargeCents != 100 || len(report.Shop.UpgradeGroupGaps) != 0 {
		t.Fatalf("shop %+v", report.Shop)
	}
	now := e.clock.Now()
	group := model.NodeGroup{ID: uuid.NewString(), Name: "轻量专属", Region: "HK", LineType: "direct", RatePermille: 1000, PlanIDs: `["starter"]`, Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := e.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	e.setNow(at(2026, 11, 1, 0))
	u := e.user("upgdoc", "user")
	e.buy(u, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	u = e.relogin(u)
	if q := e.quoteOK(u, "pro"); !slices.Equal(q.LostGroups, []string{"轻量专属"}) {
		t.Fatalf("lost groups %v", q.LostGroups)
	}
	out, g := e.upgradeOrder(u, "pro")
	e.db.Model(&model.Subscription{}).Where("user_id = ?", u.ID).Update("expires_at", at(2026, 11, 30, 0))
	e.want(e.pay(g), 200)
	if o := e.order(out.Order.ID); o.Status != model.OrderReview {
		t.Fatalf("stale upgrade %s", o.Status)
	}
	report = inspect()
	if !slices.Contains(report.Warnings, "plans_upgrade_group_gap") || !slices.Contains(report.Shop.UpgradeGroupGaps, "starter>pro:轻量专属") || !slices.Contains(report.Shop.UpgradeGroupGaps, "starter>max:轻量专属") ||
		!slices.Contains(report.Warnings, "upgrade_orders_need_review") || report.Shop.UpgradeReviewOrders != 1 {
		t.Fatalf("doctor %+v %v", report.Shop, report.Warnings)
	}
}
