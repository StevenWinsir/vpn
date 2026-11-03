package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// Round 3 of handoff_plans.md (R3-04): the security review of mid-period upgrades, kept as
// executable checks. The pricing invariants of section 6.8 are proved by the 2000 random
// trajectories of upgrade_invariant_test.go, the older binary by
// TestUpgradeOrderRefusedByOlderFulfilment and the release rehearsal (the real dbe75ca
// binary), impersonation by console_impersonation_test.go; the pieces below are the ones
// only a review of the whole feature asks for. Instants are those of example 1 (6.6):
// starter bought 2026-11-01 00:00 for 19.90, upgraded on 11-11 10:00 for 14.00.

func (e *consoleEnv) ordersOf(u payUser) int64 {
	e.t.Helper()
	var n int64
	if err := e.db.Model(&model.Order{}).Where("user_id = ?", u.ID).Count(&n).Error; err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *consoleEnv) repricePro(cents, traffic int64, devices int) {
	e.t.Helper()
	if err := e.db.Model(&model.Plan{}).Where("id = ?", "pro").Updates(map[string]any{"price_cents": cents, "traffic_bytes": traffic, "max_devices": devices}).Error; err != nil {
		e.t.Fatal(err)
	}
}

// Tampering with the money: the request never carries a price, unknown fields are refused,
// the expected amount only ever refuses, a coupon cannot be smuggled in, and a repricing
// between the quote and the order or between the order and the payment cannot change what
// is paid or what is delivered.
func TestUpgradeSecurityMoney(t *testing.T) {
	e := newConsoleEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	a := e.user("secmoney", "user")
	e.buy(a, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	a = e.relogin(a)
	e.admin = e.relogin(e.admin)
	q := e.quoteOK(a, "pro")
	wantQuote(t, q, 20, 19, 2660, 1260, 1400, q.TrafficAddBytes)
	before := e.ordersOf(a)

	// Fields an attacker might add to set the price, the credit, the cycle, the owner or a coupon:
	// the strict decoder refuses each of them, so nothing is placed.
	for _, field := range []string{"amount_cents", "price_cents", "list_price_cents", "credit_cents", "original_cents", "upgrade_days", "coupon_code", "billing_cycle", "user_id", "from_plan_id"} {
		body := map[string]any{"plan_id": "pro", "pay_type": "alipay", "expected_amount_cents": q.AmountCents, field: 1}
		if w := e.call(a.cookies, "POST", "/api/v1/payments/upgrade-orders", body, uuid.NewString()); w.Code != 400 {
			t.Fatalf("%s accepted: %d %s", field, w.Code, w.Body.String())
		}
	}
	// The expected amount only refuses: below, above, missing, zero or negative.
	e.code(e.placeUpgrade(a, "pro", "alipay", 1, uuid.NewString()), 409, "price_changed")
	e.code(e.placeUpgrade(a, "pro", "alipay", q.AmountCents+1, uuid.NewString()), 409, "price_changed")
	e.code(e.placeUpgrade(a, "pro", "alipay", 0, uuid.NewString()), 400, "invalid_expected_amount")
	e.code(e.placeUpgrade(a, "pro", "alipay", -1400, uuid.NewString()), 400, "invalid_expected_amount")
	e.code(e.call(a.cookies, "POST", "/api/v1/payments/upgrade-orders", map[string]any{"plan_id": "pro", "pay_type": "alipay"}, uuid.NewString()), 400, "invalid_expected_amount")
	if n := e.ordersOf(a); n != before {
		t.Fatalf("refused requests left %d orders", n-before)
	}

	// A coupon on the ordinary purchase path cannot buy the higher plan during the period.
	e.decode(e.post("/coupons", map[string]any{"code": "SECHALF", "discount_type": "percent", "percent_off": 50, "plan_ids": []string{"pro"},
		"max_uses": 5, "per_user_limit": 1, "enabled": true, "version": 0}), 201, nil)
	w := e.call(a.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "alipay", "coupon_code": "SECHALF"}, uuid.NewString())
	if code, message, _ := errorOf(w); w.Code != 409 || code != "plan_change_not_supported" || !strings.Contains(message, "升级") {
		t.Fatalf("coupon purchase during the period: %d %s", w.Code, w.Body.String())
	}
	if n := e.ordersOf(a); n != before {
		t.Fatal("the coupon purchase placed an order")
	}

	// Repriced between the quote and the order: refused, and the next quote is the new price.
	e.repricePro(4990, 300*gib, 5)
	e.code(e.placeUpgrade(a, "pro", "alipay", q.AmountCents, uuid.NewString()), 409, "price_changed")
	if q2 := e.quoteOK(a, "pro"); q2.ValueCents != 3327 || q2.CreditCents != 1260 || q2.AmountCents != 2067 {
		t.Fatalf("quote after repricing %+v", q2)
	}
	e.repricePro(3990, 300*gib, 5)

	// Repriced between the order and the payment: the order is paid and delivered as sold.
	out, g := e.upgradeOrder(a, "pro")
	placed := e.order(out.Order.ID)
	e.repricePro(9990, 900*gib, 9)
	limit := e.subscription(a.ID).TrafficLimitBytes
	e.want(e.pay(g), 200)
	paid := e.order(out.Order.ID)
	sub := e.subscription(a.ID)
	if paid.Status != model.OrderPaid || *paid.PaidCents != 1400 || paid.UpgradeTrafficBytes != placed.UpgradeTrafficBytes ||
		sub.TrafficLimitBytes != limit+placed.UpgradeTrafficBytes || sub.MaxDevices != 5 || sub.PlanID != "pro" {
		t.Fatalf("delivered %+v / %+v (sold %+v)", paid, sub, placed)
	}
	e.repricePro(3990, 300*gib, 5)
	// What an older binary would read from the order: nothing to sell (it can only refuse, R1-09).
	if paid.DurationDays != 0 || paid.TrafficBytes != 0 || paid.QuotaCycleDays != 0 || paid.QuotaCycleBytes != 0 || paid.CouponID != nil || paid.DiscountCents != 0 {
		t.Fatalf("an upgrade order sells something to an older reader: %+v", paid)
	}
}

