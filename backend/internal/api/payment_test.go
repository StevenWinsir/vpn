package api

import (
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// R1-03: quote -> order -> gateway page -> callback -> paid and fulfilled; a repeated
// callback changes nothing; the URL follows the documented shape.
func TestPaymentHappyPath(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("buyer", "user")
	quote := e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter"}, ""), 200)
	q := quote["quote"].(map[string]any)
	if q["amount_cents"].(float64) != 1990 || q["kind"] != "new" || q["purchasable"] != true || q["test_price"] != false {
		t.Fatalf("quote %v", q)
	}
	for _, kind := range []string{payment.TypeWeChat, payment.TypeAlipay} {
		e := newPayEnv(t, nil)
		u := e.user("buyer-"+kind, "user")
		p := e.place(u, "starter", kind, "")
		if p.Order.Status != model.OrderPending || p.Order.TradeNo == nil || !payment.ValidTradeNo(*p.Order.TradeNo) || p.Order.AmountCents != 1990 || p.Order.PriceSource != priceList || p.Order.PaidAt != nil {
			t.Fatalf("placed %+v", p.Order)
		}
		parsed, _ := url.Parse(p.PayURL)
		v := parsed.Query()
		if v.Get("type") != kind || v.Get("money") != "19.90" || v.Get("channel") != gatewayName || v.Get("sign") != payment.Sign(*p.Order.TradeNo, e.cfg.Payment.Secret) ||
			v.Get("notify_url") != e.cfg.Payment.NotifyURL || v.Get("return_url") != e.cfg.WebsiteBaseURL+"/pay/result?order="+p.Order.ID || v.Get("name") != "轻量计划" {
			t.Fatalf("pay URL parameters %v", v)
		}
		gw, status := e.gw.open(t, p.PayURL)
		if status != 200 || gw.Cents != 1990 {
			t.Fatalf("gateway refused the order: %d", status)
		}
		e.want(e.pay(gw), 200)
		paid := e.order(p.Order.ID)
		if paid.Status != model.OrderPaid || paid.PaidAt == nil || paid.PaidCents == nil || *paid.PaidCents != 1990 {
			t.Fatalf("order not paid: %+v", paid)
		}
		sub := e.subscription(u.ID)
		if sub == nil || sub.PlanID != "starter" || sub.IsTest || sub.TrafficLimitBytes != 100*gib || !sub.ExpiresAt.Equal(sub.StartsAt.UTC().AddDate(0, 0, 30)) {
			t.Fatalf("subscription %+v", sub)
		}
		if n := e.lastNotification(); n.Result != model.NotifyAccepted || n.OrderID == nil || *n.OrderID != p.Order.ID || n.SourceIP != gatewayIP {
			t.Fatalf("notification %+v", n)
		}
		e.want(e.notify(gatewayIP, callbackBody(gw, payment.TradeSuccess)), 200)
		if again := e.subscription(u.ID); again.TrafficLimitBytes != sub.TrafficLimitBytes || !again.ExpiresAt.Equal(sub.ExpiresAt) || e.lastNotification().Result != model.NotifyDuplicate {
			t.Fatal("duplicate callback fulfilled twice")
		}
		view := e.want(e.call(u.cookies, "GET", "/api/v1/payments/orders/"+p.Order.ID, nil, ""), 200)
		if view["order"].(map[string]any)["status"] != model.OrderPaid {
			t.Fatalf("order view %v", view)
		}
		var listed struct {
			Orders []model.Order `json:"orders"`
		}
		_ = json.Unmarshal(e.call(u.cookies, "GET", "/api/v1/orders", nil, "").Body.Bytes(), &listed)
		if len(listed.Orders) != 1 || listed.Orders[0].Status != model.OrderPaid || strings.Contains(e.call(u.cookies, "GET", "/api/v1/orders", nil, "").Body.String(), "review_reason") {
			t.Fatal("order list wrong or leaks internal fields")
		}
	}
}

// D6: renewal keeps the period start; switching plans while valid is refused; a paid
// purchase replaces a valid test subscription with a new period.
func TestPaymentRenewalRules(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("renew", "user")
	first, _ := e.gw.open(t, e.place(u, "starter", payment.TypeWeChat, "").PayURL)
	e.want(e.pay(first), 200)
	before := e.subscription(u.ID)
	if err := e.db.Model(&model.Subscription{}).Where("id = ?", before.ID).Update("used_units", 5000).Error; err != nil {
		t.Fatal(err)
	}
	q := e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter"}, ""), 200)["quote"].(map[string]any)
	if q["kind"] != "renew" {
		t.Fatalf("renew quote %v", q)
	}
	e.clock.Advance(time.Hour)
	second, _ := e.gw.open(t, e.place(u, "starter", payment.TypeAlipay, "").PayURL)
	e.want(e.pay(second), 200)
	after := e.subscription(u.ID)
	if !after.StartsAt.Equal(before.StartsAt) || !after.ExpiresAt.Equal(before.ExpiresAt.UTC().AddDate(0, 0, 30)) || after.TrafficLimitBytes != 200*gib || after.UsedUnits != 5000 {
		t.Fatalf("renewal %+v from %+v", after, before)
	}
	q = e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "pro"}, ""), 200)["quote"].(map[string]any)
	if q["purchasable"] != false || q["reason"] != "plan_change_not_supported" {
		t.Fatalf("plan change quote %v", q)
	}
	if code := payErrorCode(e.want(e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay"}, uuid.NewString()), 409)); code != "plan_change_not_supported" {
		t.Fatal(code)
	}

	tester := e.user("tester", "user")
	now := e.clock.Now()
	test := model.Subscription{ID: uuid.NewString(), UserID: tester.ID, PlanID: "pro", PlanName: "进阶计划", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(29 * 24 * time.Hour), TrafficLimitBytes: 300 * gib, UsedUnits: 777, MaxDevices: 5, AllowDedicated: true, IsTest: true}
	if err := e.db.Omit("User", "Plan").Create(&test).Error; err != nil {
		t.Fatal(err)
	}
	gw, _ := e.gw.open(t, e.place(tester, "starter", payment.TypeWeChat, "").PayURL)
	e.want(e.pay(gw), 200)
	replaced := e.subscription(tester.ID)
	if replaced.ID != test.ID || replaced.IsTest || replaced.PlanID != "starter" || !replaced.StartsAt.Equal(now) || replaced.UsedUnits != 0 || replaced.TrafficLimitBytes != 100*gib || replaced.AllowDedicated {
		t.Fatalf("test subscription not replaced: %+v", replaced)
	}
}

