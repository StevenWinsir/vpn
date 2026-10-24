package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/model"
)

// Round 3 of handoff_payment_account.md (R3-01 … R3-07): account settings, the two
// phase deletion, and login records.

const (
	acctOldPassword = resetOldPassword
	acctNewPassword = "Brand-New-Password-2026"
)

// acctEnv is a regEnv whose API handler reads the same controllable clock as the mail
// service, so rate windows and timestamps are deterministic.
type acctEnv struct {
	*regEnv
	h http.Handler
}

func newAcctEnv(t *testing.T) *acctEnv {
	t.Helper()
	e := newRegEnv(t)
	installBarriers(t, e.db)
	return &acctEnv{regEnv: e, h: newServerAt(e.db, e.cfg, e.mail.service, e.clock.Now)}
}

func (a *acctEnv) user(t *testing.T, role string) model.User {
	t.Helper()
	return a.historicalUser(t, strings.ReplaceAll(uuid.NewString()[:12], "-", "")+"@yahoo.com", role, "active")
}

func (a *acctEnv) login(t *testing.T, u model.User) []*http.Cookie {
	t.Helper()
	return a.webLogin(t, a.h, u.Email, acctOldPassword)
}

func (a *acctEnv) post(path string, body any, cookies []*http.Cookie, options ...requestOption) *httptest.ResponseRecorder {
	return a.send(a.h, "POST", path, body, append([]requestOption{withCookies(cookies)}, options...)...)
}

func (a *acctEnv) me(cookies []*http.Cookie) int {
	return a.send(a.h, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(cookies))).Code
}

func (a *acctEnv) native(t *testing.T, u model.User) string {
	t.Helper()
	w := a.send(a.h, "POST", "/api/v1/client/login", nativeLoginBody(u.Email, acctOldPassword, "windows"))
	expect(t, w, 200, "")
	var out struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Token
}

func (a *acctEnv) nativeStatus(token string) *httptest.ResponseRecorder {
	return a.send(a.h, "GET", "/api/v1/client/session", nil, withBearer(token))
}

func changeBody(current, next string) map[string]string {
	return map[string]string{"current_password": current, "new_password": next}
}

