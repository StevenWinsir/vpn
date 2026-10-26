package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/payment"
)

// Round 6 of handoff_payment_account.md (R6-03, R6-04): the security review, kept as
// executable checks so a later change cannot quietly undo them.

// logCapture collects everything written through the standard logger, which is where
// the payment, purge and login code report.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	capture := &logCapture{}
	previous, flags := log.Writer(), log.Flags()
	log.SetOutput(capture)
	t.Cleanup(func() { log.SetOutput(previous); log.SetFlags(flags) })
	return capture
}

// requireCleanLogs fails when a log line carries something the plan forbids (13):
// the merchant secret, a gateway signature, a gateway URL, a whole trade number, a
// password or a token.
func requireCleanLogs(t *testing.T, e *payEnv, logs *logCapture, extra ...string) {
	t.Helper()
	text := logs.String()
	forbidden := append([]string{e.cfg.Payment.Secret, e.gw.server.URL, "/submit?", "sign=", payPassword, e.cfg.Payment.NotifyURL, e.cfg.JWTSecret}, extra...)
	for _, bad := range forbidden {
		if bad != "" && strings.Contains(text, bad) {
			t.Fatalf("a log line carries %q:\n%s", bad, text)
		}
	}
	var orders []model.Order
	if err := e.db.Select("trade_no").Where("trade_no IS NOT NULL").Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	for _, o := range orders {
		if strings.Contains(text, *o.TradeNo) {
			t.Fatalf("a log line carries a whole trade number:\n%s", text)
		}
	}
}

func (e *payEnv) engine() *gin.Engine {
	e.t.Helper()
	engine, ok := e.router.(*gin.Engine)
	if !ok {
		e.t.Fatal("router is not a gin engine")
	}
	return engine
}