// D3/R1-08: several pending orders at once; the gateway shifts equal amounts by 0.01
// and each callback settles its own order. A second plan bought while the first is
// valid goes to review instead of being applied.
func TestPaymentMultiplePendingOrders(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("many", "user")
	var orders []placed
	var gws []*fakeGatewayOrder
	for i := range 3 {
		p := e.place(u, "starter", payment.TypeWeChat, "")
		if p.Others != i {
			t.Fatalf("order %d reports %d other pending", i, p.Others)
		}
		gw, status := e.gw.open(t, p.PayURL)
		if status != 200 || gw.Cents != 1990+int64(i) {
			t.Fatalf("gateway amount for order %d: %+v", i, gw)
		}
		orders, gws = append(orders, p), append(gws, gw)
	}
	// Alipay amounts are deduplicated separately.
	if gw, _ := e.gw.open(t, e.place(u, "starter", payment.TypeAlipay, "").PayURL); gw.Cents != 1990 {
		t.Fatalf("alipay amount %d", gw.Cents)
	}
	e.want(e.pay(gws[2]), 200)
	e.want(e.pay(gws[1]), 200)
	for _, i := range []int{1, 2} {
		o := e.order(orders[i].Order.ID)
		if o.Status != model.OrderPaid || *o.PaidCents != 1990+int64(i) {
			t.Fatalf("order %d: %+v", i, o)
		}
	}
	if sub := e.subscription(u.ID); sub.TrafficLimitBytes != 200*gib || !sub.ExpiresAt.Equal(sub.StartsAt.UTC().AddDate(0, 0, 60)) {
		t.Fatalf("two paid orders did not stack: %+v", sub)
	}
	if e.order(orders[0].Order.ID).Status != model.OrderPending {
		t.Fatal("unpaid order changed")
	}

	other := e.user("two-plans", "user")
	a, _ := e.gw.open(t, e.place(other, "starter", payment.TypeWeChat, "").PayURL)
	bPlaced := e.place(other, "pro", payment.TypeWeChat, "")
	b, _ := e.gw.open(t, bPlaced.PayURL)
	e.want(e.pay(a), 200)
	e.want(e.pay(b), 200)
	if o := e.order(bPlaced.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewStateConflict || *o.PaidCents != 3990 {
		t.Fatalf("conflicting plan: %+v", o)
	}
	if sub := e.subscription(other.ID); sub.PlanID != "starter" || sub.TrafficLimitBytes != 100*gib {
		t.Fatalf("conflicting plan changed the subscription: %+v", sub)
	}
}

// R1-04: tampering with the amount, forged callbacks and an empty source list.
func TestPaymentTamperingAndForgery(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("tamper", "user")
	p := e.place(u, "pro", payment.TypeWeChat, "")
	tampered := strings.Replace(p.PayURL, "money=39.90", "money=0.01", 1)
	// The signature covers trade_no only, so the gateway accepts the edited amount...
	gw, status := e.gw.open(t, tampered)
	if status != 200 || gw.Cents != 1 {
		t.Fatalf("tampered submit %d %+v", status, gw)
	}
	// ...and the genuine URL can no longer be used for that trade_no.
	if _, status = e.gw.open(t, p.PayURL); status != 400 {
		t.Fatalf("reused trade_no answered %d", status)
	}
	e.want(e.pay(gw), 200)
	if o := e.order(p.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewAmountMismatch || *o.PaidCents != 1 {
		t.Fatalf("underpaid order: %+v", o)
	}
	if e.subscription(u.ID) != nil {
		t.Fatal("underpaid order fulfilled")
	}

	forged := e.place(u, "pro", payment.TypeWeChat, "")
	body := `{"trade_no":"` + *forged.Order.TradeNo + `","name":"进阶计划","money":39.90,"trade_status":"TRADE_SUCCESS"}`
	e.want(e.notify("198.51.100.1", body), 200)
	if e.order(forged.Order.ID).Status != model.OrderPending || e.lastNotification().Result != model.NotifyIPRejected || e.lastNotification().SourceIP != "198.51.100.1" {
		t.Fatal("callback from an unknown source was trusted")
	}
	// A forged X-Forwarded-For cannot change the source (no trusted proxies configured).
	forgedHeader := e.notifyWith(gatewayIP+"0", body, map[string]string{"X-Forwarded-For": gatewayIP})
	if forgedHeader.Code != 200 || e.lastNotification().Result != model.NotifyIPRejected || e.order(forged.Order.ID).Status != model.OrderPending {
		t.Fatal("X-Forwarded-For from an untrusted peer was believed")
	}
	low := `{"trade_no":"` + *forged.Order.TradeNo + `","name":"进阶计划","money":39.89,"trade_status":"TRADE_SUCCESS"}`
	e.want(e.notify(gatewayIP, low), 200)
	if o := e.order(forged.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewAmountMismatch {
		t.Fatalf("underpaying callback: %+v", o)
	}
	over := e.place(u, "pro", payment.TypeWeChat, "")
	e.want(e.notify(gatewayIP, `{"trade_no":"`+*over.Order.TradeNo+`","name":"x","money":40.41,"trade_status":"TRADE_SUCCESS"}`), 200)
	if o := e.order(over.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewAmountMismatch {
		t.Fatalf("amount above the tolerance: %+v", o)
	}
	within := e.place(u, "pro", payment.TypeWeChat, "")
	e.want(e.notify(gatewayIP, `{"trade_no":"`+*within.Order.TradeNo+`","name":"x","money":40.40,"trade_status":"TRADE_SUCCESS"}`), 200)
	if o := e.order(within.Order.ID); o.Status != model.OrderPaid || *o.PaidCents != 4040 {
		t.Fatalf("amount within the tolerance: %+v", o)
	}

	open := newPayEnv(t, func(c *config.Config) { c.Payment.NotifyAllowed = nil })
	v := open.user("unverified", "user")
	gw2, _ := open.gw.open(t, open.place(v, "starter", payment.TypeAlipay, "").PayURL)
	open.want(open.pay(gw2), 200)
	n := open.lastNotification()
	if o := open.order(*n.OrderID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewUnverifiedSource || n.SourceIP != gatewayIP || open.subscription(v.ID) != nil {
		t.Fatalf("empty source list fulfilled automatically: %+v", o)
	}
}

// R1-05: malformed callbacks never touch an order and are always logged.
func TestPaymentCallbackEdges(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("edges", "user")
	p := e.place(u, "starter", payment.TypeWeChat, "")
	trade := *p.Order.TradeNo
	bodies := []string{
		``, `not json`, `[]`, `{}`,
		`{"trade_no":"` + trade + `","name":"x","money":19.901,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"` + trade + `","name":"x","money":-19.90,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"` + trade + `","name":"x","money":"lots","trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"` + trade + `","name":"x","money":1e30,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"` + trade + `","name":"x","money":19.90,"trade_status":"PAID"}`,
		`{"trade_no":"` + trade + `","money":19.90,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"` + trade + `","name":"x","money":19.90,"trade_status":"TRADE_SUCCESS"}{"again":1}`,
		`{"trade_no":"` + strings.Repeat("9", 5000) + `"}`,
	}
	for _, body := range bodies {
		e.want(e.notify(gatewayIP, body), 200)
		if n := e.lastNotification(); n.Result != model.NotifyInvalid {
			t.Fatalf("%.60s: result %s", body, n.Result)
		}
	}
	e.want(e.notify(gatewayIP, `{"trade_no":"20000101000000000000","name":"x","money":19.90,"trade_status":"TRADE_SUCCESS"}`), 200)
	if e.lastNotification().Result != model.NotifyUnknownOrder {
		t.Fatal("unknown trade_no")
	}
	var count int64
	e.db.Model(&model.PaymentNotification{}).Count(&count)
	if count != int64(len(bodies)+1) {
		t.Fatalf("logged %d callbacks", count)
	}
	if o := e.order(p.Order.ID); o.Status != model.OrderPending || o.PaidCents != nil {
		t.Fatalf("malformed callbacks changed the order: %+v", o)
	}
	// TRADE_FAILED fails a pending order; a later genuine success still settles it.
	gw, _ := e.gw.open(t, p.PayURL)
	e.want(e.notify(gatewayIP, callbackBody(gw, payment.TradeFailed)), 200)
	if e.order(p.Order.ID).Status != model.OrderFailed {
		t.Fatal("TRADE_FAILED ignored")
	}
	e.want(e.pay(gw), 200)
	if e.order(p.Order.ID).Status != model.OrderPaid || e.subscription(u.ID) == nil {
		t.Fatal("late success after failure not honoured")
	}
	// A disabled account's payment waits for review.
	d := e.user("disabled", "user")
	dp := e.place(d, "starter", payment.TypeWeChat, "")
	dgw, _ := e.gw.open(t, dp.PayURL)
	e.db.Model(&model.User{}).Where("id = ?", d.ID).Update("status", "disabled")
	e.want(e.pay(dgw), 200)
	if o := e.order(dp.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewStateConflict {
		t.Fatalf("disabled account: %+v", o)
	}
}

// R1-06: concurrent callbacks, idempotent orders and the last coupon use.
func TestPaymentConcurrency(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("racer", "user")
	p := e.place(u, "max", payment.TypeWeChat, "")
	gw, _ := e.gw.open(t, p.PayURL)
	e.gw.markPaid(gw)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := e.notify(gatewayIP, callbackBody(gw, payment.TradeSuccess)); w.Code != 200 {
				t.Errorf("callback %d", w.Code)
			}
		}()
	}
	wg.Wait()
	var accepted, duplicate int64
	e.db.Model(&model.PaymentNotification{}).Where("result = ?", model.NotifyAccepted).Count(&accepted)
	e.db.Model(&model.PaymentNotification{}).Where("result = ?", model.NotifyDuplicate).Count(&duplicate)
	if sub := e.subscription(u.ID); accepted != 1 || duplicate != 19 || sub.TrafficLimitBytes != 800*gib {
		t.Fatalf("accepted %d duplicate %d sub %+v", accepted, duplicate, sub)
	}

	key := uuid.NewString()
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "max", "pay_type": "alipay"}, key)
			var out placed
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			if w.Code != 201 && w.Code != 200 {
				t.Errorf("idempotent order %d %s", w.Code, w.Body.String())
			}
			ids <- out.Order.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("one Idempotency-Key produced two orders")
		}
	}
	if code := payErrorCode(e.want(e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "max", "pay_type": "wxpay"}, key), 409)); code != "idempotency_conflict" {
		t.Fatal(code)
	}

	last := e.coupon(model.Coupon{Code: "LASTONE", DiscountType: model.DiscountPercent, PercentOff: 10, MaxUses: 1, Enabled: true})
	a, b := e.user("coupon-a", "user"), e.user("coupon-b", "user")
	codes := make(chan int, 2)
	for _, buyer := range []payUser{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- e.call(buyer.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay", "coupon_code": "lastone"}, uuid.NewString()).Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	var reserved int64
	livePending(e.db.Model(&model.Order{}), e.clock.Now()).Where("coupon_id = ?", last.ID).Count(&reserved)
	if got[201] != 1 || got[409] != 1 || reserved != 1 {
		t.Fatalf("last coupon use: %v reserved %d", got, reserved)
	}
}

// R1-07: reconciliation after the local window.
func TestPaymentReconciliation(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("recon", "user")
	coupon := e.coupon(model.Coupon{Code: "ONCE-ONLY", DiscountType: model.DiscountAmount, AmountOffCents: 500, MaxUses: 1, Enabled: true})
	timedOut := e.place(u, "starter", payment.TypeWeChat, "ONCE-ONLY")
	if timedOut.Order.AmountCents != 1490 || timedOut.Order.CouponCode != "ONCE-ONLY" {
		t.Fatalf("coupon order %+v", timedOut.Order)
	}
	e.gw.open(t, timedOut.PayURL)
	paidNoCallback := e.place(u, "starter", payment.TypeWeChat, "")
	pgw, _ := e.gw.open(t, paidNoCallback.PayURL)
	e.gw.markPaid(pgw)
	unreachable := e.place(u, "starter", payment.TypeAlipay, "")
	e.gw.open(t, unreachable.PayURL)
	other := e.user("waiting", "user")
	q := e.want(e.call(other.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter", "coupon_code": "ONCE-ONLY"}, ""), 200)["quote"].(map[string]any)
	if c := q["coupon"].(map[string]any); c["applied"] != false || c["reason"] != "coupon_exhausted" {
		t.Fatalf("reserved coupon offered again: %v", c)
	}
	if e.reconcile() != 0 || e.gw.calls() != 0 {
		t.Fatal("orders inside their window were reconciled")
	}
	e.clock.Advance(181 * time.Second)
	// Make one order's gateway unreachable by failing every status call for one pass.
	e.gw.statusFailure = true
	if e.reconcile() != 0 {
		t.Fatal("unreachable gateway settled an order")
	}
	e.gw.statusFailure = false
	if calls := e.gw.calls(); e.reconcile() != 0 || e.gw.calls() != calls {
		t.Fatal("orders were queried again within the recheck interval")
	}
	e.clock.Advance(31 * time.Second)
	if e.reconcile() != 3 {
		t.Fatal("orders not settled")
	}
	if o := e.order(timedOut.Order.ID); o.Status != model.OrderExpired {
		t.Fatalf("timed out: %+v", o)
	}
	if o := e.order(paidNoCallback.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewCallbackMissing || e.subscription(u.ID) != nil {
		t.Fatalf("paid without callback must wait for review: %+v", o)
	}
	if o := e.order(unreachable.Order.ID); o.Status != model.OrderExpired {
		t.Fatalf("third order: %+v", o)
	}
	// The expired order's coupon reservation is released.
	q = e.want(e.call(other.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter", "coupon_code": "ONCE-ONLY"}, ""), 200)["quote"].(map[string]any)
	if c := q["coupon"].(map[string]any); c["applied"] != true || q["amount_cents"].(float64) != 1490 {
		t.Fatalf("released coupon: %v", q)
	}
	// A genuine callback that arrives late settles a review(callback_missing) order.
	e.want(e.notify(gatewayIP, callbackBody(pgw, payment.TradeSuccess)), 200)
	if o := e.order(paidNoCallback.Order.ID); o.Status != model.OrderPaid || e.subscription(u.ID) == nil {
		t.Fatalf("late callback: %+v", o)
	}
	// ...and one for an expired order is honoured, counting the coupon use.
	tgw := e.gw.orders[*timedOut.Order.TradeNo]
	e.want(e.pay(tgw), 200)
	var used model.Coupon
	e.db.First(&used, "id = ?", coupon.ID)
	if o := e.order(timedOut.Order.ID); o.Status != model.OrderPaid || used.UsedCount != 1 {
		t.Fatalf("late payment of an expired order: %+v used %d", o, used.UsedCount)
	}

	// An order the gateway never answers for goes to review after 30 minutes.
	stuck := e.place(other, "starter", payment.TypeWeChat, "")
	e.gw.open(t, stuck.PayURL)
	e.gw.statusFailure = true
	e.clock.Advance(4 * time.Minute)
	e.reconcile()
	if e.order(stuck.Order.ID).Status != model.OrderPending {
		t.Fatal("gave up too early")
	}
	e.clock.Advance(30 * time.Minute)
	e.reconcile()
	if o := e.order(stuck.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewGatewayUnreachable {
		t.Fatalf("unreachable gateway: %+v", o)
	}
}

// D3: caps on live orders and on order attempts.
func TestPaymentLimits(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("limits", "user")
	for range 5 {
		e.place(u, "starter", payment.TypeWeChat, "")
	}
	w := e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString())
	if payErrorCode(e.want(w, 429)) != "too_many_pending_orders" || w.Header().Get("Retry-After") == "" {
		t.Fatal("pending cap")
	}
	e.clock.Advance(181 * time.Second)
	e.place(u, "starter", payment.TypeWeChat, "")
	last := 0
	for range 20 {
		last = e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString()).Code
	}
	if last != 429 || payErrorCode(e.want(e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString()), 429)) != "order_rate_limited" {
		t.Fatal("order attempt limit")
	}
	for _, bad := range []map[string]any{{"plan_id": "starter", "pay_type": "paypal"}, {"plan_id": "starter"}, {"plan_id": "", "pay_type": "wxpay"}, {"plan_id": "starter", "pay_type": "wxpay", "price_cents": 1}, {"plan_id": "starter", "pay_type": "wxpay", "amount_cents": 1}} {
		if code := e.call(u.cookies, "POST", "/api/v1/payments/orders", bad, uuid.NewString()).Code; code != 400 && code != 429 {
			t.Fatalf("bad order %v answered %d", bad, code)
		}
	}
	v := e.user("limits-2", "user")
	e.want(e.call(v.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, ""), 400)
	e.want(e.call(v.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "missing", "pay_type": "wxpay"}, uuid.NewString()), 404)
	e.want(e.call(nil, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString()), 401)
	e.want(e.call(nil, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter"}, ""), 401)
}

// R1-09: the test price applies to listed accounts only and is marked on the order.
func TestPaymentTestPrice(t *testing.T) {
	e := newPayEnv(t, func(c *config.Config) {
		c.Payment.TestPrice, c.Payment.TestPriceCents, c.Payment.TestEmails = true, 1, []string{"owner@example.com"}
	})
	owner, customer := e.user("owner", "user"), e.user("customer", "user")
	q := e.want(e.call(owner.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "max"}, ""), 200)["quote"].(map[string]any)
	if q["test_price"] != true || q["amount_cents"].(float64) != 1 || q["list_price_cents"].(float64) != 7990 {
		t.Fatalf("owner quote %v", q)
	}
	p := e.place(owner, "max", payment.TypeWeChat, "")
	if p.Order.AmountCents != 1 || p.Order.ListPriceCents != 7990 || p.Order.PriceSource != priceTest || !strings.Contains(p.PayURL, "money=0.01") {
		t.Fatalf("test price order %+v", p.Order)
	}
	gw, _ := e.gw.open(t, p.PayURL)
	e.want(e.pay(gw), 200)
	if sub := e.subscription(owner.ID); sub == nil || sub.IsTest || sub.PlanID != "max" {
		t.Fatalf("test price subscription %+v", sub)
	}
	if c := e.place(customer, "max", payment.TypeWeChat, ""); c.Order.AmountCents != 7990 || c.Order.PriceSource != priceList {
		t.Fatalf("unlisted account got the test price: %+v", c.Order)
	}
	// A coupon on top of the test price never goes below 0.01.
	e.coupon(model.Coupon{Code: "HALF", DiscountType: model.DiscountPercent, PercentOff: 50, Enabled: true})
	q = e.want(e.call(owner.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "max", "coupon_code": "HALF"}, ""), 200)["quote"].(map[string]any)
	if q["amount_cents"].(float64) != 1 || q["discount_cents"].(float64) != 0 {
		t.Fatalf("coupon below the floor: %v", q)
	}
}

// R1-10: coupon arithmetic, applicability, windows, limits and the attempt limit.
func TestPaymentCoupons(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("coupons", "user")
	now := e.clock.Now()
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	e.coupon(model.Coupon{Code: "PCT33", DiscountType: model.DiscountPercent, PercentOff: 33, Enabled: true})
	e.coupon(model.Coupon{Code: "BIGONE", DiscountType: model.DiscountAmount, AmountOffCents: 999999, Enabled: true})
	e.coupon(model.Coupon{Code: "PROONLY", DiscountType: model.DiscountPercent, PercentOff: 10, PlanIDs: `["pro"]`, Enabled: true})
	e.coupon(model.Coupon{Code: "LATER", DiscountType: model.DiscountPercent, PercentOff: 10, StartsAt: &future, Enabled: true})
	e.coupon(model.Coupon{Code: "OVER", DiscountType: model.DiscountPercent, PercentOff: 10, EndsAt: &past, Enabled: true})
	e.coupon(model.Coupon{Code: "OFFLINE", DiscountType: model.DiscountPercent, PercentOff: 10, Enabled: false})
	e.coupon(model.Coupon{Code: "GONE", DiscountType: model.DiscountPercent, PercentOff: 10, Enabled: true, DeletedAt: &past})
	e.coupon(model.Coupon{Code: "TWICE", DiscountType: model.DiscountAmount, AmountOffCents: 100, PerUserLimit: 1, Enabled: true})
	quote := func(plan, code string) map[string]any {
		return e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": plan, "coupon_code": code}, ""), 200)["quote"].(map[string]any)
	}
	if q := quote("starter", " pct33 "); q["discount_cents"].(float64) != 656 || q["amount_cents"].(float64) != 1334 {
		t.Fatalf("percent rounds down: %v", q)
	}
	if q := quote("starter", "BIGONE"); q["amount_cents"].(float64) != 1 {
		t.Fatalf("amount capped at 0.01: %v", q)
	}
	for code, reason := range map[string]string{"PROONLY": "coupon_not_applicable", "LATER": "coupon_invalid", "OVER": "coupon_invalid", "OFFLINE": "coupon_invalid", "GONE": "coupon_invalid", "NOPE": "coupon_invalid", "x": "coupon_invalid"} {
		q := quote("starter", code)
		if c := q["coupon"].(map[string]any); c["applied"] != false || c["reason"] != reason || q["amount_cents"].(float64) != 1990 {
			t.Fatalf("%s: %v", code, q)
		}
	}
	if q := quote("pro", "PROONLY"); q["amount_cents"].(float64) != 3591 {
		t.Fatalf("plan-scoped coupon: %v", q)
	}
	if code := payErrorCode(e.want(e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay", "coupon_code": "PROONLY"}, uuid.NewString()), 409)); code != "coupon_not_applicable" {
		t.Fatal(code)
	}
	first := e.place(u, "starter", payment.TypeWeChat, "TWICE")
	if code := payErrorCode(e.want(e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay", "coupon_code": "TWICE"}, uuid.NewString()), 409)); code != "coupon_used" {
		t.Fatal("a second pending order bypassed the per-user limit")
	}
	gw, _ := e.gw.open(t, first.PayURL)
	e.want(e.pay(gw), 200)
	if c := quote("starter", "TWICE")["coupon"].(map[string]any); c["reason"] != "coupon_used" {
		t.Fatal("paid use not counted")
	}
	// The attempt limit counts every refused code (8 so far) and then blocks checks.
	limited := 0
	for range 30 {
		w := e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter", "coupon_code": "NOPE-" + uuid.NewString()[:4]}, "")
		if w.Code == 429 {
			limited++
		}
	}
	if limited == 0 || e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter", "coupon_code": "PCT33"}, "").Code != 429 {
		t.Fatal("coupon attempts not limited")
	}
	// Without a code the quote is unaffected by the coupon limit.
	e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter"}, ""), 200)
}

func TestPaymentDisabledAndOrderAccess(t *testing.T) {
	off := newPayEnv(t, func(c *config.Config) { c.Payment = config.Payment{} })
	u := off.user("off", "user")
	meta := off.want(off.call(nil, "GET", "/api/v1/meta", nil, ""), 200)
	if meta["payment_enabled"] != false || len(meta["payment_methods"].([]any)) != 0 {
		t.Fatalf("meta %v", meta)
	}
	for _, path := range []string{"/api/v1/payments/quote", "/api/v1/payments/orders"} {
		if payErrorCode(off.want(off.call(u.cookies, "POST", path, map[string]any{"plan_id": "starter", "pay_type": "wxpay"}, uuid.NewString()), 503)) != "payment_disabled" {
			t.Fatal(path)
		}
	}
	e := newPayEnv(t, nil)
	meta = e.want(e.call(nil, "GET", "/api/v1/meta", nil, ""), 200)
	if meta["payment_enabled"] != true || len(meta["payment_methods"].([]any)) != 2 || meta["payment_pending_seconds"].(float64) != 180 {
		t.Fatalf("meta %v", meta)
	}
	for _, secret := range []string{e.cfg.Payment.Secret, e.cfg.Payment.Channel, "notify", e.gw.server.URL} {
		if strings.Contains(e.call(nil, "GET", "/api/v1/meta", nil, "").Body.String(), secret) {
			t.Fatalf("meta leaked %s", secret)
		}
	}
	owner, stranger := e.user("owner2", "user"), e.user("stranger", "user")
	p := e.place(owner, "starter", payment.TypeWeChat, "")
	e.want(e.call(stranger.cookies, "GET", "/api/v1/payments/orders/"+p.Order.ID, nil, ""), 404)
	e.want(e.call(owner.cookies, "GET", "/api/v1/payments/orders/not-a-uuid", nil, ""), 404)
	gw, _ := e.gw.open(t, p.PayURL)
	e.gw.markPaid(gw)
	view := e.want(e.call(owner.cookies, "GET", "/api/v1/payments/orders/"+p.Order.ID+"?status=paid", nil, ""), 200)["order"].(map[string]any)
	if view["status"] != model.OrderPending || view["gateway_paid"] != true {
		t.Fatalf("result page hint: %v", view)
	}
	if e.order(p.Order.ID).Status != model.OrderPending {
		t.Fatal("the result page fulfilled an order")
	}
	calls := e.gw.calls()
	e.call(owner.cookies, "GET", "/api/v1/payments/orders/"+p.Order.ID, nil, "")
	if e.gw.calls() != calls {
		t.Fatal("result page queried the gateway inside the recheck interval")
	}
}

// The development test purchase still works and now shares the fulfilment path.
func TestTestPurchaseUsesSharedFulfilment(t *testing.T) {
	e := newPayEnv(t, func(c *config.Config) { c.TestPurchase = true })
	u := e.user("mock", "user")
	w := e.call(u.cookies, "POST", "/api/v1/orders/test-purchase", map[string]any{"plan_id": "starter"}, uuid.NewString())
	out := e.want(w, 201)["order"].(map[string]any)
	if out["status"] != model.OrderPaidTest || out["paid_at"] == nil || out["price_source"] != priceTestPurchase {
		t.Fatalf("test purchase %v", out)
	}
	if sub := e.subscription(u.ID); sub == nil || !sub.IsTest {
		t.Fatal("test subscription missing")
	}
	// A paid purchase replaces it; a test purchase cannot replace a paid one.
	gw, _ := e.gw.open(t, e.place(u, "starter", payment.TypeWeChat, "").PayURL)
	e.want(e.pay(gw), 200)
	if payErrorCode(e.want(e.call(u.cookies, "POST", "/api/v1/orders/test-purchase", map[string]any{"plan_id": "starter"}, uuid.NewString()), 409)) != "plan_change_not_supported" {
		t.Fatal("test purchase over a paid subscription")
	}
}