// R3-01
func TestAccountChangePassword(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	browserA, browserB := a.login(t, u), a.login(t, u)
	token := a.native(t, u)
	if a.nativeStatus(token).Code != 200 {
		t.Fatal("client session did not start")
	}

	// Format rules and the unchanged-password rule come before the old password counts.
	expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, "short"), browserA), 400, "invalid_password")
	expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, strings.Repeat("x", 73)), browserA), 400, "invalid_password")
	expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, acctOldPassword), browserA), 400, "password_unchanged")
	expect(t, a.post("/api/v1/me/password", map[string]any{"current_password": acctOldPassword, "new_password": acctNewPassword, "role": "admin"}, browserA), 400, "invalid_json")
	expect(t, a.post("/api/v1/me/password", changeBody("wrong-"+acctOldPassword, acctNewPassword), browserA), 403, "incorrect_password")
	if a.countWhere(t, &model.AdminAudit{}, "action = ?", AuditPasswordChange) != 0 || a.reload(t, u.ID).AuthVersion != 1 {
		t.Fatal("a refused change altered the account")
	}
	if a.me(browserA) != 200 || a.me(browserB) != 200 || a.nativeStatus(token).Code != 200 {
		t.Fatal("a refused change ended a session")
	}

	w := a.post("/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), browserA)
	expect(t, w, 200, "")
	fresh := w.Result().Cookies()
	if len(fresh) != 2 {
		t.Fatalf("expected a fresh session for this browser, got %d cookies", len(fresh))
	}
	if a.me(fresh) != 200 {
		t.Fatal("this browser was signed out by its own change")
	}
	if a.me(browserA) != 401 || a.me(browserB) != 401 {
		t.Fatal("an old browser session survived the change")
	}
	expect(t, a.nativeStatus(token), 401, "native_session_expired")
	expect(t, a.send(a.h, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": acctOldPassword}), 401, "invalid_credentials")
	a.webLogin(t, a.h, u.Email, acctNewPassword)
	got := a.reload(t, u.ID)
	if got.AuthVersion != 2 || got.PasswordChangedAt == nil {
		t.Fatalf("account state after change: version %d", got.AuthVersion)
	}
	if a.countWhere(t, &model.AdminAudit{}, "action = ? AND actor_id = ? AND target_id = ?", AuditPasswordChange, u.ID, u.ID) != 1 {
		t.Fatal("password change was not audited")
	}
	a.mail.deliver(t)
	notices := 0
	for _, m := range a.mail.box.to(u.Email) {
		if strings.Contains(m.Subject, "密码已修改") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("expected one change notice, got %d", notices)
	}
	// Signed-out callers cannot use the endpoint.
	expect(t, a.post("/api/v1/me/password", changeBody(acctNewPassword, acctOldPassword), nil), 401, "unauthorized")
}

// R3-01: only the cookie of a persistent ("remember me") browser stays persistent.
func TestAccountChangePasswordKeepsRememberChoice(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	w := a.send(a.h, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": acctOldPassword, "remember": false})
	expect(t, w, 200, "")
	w = a.post("/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), w.Result().Cookies())
	expect(t, w, 200, "")
	for _, c := range w.Result().Cookies() {
		if c.MaxAge != 0 {
			t.Fatalf("cookie %s became persistent", c.Name)
		}
	}
	remembered := a.webLogin(t, a.h, u.Email, acctNewPassword) // webLogin asks for remember=true
	w = a.post("/api/v1/me/password", changeBody(acctNewPassword, acctOldPassword), remembered)
	expect(t, w, 200, "")
	for _, c := range w.Result().Cookies() {
		if c.MaxAge <= 0 {
			t.Fatalf("cookie %s lost the remember choice", c.Name)
		}
	}
}

// R3-01: five wrong current passwords in 15 minutes lock the endpoint, even for the
// right password; the window passes, the account works again.
func TestAccountPasswordAttemptsAreLimited(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	cookies := a.login(t, u)
	for i := 0; i < 5; i++ {
		expect(t, a.post("/api/v1/me/password", changeBody("wrong-"+acctOldPassword, acctNewPassword), cookies), 403, "incorrect_password")
	}
	w := a.post("/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), cookies)
	expect(t, w, 429, "password_rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on the lock")
	}
	if a.reload(t, u.ID).AuthVersion != 1 {
		t.Fatal("a locked request changed the password")
	}
	// The delete endpoint shares the allowance: it cannot be used to guess the password.
	expect(t, a.post("/api/v1/me/account/delete", map[string]string{"password": acctOldPassword, "email": u.Email}, cookies), 429, "password_rate_limited")
	// Another account is not affected.
	other := a.user(t, "user")
	expect(t, a.post("/api/v1/me/password", changeBody("wrong-"+acctOldPassword, acctNewPassword), a.login(t, other)), 403, "incorrect_password")
	a.clock.Advance(16 * time.Minute)
	expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), cookies), 200, "")
}

// R3-01: administrators need 16 bytes.
func TestAccountPasswordRuleForAdministrators(t *testing.T) {
	a := newAcctEnv(t)
	admin := a.user(t, "admin")
	cookies := a.login(t, admin)
	expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, "Fifteen-Chars-1!"[:15]), cookies), 400, "invalid_password")
	expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, "Sixteen-Chars-1!!"), cookies), 200, "")
}

// R3-02: a login that verified the old password before the change committed cannot
// leave a session behind, and a login that committed first is revoked by the change.
func TestAccountPasswordChangeRacesLogin(t *testing.T) {
	a := newAcctEnv(t)
	t.Run("change_first", func(t *testing.T) {
		u := a.user(t, "user")
		cookies := a.login(t, u)
		b := newBarrier("users")
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- a.send(a.h, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": acctOldPassword}, withContext(b.ctx()))
		}()
		b.wait(t)
		expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), cookies), 200, "")
		b.open()
		w := <-done
		expect(t, w, 401, "invalid_credentials")
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("stale login set cookies")
		}
		if n := a.countWhere(t, &model.Session{}, "user_id = ? AND revoked_at IS NULL", u.ID); n != 1 {
			t.Fatalf("%d live sessions; only the changer's fresh one may exist", n)
		}
	})
	t.Run("login_first", func(t *testing.T) {
		u := a.user(t, "user")
		cookies := a.login(t, u)
		b := newBarrier("users")
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- a.send(a.h, "POST", "/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), withCookies(cookies), withContext(b.ctx()))
		}()
		b.wait(t) // bcrypt done, the transaction has not locked the user
		other := a.login(t, u)
		if a.me(other) != 200 {
			t.Fatal("login before the change did not work")
		}
		b.open()
		expect(t, <-done, 200, "")
		if a.me(other) != 401 {
			t.Fatal("a session created before the change survived it")
		}
	})
	t.Run("two_changes", func(t *testing.T) {
		u := a.user(t, "user")
		cookies := a.login(t, u)
		b := newBarrier("users")
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- a.send(a.h, "POST", "/api/v1/me/password", changeBody(acctOldPassword, "Second-Writer-Password-1"), withCookies(cookies), withContext(b.ctx()))
		}()
		b.wait(t)
		expect(t, a.post("/api/v1/me/password", changeBody(acctOldPassword, acctNewPassword), cookies), 200, "")
		b.open()
		// The slower request verified the old password, which is no longer current.
		expect(t, <-done, 409, "account_changed")
		a.webLogin(t, a.h, u.Email, acctNewPassword)
	})
}