// R6-03: only the gateway's address, enough money and an unpredictable order number
// ever fulfil an order, and the browser never names a price.
func TestSecurityPaymentBypassAttempts(t *testing.T) {
	logs := captureLogs(t)
	e := newPayEnv(t, nil)
	attacker := e.user("attacker", "user")
	cb := func(o model.Order, money string) string {
		return fmt.Sprintf(`{"trade_no":%q,"name":%q,"money":%s,"trade_status":%q}`, *o.TradeNo, o.PlanName, money, payment.TradeSuccess)
	}
	untouched := func(o model.Order) {
		t.Helper()
		if got := e.order(o.ID); got.Status != model.OrderPending || got.PaidAt != nil {
			t.Fatalf("order moved: %+v", got)
		}
		if e.subscription(attacker.ID) != nil {
			t.Fatal("a forged callback opened a subscription")
		}
	}

	// The browser cannot name an amount: every price field is an unknown field.
	for _, field := range []string{"amount_cents", "price_cents", "amount", "money", "paid_cents", "list_price_cents", "discount_cents", "status", "user_id", "trade_no", "notify_url", "return_url"} {
		w := e.call(attacker.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay", field: 1}, uuid.NewString())
		if payErrorCode(e.want(w, 400)) != "invalid_json" {
			t.Fatalf("%s accepted in an order request", field)
		}
	}
	e.want(e.call(attacker.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "pro", "amount_cents": 1}, ""), 400)
	e.want(e.call(attacker.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "bank"}, uuid.NewString()), 400)
	e.want(e.call(attacker.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "pro", "pay_type": "wxpay"}, "not-a-uuid"), 400)

	// Callback attacks, each on its own order so one verdict cannot hide another.
	outsider := e.place(attacker, "pro", payment.TypeWeChat, "").Order
	if outsider.AmountCents != outsider.ListPriceCents || outsider.AmountCents < 1000 {
		t.Fatalf("unexpected amount %+v", outsider)
	}
	exact := payment.FormatAmount(outsider.AmountCents)
	for name, headers := range map[string]map[string]string{
		"plain":           nil,
		"x-forwarded-for": {"X-Forwarded-For": gatewayIP},
		"x-real-ip":       {"X-Real-IP": gatewayIP},
		"forwarded":       {"Forwarded": "for=" + gatewayIP},
		"client-ip":       {"X-Client-IP": gatewayIP, "True-Client-IP": gatewayIP, "CF-Connecting-IP": gatewayIP},
	} {
		e.want(e.notifyWith("198.51.100.77", cb(outsider, exact), headers), 200)
		if n := e.lastNotification(); n.Result != model.NotifyIPRejected || n.SourceIP != "198.51.100.77" {
			t.Fatalf("%s: callback from outside the list judged %q from %q: %+v body %s", name, n.Result, n.SourceIP, n, cb(outsider, exact))
		}
		untouched(outsider)
	}

	// From the gateway's address but paying too little: nothing is granted.
	cheap := e.place(attacker, "pro", payment.TypeAlipay, "").Order
	e.want(e.notify(gatewayIP, cb(cheap, "0.01")), 200)
	if got := e.order(cheap.ID); got.Status != model.OrderReview || got.ReviewReason != model.ReviewAmountMismatch || got.PaidAt != nil {
		t.Fatalf("underpayment: %+v", got)
	}
	if e.subscription(attacker.ID) != nil {
		t.Fatal("an underpayment opened a subscription")
	}
	// Money far above the tolerance is also a discrepancy, not a free upgrade.
	over := e.place(attacker, "pro", payment.TypeWeChat, "").Order
	e.want(e.notify(gatewayIP, cb(over, payment.FormatAmount(over.AmountCents+e.cfg.Payment.ToleranceCents+1))), 200)
	if got := e.order(over.ID); got.Status != model.OrderReview || got.ReviewReason != model.ReviewAmountMismatch {
		t.Fatalf("overpayment: %+v", got)
	}
	// Malformed amounts never parse into money.
	for _, money := range []string{`"-99.00"`, `"0"`, `"abc"`, `null`, `"99999999999999999999"`, `"1,00"`} {
		e.want(e.notify(gatewayIP, cb(outsider, money)), 200)
		if n := e.lastNotification(); n.Result != model.NotifyInvalid {
			t.Fatalf("money %s judged %q", money, n.Result)
		}
	}
	untouched(outsider)

	// Order numbers cannot be guessed: a fresh number is 8 date digits and 12 random
	// ones, and neighbours of a real number are unknown orders.
	seen := map[string]bool{}
	now := e.clock.Now()
	for range 2000 {
		n, err := payment.NewTradeNo(now)
		if err != nil || len(n) != 20 || seen[n] {
			t.Fatalf("trade number %q (%v)", n, err)
		}
		seen[n] = true
	}
	base, err := strconv.ParseInt((*outsider.TradeNo)[8:], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int64{-2, -1, 1, 2} {
		if base+delta < 0 || base+delta >= 1_000_000_000_000 {
			continue
		}
		guess := (*outsider.TradeNo)[:8] + fmt.Sprintf("%012d", base+delta)
		body := fmt.Sprintf(`{"trade_no":%q,"name":"x","money":%s,"trade_status":%q}`, guess, exact, payment.TradeSuccess)
		e.want(e.notify(gatewayIP, body), 200)
		if n := e.lastNotification(); n.Result != model.NotifyUnknownOrder {
			t.Fatalf("neighbour %d judged %q", delta, n.Result)
		}
	}
	untouched(outsider)

	// The gateway page URL carries the signature; the API never returns it anywhere else.
	for _, path := range []string{"/api/v1/orders", "/api/v1/payments/orders/" + outsider.ID, "/api/v1/auth/me", "/api/v1/me/dashboard"} {
		body := e.call(attacker.cookies, "GET", path, nil, "").Body.String()
		if strings.Contains(body, "sign=") || strings.Contains(body, e.cfg.Payment.Secret) || strings.Contains(body, "review_reason") || strings.Contains(body, "idempotency") {
			t.Fatalf("%s leaks payment internals: %s", path, body)
		}
	}
	requireCleanLogs(t, e, logs)
}

