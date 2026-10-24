package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// Round 6 of handoff_payment_account.md (R6-02, R6-05): the operator's real-payment
// checklist (section 15.2, U-01 … U-09) walked step by step against the local gateway
// fake, and what a signed-in client sees while the website takes money.
//
// The fake follows 支付接入技术文档.md and nothing more. A step passing here proves the
// local implementation and the checklist wording; it says nothing about the real
// gateway, which only the operator's own payments can show.

const nativeProfileYAML = "proxies:\n  - name: Fixture\n    type: ss\n    server: 127.0.0.1\n    port: 18443\n    cipher: aes-128-gcm\n    password: test-only\nproxy-groups:\n  - name: VIP\n    type: select\n    proxies: [Fixture]\nrules: ['MATCH,VIP']\n"

// restart rebuilds the router over the same database with a changed configuration,
// the way editing backend/.env and restarting the service does.
func (e *payEnv) restart(mutate func(*config.Config)) {
	e.t.Helper()
	mutate(&e.cfg)
	e.router = newServerAt(e.db, e.cfg, authmail.NewService(e.db, e.cfg), e.clock.Now)
}

func (e *payEnv) native(method, path, token string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// clientSession is what the app polls: whether it may connect and why not.
type clientSession struct {
	CanConnect            bool       `json:"can_connect"`
	Reason                string     `json:"reason"`
	SubscriptionExpiresAt *time.Time `json:"subscription_expires_at"`
	TotalBytes            int64      `json:"total_bytes"`
}

func (e *payEnv) clientSession(token string) (clientSession, int) {
	e.t.Helper()
	w := e.native("GET", "/api/v1/client/session", token, nil)
	var out clientSession
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			e.t.Fatal(err)
		}
	}
	return out, w.Code
}

// clientLogin signs a device in and fetches its profile, as the app does after the user logs in.
func (e *payEnv) clientLogin(email string) string {
	e.t.Helper()
	w := e.native("POST", "/api/v1/client/login", "", nativeLoginBody(email, payPassword, "windows"))
	out := e.want(w, 200)
	token, _ := out["token"].(string)
	if token == "" {
		e.t.Fatalf("no client token: %s", w.Body.String())
	}
	return token
}