// Callbacks: an underpaid or untrusted callback never upgrades; a repeated one upgrades once.
func TestUpgradeSecurityCallbacks(t *testing.T) {
	e := newConsoleEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	users := map[string]payUser{}
	for _, name := range []string{"secshort", "secsource", "secrepeat"} {
		users[name] = e.user(name, "user")
		e.buy(users[name], "starter", "", "")
	}
	e.setNow(at(2026, 11, 11, 10))
	for name, u := range users {
		users[name] = e.relogin(u)
	}
	unchanged := func(u payUser, before *model.Subscription) {
		t.Helper()
		if after := e.subscription(u.ID); after.PlanID != "starter" || after.Version != before.Version || after.TrafficLimitBytes != before.TrafficLimitBytes {
			t.Fatalf("the subscription moved: %+v", after)
		}
	}

	u := users["secshort"]
	before := e.subscription(u.ID)
	out, g := e.upgradeOrder(u, "pro")
	short := *g
	short.Cents = g.Cents - 1
	e.gw.markPaid(g)
	e.want(e.notify(gatewayIP, callbackBody(&short, payment.TradeSuccess)), 200)
	if o := e.order(out.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewAmountMismatch {
		t.Fatalf("underpaid: %s %s", o.Status, o.ReviewReason)
	}
	unchanged(u, before)

	u = users["secsource"]
	before = e.subscription(u.ID)
	out, g = e.upgradeOrder(u, "pro")
	e.gw.markPaid(g)
	e.notify("198.51.100.7", callbackBody(g, payment.TradeSuccess))
	if o, n := e.order(out.Order.ID), e.lastNotification(); o.Status != model.OrderPending || n.Result != model.NotifyIPRejected {
		t.Fatalf("a callback from outside the gateway's addresses: logged %s, order %s %s", n.Result, o.Status, o.ReviewReason)
	}
	unchanged(u, before)

	u = users["secrepeat"]
	out, g = e.upgradeOrder(u, "pro")
	e.want(e.pay(g), 200)
	once := e.subscription(u.ID)
	for range 2 {
		e.notify(gatewayIP, callbackBody(g, payment.TradeSuccess))
	}
	if again := e.subscription(u.ID); again.Version != once.Version || again.TrafficLimitBytes != once.TrafficLimitBytes || e.order(out.Order.ID).Status != model.OrderPaid {
		t.Fatalf("a repeated callback upgraded again: %+v / %+v", again, once)
	}
}

// Ownership and requests: another account's idempotency key, order or quote gives nothing
// away; every upgrade write needs a session, a trusted Origin and JSON; a disabled account
// cannot upgrade.
func TestUpgradeSecurityOwnership(t *testing.T) {
	e := newConsoleEnv(t, nil)
	e.setNow(at(2026, 11, 1, 0))
	a, b := e.user("secowner", "user"), e.user("secother", "user")
	e.buy(a, "starter", "", "")
	e.buy(b, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	a, b = e.relogin(a), e.relogin(b)

	key := uuid.NewString()
	q := e.quoteOK(a, "pro")
	w := e.placeUpgrade(a, "pro", "alipay", q.AmountCents, key)
	var mine placed
	e.decode(w, 201, &mine)
	// The same key from another account is that account's own request, never a replay of a's.
	var theirs placed
	e.decode(e.placeUpgrade(b, "pro", "alipay", q.AmountCents, key), 201, &theirs)
	if theirs.Order.ID == mine.Order.ID || e.order(theirs.Order.ID).UserID != b.ID {
		t.Fatalf("a key replayed another account's order: %+v", theirs.Order)
	}
	// a's order is invisible to b and byte-identical to a missing one.
	missing := e.call(b.cookies, "GET", "/api/v1/payments/orders/"+uuid.NewString(), nil, "")
	stranger := e.call(b.cookies, "GET", "/api/v1/payments/orders/"+mine.Order.ID, nil, "")
	if stranger.Code != 404 || stranger.Body.String() != missing.Body.String() {
		t.Fatalf("another account's order: %d %s", stranger.Code, stranger.Body.String())
	}
	if list := e.call(b.cookies, "GET", "/api/v1/orders", nil, ""); strings.Contains(list.Body.String(), mine.Order.ID) {
		t.Fatal("another account's order is listed")
	}
	// A quote is always the caller's own: b's quote does not depend on a's pending order.
	if qb, _ := e.upgradeQuote(b, "pro"); qb.AmountCents != q.AmountCents {
		t.Fatalf("b's quote %+v", qb)
	}
	// The customer-facing order never carries the computation, the window or the subscription.
	body := e.call(a.cookies, "GET", "/api/v1/payments/orders/"+mine.Order.ID, nil, "").Body.String()
	for _, leak := range []string{"upgrade_snapshot", "covers_", "subscription_id", "from_plan_id", "contributions", "idempotency"} {
		if strings.Contains(body, leak) {
			t.Fatalf("order view leaks %s: %s", leak, body)
		}
	}

	// Every upgrade write needs a trusted Origin, JSON and a session.
	data, _ := json.Marshal(map[string]any{"plan_id": "pro", "pay_type": "alipay", "expected_amount_cents": q.AmountCents})
	for _, path := range []string{"/api/v1/payments/upgrade-orders", "/api/v1/payments/upgrade-quote"} {
		e.code(rawTicketCall(e, a.cookies, path, data, "", "application/json"), 403, "origin_required")
		e.code(rawTicketCall(e, a.cookies, path, data, "https://evil.example", "application/json"), 403, "origin_forbidden")
		e.code(rawTicketCall(e, a.cookies, path, data, e.cfg.Origins[0], "text/plain"), 415, "json_required")
		if w := rawTicketCall(e, nil, path, data, e.cfg.Origins[0], "application/json"); w.Code != 401 {
			t.Fatalf("%s without a session: %d", path, w.Code)
		}
	}
	if w := e.call(nil, "GET", "/api/v1/payments/upgrade-options", nil, ""); w.Code != 401 {
		t.Fatalf("options without a session: %d", w.Code)
	}

	// A disabled account cannot upgrade (its session ends with it).
	c := e.user("secdisabled", "user")
	e.setNow(at(2026, 11, 1, 0))
	c = e.relogin(c)
	e.buy(c, "starter", "", "")
	e.setNow(at(2026, 11, 11, 10))
	c = e.relogin(c)
	if err := e.db.Model(&model.User{}).Where("id = ?", c.ID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if w := e.placeUpgrade(c, "pro", "alipay", q.AmountCents, uuid.NewString()); w.Code != 401 && w.Code != 403 {
		t.Fatalf("a disabled account placed an upgrade: %d %s", w.Code, w.Body.String())
	}
	if n := e.ordersOf(c); n != 1 {
		t.Fatalf("a disabled account has %d orders", n)
	}
}