// R6-03: every route is either public on purpose or demands the right login, and the
// read-only and administrator rules hold for the whole route table, so a route added
// later without them fails here.
func TestSecurityRouteAccessMatrix(t *testing.T) {
	e := newConsoleEnv(t, nil)
	user := e.user("matrix-user", "user")
	victim := e.user("matrix-victim", "user")
	routes := e.engine().Routes()
	public := map[string]bool{
		"GET /healthz": true, "GET /readyz": true, "GET /api/v1/meta": true, "GET /api/v1/plans": true,
		"POST /api/v1/auth/register-code": true, "POST /api/v1/auth/register": true, "POST /api/v1/auth/forgot-password": true,
		"POST /api/v1/auth/reset-password/validate": true, "POST /api/v1/auth/reset-password": true, "POST /api/v1/auth/login": true,
		"POST /api/v1/auth/refresh": true, "POST /api/v1/auth/logout": true,
		"POST /api/v1/client/login": true, "GET /api/v1/client/capabilities": true, "POST /api/v1/payments/notify": true,
	}
	concrete := func(path string) string {
		parts := strings.Split(path, "/")
		for i, p := range parts {
			if strings.HasPrefix(p, ":") {
				parts[i] = uuid.NewString()
			}
		}
		return strings.Join(parts, "/")
	}
	body := func(method string) any {
		if method == "GET" {
			return nil
		}
		return map[string]any{}
	}
	var checked, admin int
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if public[key] {
			continue
		}
		path := concrete(route.Path)
		checked++
		// Anonymous callers are always turned away.
		if w := e.call(nil, route.Method, path, body(route.Method), ""); w.Code != 401 {
			t.Errorf("%s: anonymous request answered %d, not 401", key, w.Code)
		}
		cookieRoute := strings.HasPrefix(route.Path, "/api/v1/") && !strings.HasPrefix(route.Path, "/api/v1/client/")
		if !cookieRoute {
			continue
		}
		if strings.HasPrefix(route.Path, "/api/v1/admin/") {
			admin++
			w := e.call(user.cookies, route.Method, path, body(route.Method), "")
			if w.Code < 400 || w.Code == 401 || w.Code >= 500 {
				t.Errorf("%s: an ordinary user got %d", key, w.Code)
			}
			if strings.HasPrefix(route.Path, "/api/v1/admin/console/") && w.Code != 403 {
				t.Errorf("%s: console answered an ordinary user with %d", key, w.Code)
			}
		}
	}
	if checked < 60 || admin < 30 {
		t.Fatalf("route table smaller than expected: %d checked, %d administrator routes", checked, admin)
	}

	// An impersonation session is read-only on every route that writes.
	b := e.loginAgain(e.admin)
	e.want(e.impersonate(b, victim.ID), 200)
	for _, route := range routes {
		if route.Method == "GET" || public[route.Method+" "+route.Path] || strings.HasPrefix(route.Path, "/api/v1/client/") || route.Path == returnPath {
			continue
		}
		w := b.do(route.Method, concrete(route.Path), body(route.Method))
		if w.Code != 403 || payErrorCode(e.want(w, 403)) != errImpersonation {
			t.Errorf("%s %s: impersonation session was not refused (%d %s)", route.Method, route.Path, w.Code, w.Body.String())
		}
	}
	// ...and it can still read what the user sees, but never the console.
	e.want(b.get("/me/dashboard"), 200)
	e.want(b.get("/admin/console/users"), 403)
}