func (e *payEnv) writeProfiles() {
	e.t.Helper()
	for _, plan := range []string{"starter", "pro"} {
		if err := os.WriteFile(filepath.Join(e.cfg.ClientProfileDir, plan+".yaml"), []byte(nativeProfileYAML), 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
}

// poll is the app's one-second status call, made after each step so the session never idles out.
func (e *payEnv) poll(token string) {
	e.t.Helper()
	if _, code := e.clientSession(token); code != 200 {
		e.t.Fatalf("the client session ended (%d) at %s", code, e.clock.Now())
	}
}

func (e *payEnv) amountOf(p placed) int64 {
	e.t.Helper()
	gw, status := e.gw.open(e.t, p.PayURL)
	if status != 200 {
		e.t.Fatalf("gateway refused the order: %d", status)
	}
	return gw.Cents
}

// R6-05: U-01 … U-09 in order, with the operator's configuration: gateway secret set,
// the test price on for one listed account, the callback allow-list empty at first.
func TestUserPaymentChecklist(t *testing.T) {
	logs := captureLogs(t)
	e := newPayEnv(t, func(c *config.Config) {
		c.Payment.NotifyAllowed = nil
		c.Payment.TestPrice = true
		c.Payment.TestEmails = []string{"owner@example.com"}
	})
	e.writeProfiles()
	admin := e.user("root", "admin")
	owner := e.user("owner", "user")
	stranger := e.user("stranger", "user")
	console := func(method, path string, body any) *httptest.ResponseRecorder {
		return e.call(admin.cookies, method, "/api/v1/admin/console"+path, body, "")
	}
	var plan model.Plan
	if err := e.db.First(&plan, "id = ?", "starter").Error; err != nil {
		t.Fatal(err)
	}

	// U-01: the first payment, WeChat, at the test price. The gateway page shows 0.01;
	// with the allow-list empty the callback is not trusted, so the order waits for a
	// person, and the order detail shows the callback's source address.
	u1 := e.place(owner, "starter", payment.TypeWeChat, "")
	if u1.Order.AmountCents != 1 || u1.Order.PriceSource != priceTest || u1.Order.ListPriceCents != plan.PriceCents {
		t.Fatalf("U-01 order: %+v", u1.Order)
	}
	g1, _ := e.gw.open(t, u1.PayURL)
	if g1.Cents != 1 || g1.Type != payment.TypeWeChat {
		t.Fatalf("U-01 gateway page: %+v", g1)
	}
	e.want(e.pay(g1), 200)
	if o := e.order(u1.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewUnverifiedSource || e.subscription(owner.ID) != nil {
		t.Fatalf("U-01 should wait for review: %+v", o)
	}
	var detail struct {
		Notifications []consoleNotification `json:"notifications"`
	}
	if err := json.Unmarshal(console("GET", "/orders/"+u1.Order.ID, nil).Body.Bytes(), &detail); err != nil || len(detail.Notifications) != 1 || detail.Notifications[0].SourceIP != gatewayIP {
		t.Fatalf("U-01 order detail does not show the source address: %+v %v", detail, err)
	}
	userView := e.want(e.call(owner.cookies, "GET", "/api/v1/payments/orders/"+u1.Order.ID, nil, ""), 200)["order"].(map[string]any)
	if userView["status"] != model.OrderReview {
		t.Fatalf("U-01 result page status %v", userView["status"])
	}

	// U-02: the operator puts that address in the allow-list, restarts, and confirms the
	// order by hand after checking the amount in the merchant console.
	e.restart(func(c *config.Config) {
		c.Payment.NotifyAllowed = []netip.Prefix{netip.MustParsePrefix(gatewayIP + "/32")}
	})
	e.want(console("POST", "/orders/"+u1.Order.ID+"/confirm", map[string]any{"admin_password": payPassword, "paid_cents": 1, "note": "U-02"}), 200)
	if o := e.order(u1.Order.ID); o.Status != model.OrderPaid {
		t.Fatalf("U-02 not paid: %+v", o)
	}
	sub := e.subscription(owner.ID)
	if sub == nil || sub.PlanID != "starter" || sub.IsTest {
		t.Fatalf("U-02 subscription: %+v", sub)
	}
	// The app signs in on the new subscription and stays connectable through U-03 … U-07.
	token := e.clientLogin(owner.Email)
	e.want(e.native("GET", "/api/v1/client/config", token, nil), 200)
	if s, code := e.clientSession(token); code != 200 || !s.CanConnect || s.Reason != "" {
		t.Fatalf("U-02 client session: %+v %d", s, code)
	}

	// U-03: Alipay. The callback now comes from a listed address, so it pays out by itself.
	u3 := e.place(owner, "starter", payment.TypeAlipay, "")
	g3, _ := e.gw.open(t, u3.PayURL)
	if g3.Type != payment.TypeAlipay || g3.Cents != 1 {
		t.Fatalf("U-03 gateway page: %+v", g3)
	}
	e.want(e.pay(g3), 200)
	if o := e.order(u3.Order.ID); o.Status != model.OrderPaid || o.PaidAt == nil {
		t.Fatalf("U-03 not paid by the callback: %+v", o)
	}
	if view := e.want(e.call(owner.cookies, "GET", "/api/v1/payments/orders/"+u3.Order.ID, nil, ""), 200)["order"].(map[string]any); view["status"] != model.OrderPaid {
		t.Fatalf("U-03 result page: %v", view)
	}

	// U-04: open the payment page, pay nothing, wait out the gateway's 2 minutes and press
	// the button on its timeout page: back on the result page the order is expired, with no charge.
	before := e.subscription(owner.ID)
	u4 := e.place(owner, "starter", payment.TypeWeChat, "")
	g4, _ := e.gw.open(t, u4.PayURL)
	e.clock.Advance(payment.OrderLifetime + time.Second)
	e.poll(token)
	e.want(e.call(owner.cookies, "GET", "/api/v1/payments/orders/"+u4.Order.ID, nil, ""), 200)
	if o := e.order(u4.Order.ID); o.Status != model.OrderExpired || o.PaidAt != nil {
		t.Fatalf("U-04 order: %+v", o)
	}
	if after := e.subscription(owner.ID); after.Version != before.Version || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatal("U-04 changed the subscription")
	}
	_ = g4

	// U-05: close the window, buy the same plan again at once. The gateway shows 0.02 for
	// the second order (0.01 and its de-duplication cent); paying it works within the
	// tolerance; the abandoned first order becomes expired after the local window.
	u5a := e.place(owner, "starter", payment.TypeWeChat, "")
	_, _ = e.gw.open(t, u5a.PayURL)
	e.clock.Advance(10 * time.Second)
	e.poll(token)
	u5b := e.place(owner, "starter", payment.TypeWeChat, "")
	g5b, _ := e.gw.open(t, u5b.PayURL)
	if g5b.Cents != 2 || u5b.Order.AmountCents != 1 || u5b.Others != 1 {
		t.Fatalf("U-05 second order: gateway %d cents, order %d, others %d", g5b.Cents, u5b.Order.AmountCents, u5b.Others)
	}
	e.want(e.pay(g5b), 200)
	if o := e.order(u5b.Order.ID); o.Status != model.OrderPaid || *o.PaidCents != 2 {
		t.Fatalf("U-05 second order: %+v", o)
	}
	e.poll(token)
	e.clock.Advance(90 * time.Second)
	e.poll(token)
	e.clock.Advance(91 * time.Second)
	e.poll(token)
	e.reconcile()
	if o := e.order(u5a.Order.ID); o.Status != model.OrderExpired {
		t.Fatalf("U-05 first order after 3 minutes: %+v", o)
	}

	// U-06: a coupon. At the test price the discount cannot go below 0.01, which is
	// what the operator sees; the account that pays list price sees the real discount.
	coupon := e.coupon(model.Coupon{Code: "SPRING20", DiscountType: model.DiscountPercent, PercentOff: 20, Enabled: true})
	quote := func(u payUser, code string) map[string]any {
		return e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter", "coupon_code": code}, ""), 200)["quote"].(map[string]any)
	}
	if q := quote(stranger, "spring20"); q["amount_cents"].(float64) != float64(plan.PriceCents-plan.PriceCents*20/100) || q["discount_cents"].(float64) <= 0 || q["coupon"].(map[string]any)["applied"] != true {
		t.Fatalf("U-06 list-price quote: %v", q)
	}
	u6 := e.place(owner, "starter", payment.TypeAlipay, "spring20")
	if u6.Order.AmountCents != 1 || u6.Order.CouponCode != "SPRING20" {
		t.Fatalf("U-06 order at the test price: %+v", u6.Order)
	}
	g6, _ := e.gw.open(t, u6.PayURL)
	e.want(e.pay(g6), 200)
	var used model.Coupon
	e.db.First(&used, "id = ?", coupon.ID)
	if used.UsedCount != 1 {
		t.Fatalf("U-06 coupon used %d times", used.UsedCount)
	}

	// U-07: the same plan again extends the subscription and adds the traffic; the app
	// stays connected (same period, nothing to re-authorise).
	before = e.subscription(owner.ID)
	u7 := e.place(owner, "starter", payment.TypeWeChat, "")
	g7, _ := e.gw.open(t, u7.PayURL)
	e.want(e.pay(g7), 200)
	after := e.subscription(owner.ID)
	if !after.ExpiresAt.Equal(before.ExpiresAt.AddDate(0, 0, plan.DurationDays)) || after.TrafficLimitBytes != before.TrafficLimitBytes+plan.TrafficBytes || !after.StartsAt.Equal(before.StartsAt) {
		t.Fatalf("U-07 renewal: before %+v after %+v", before, after)
	}
	if s, code := e.clientSession(token); code != 200 || !s.CanConnect || s.Reason != "" || s.TotalBytes != after.TrafficLimitBytes || !s.SubscriptionExpiresAt.Equal(after.ExpiresAt) {
		t.Fatalf("U-07 the app was disturbed or did not see the renewal: %+v %d", s, code)
	}

	// U-08: with the test price off, even the listed account sees the list price, and an
	// unlisted account never saw anything else.
	if q := quote(stranger, ""); q["amount_cents"].(float64) != float64(plan.PriceCents) || q["test_price"] != false {
		t.Fatalf("U-08 unlisted account: %v", q)
	}
	e.restart(func(c *config.Config) { c.Payment.TestPrice = false })
	if q := quote(owner, ""); q["amount_cents"].(float64) != float64(plan.PriceCents) || q["test_price"] != false {
		t.Fatalf("U-08 listed account with the test price off: %v", q)
	}

	// U-09: the log has the callback lines and nothing secret.
	requireCleanLogs(t, e, logs)
}

// R6-02: what the app sees while the website takes money, changes passwords and deletes
// accounts. A renewal does not disconnect; a new period or plan asks the app to authorise
// again, and it recovers; a password change or deletion ends the client session.
func TestClientCompatibilityWithPayments(t *testing.T) {
	e := newPayEnv(t, nil)
	e.writeProfiles()
	u := e.user("compat", "user")
	buy := func(plan, kind string) {
		t.Helper()
		g, _ := e.gw.open(t, e.place(u, plan, kind, "").PayURL)
		e.want(e.pay(g), 200)
	}
	buy("starter", payment.TypeWeChat)
	token := e.clientLogin(u.Email)
	e.want(e.native("GET", "/api/v1/client/config", token, nil), 200)
	first, code := e.clientSession(token)
	if code != 200 || !first.CanConnect {
		t.Fatalf("session before the renewal: %+v %d", first, code)
	}

	// Renewal: the same session keeps working and sees the later expiry.
	buy("starter", payment.TypeAlipay)
	renewed, code := e.clientSession(token)
	if code != 200 || !renewed.CanConnect || renewed.Reason != "" || !renewed.SubscriptionExpiresAt.After(*first.SubscriptionExpiresAt) {
		t.Fatalf("renewal disturbed the app: %+v %d", renewed, code)
	}

	// A lapsed subscription, then a new purchase of another plan: a new period. The app is
	// told to authorise again, does so, and is connectable on the new plan.
	if err := e.db.Model(&model.Subscription{}).Where("user_id = ?", u.ID).Update("expires_at", e.clock.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if s, _ := e.clientSession(token); s.CanConnect || s.Reason != "subscription_expired" {
		t.Fatalf("lapsed subscription: %+v", s)
	}
	buy("pro", payment.TypeWeChat)
	changed, code := e.clientSession(token)
	if code != 200 || changed.CanConnect || changed.Reason != "subscription_changed" {
		t.Fatalf("new period must ask the app to authorise again: %+v %d", changed, code)
	}
	if code := payErrorCode(e.want(e.native("GET", "/api/v1/client/config", token, nil), 409)); code != "subscription_changed" {
		t.Fatalf("the old session must not fetch a profile for the new period: %s", code)
	}
	token = e.clientLogin(u.Email)
	e.want(e.native("GET", "/api/v1/client/config", token, nil), 200)
	if recovered, _ := e.clientSession(token); !recovered.CanConnect || recovered.Reason != "" {
		t.Fatalf("app did not recover after signing in again: %+v", recovered)
	}

	// A password change ends every client session and every browser session.
	device := e.clientLogin(u.Email)
	e.want(e.call(u.cookies, "POST", "/api/v1/me/password", map[string]any{"current_password": payPassword, "new_password": "A-Different-Password-2026"}, ""), 200)
	for _, tok := range []string{token, device} {
		if w := e.native("GET", "/api/v1/client/session", tok, nil); w.Code != http.StatusUnauthorized || payErrorCode(e.want(w, 401)) != "native_session_expired" {
			t.Fatalf("client session survived the password change: %d %s", w.Code, w.Body.String())
		}
	}

	// Deleting the account ends them too, and signing in again is refused.
	other := e.user("compat-delete", "user")
	g, _ := e.gw.open(t, e.place(other, "starter", payment.TypeAlipay, "").PayURL)
	e.want(e.pay(g), 200)
	otherToken := e.clientLogin(other.Email)
	e.want(e.native("GET", "/api/v1/client/config", otherToken, nil), 200)
	e.want(e.call(other.cookies, "POST", "/api/v1/me/account/delete", map[string]any{"password": payPassword, "email": other.Email}, ""), 200)
	if w := e.native("GET", "/api/v1/client/session", otherToken, nil); payErrorCode(e.want(w, 401)) != "native_session_expired" {
		t.Fatalf("client session survived the deletion: %s", w.Body.String())
	}
	e.want(e.native("POST", "/api/v1/client/login", "", nativeLoginBody(other.Email, payPassword, "android")), 401)
	e.want(e.call(other.cookies, "GET", "/api/v1/auth/me", nil, ""), 401)
}