type deletionFixture struct {
	user      model.User
	cookies   []*http.Cookie
	native    string
	sessionID string
	grant     model.ConnectionGrant
	orderID   string
}

// richAccount builds an account that owns a row in every table the purge must clear.
func (a *acctEnv) richAccount(t *testing.T) deletionFixture {
	t.Helper()
	f := deletionFixture{user: a.user(t, "user")}
	f.cookies = a.login(t, f.user)
	f.native = a.native(t, f.user)
	now := a.clock.Now()
	var ns model.NativeSession
	if err := a.db.Where("user_id = ?", f.user.ID).Take(&ns).Error; err != nil {
		t.Fatal(err)
	}
	f.sessionID = ns.ID
	sub := model.Subscription{ID: uuid.NewString(), UserID: f.user.ID, PlanID: "pro", PlanName: "进阶计划", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), TrafficLimitBytes: 1 << 30, MaxDevices: 3}
	period := model.SubscriptionPeriod{ID: uuid.NewString(), UserID: f.user.ID, SubscriptionID: sub.ID, StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt, LimitBytes: 1 << 30}
	f.grant = model.ConnectionGrant{ID: uuid.NewString(), UserID: f.user.ID, SessionID: ns.ID, IdempotencyKey: "grant-key-" + uuid.NewString()[:8], NodeID: uuid.NewString(), AgentID: uuid.NewString(),
		PeriodID: period.ID, State: grantReady, Protocol: "ss", RatePermille: 1000, CredentialCipher: "sealed", ExpiresAt: now.Add(10 * time.Minute)}
	lease := model.ConnectionLease{ID: uuid.NewString(), UserID: f.user.ID, SessionID: ns.ID, DeviceID: ns.DeviceID, Platform: "windows", AppVersion: "t", GroupID: uuid.NewString(), NodeID: f.grant.NodeID,
		Method: model.LeaseByGrant, GrantID: f.grant.ID, State: model.LeaseActive, AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	group := model.NodeGroup{ID: uuid.NewString(), Name: "G", Region: "JP", LineType: "direct", RatePermille: 1000, PlanIDs: "[]", Enabled: true, Version: 1}
	node := model.Node{ID: group.ID, Name: "N", Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, Version: 1, GroupID: &group.ID}
	trade := "2026102000000000" + uuid.NewString()[:4]
	paid := now.Add(-time.Minute)
	order := model.Order{ID: uuid.NewString(), UserID: f.user.ID, IdempotencyKey: uuid.NewString(), PlanID: "pro", PlanName: "进阶计划", PriceCents: 3990, DurationDays: 30, TrafficBytes: 1 << 30,
		Status: model.OrderPaid, Provider: "gateway", CreatedAt: now.Add(-2 * time.Minute), PaidAt: &paid, TradeNo: &trade, AmountCents: 3990}
	f.orderID = order.ID
	err := a.db.Transaction(func(tx *gorm.DB) error {
		for _, row := range []any{&sub, &period, &group, &node, &f.grant, &lease} {
			if e := tx.Omit("User", "Session", "Plan", "Group", "Node").Create(row).Error; e != nil {
				return e
			}
		}
		for _, row := range []any{
			&order,
			&model.NodeUsageReport{ID: uuid.NewString(), GrantID: f.grant.ID, Epoch: 1, Sequence: 1},
			&model.ClientTrafficReport{ID: uuid.NewString(), SessionID: ns.ID, Sequence: 1, UserID: f.user.ID, SubscriptionID: sub.ID, Source: "client_reported"},
			&model.NodeAssignment{UserID: f.user.ID, GroupID: group.ID, NodeID: node.ID, AssignedAt: now, LastUsedAt: now},
			&model.UsageBucket{UserID: f.user.ID, BucketStart: model.UsageBaseline(now), UpdatedAt: now},
			&model.NodeHealthEvent{ID: uuid.NewString(), NodeID: node.ID, UserID: f.user.ID, Reason: "unreachable", OccurredAt: now},
			&model.AnnouncementRead{AnnouncementID: uuid.NewString(), UserID: f.user.ID, AcknowledgedAt: now},
			&model.PaymentNotification{ID: uuid.NewString(), TradeNo: trade, OrderID: &order.ID, MoneyText: "39.90", TradeStatus: "TRADE_SUCCESS", Name: "x", SourceIP: "203.0.113.10", Result: model.NotifyAccepted, ReceivedAt: now},
		} {
			if e := tx.Omit("User", "Session", "Plan", "Group", "Node", "Grant").Create(row).Error; e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var accountTables = []struct {
	name  string
	model any
	where string
}{
	{"users", &model.User{}, "id = ?"}, {"sessions", &model.Session{}, "user_id = ?"}, {"native_sessions", &model.NativeSession{}, "user_id = ?"},
	{"subscriptions", &model.Subscription{}, "user_id = ?"}, {"subscription_periods", &model.SubscriptionPeriod{}, "user_id = ?"},
	{"connection_grants", &model.ConnectionGrant{}, "user_id = ?"}, {"connection_leases", &model.ConnectionLease{}, "user_id = ?"},
	{"orders", &model.Order{}, "user_id = ?"}, {"client_traffic_reports", &model.ClientTrafficReport{}, "user_id = ?"},
	{"node_assignments", &model.NodeAssignment{}, "user_id = ?"}, {"usage_buckets", &model.UsageBucket{}, "user_id = ?"},
	{"node_health_events", &model.NodeHealthEvent{}, "user_id = ?"}, {"login_events", &model.LoginEvent{}, "user_id = ?"}, {"announcement_reads", &model.AnnouncementRead{}, "user_id = ?"},
}

func (a *acctEnv) rows(t *testing.T, userID string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, tbl := range accountTables {
		out[tbl.name] = a.countWhere(t, tbl.model, tbl.where, userID)
	}
	return out
}

func (a *acctEnv) purge(t *testing.T) PurgeResult {
	t.Helper()
	s := &Server{db: a.db, cfg: a.cfg, clock: a.clock.Now}
	res, err := s.purgeDeletedAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func deletion(u model.User) map[string]string {
	return map[string]string{"password": acctOldPassword, "email": u.Email}
}

// R3-03 and R3-05: what the first phase does and what blocks it.
func TestAccountDeletionFirstPhase(t *testing.T) {
	a := newAcctEnv(t)
	f := a.richAccount(t)
	u := f.user

	expect(t, a.post("/api/v1/me/account/delete", map[string]string{"password": acctOldPassword, "email": "other@yahoo.com"}, f.cookies), 400, "email_mismatch")
	expect(t, a.post("/api/v1/me/account/delete", map[string]string{"password": "wrong-" + acctOldPassword, "email": u.Email}, f.cookies), 403, "incorrect_password")
	expect(t, a.post("/api/v1/me/account/delete", map[string]any{"password": acctOldPassword, "email": u.Email, "force": true}, f.cookies), 400, "invalid_json")
	expect(t, a.post("/api/v1/me/account/delete", deletion(u), nil), 401, "unauthorized")
	if a.reload(t, u.ID).Status != "active" || a.me(f.cookies) != 200 {
		t.Fatal("a refused deletion changed the account")
	}

	// An administrator cannot delete their own account from the website.
	admin := a.user(t, "admin")
	expect(t, a.post("/api/v1/me/account/delete", deletion(admin), a.login(t, admin)), 403, "admin_account_protected")
	if a.reload(t, admin.ID).Status != "active" {
		t.Fatal("administrator was marked for deletion")
	}

	// A live payment order blocks it and says how long to wait.
	expires := a.clock.Now().Add(100 * time.Second)
	trade := "20261020000000009999"
	pending := model.Order{ID: uuid.NewString(), UserID: u.ID, IdempotencyKey: uuid.NewString(), PlanID: "pro", PlanName: "进阶计划", PriceCents: 3990, DurationDays: 30, TrafficBytes: 1,
		Status: model.OrderPending, Provider: "gateway", CreatedAt: a.clock.Now(), TradeNo: &trade, AmountCents: 3990, ExpiresAt: &expires}
	if err := a.db.Omit("User").Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	w := a.post("/api/v1/me/account/delete", deletion(u), f.cookies)
	expect(t, w, 409, "pending_orders")
	if !strings.Contains(w.Body.String(), "100 秒") || w.Header().Get("Retry-After") != "100" {
		t.Fatalf("wait time not reported: %s retry-after=%q", w.Body.String(), w.Header().Get("Retry-After"))
	}
	if a.reload(t, u.ID).Status != "active" {
		t.Fatal("blocked deletion changed the account")
	}
	a.clock.Advance(101 * time.Second)

	w = a.post("/api/v1/me/account/delete", deletion(u), f.cookies)
	expect(t, w, 200, "")
	for _, c := range w.Result().Cookies() {
		if c.MaxAge >= 0 {
			t.Fatalf("cookie %s was not cleared", c.Name)
		}
	}
	got := a.reload(t, u.ID)
	if got.Status != model.UserDeleting || got.DeletionRequestedAt == nil {
		t.Fatalf("account state: %q", got.Status)
	}
	expect(t, a.send(a.h, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": acctOldPassword}), 401, "invalid_credentials")
	expect(t, a.send(a.h, "POST", "/api/v1/client/login", nativeLoginBody(u.Email, acctOldPassword, "windows")), 401, "invalid_credentials")
	if a.me(f.cookies) != 401 {
		t.Fatal("web session survived the deletion")
	}
	expect(t, a.nativeStatus(f.native), 401, "native_session_expired")
	var lease model.ConnectionLease
	if err := a.db.Where("user_id = ?", u.ID).Take(&lease).Error; err != nil || lease.State != model.LeaseEnded || lease.EndReason != model.LeaseSessionRevoked {
		t.Fatalf("lease not ended by the revoked session: %+v %v", lease, err)
	}
	var grant model.ConnectionGrant
	if err := a.db.Where("id = ?", f.grant.ID).Take(&grant).Error; err != nil || grant.State != grantRevoking || grant.ReleasedAt != nil {
		t.Fatalf("grant must be revoking and still waiting for the node: %+v", grant)
	}
	if a.countWhere(t, &model.AdminAudit{}, "action = ? AND target_id = ?", AuditDeleteRequested, u.ID) != 1 {
		t.Fatal("deletion request not audited")
	}
	// A deleting account gets no reset mail, and its sign-in attempts leave no record.
	before := len(a.mail.box.to(u.Email))
	expect(t, a.send(a.h, "POST", "/api/v1/auth/forgot-password", map[string]string{"email": u.Email}), 202, "")
	a.mail.deliver(t)
	for _, m := range a.mail.box.to(u.Email)[before:] {
		if strings.Contains(m.Subject, "重置") {
			t.Fatal("a reset link was mailed to a deleting account")
		}
	}
	notices := 0
	for _, m := range a.mail.box.to(u.Email) {
		if strings.Contains(m.Subject, "账号已删除") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("expected one deletion notice, got %d", notices)
	}
	time.Sleep(100 * time.Millisecond)
	if a.countWhere(t, &model.LoginEvent{}, "user_id = ? AND result = ? AND occurred_at >= ?", u.ID, model.LoginFailure, got.DeletionRequestedAt) != 0 {
		t.Fatal("sign-in attempts of a deleting account were recorded")
	}
}

// R3-04: the purge waits for the node, then removes everything but the anonymous ledger.
func TestAccountPurge(t *testing.T) {
	a := newAcctEnv(t)
	f := a.richAccount(t)
	bystander := a.richAccount(t)
	// Records only the account's own mail challenge: a registration proof and its job.
	challenge := model.EmailAuthChallenge{ID: uuid.NewString(), Purpose: authmail.PurposeRegister, Email: f.user.Email, Digest: strings.Repeat("a", 64), Generation: 1, Status: "consumed", IssuedAt: a.clock.Now(), ExpiresAt: a.clock.Now().Add(time.Hour)}
	job := model.AuthMailJob{ID: uuid.NewString(), ChallengeID: &challenge.ID, Template: "register_code", RecipientHint: "a***@yahoo.com", Status: "sent", NextAttemptAt: a.clock.Now(), MessageID: "x@y", CreatedAt: a.clock.Now()}
	if err := a.db.Create(&challenge).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	expect(t, a.post("/api/v1/me/account/delete", deletion(f.user), f.cookies), 200, "")

	// The grant is still open on the node: nothing is removed yet.
	res := a.purge(t)
	if res.Purged != 0 || res.Waiting != 1 || len(res.Failed) != 0 {
		t.Fatalf("purge before settlement: %+v", res)
	}
	if a.rows(t, f.user.ID)["users"] != 1 {
		t.Fatal("account removed before its grant settled")
	}

	// The node confirms removal.
	released := a.clock.Now()
	if err := a.db.Model(&model.ConnectionGrant{}).Where("id = ?", f.grant.ID).Updates(map[string]any{"state": grantRevoked, "released_at": released}).Error; err != nil {
		t.Fatal(err)
	}
	before := a.rows(t, bystander.user.ID)
	res = a.purge(t)
	if res.Purged != 1 || len(res.Failed) != 0 {
		t.Fatalf("purge after settlement: %+v", res)
	}
	for name, n := range a.rows(t, f.user.ID) {
		if n != 0 {
			t.Fatalf("%s still has %d rows", name, n)
		}
	}
	if a.countWhere(t, &model.NodeUsageReport{}, "grant_id = ?", f.grant.ID) != 0 {
		t.Fatal("usage reports remain")
	}
	if a.countWhere(t, &model.EmailAuthChallenge{}, "email = ?", f.user.Email) != 0 || a.countWhere(t, &model.AuthMailJob{}, "id = ?", job.ID) != 0 {
		t.Fatal("mailbox challenges remain")
	}
	// The anonymous ledger stays, without a link to the account.
	var ledger []model.PaymentNotification
	if err := a.db.Where("order_id IS NULL AND money_text = ?", "39.90").Find(&ledger).Error; err != nil || len(ledger) != 1 || ledger[0].SourceIP != "203.0.113.10" {
		t.Fatalf("anonymous ledger: %+v %v", ledger, err)
	}
	if a.countWhere(t, &model.AdminAudit{}, "action = ? AND target_id = ?", AuditPurged, f.user.ID) != 1 {
		t.Fatal("purge not audited")
	}
	var audit model.AdminAudit
	_ = a.db.Where("action = ?", AuditPurged).Take(&audit).Error
	if strings.Contains(audit.Detail, "@") {
		t.Fatal("audit keeps an address")
	}
	// Another account's data is untouched.
	after := a.rows(t, bystander.user.ID)
	for name, n := range before {
		if after[name] != n {
			t.Fatalf("bystander %s: %d -> %d", name, n, after[name])
		}
	}
	// The same address registers again with no history.
	id := a.requestCode(t, f.user.Email)
	reg := a.call(a.h, "register", registration(f.user.Email, id, a.mail.latestCode(t, f.user.Email)), "")
	expect(t, reg, 201, "")
	var again model.User
	if err := a.db.Where("email = ?", f.user.Email).Take(&again).Error; err != nil || again.ID == f.user.ID {
		t.Fatalf("re-registration: %v", err)
	}
	if a.countWhere(t, &model.Order{}, "user_id = ?", again.ID)+a.countWhere(t, &model.Subscription{}, "user_id = ?", again.ID) != 0 {
		t.Fatal("new account inherited history")
	}
	// A second pass has nothing to do.
	if res = a.purge(t); res.Purged != 0 || res.Waiting != 0 {
		t.Fatalf("second pass: %+v", res)
	}
}

// R3-04: an unconfirmed grant no longer blocks once it has been expired longer than
// the settle grace (the node stopped forwarding on its own).
func TestAccountPurgeAfterGraceWithoutConfirmation(t *testing.T) {
	a := newAcctEnv(t)
	f := a.richAccount(t)
	expect(t, a.post("/api/v1/me/account/delete", deletion(f.user), f.cookies), 200, "")
	if res := a.purge(t); res.Purged != 0 || res.Waiting != 1 {
		t.Fatalf("before expiry: %+v", res)
	}
	a.clock.Advance(10*time.Minute + a.cfg.SettleGrace() - time.Second)
	if res := a.purge(t); res.Purged != 0 || res.Waiting != 1 {
		t.Fatalf("inside the grace: %+v", res)
	}
	a.clock.Advance(2 * time.Second)
	if res := a.purge(t); res.Purged != 1 {
		t.Fatalf("after the grace: %+v", res)
	}
	if a.rows(t, f.user.ID)["users"] != 0 {
		t.Fatal("account remains")
	}
}

// R3-04: a failing step rolls the whole account back and the next pass retries it.
func TestAccountPurgeIsAtomic(t *testing.T) {
	a := newAcctEnv(t)
	f := a.richAccount(t)
	expect(t, a.post("/api/v1/me/account/delete", deletion(f.user), f.cookies), 200, "")
	released := a.clock.Now()
	if err := a.db.Model(&model.ConnectionGrant{}).Where("id = ?", f.grant.ID).Updates(map[string]any{"state": grantRevoked, "released_at": released}).Error; err != nil {
		t.Fatal(err)
	}
	// A row the pipeline does not know about, referencing the user with a foreign key.
	if err := a.db.Exec(`CREATE TABLE "` + a.cfg.Schema + `".blocker (user_id uuid REFERENCES "` + a.cfg.Schema + `".users(id))`).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Exec(`INSERT INTO "`+a.cfg.Schema+`".blocker VALUES (?)`, f.user.ID).Error; err != nil {
		t.Fatal(err)
	}
	before := a.rows(t, f.user.ID)
	res := a.purge(t)
	if res.Purged != 0 || len(res.Failed) != 1 || res.Failed[0] != f.user.ID {
		t.Fatalf("expected the failure to be reported: %+v", res)
	}
	for name, n := range a.rows(t, f.user.ID) {
		if n != before[name] {
			t.Fatalf("%s changed after a rolled-back purge: %d -> %d", name, before[name], n)
		}
	}
	if err := a.db.Exec(`DROP TABLE "` + a.cfg.Schema + `".blocker`).Error; err != nil {
		t.Fatal(err)
	}
	if res = a.purge(t); res.Purged != 1 {
		t.Fatalf("retry: %+v", res)
	}
}

// R3-04: two instances purging at once do not collide.
func TestAccountPurgeSingleRunner(t *testing.T) {
	a := newAcctEnv(t)
	f := a.richAccount(t)
	expect(t, a.post("/api/v1/me/account/delete", deletion(f.user), f.cookies), 200, "")
	released := a.clock.Now()
	if err := a.db.Model(&model.ConnectionGrant{}).Where("id = ?", f.grant.ID).Updates(map[string]any{"state": grantRevoked, "released_at": released}).Error; err != nil {
		t.Fatal(err)
	}
	var purged, skipped atomic.Int32
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			s := &Server{db: a.db, cfg: a.cfg, clock: a.clock.Now}
			res, err := s.purgeDeletedAccounts(t.Context())
			if err != nil || len(res.Failed) != 0 {
				t.Errorf("runner: %v %+v", err, res)
			}
			purged.Add(int32(res.Purged))
			if res.Skipped {
				skipped.Add(1)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if purged.Load() != 1 {
		t.Fatalf("account purged %d times", purged.Load())
	}
}

// A payment callback for an account that is being deleted never fulfils: it waits for review.
func TestAccountDeletingBlocksPaymentFulfilment(t *testing.T) {
	e := newPayEnv(t, nil)
	u := e.user("deleter", "user")
	p := e.place(u, "pro", "wxpay", "")
	order, _ := e.gw.open(t, p.PayURL)
	w := e.call(u.cookies, "POST", "/api/v1/me/account/delete", map[string]string{"password": payPassword, "email": u.Email}, "")
	if w.Code != 409 {
		t.Fatalf("pending order did not block deletion: %d", w.Code)
	}
	e.clock.Advance(200 * time.Second)
	e.reconcile()
	e.want(e.call(u.cookies, "POST", "/api/v1/me/account/delete", map[string]string{"password": payPassword, "email": u.Email}, ""), 200)
	e.pay(order)
	got := e.order(p.Order.ID)
	if got.Status == model.OrderPaid || e.subscription(u.ID) != nil {
		t.Fatalf("a deleting account was fulfilled: status %s", got.Status)
	}
}
