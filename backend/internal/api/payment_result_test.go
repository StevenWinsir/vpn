package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// A paid order whose trusted callback has not settled it yet: once the gateway reports
// it paid, every later poll keeps saying so (the result page used to flip back to
// "待支付" between gateway queries), the order list says so too, and nothing is granted
// until the callback arrives.
func TestGatewayPaidHintIsSticky(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("sticky", "user")
	p := e.place(u, "starter", payment.TypeWeChat, "")
	gw, _ := e.gw.open(t, p.PayURL)
	path := "/api/v1/payments/orders/" + p.Order.ID
	view := func() map[string]any {
		t.Helper()
		return e.want(e.call(u.cookies, "GET", path, nil, ""), 200)["order"].(map[string]any)
	}
	if v := view(); v["status"] != model.OrderPending || v["gateway_paid"] != nil {
		t.Fatalf("unpaid order: %v", v)
	}
	e.gw.markPaid(gw)
	e.clock.Advance(statusRecheck)
	if v := view(); v["status"] != model.OrderPending || v["gateway_paid"] != true {
		t.Fatalf("gateway paid not reported: %v", v)
	}
	calls := e.gw.calls()
	for range 5 {
		e.clock.Advance(2 * time.Second)
		if v := view(); v["status"] != model.OrderPending || v["gateway_paid"] != true {
			t.Fatalf("a later poll fell back to awaiting payment: %v", v)
		}
	}
	e.clock.Advance(statusRecheck)
	view()
	if e.gw.calls() != calls {
		t.Fatal("the gateway was asked again after it had reported the order paid")
	}
	list := e.want(e.call(u.cookies, "GET", "/api/v1/orders", nil, ""), 200)["orders"].([]any)
	if row := list[0].(map[string]any); row["id"] != p.Order.ID || row["gateway_paid"] != true || row["status"] != model.OrderPending {
		t.Fatalf("order list: %v", row)
	}
	if o := e.order(p.Order.ID); o.GatewayPaidAt == nil || o.PaidAt != nil || e.subscription(u.ID) != nil {
		t.Fatalf("the hint granted something: %+v", o)
	}
	// The trusted callback settles it; the hint is gone with the pending state.
	e.want(e.notify(gatewayIP, callbackBody(gw, payment.TradeSuccess)), 200)
	if v := view(); v["status"] != model.OrderPaid || v["gateway_paid"] != nil {
		t.Fatalf("settled order: %v", v)
	}

	// Without a callback the reconciler hands it to review, and the console sees when the
	// gateway reported it paid.
	q := e.place(u, "starter", payment.TypeAlipay, "")
	qgw, _ := e.gw.open(t, q.PayURL)
	e.gw.markPaid(qgw)
	e.clock.Advance(4 * time.Minute)
	e.reconcile()
	o := e.order(q.Order.ID)
	if o.Status != model.OrderReview || o.ReviewReason != model.ReviewCallbackMissing || o.GatewayPaidAt == nil {
		t.Fatalf("missing callback: %+v", o)
	}
	if v := e.want(e.call(u.cookies, "GET", "/api/v1/payments/orders/"+q.Order.ID, nil, ""), 200)["order"].(map[string]any); v["status"] != model.OrderReview || v["gateway_paid"] != nil {
		t.Fatalf("review order: %v", v)
	}
}

// The gateway sends each callback once (支付接入技术文档.md "回调说明"). The browser
// CSRF and JSON rules must not refuse it before it is logged and judged: an Origin, a
// cookie, a missing or wrong Content-Type and a form body are all still read.
func TestPaymentNotifyIsNeverRefusedBeforeJudging(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("shapes", "user")
	settle := func(name string, headers map[string]string, body func(*fakeGatewayOrder) string) {
		t.Helper()
		p := e.place(u, "starter", payment.TypeWeChat, "")
		gw, _ := e.gw.open(t, p.PayURL)
		e.gw.markPaid(gw)
		if w := e.notifyWith(gatewayIP, body(gw), headers); w.Code != 200 {
			t.Fatalf("%s: callback answered %d %s", name, w.Code, w.Body.String())
		}
		if n := e.lastNotification(); n.Result != model.NotifyAccepted || n.SourceIP != gatewayIP {
			t.Fatalf("%s: callback judged %q", name, n.Result)
		}
		if o := e.order(p.Order.ID); o.Status != model.OrderPaid {
			t.Fatalf("%s: order %s", name, o.Status)
		}
	}
	json := func(o *fakeGatewayOrder) string { return callbackBody(o, payment.TradeSuccess) }
	form := func(o *fakeGatewayOrder) string {
		return url.Values{"trade_no": {o.TradeNo}, "name": {o.Name}, "money": {payment.FormatAmount(o.Cents)}, "trade_status": {payment.TradeSuccess}}.Encode()
	}
	settle("foreign origin", map[string]string{"Origin": "https://pay.example.net"}, json)
	settle("cookie without origin", map[string]string{"Cookie": "vpn_access=stale"}, json)
	settle("text/plain", map[string]string{"Content-Type": "text/plain"}, json)
	settle("no content type", map[string]string{"Content-Type": ""}, json)
	settle("form", map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"}, form)

	// A form callback is validated like the JSON one, and still logged.
	p := e.place(u, "starter", payment.TypeWeChat, "")
	for _, body := range []string{
		"trade_no=" + *p.Order.TradeNo + "&name=x&money=1&trade_status=PAID",
		"trade_no=" + *p.Order.TradeNo + "&trade_no=" + *p.Order.TradeNo + "&name=x&money=1&trade_status=TRADE_SUCCESS",
		"trade_no=" + *p.Order.TradeNo + "&name=x&trade_status=TRADE_SUCCESS",
		"%zz",
	} {
		e.want(e.notifyWith(gatewayIP, body, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), 200)
		if n := e.lastNotification(); n.Result != model.NotifyInvalid {
			t.Fatalf("%s: judged %q", body, n.Result)
		}
	}
	if o := e.order(p.Order.ID); o.Status != model.OrderPending {
		t.Fatalf("an invalid form callback changed the order: %s", o.Status)
	}
	// From outside the allow-list a form callback is refused like a JSON one.
	gw, _ := e.gw.open(t, p.PayURL)
	e.want(e.notifyWith("198.51.100.4", form(gw), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://pay.example.net"}), 200)
	if n := e.lastNotification(); n.Result != model.NotifyIPRejected || e.order(p.Order.ID).Status != model.OrderPending {
		t.Fatalf("outsider form callback judged %q", n.Result)
	}

	// Every other write keeps the browser rules.
	req := e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter"}, "")
	if req.Code != 200 {
		t.Fatalf("quote %d", req.Code)
	}
	raw := e.callRaw(u.cookies, "POST", "/api/v1/payments/quote", `{"plan_id":"starter"}`, map[string]string{"Content-Type": "text/plain", "Origin": e.cfg.Origins[0]})
	if payErrorCode(e.want(raw, 415)) != "json_required" {
		t.Fatal("the quote accepted a non-JSON body")
	}
	raw = e.callRaw(u.cookies, "POST", "/api/v1/payments/quote", `{"plan_id":"starter"}`, map[string]string{"Content-Type": "application/json", "Origin": "https://pay.example.net"})
	if payErrorCode(e.want(raw, 403)) != "origin_forbidden" {
		t.Fatal("the quote accepted a foreign origin")
	}
}

// callRaw sends body as is with exactly the given headers (no default Origin or type).
func (e *payEnv) callRaw(cookies []*http.Cookie, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}