// R6-03: other people's data is unreachable by changing an identifier.
func TestSecurityObjectAccess(t *testing.T) {
	e := newConsoleEnv(t, nil)
	owner, stranger := e.user("idor-owner", "user"), e.user("idor-stranger", "user")
	p := e.place(owner, "starter", payment.TypeWeChat, "")
	gw, _ := e.gw.open(t, p.PayURL)
	e.want(e.pay(gw), 200)

	// Orders: by id, by list, by the console, and by the old test-purchase list.
	e.want(e.call(stranger.cookies, "GET", "/api/v1/payments/orders/"+p.Order.ID, nil, ""), 404)
	list := e.want(e.call(stranger.cookies, "GET", "/api/v1/orders", nil, ""), 200)
	if list["total"].(float64) != 0 {
		t.Fatalf("a stranger lists orders: %v", list)
	}
	if got := e.want(e.call(owner.cookies, "GET", "/api/v1/orders", nil, ""), 200)["total"].(float64); got != 1 {
		t.Fatalf("owner lists %v orders", got)
	}
	e.want(e.call(stranger.cookies, "GET", "/api/v1/admin/console/orders/"+p.Order.ID, nil, ""), 403)
	e.want(e.call(stranger.cookies, "GET", "/api/v1/admin/console/users/"+owner.ID, nil, ""), 403)
	e.want(e.call(stranger.cookies, "GET", "/api/v1/admin/console/logins", nil, ""), 403)

	// Sign-in records and devices are per user.
	for i := 0; i < 3; i++ {
		e.loginAgain(owner)
	}
	var mine, theirs struct {
		Logins []map[string]any `json:"logins"`
	}
	e.decode(e.call(owner.cookies, "GET", "/api/v1/me/logins", nil, ""), 200, &mine)
	e.decode(e.call(stranger.cookies, "GET", "/api/v1/me/logins", nil, ""), 200, &theirs)
	if len(mine.Logins) < 4 || len(theirs.Logins) != 1 {
		t.Fatalf("login records not separated: %d vs %d", len(mine.Logins), len(theirs.Logins))
	}

	// Announcements are shared but the acknowledgement is the caller's own; an unknown
	// or draft id is a 404 and never records anything.
	e.want(e.call(stranger.cookies, "POST", "/api/v1/me/announcements/"+uuid.NewString()+"/ack", map[string]any{}, ""), 404)
	e.want(e.call(stranger.cookies, "POST", "/api/v1/me/announcements/not-a-uuid/ack", map[string]any{}, ""), 404)
	var reads int64
	e.db.Model(&model.AnnouncementRead{}).Count(&reads)
	if reads != 0 {
		t.Fatal("a refused acknowledgement was recorded")
	}

	// Impersonating one user shows that user only; the other's data stays out of reach.
	b := e.loginAgain(e.admin)
	e.want(e.impersonate(b, owner.ID), 200)
	got := e.want(b.get("/orders"), 200)
	if got["total"].(float64) != 1 {
		t.Fatalf("impersonation shows %v orders", got["total"])
	}
	e.want(b.get("/payments/orders/"+uuid.NewString()), 404)
}

