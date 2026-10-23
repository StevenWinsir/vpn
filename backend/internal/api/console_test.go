package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// Round 4 of handoff_payment_account.md (R4-01 … R4-07): the administrator console,
// part one. Everything runs against the real router and a private PostgreSQL.

type consoleEnv struct {
	*payEnv
	admin payUser
}

func newConsoleEnv(t *testing.T, mutate func(*config.Config)) *consoleEnv {
	t.Helper()
	e := newPayEnv(t, func(c *config.Config) {
		c.ClientNodeCatalog = false // the console must not depend on it (R4-01)
		if mutate != nil {
			mutate(c)
		}
	})
	return &consoleEnv{payEnv: e, admin: e.user("root", "admin")}
}

func (e *consoleEnv) get(path string) *httptest.ResponseRecorder {
	return e.call(e.admin.cookies, "GET", "/api/v1/admin/console"+path, nil, "")
}

func (e *consoleEnv) post(path string, body any) *httptest.ResponseRecorder {
	return e.call(e.admin.cookies, "POST", "/api/v1/admin/console"+path, body, "")
}

func (e *consoleEnv) decode(w *httptest.ResponseRecorder, status int, into any) {
	e.t.Helper()
	if w.Code != status {
		e.t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if into != nil {
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *consoleEnv) code(w *httptest.ResponseRecorder, status int, code string) {
	e.t.Helper()
	out := e.want(w, status)
	if got := payErrorCode(out); got != code {
		e.t.Fatalf("error code %q want %q", got, code)
	}
}

type userPage struct {
	Users []consoleUser `json:"users"`
	Total int64         `json:"total"`
}

func (e *consoleEnv) users(query string) userPage {
	e.t.Helper()
	var out userPage
	e.decode(e.get("/users?"+query), 200, &out)
	return out
}

func emails(p userPage) []string {
	out := make([]string, len(p.Users))
	for i, u := range p.Users {
		out[i] = u.Email
	}
	return out
}

func (e *consoleEnv) grant(userID, plan string, days int) consoleSub {
	e.t.Helper()
	var out struct {
		Subscription consoleSub `json:"subscription"`
	}
	e.decode(e.post("/users/"+userID+"/subscription", map[string]any{"version": 0, "plan_id": plan, "expires_at": e.clock.Now().AddDate(0, 0, days)}), 200, &out)
	return out.Subscription
}

// R4-01
func TestConsoleAccessControl(t *testing.T) {
	e := newConsoleEnv(t, nil)
	user := e.user("plain", "user")
	target := e.user("target", "user")
	id := target.ID
	order := uuid.NewString()
	routes := []struct{ method, path string }{
		{"GET", "/users"}, {"GET", "/users/" + id}, {"POST", "/users/" + id + "/password"}, {"POST", "/users/" + id + "/subscription"},
		{"POST", "/users/" + id + "/subscription/end"}, {"POST", "/users/" + id + "/delete"},
		{"GET", "/orders"}, {"GET", "/orders/" + order}, {"POST", "/orders/" + order + "/check"}, {"POST", "/orders/" + order + "/confirm"}, {"POST", "/orders/" + order + "/close"},
		{"GET", "/logins"}, {"GET", "/coupons"},
	}
	for _, r := range routes {
		var body any
		if r.method == "POST" {
			body = map[string]any{}
		}
		path := "/api/v1/admin/console" + r.path
		if w := e.call(nil, r.method, path, body, ""); w.Code != 401 {
			t.Fatalf("anonymous %s %s: %d", r.method, r.path, w.Code)
		}
		if w := e.call(user.cookies, r.method, path, body, ""); w.Code != 403 {
			t.Fatalf("user %s %s: %d", r.method, r.path, w.Code)
		}
		if w := e.call(e.admin.cookies, r.method, path, body, ""); w.Code == 401 || w.Code == 403 && r.method == "GET" {
			t.Fatalf("admin %s %s: %d %s", r.method, r.path, w.Code, w.Body.String())
		}
	}
	// The console works with the node catalog off; node administration keeps its own gate.
	e.want(e.get("/users"), 200)
	if w := e.call(e.admin.cookies, "GET", "/api/v1/admin/nodes", nil, ""); w.Code == 200 {
		t.Fatalf("node administration unexpectedly open without the catalog: %d", w.Code)
	}
	// Status codes and filters are validated, not ignored.
	for _, q := range []string{"page=0", "page=x", "page_size=7", "role=root", "sub=forever", "sort=password_hash", "order=sideways", "created_from=yesterday", "plan=" + strings.Repeat("x", 33)} {
		e.want(e.get("/users?"+q), 400)
	}
}

// R4-02
func TestConsoleUserListFilters(t *testing.T) {
	e := newConsoleEnv(t, nil)
	now := e.clock.Now()
	mk := func(email, name, role, status string, created time.Time, login *time.Time, ip *string) model.User {
		u := model.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: payHash, Role: role, Status: status, AuthVersion: 1, CreatedAt: created, LastLoginAt: login, LastLoginIP: ip, RegisterIP: ip}
		if err := e.db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}
	ip := "203.0.113.9"
	l1, l2 := now.Add(-time.Hour), now.Add(-72*time.Hour)
	a := mk("alpha@example.com", "Alpha", "user", "active", now.Add(-10*24*time.Hour), &l1, &ip)
	b := mk("beta_100%@example.com", "Beta", "user", "active", now.Add(-8*24*time.Hour), &l2, nil)
	c := mk("gamma@example.com", "Gamma", "user", "active", now.Add(-6*24*time.Hour), nil, nil)
	d := mk("delta@example.com", "Delta", "user", "deleting", now.Add(-4*24*time.Hour), nil, nil)
	_ = d
	// Subscriptions: alpha active starter, beta expired pro, gamma active test.
	e.grant(a.ID, "starter", 20)
	e.grant(b.ID, "pro", 20)
	e.db.Model(&model.Subscription{}).Where("user_id = ?", b.ID).Update("expires_at", now.Add(-time.Hour))
	e.grant(c.ID, "starter", 20)
	e.db.Model(&model.Subscription{}).Where("user_id = ?", c.ID).Update("is_test", true)

	check := func(query string, want ...string) {
		t.Helper()
		got := emails(e.users(query))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: got %v want %v", query, got, want)
		}
	}
	check("q=alpha", "alpha@example.com")
	check("q=ALPHA", "alpha@example.com")
	check("q=Gam", "gamma@example.com") // by name too
	check("q="+url.QueryEscape("%"), "beta_100%@example.com")
	check("q="+url.QueryEscape("_"), "beta_100%@example.com")
	check("q=" + url.QueryEscape("a_p")) // "_" is literal, not "any character"
	check("q=" + url.QueryEscape(`\`))
	check("status=deleting", "delta@example.com")
	check("role=admin&sort=created_at", "root@example.com")
	check("sub=active&sort=created_at&order=asc", "alpha@example.com")
	check("sub=expired", "beta_100%@example.com")
	check("sub=test", "gamma@example.com")
	check("sub=none&role=user&sort=created_at&order=asc", "delta@example.com")
	check("plan=pro", "beta_100%@example.com")
	check("role=user&sort=created_at&order=asc", "alpha@example.com", "beta_100%@example.com", "gamma@example.com", "delta@example.com")
	check("role=user&sort=created_at&order=desc", "delta@example.com", "gamma@example.com", "beta_100%@example.com", "alpha@example.com")
	if got := emails(e.users("role=user&sort=last_login_at&order=desc")); got[0] != "alpha@example.com" || got[1] != "beta_100%@example.com" {
		t.Fatalf("last_login desc: %v", got)
	}
	// Never-signed-in accounts sort last in both directions.
	if got := emails(e.users("role=user&sort=last_login_at&order=asc")); got[0] != "beta_100%@example.com" || got[1] != "alpha@example.com" {
		t.Fatalf("last_login asc: %v", got)
	}
	e.db.Model(&model.Subscription{}).Where("user_id = ?", c.ID).Update("expires_at", now.AddDate(0, 0, 25))
	check("role=user&sort=expires_at&order=desc&sub=test", "gamma@example.com")
	if got := emails(e.users("role=user&sort=expires_at&order=desc")); got[0] != "gamma@example.com" || got[1] != "alpha@example.com" || got[2] != "beta_100%@example.com" {
		t.Fatalf("expires desc: %v", got)
	}
	check("created_from="+now.Add(-9*24*time.Hour).Format(time.RFC3339)+"&created_to="+now.Add(-5*24*time.Hour).Format(time.RFC3339)+"&sort=created_at&order=asc", "beta_100%@example.com", "gamma@example.com")
	check("role=user&login_from="+now.Add(-2*time.Hour).Format(time.RFC3339), "alpha@example.com")
	check("role=user&login_to="+now.Add(-2*time.Hour).Format(time.RFC3339), "beta_100%@example.com")
	// Combined filters narrow, never widen.
	check("q=a&role=user&sub=active&plan=starter&status=active&sort=created_at&order=asc", "alpha@example.com")

	page := e.users("role=user&sort=created_at&order=asc&page_size=50")
	if page.Total != 4 || len(page.Users) != 4 {
		t.Fatalf("total %d", page.Total)
	}
	first := page.Users[0]
	if first.RegisterIP == nil || *first.RegisterIP != ip || first.LastLoginIP == nil || first.Subscription == nil || first.Subscription.PlanID != "starter" || first.Subscription.State != "active" {
		t.Fatalf("row %+v", first)
	}
	if page.Users[3].RegisterIP != nil || page.Users[3].LastLoginAt != nil || page.Users[3].Subscription != nil {
		t.Fatalf("a user with no history must show nothing: %+v", page.Users[3])
	}
	// Paging.
	p2 := e.users("role=user&sort=created_at&order=asc&page=2&page_size=20")
	if len(p2.Users) != 0 || p2.Total != 4 {
		t.Fatalf("page 2: %+v", p2)
	}
	// Online devices and the sign-in location come from their own tables.
	e.db.Create(&model.LoginEvent{ID: uuid.NewString(), UserID: a.ID, Channel: "web", IP: ip, Result: "success", OccurredAt: l1, GeoStatus: model.GeoDone, GeoText: "日本 东京"})
	if got := e.users("q=alpha").Users[0].LastLoginGeo; got != "日本 东京" {
		t.Fatalf("geo %q", got)
	}
}

// R4-02: 10,000 users, the filters the console offers, timed.
func TestConsoleUserListScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale check")
	}
	e := newConsoleEnv(t, nil)
	schema := e.cfg.Schema
	if err := e.db.Exec(`INSERT INTO "` + schema + `".users (id, email, name, password_hash, role, status, created_at, updated_at, auth_version, last_login_at, last_login_ip, register_ip)
		SELECT gen_random_uuid(), 'bulk' || g || '@example.com', 'Bulk ' || g, 'x', 'user', 'active', now() - (g || ' minutes')::interval, now(), 1,
		CASE WHEN g % 3 = 0 THEN now() - (g || ' seconds')::interval END, '198.51.100.' || (g % 250), '198.51.100.' || (g % 250)
		FROM generate_series(1, 10000) AS g`).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.db.Exec(`INSERT INTO "` + schema + `".subscriptions (id, user_id, plan_id, plan_name, starts_at, expires_at, traffic_limit_bytes, used_units, max_devices, allow_dedicated, is_test, created_at, updated_at, version)
		SELECT gen_random_uuid(), id, 'starter', '轻量计划', now() - interval '1 day', now() + (random() * 30 || ' days')::interval, 1073741824, 0, 2, false, false, now(), now(), 1
		FROM "` + schema + `".users WHERE email LIKE 'bulk%' AND email ~ '[02468]@'`).Error; err != nil {
		t.Fatal(err)
	}
	queries := []string{"sort=created_at&order=desc", "sort=last_login_at&order=desc&page=100&page_size=50", "q=bulk99&sort=expires_at&order=asc", "sub=active&plan=starter&sort=expires_at&page=50",
		"login_from=" + url.QueryEscape(e.clock.Now().Add(-time.Hour).Format(time.RFC3339)), "q=" + url.QueryEscape("%_%"), "status=active&role=user&sub=none&page=100"}
	for _, q := range queries {
		start := time.Now()
		var out userPage
		e.decode(e.get("/users?"+q), 200, &out)
		took := time.Since(start)
		t.Logf("10,000 users: %-85s %4d rows of %5d in %v", q, len(out.Users), out.Total, took.Round(time.Millisecond))
		if took > 3*time.Second {
			t.Fatalf("%s took %v", q, took)
		}
	}
	// A worst case for the login panel too: 10,000 events.
	if err := e.db.Exec(`INSERT INTO "` + schema + `".login_events (id, user_id, channel, platform, ip, user_agent, result, failure_reason, occurred_at, geo_status, geo_text, geo_attempts)
		SELECT gen_random_uuid(), id, 'web', '', register_ip, '', CASE WHEN random() < 0.3 THEN 'failure' ELSE 'success' END, '', now() - (random() * 3600 * 24 || ' seconds')::interval, 'done', 'x', 0
		FROM "` + schema + `".users WHERE register_ip IS NOT NULL`).Error; err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"", "quick=shared_ip", "quick=burst_failures", "result=failure&page=100", "email=bulk77"} {
		start := time.Now()
		var out struct{ Total int64 }
		e.decode(e.get("/logins?"+q), 200, &out)
		t.Logf("10,000 sign-ins: %-30q total %5d in %v", q, out.Total, time.Since(start).Round(time.Millisecond))
		if time.Since(start) > 3*time.Second {
			t.Fatalf("logins %s too slow", q)
		}
	}
}

func TestConsoleUserDetail(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("detail", "user")
	e.want(e.get("/users/"+uuid.NewString()), 404)
	e.want(e.get("/users/not-a-uuid"), 404)
	e.grant(u.ID, "starter", 10)
	p := e.place(u, "starter", payment.TypeWeChat, "")
	e.db.Create(&model.LoginEvent{ID: uuid.NewString(), UserID: u.ID, Channel: "native", Platform: "windows", IP: "203.0.113.5", Result: "failure", FailureReason: "bad_password", OccurredAt: e.clock.Now(), GeoStatus: model.GeoPending})
	var out struct {
		User    consoleUser    `json:"user"`
		Orders  []consoleOrder `json:"orders"`
		Logins  []consoleLogin `json:"logins"`
		Devices struct {
			Online   []any `json:"online"`
			SignedIn []any `json:"signed_in"`
		} `json:"devices"`
	}
	raw := e.get("/users/" + u.ID)
	e.decode(raw, 200, &out)
	if out.User.Email != u.Email || out.User.Subscription == nil || out.User.Subscription.PlanID != "starter" || len(out.Orders) != 1 || out.Orders[0].ID != p.Order.ID || len(out.Logins) < 2 {
		t.Fatalf("detail %s", raw.Body.String())
	}
	// Never the password hash, tokens or gateway links.
	for _, banned := range []string{"password_hash", "$2a$", "refresh", "pay_url", "sign="} {
		if strings.Contains(strings.ToLower(raw.Body.String()), banned) {
			t.Fatalf("detail leaks %q", banned)
		}
	}
	if out.Devices.Online == nil || out.Devices.SignedIn == nil {
		t.Fatal("device lists must be arrays")
	}
}

// R4-04, R4-06
func TestConsoleUserPassword(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("pwtarget", "user")
	other := e.user("otheradmin", "admin")
	body := func(adminPw, pw string, notify bool) map[string]any {
		return map[string]any{"admin_password": adminPw, "new_password": pw, "notify": notify}
	}
	const newPw = "Administrator-Set-2026"
	e.code(e.post("/users/"+u.ID+"/password", body("wrong", newPw, false)), 403, "incorrect_password")
	e.code(e.post("/users/"+u.ID+"/password", body(payPassword, "short", false)), 400, "invalid_password")
	e.want(e.post("/users/"+u.ID+"/password", map[string]any{"admin_password": payPassword, "new_password": newPw, "role": "admin"}), 400)
	// Other administrators and the acting administrator are protected (D9).
	e.code(e.post("/users/"+other.ID+"/password", body(payPassword, newPw, false)), 403, "admin_account_protected")
	e.code(e.post("/users/"+e.admin.ID+"/password", body(payPassword, newPw, false)), 403, "self_protected")
	e.code(e.post("/users/"+uuid.NewString()+"/password", body(payPassword, newPw, false)), 404, "user_not_found")
	if got := e.reload(u.ID); got.AuthVersion != 1 {
		t.Fatal("a refused change altered the account")
	}
	// The one session the user has dies, and they can sign in with the new password only.
	if w := e.call(u.cookies, "GET", "/api/v1/auth/me", nil, ""); w.Code != 200 {
		t.Fatal("user session")
	}
	e.want(e.post("/users/"+u.ID+"/password", body(payPassword, newPw, true)), 200)
	if w := e.call(u.cookies, "GET", "/api/v1/auth/me", nil, ""); w.Code != 401 {
		t.Fatalf("old session survived: %d", w.Code)
	}
	if w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": payPassword}, ""); w.Code != 401 {
		t.Fatal("old password still works")
	}
	if w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": newPw}, ""); w.Code != 200 {
		t.Fatal("new password does not work")
	}
	// The administrator is still signed in.
	e.want(e.get("/users"), 200)
	var audit model.AdminAudit
	if err := e.db.Where("action = ? AND target_id = ?", AuditUserPassword, u.ID).First(&audit).Error; err != nil || audit.ActorID != e.admin.ID {
		t.Fatalf("audit: %v", err)
	}
	// Second factor: five misses within the window lock the administrator out (R4-04).
	for range 3 {
		e.code(e.post("/users/"+u.ID+"/password", body("nope", newPw, false)), 403, "incorrect_password")
	}
	e.code(e.post("/users/"+u.ID+"/password", body("nope", newPw, false)), 403, "incorrect_password") // the 5th miss in the window (one came earlier)
	w := e.post("/users/"+u.ID+"/password", body(payPassword, newPw+"2", false))
	e.code(w, 429, "reauth_rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	e.clock.Advance(adminReauthWindow + time.Minute)
	e.want(e.post("/users/"+u.ID+"/password", body(payPassword, newPw+"2", false)), 200)
}

func (e *consoleEnv) reload(id string) model.User {
	e.t.Helper()
	var u model.User
	if err := e.db.First(&u, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return u
}

// R4-03
func TestConsoleUserSubscription(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("subtarget", "user")
	otherAdmin := e.user("second", "admin")
	path := "/users/" + u.ID + "/subscription"
	now := e.clock.Now()

	// Granting needs a plan and an expiry; limits are enforced.
	e.code(e.post(path, map[string]any{"version": 0}), 400, "invalid_subscription")
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "nope", "expires_at": now.Add(time.Hour * 48)}), 400, "plan_not_found")
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.Add(-time.Hour)}), 400, "invalid_expiry")
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.AddDate(11, 0, 0)}), 400, "invalid_expiry")
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.AddDate(0, 0, 5), "traffic_limit_bytes": 0}), 400, "invalid_traffic")
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.AddDate(0, 0, 5), "used": "bogus"}), 400, "invalid_subscription")
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.AddDate(0, 0, 5), "extra": 1}), 400, "invalid_json")
	if e.subscription(u.ID) != nil {
		t.Fatal("a refused edit created a subscription")
	}
	first := e.grant(u.ID, "starter", 30)
	if first.Version != 1 || first.PlanID != "starter" || first.State != "active" {
		t.Fatalf("granted %+v", first)
	}
	var plan model.Plan
	e.db.First(&plan, "id = ?", "starter")
	if first.TrafficLimitBytes != plan.TrafficBytes || first.IsTest {
		t.Fatalf("defaults %+v", first)
	}

	// A session of the client is bound to the entitlement; a renewal keeps it, a plan change does not.
	sub := e.subscription(u.ID)
	sess := model.NativeSession{SubscriptionID: &sub.ID, SubscriptionStartsAt: &sub.StartsAt, EntitlementFingerprint: nativeEntitlementFingerprint(sub)}
	if !nativeAuthorizationBound(sess, sub) {
		t.Fatal("test session is not bound")
	}
	// A period ledger row exists once a grant was made; edits must keep it in step.
	if _, err := ensurePeriod(e.db, sub); err != nil {
		t.Fatal(err)
	}
	e.db.Model(&model.Subscription{}).Where("id = ?", sub.ID).Update("used_units", 5000)

	// Two administrators open the same form; the second to save loses (R4-03).
	version := first.Version
	e.want(e.post(path, map[string]any{"version": version, "expires_at": now.AddDate(0, 0, 60)}), 200)
	stale := e.call(otherAdmin.cookies, "POST", "/api/v1/admin/console"+path, map[string]any{"version": version, "traffic_limit_bytes": 2 * gib}, "")
	e.code(stale, 409, "version_conflict")
	after := e.subscription(u.ID)
	if after.TrafficLimitBytes != plan.TrafficBytes || !after.ExpiresAt.Equal(now.AddDate(0, 0, 60)) {
		t.Fatalf("the losing edit changed data: %+v", after)
	}
	if !nativeAuthorizationBound(sess, after) {
		t.Fatal("a longer expiry must not disturb the client")
	}
	// A purchase between load and save also invalidates the form.
	e.decode(e.get("/users/"+u.ID), 200, nil)
	p := e.place(u, "starter", payment.TypeAlipay, "")
	gw, _ := e.gw.open(t, p.PayURL)
	e.want(e.pay(gw), 200)
	e.code(e.post(path, map[string]any{"version": after.Version, "traffic_limit_bytes": gib}), 409, "version_conflict")
	after = e.subscription(u.ID)
	if after.Version <= version+1 {
		t.Fatalf("the purchase did not move the version: %d", after.Version)
	}

	// Reset usage: the subscription and its ledger agree.
	e.want(e.post(path, map[string]any{"version": after.Version, "used": "reset"}), 200)
	after = e.subscription(u.ID)
	var period model.SubscriptionPeriod
	e.db.First(&period, "subscription_id = ? AND starts_at = ?", after.ID, after.StartsAt)
	if after.UsedUnits != 0 || period.UsedUnits != 0 || period.LimitBytes != after.TrafficLimitBytes || !period.ExpiresAt.Equal(after.ExpiresAt) {
		t.Fatalf("period out of step: sub %+v period %+v", after, period)
	}
	e.want(e.post(path, map[string]any{"version": after.Version, "used": "set", "used_units": 1234, "traffic_limit_bytes": 3 * gib}), 200)
	after = e.subscription(u.ID)
	e.db.First(&period, "id = ?", period.ID)
	if after.UsedUnits != 1234 || period.UsedUnits != 1234 || period.LimitBytes != 3*gib {
		t.Fatalf("set: sub %+v period %+v", after, period)
	}
	e.code(e.post(path, map[string]any{"version": after.Version}), 400, "no_change")

	// Changing the plan makes clients sign in again.
	e.want(e.post(path, map[string]any{"version": after.Version, "plan_id": "pro"}), 200)
	changed := e.subscription(u.ID)
	if changed.PlanID != "pro" || changed.MaxDevices == after.MaxDevices && changed.AllowDedicated == after.AllowDedicated && changed.PlanName == after.PlanName {
		t.Fatalf("plan not applied: %+v", changed)
	}
	if nativeAuthorizationBound(sess, changed) {
		t.Fatal("a plan change must make the client authorise again")
	}

	// End now.
	end := "/users/" + u.ID + "/subscription/end"
	e.code(e.post(end, map[string]any{"version": changed.Version - 1}), 409, "version_conflict")
	var ended struct {
		Subscription consoleSub `json:"subscription"`
	}
	e.decode(e.post(end, map[string]any{"version": changed.Version}), 200, &ended)
	if ended.Subscription.State != "expired" {
		t.Fatalf("not ended: %+v", ended)
	}
	e.code(e.post(end, map[string]any{"version": ended.Subscription.Version}), 409, "subscription_not_active")
	e.db.First(&period, "id = ?", period.ID)
	if period.ExpiresAt.After(now.Add(time.Minute)) {
		t.Fatalf("period still open: %v", period.ExpiresAt)
	}
	// Nothing for accounts that no longer exist or are being deleted.
	e.code(e.post("/users/"+uuid.NewString()+"/subscription", map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.AddDate(0, 0, 5)}), 404, "user_not_found")
	e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("status", model.UserDeleting)
	e.code(e.post(path, map[string]any{"version": 0, "plan_id": "starter", "expires_at": now.AddDate(0, 0, 5)}), 409, "user_deleting")
}

// R4-04, R3-03 for administrators
func TestConsoleUserDelete(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("doomed", "user")
	other := e.user("peer", "admin")
	del := func(id, email, pw string) *httptest.ResponseRecorder {
		return e.post("/users/"+id+"/delete", map[string]any{"admin_password": pw, "email": email})
	}
	e.code(del(e.admin.ID, e.admin.Email, payPassword), 403, "self_protected")
	e.code(del(other.ID, other.Email, payPassword), 403, "admin_account_protected")
	e.code(del(u.ID, u.Email, "wrong"), 403, "incorrect_password")
	e.code(del(u.ID, "someone-else@example.com", payPassword), 400, "email_mismatch")
	e.code(del(uuid.NewString(), u.Email, payPassword), 404, "user_not_found")
	// A live payment order blocks it, like it blocks the owner.
	e.grant(u.ID, "starter", 10)
	p := e.place(u, "starter", payment.TypeWeChat, "")
	w := del(u.ID, u.Email, payPassword)
	e.code(w, 409, "pending_orders")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After for the pending-order block")
	}
	e.clock.Advance(e.cfg.Payment.Window() + time.Second)
	e.want(del(u.ID, " "+strings.ToUpper(u.Email)+" ", payPassword), 200)
	if got := e.reload(u.ID); got.Status != model.UserDeleting || got.DeletionRequestedAt == nil {
		t.Fatalf("status %s", got.Status)
	}
	if w := e.call(u.cookies, "GET", "/api/v1/auth/me", nil, ""); w.Code != 401 {
		t.Fatal("deleted user's session survived")
	}
	if w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": payPassword}, ""); w.Code != 401 {
		t.Fatal("deleted user can sign in")
	}
	e.code(del(u.ID, u.Email, payPassword), 409, "user_deleting")
	var audit model.AdminAudit
	if err := e.db.Where("action = ? AND target_id = ?", AuditUserDelete, u.ID).First(&audit).Error; err != nil {
		t.Fatal("not audited")
	}
	// The purge loop finishes it; the order and subscription go with the user.
	res, err := e.server().purgeDeletedAccounts(t.Context())
	if err != nil || res.Purged != 1 {
		t.Fatalf("purge %+v %v", res, err)
	}
	var left int64
	e.db.Model(&model.User{}).Where("id = ?", u.ID).Count(&left)
	e.db.Model(&model.Order{}).Where("id = ?", p.Order.ID).Count(&left)
	if left != 0 {
		t.Fatal("rows remain after the purge")
	}
	e.want(e.get("/users?q="+url.QueryEscape(u.Email)), 200)
}

func (e *consoleEnv) orders(query string) []consoleOrder {
	e.t.Helper()
	var out struct {
		Orders []consoleOrder `json:"orders"`
	}
	e.decode(e.get("/orders?"+query), 200, &out)
	return out.Orders
}

// R4-05
func TestConsoleOrders(t *testing.T) {
	e := newConsoleEnv(t, func(c *config.Config) { c.Payment.NotifyAllowed = nil })
	u, v := e.user("buyer1", "user"), e.user("buyer2", "user")
	// Without an allow-list every callback goes to review (decision D4).
	p1 := e.place(u, "starter", payment.TypeWeChat, "")
	gw1, _ := e.gw.open(t, p1.PayURL)
	e.want(e.pay(gw1), 200)
	if o := e.order(p1.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewUnverifiedSource {
		t.Fatalf("order %+v", o)
	}
	p2 := e.place(v, "pro", payment.TypeAlipay, "")
	p3 := e.place(v, "starter", payment.TypeWeChat, "")
	e.db.Model(&model.Order{}).Where("id = ?", p3.Order.ID).Update("price_source", priceTest)

	if got := e.orders("status=review"); len(got) != 1 || got[0].ID != p1.Order.ID || got[0].Email != u.Email || got[0].ReviewReason != model.ReviewUnverifiedSource {
		t.Fatalf("review filter %+v", got)
	}
	if got := e.orders("pay_type=alipay"); len(got) != 1 || got[0].ID != p2.Order.ID {
		t.Fatalf("pay type filter %+v", got)
	}
	if got := e.orders("source=test_price"); len(got) != 1 || got[0].ID != p3.Order.ID {
		t.Fatalf("source filter %+v", got)
	}
	if got := e.orders("email=" + url.QueryEscape("%")); len(got) != 0 {
		t.Fatalf("wildcard matched %d orders", len(got))
	}
	if got := e.orders("email=buyer2"); len(got) != 2 {
		t.Fatalf("email filter %+v", got)
	}
	if got := e.orders("trade_no=" + *p2.Order.TradeNo); len(got) != 1 || got[0].ID != p2.Order.ID {
		t.Fatalf("trade filter %+v", got)
	}
	if got := e.orders("order_id=" + p3.Order.ID); len(got) != 1 {
		t.Fatalf("id filter %+v", got)
	}
	if got := e.orders("order_id=junk"); len(got) != 0 {
		t.Fatalf("junk id matched %+v", got)
	}
	e.want(e.get("/orders?status=refunded"), 400)

	// Detail: the callback ledger with its source address, and the read-only gateway query.
	var detail struct {
		Order         consoleOrder          `json:"order"`
		Notifications []consoleNotification `json:"notifications"`
	}
	e.decode(e.get("/orders/"+p1.Order.ID), 200, &detail)
	if len(detail.Notifications) != 1 || detail.Notifications[0].SourceIP != gatewayIP || detail.Notifications[0].Result != model.NotifyReview || detail.Order.PaidCents == nil {
		t.Fatalf("detail %+v", detail)
	}
	e.want(e.get("/orders/"+uuid.NewString()), 404)
	var check struct {
		GatewayStatus string `json:"gateway_status"`
	}
	e.decode(e.post("/orders/"+p1.Order.ID+"/check", map[string]any{}), 200, &check)
	if check.GatewayStatus != payment.StatusPaid {
		t.Fatalf("gateway says %q", check.GatewayStatus)
	}
	if o := e.order(p1.Order.ID); o.Status != model.OrderReview {
		t.Fatal("a status check changed the order")
	}

	// Confirmation: second factor, a note, an amount.
	confirm := func(id string, body map[string]any) *httptest.ResponseRecorder {
		return e.post("/orders/"+id+"/confirm", body)
	}
	ok := map[string]any{"admin_password": payPassword, "paid_cents": p1.Order.AmountCents, "note": "商户后台已核实"}
	e.code(confirm(p1.Order.ID, map[string]any{"admin_password": "wrong", "paid_cents": 1990, "note": "x"}), 403, "incorrect_password")
	e.code(confirm(p1.Order.ID, map[string]any{"admin_password": payPassword, "paid_cents": 1990, "note": " "}), 400, "invalid_note")
	e.code(confirm(p1.Order.ID, map[string]any{"admin_password": payPassword, "paid_cents": 0, "note": "x"}), 400, "invalid_amount")
	e.code(confirm(p2.Order.ID, map[string]any{"admin_password": payPassword, "paid_cents": 3990, "note": "x"}), 409, "order_not_reviewable") // still pending
	if e.subscription(u.ID) != nil {
		t.Fatal("a refused confirmation granted a subscription")
	}
	e.want(confirm(p1.Order.ID, ok), 200)
	paid := e.order(p1.Order.ID)
	if paid.Status != model.OrderPaid || paid.PaidCents == nil || *paid.PaidCents != p1.Order.AmountCents || paid.PaidAt == nil {
		t.Fatalf("confirmed %+v", paid)
	}
	sub := e.subscription(u.ID)
	var starter model.Plan
	e.db.First(&starter, "id = ?", "starter")
	if sub == nil || sub.PlanID != "starter" || sub.TrafficLimitBytes != starter.TrafficBytes {
		t.Fatalf("subscription %+v", sub)
	}
	// Once only, and the audit is there.
	e.code(confirm(p1.Order.ID, ok), 409, "order_not_reviewable")
	if e.subscription(u.ID).TrafficLimitBytes != starter.TrafficBytes {
		t.Fatal("double fulfilment")
	}
	var audit model.AdminAudit
	if err := e.db.Where("action = ? AND target_id = ?", AuditPaymentConfirm, p1.Order.ID).First(&audit).Error; err != nil || !strings.Contains(audit.Detail, "商户后台已核实") {
		t.Fatalf("audit %v %s", err, audit.Detail)
	}
	if e.admin.ID != audit.ActorID {
		t.Fatal("audit actor")
	}

	// A plan that cannot apply is refused without side effects; closing is final.
	e.db.Model(&model.Order{}).Where("id = ?", p2.Order.ID).Updates(map[string]any{"status": model.OrderReview, "review_reason": model.ReviewAmountMismatch})
	e.grant(v.ID, "starter", 10)
	e.code(confirm(p2.Order.ID, map[string]any{"admin_password": payPassword, "paid_cents": 3990, "note": "n"}), 409, "fulfil_conflict")
	if o := e.order(p2.Order.ID); o.Status != model.OrderReview {
		t.Fatal("a refused confirmation moved the order")
	}
	e.code(e.post("/orders/"+p2.Order.ID+"/close", map[string]any{"note": ""}), 400, "invalid_note")
	e.want(e.post("/orders/"+p2.Order.ID+"/close", map[string]any{"note": "线下退款"}), 200)
	if o := e.order(p2.Order.ID); o.Status != model.OrderFailed || o.ReviewReason != model.ReviewClosed {
		t.Fatalf("closed %+v", o)
	}
	e.code(e.post("/orders/"+p2.Order.ID+"/close", map[string]any{"note": "again"}), 409, "order_not_reviewable")
	// A callback after the administrator closed it does not reopen it.
	gw2, _ := e.gw.open(t, p2.PayURL)
	e.gw.markPaid(gw2)
	e.want(e.notify(gatewayIP, callbackBody(gw2, payment.TradeSuccess)), 200)
	if o := e.order(p2.Order.ID); o.Status != model.OrderFailed {
		t.Fatalf("callback reopened a closed order: %s", o.Status)
	}
	var closeAudit model.AdminAudit
	if err := e.db.Where("action = ? AND target_id = ?", AuditPaymentClose, p2.Order.ID).First(&closeAudit).Error; err != nil {
		t.Fatal("close not audited")
	}
	// Non-gateway orders cannot be queried.
	legacy := model.Order{ID: uuid.NewString(), UserID: u.ID, IdempotencyKey: uuid.NewString(), PlanID: "starter", PlanName: "x", Status: model.OrderPaidTest, Provider: "test", CreatedAt: e.clock.Now()}
	e.db.Create(&legacy)
	e.code(e.post("/orders/"+legacy.ID+"/check", map[string]any{}), 409, "no_gateway_order")
	// Summary: the confirmed order counts, test-price orders do not.
	var list struct {
		Summary struct {
			Today struct{ Count, Cents int64 } `json:"today"`
			Month struct{ Count, Cents int64 } `json:"month"`
		} `json:"summary"`
	}
	e.decode(e.get("/orders"), 200, &list)
	if list.Summary.Today.Count != 1 || list.Summary.Today.Cents != p1.Order.AmountCents || list.Summary.Month.Count != 1 {
		t.Fatalf("summary %+v", list.Summary)
	}
}

// R4-05: a confirmation and a late callback for the same order fulfil it once.
func TestConsoleConfirmRacesCallback(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("racer", "user")
	var starter model.Plan
	e.db.First(&starter, "id = ?", "starter")
	for round := range 5 {
		p := e.place(u, "starter", payment.TypeWeChat, "")
		gw, _ := e.gw.open(t, p.PayURL)
		e.gw.markPaid(gw)
		e.clock.Advance(e.cfg.Payment.Window() + time.Minute)
		if e.reconcile() < 1 {
			t.Fatal("reconcile did nothing")
		}
		if o := e.order(p.Order.ID); o.Status != model.OrderReview || o.ReviewReason != model.ReviewCallbackMissing {
			t.Fatalf("round %d: %+v", round, o)
		}
		before := e.subscription(u.ID)
		var wg sync.WaitGroup
		var confirmCode int
		wg.Add(2)
		go func() {
			defer wg.Done()
			w := e.post("/orders/"+p.Order.ID+"/confirm", map[string]any{"admin_password": payPassword, "paid_cents": p.Order.AmountCents, "note": "race"})
			confirmCode = w.Code
		}()
		go func() {
			defer wg.Done()
			e.notify(gatewayIP, callbackBody(gw, payment.TradeSuccess))
		}()
		wg.Wait()
		if confirmCode != 200 && confirmCode != 409 {
			t.Fatalf("confirm answered %d", confirmCode)
		}
		after := e.subscription(u.ID)
		added := after.TrafficLimitBytes
		if before != nil {
			added -= before.TrafficLimitBytes
		}
		if o := e.order(p.Order.ID); o.Status != model.OrderPaid || added != starter.TrafficBytes {
			t.Fatalf("round %d: status %s, granted %d bytes (want exactly %d)", round, o.Status, added, starter.TrafficBytes)
		}
		var confirms int64
		e.db.Model(&model.AdminAudit{}).Where("action = ? AND target_id = ?", AuditPaymentConfirm, p.Order.ID).Count(&confirms)
		if (confirmCode == 200) != (confirms == 1) {
			t.Fatalf("confirm %d but %d audit rows", confirmCode, confirms)
		}
	}
}

// R4-06: no console write puts a secret in the audit trail.
func TestConsoleAuditDetailsAreClean(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("auditee", "user")
	victim := e.user("victim", "user")
	const secret = "Totally-Secret-New-Password-1"
	e.want(e.post("/users/"+u.ID+"/password", map[string]any{"admin_password": payPassword, "new_password": secret, "notify": false}), 200)
	e.grant(u.ID, "pro", 12)
	e.want(e.post("/users/"+u.ID+"/subscription", map[string]any{"version": e.subscription(u.ID).Version, "used": "set", "used_units": 7}), 200)
	e.want(e.post("/users/"+u.ID+"/subscription/end", map[string]any{"version": e.subscription(u.ID).Version}), 200)
	e.want(e.post("/users/"+victim.ID+"/delete", map[string]any{"admin_password": payPassword, "email": victim.Email}), 200)
	var rows []model.AdminAudit
	e.db.Where("actor_id = ?", e.admin.ID).Find(&rows)
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Action] = true
		if row.Detail != "" && !json.Valid([]byte(row.Detail)) {
			t.Fatalf("%s: detail is not JSON: %s", row.Action, row.Detail)
		}
		low := strings.ToLower(row.Detail)
		for _, banned := range []string{strings.ToLower(secret), strings.ToLower(payPassword), "$2a$", "password", "token", "@example.com", "cookie"} {
			if strings.Contains(low, banned) {
				t.Fatalf("%s: audit detail contains %q: %s", row.Action, banned, row.Detail)
			}
		}
	}
	for _, action := range []string{AuditUserPassword, AuditUserSubscription, AuditUserSubscriptionEnd, AuditUserDelete} {
		if !seen[action] {
			t.Fatalf("no audit row for %s", action)
		}
	}
}

// R4-04 for the sign-in panel and R4-02 filters.
func TestConsoleLogins(t *testing.T) {
	e := newConsoleEnv(t, nil)
	a, b := e.user("logina", "user"), e.user("loginb", "user")
	e.db.Where("1 = 1").Delete(&model.LoginEvent{}) // the sign-ins that created the fixtures
	now := e.clock.Now()
	add := func(u payUser, ip, result, channel string, age time.Duration) {
		ev := model.LoginEvent{ID: uuid.NewString(), UserID: u.ID, Channel: channel, Platform: "", IP: ip, UserAgent: "UA", Result: result, OccurredAt: now.Add(-age), GeoStatus: model.GeoPending}
		if result == model.LoginFailure {
			ev.FailureReason = "bad_password"
		}
		if err := e.db.Create(&ev).Error; err != nil {
			t.Fatal(err)
		}
	}
	add(a, "203.0.113.1", "success", "web", time.Minute)
	add(b, "203.0.113.1", "success", "native", 2*time.Minute) // same address, two accounts
	add(a, "198.51.100.7", "success", "web", 3*time.Minute)
	for i := range 5 {
		add(b, "192.0.2.50", "failure", "web", time.Duration(i+4)*time.Minute)
	}
	add(a, "198.51.100.8", "failure", "web", 3*time.Hour)
	add(a, "203.0.113.1", "success", "web", 48*time.Hour) // outside the 24 h window
	type page struct {
		Logins []consoleLogin `json:"logins"`
		Total  int64          `json:"total"`
	}
	list := func(q string) page {
		t.Helper()
		var out page
		e.decode(e.get("/logins?"+q), 200, &out)
		return out
	}
	if p := list(""); p.Total != 10 || p.Logins[0].Email != a.Email || p.Logins[0].OccurredAt.Before(p.Logins[1].OccurredAt) {
		t.Fatalf("all: %d, first %+v", p.Total, p.Logins[0])
	}
	if p := list("email=loginb"); p.Total != 6 {
		t.Fatalf("email: %d", p.Total)
	}
	if p := list("email=" + url.QueryEscape("_")); p.Total != 0 {
		t.Fatalf("wildcard: %d", p.Total)
	}
	if p := list("ip=192.0.2.50"); p.Total != 5 {
		t.Fatalf("ip: %d", p.Total)
	}
	if p := list("channel=native"); p.Total != 1 || p.Logins[0].Channel != "native" {
		t.Fatalf("channel: %d", p.Total)
	}
	if p := list("result=failure"); p.Total != 6 {
		t.Fatalf("result: %d", p.Total)
	}
	if p := list("quick=shared_ip"); p.Total != 2 {
		t.Fatalf("shared ip: %d (%+v)", p.Total, p.Logins)
	}
	if p := list("quick=burst_failures"); p.Total != 5 || p.Logins[0].Email != b.Email {
		t.Fatalf("burst: %d", p.Total)
	}
	if p := list("from=" + url.QueryEscape(now.Add(-10*time.Minute).Format(time.RFC3339))); p.Total != 8 {
		t.Fatalf("from: %d", p.Total)
	}
	if p := list("page_size=50&to=" + url.QueryEscape(now.Add(-24*time.Hour).Format(time.RFC3339))); p.Total != 1 {
		t.Fatalf("to: %d", p.Total)
	}
	e.want(e.get("/logins?quick=everything"), 400)
	e.want(e.get("/logins?channel=sms"), 400)
	if w := e.call(nil, "GET", "/api/v1/admin/console/logins", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous read")
	}
}