// R6-03: stored text is returned as JSON and cannot be sniffed into markup.
func TestSecurityResponseHeadersAndText(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("xss", "user")
	payload := `<img src=x onerror=alert(1)><script>alert(2)</script>`
	a := e.draft(payload, payload)
	e.transition(a.ID, "publish", 200)
	w := e.call(u.cookies, "GET", "/api/v1/me/announcements", nil, "")
	e.want(w, 200)
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("announcement content type %q", got)
	}
	for _, header := range []struct{ name, want string }{{"X-Content-Type-Options", "nosniff"}, {"X-Frame-Options", "DENY"}, {"Cache-Control", "no-store"}, {"Referrer-Policy", "no-referrer"}} {
		if got := w.Header().Get(header.name); got != header.want {
			t.Fatalf("%s = %q, want %q", header.name, got, header.want)
		}
	}
	var out struct {
		Announcements []struct{ Title, Body string } `json:"announcements"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Announcements) != 1 || out.Announcements[0].Body != payload || out.Announcements[0].Title != payload {
		t.Fatalf("text altered or missing: %s", w.Body.String())
	}
	// Errors are JSON as well, never an HTML page that echoes the input.
	bad := e.call(u.cookies, "GET", "/api/v1/payments/orders/"+strings.ReplaceAll(payload, " ", "%20"), nil, "")
	if !strings.HasPrefix(bad.Header().Get("Content-Type"), "application/json") && bad.Code != 404 {
		t.Fatalf("error page: %d %q", bad.Code, bad.Header().Get("Content-Type"))
	}
	if strings.Contains(bad.Body.String(), "<script>") {
		t.Fatal("an error response echoed markup")
	}
}

// R6-03: a whole run through payments, the console and sign-in leaves no secret in the
// logs.
func TestSecurityLogsStayClean(t *testing.T) {
	logs := captureLogs(t)
	e := newConsoleEnv(t, nil)
	buyer := e.user("log-buyer", "user")
	odd := e.user("log-odd", "user")

	good := e.place(buyer, "starter", payment.TypeWeChat, "")
	gw, _ := e.gw.open(t, good.PayURL)
	e.want(e.pay(gw), 200)
	e.want(e.notify("198.51.100.9", callbackBody(gw, payment.TradeSuccess)), 200)
	e.want(e.notify(gatewayIP, `{"trade_no":"junk"`), 200)
	// An order under review, then confirmed by the administrator.
	review := e.place(odd, "pro", payment.TypeAlipay, "")
	gw2, _ := e.gw.open(t, review.PayURL)
	e.gw.markPaid(gw2)
	e.want(e.notify(gatewayIP, fmt.Sprintf(`{"trade_no":%q,"name":"x","money":0.01,"trade_status":%q}`, gw2.TradeNo, payment.TradeSuccess)), 200)
	e.want(e.post("/orders/"+review.Order.ID+"/confirm", map[string]any{"admin_password": payPassword, "paid_cents": gw2.Cents, "note": "checked in the merchant console"}), 200)
	// Failed sign-ins, a password change and an impersonation.
	e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": buyer.Email, "password": "wrong-" + payPassword}, "")
	e.want(e.call(buyer.cookies, "POST", "/api/v1/me/password", map[string]any{"current_password": payPassword, "new_password": "Another-Password-2026"}, ""), 200)
	b := e.loginAgain(e.admin)
	e.want(e.impersonate(b, odd.ID), 200)
	e.clock.Advance(4 * time.Minute)
	e.reconcile()

	requireCleanLogs(t, e.payEnv, logs, "Another-Password-2026", "wrong-"+payPassword, e.admin.PasswordHash, payHash)
	if !strings.Contains(logs.String(), "payment notify") {
		t.Fatalf("the callback paths logged nothing, so this check proves nothing:\n%s", logs.String())
	}
}

// R6-04: a production configuration never offers the mock purchase, even if the flag
// leaks through, and the readiness report for it carries switches and counts only.
func TestProductionConfigurationDefenses(t *testing.T) {
	e := newPayEnv(t, func(c *config.Config) {
		c.Env, c.TestPurchase, c.CookieSecure, c.SSLMode = "production", true, true, "verify-full"
		c.Payment.TestPrice, c.Payment.TestEmails = false, nil
		c.TrustedProxies = []string{"10.0.0.1"}
	})
	u := e.user("prod", "user")
	meta := e.want(e.call(nil, "GET", "/api/v1/meta", nil, ""), 200)
	if meta["test_purchase_enabled"] != false {
		t.Fatalf("production meta offers the mock purchase: %v", meta)
	}
	w := e.call(u.cookies, "POST", "/api/v1/orders/test-purchase", map[string]any{"plan_id": "starter"}, uuid.NewString())
	if payErrorCode(e.want(w, 403)) != "test_purchase_disabled" {
		t.Fatal("production accepted the mock purchase")
	}
	if e.subscription(u.ID) != nil {
		t.Fatal("the refused purchase opened a subscription")
	}
	// A listed test-price account is the only way to a test price, and it is reported.
	report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	for _, secret := range []string{e.cfg.Payment.Secret, e.cfg.Payment.Channel, e.cfg.Payment.NotifyURL, e.cfg.Payment.GatewayURL} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("readiness exposes %q", secret)
		}
	}
	permanent := map[string]bool{"real_payments_and_release_signing_not_verified": true, "enforcement_must_be_accepted_per_protocol_on_the_real_node": true,
		"auth_mail_not_configured": true}
	for _, warning := range report.Warnings {
		if !permanent[warning] {
			t.Fatalf("unexpected readiness warning %q in a clean production-style setup", warning)
		}
	}
	if !report.Payment.Enabled || !report.Payment.AutoFulfil || report.Payment.TestPrice || report.Payment.PendingOrders != 0 || report.Payment.ReviewOrders != 0 || report.Impersonation.ActiveSessions != 0 {
		t.Fatalf("payment readiness: %+v", report.Payment)
	}
	// The shop checks are part of the production report too (counts and the discount only).
	if !report.Shop.SettingsRow || report.Shop.YearlyDiscount != 90 || report.Shop.ActivePlans != 3 || report.Shop.ActiveYearlyPlans != 3 {
		t.Fatalf("shop readiness in production: %+v", report.Shop)
	}
	for _, leak := range []string{"note", "description", "price_cents"} {
		if bytes.Contains(raw, []byte(`"`+leak+`"`)) {
			t.Fatalf("readiness exposes plan field %q", leak)
		}
	}
	// With the allow-list empty and a test price on, the report says so.
	e.cfg.Payment.NotifyAllowed = nil
	e.cfg.Payment.TestPrice, e.cfg.Payment.TestEmails = true, []string{u.Email}
	if report, err = operations.Inspect(t.Context(), e.db, e.cfg, ""); err != nil {
		t.Fatal(err)
	}
	have := strings.Join(report.Warnings, ",")
	if !strings.Contains(have, "payment_callbacks_manual_review_only") || !strings.Contains(have, "payment_test_price_enabled") {
		t.Fatalf("warnings %v", report.Warnings)
	}
}

// R6-01 (rollback preparation): the operator command ends every impersonation session,
// leaves ordinary sessions alone, audits each one and is safe to repeat.
func TestRevokeImpersonationSessions(t *testing.T) {
	e := newConsoleEnv(t, nil)
	target, bystander := e.user("revoke-target", "user"), e.user("revoke-bystander", "user")
	b := e.loginAgain(e.admin)
	e.want(e.impersonate(b, target.ID), 200)
	if _, status := e.meOf(b); status != 200 {
		t.Fatal("impersonation did not start")
	}
	report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
	if err != nil || report.Impersonation.ActiveSessions != 1 || !strings.Contains(strings.Join(report.Warnings, ","), "impersonation_sessions_active_revoke_before_rollback") {
		t.Fatalf("readiness does not show the open impersonation: %+v %v", report.Impersonation, err)
	}
	count, err := operations.RevokeImpersonationSessions(t.Context(), e.db)
	if err != nil || count != 1 {
		t.Fatalf("revoked %d: %v", count, err)
	}
	if _, status := e.meOf(b); status != 401 {
		t.Fatalf("impersonation session survived: %d", status)
	}
	if e.want(e.call(bystander.cookies, "GET", "/api/v1/auth/me", nil, ""), 200) == nil || e.want(e.call(e.admin.cookies, "GET", "/api/v1/admin/console/users", nil, ""), 200) == nil {
		t.Fatal("ordinary sessions were touched")
	}
	var audits int64
	e.db.Model(&model.AdminAudit{}).Where("action = ? AND actor_id = ? AND target_id = ?", operations.AuditImpersonateRevoked, e.admin.ID, target.ID).Count(&audits)
	if audits != 1 {
		t.Fatalf("%d audit rows", audits)
	}
	if again, err := operations.RevokeImpersonationSessions(t.Context(), e.db); err != nil || again != 0 {
		t.Fatalf("second run revoked %d: %v", again, err)
	}
	if report, _ = operations.Inspect(t.Context(), e.db, e.cfg, ""); report.Impersonation.ActiveSessions != 0 || strings.Contains(strings.Join(report.Warnings, ","), "impersonation_sessions") {
		t.Fatalf("readiness still warns: %+v", report)
	}
}
