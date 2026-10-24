package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// Round 5 of handoff_payment_account.md: impersonation (R5-03 … R5-05).

// browser keeps cookies the way a browser would: by name, sent only to paths the cookie's
// Path covers, deleted by Max-Age < 0.
type browser struct {
	e   *consoleEnv
	jar map[string]*http.Cookie
}

func (e *consoleEnv) browser(cookies []*http.Cookie) *browser {
	b := &browser{e: e, jar: map[string]*http.Cookie{}}
	b.absorb(cookies)
	return b
}

func (b *browser) absorb(cookies []*http.Cookie) {
	for _, c := range cookies {
		if c.MaxAge < 0 {
			delete(b.jar, c.Name)
			continue
		}
		if c.Path == "" {
			c.Path = "/"
		}
		b.jar[c.Name] = c
	}
}

func (b *browser) sendable(path string) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range b.jar {
		if strings.HasPrefix(path, c.Path) {
			out = append(out, c)
		}
	}
	return out
}

func (b *browser) do(method, path string, body any) *httptest.ResponseRecorder {
	w := b.e.call(b.sendable(path), method, path, body, "")
	b.absorb(w.Result().Cookies())
	return w
}

func (b *browser) get(path string) *httptest.ResponseRecorder {
	return b.do("GET", "/api/v1"+path, nil)
}
func (b *browser) post(path string, body any) *httptest.ResponseRecorder {
	return b.do("POST", "/api/v1"+path, body)
}

const returnPath = "/api/v1/auth/impersonation/return"

type meView struct {
	User          model.User `json:"user"`
	Impersonation *struct {
		AdminEmail string    `json:"admin_email"`
		ExpiresAt  time.Time `json:"expires_at"`
	} `json:"impersonation"`
}

func (e *consoleEnv) meOf(b *browser) (meView, int) {
	e.t.Helper()
	w := b.get("/auth/me")
	var out meView
	if w.Code == 200 {
		e.decode(w, 200, &out)
	}
	return out, w.Code
}

func (e *consoleEnv) loginAgain(u payUser) *browser {
	e.t.Helper()
	w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": payPassword}, "")
	if w.Code != 200 {
		e.t.Fatalf("login %d", w.Code)
	}
	return e.browser(w.Result().Cookies())
}

func (e *consoleEnv) impersonate(b *browser, targetID string) *httptest.ResponseRecorder {
	return b.post("/admin/console/users/"+targetID+"/impersonate", map[string]any{"admin_password": payPassword})
}

// R5-03
func TestImpersonationIsReadOnly(t *testing.T) {
	e := newConsoleEnv(t, nil)
	admin := e.browser(e.admin.cookies)
	u := e.user("watched", "user")
	peer := e.user("peer-admin", "admin")
	gone := e.user("leaving", "user")
	e.db.Model(&model.User{}).Where("id = ?", gone.ID).Update("status", model.UserDeleting)
	plain := e.browser(u.cookies)

	// Who may start one.
	e.code(plain.post("/admin/console/users/"+u.ID+"/impersonate", map[string]any{"admin_password": payPassword}), 403, "admin_required")
	e.code(e.impersonate(admin, e.admin.ID), 403, "self_protected")
	e.code(e.impersonate(admin, peer.ID), 403, "admin_account_protected")
	e.code(e.impersonate(admin, gone.ID), 409, "user_unavailable")
	e.code(e.impersonate(admin, uuid.NewString()), 404, "user_not_found")
	e.code(admin.post("/admin/console/users/"+u.ID+"/impersonate", map[string]any{"admin_password": "wrong"}), 403, "incorrect_password")
	if _, ok := admin.jar[returnCookieName]; ok {
		t.Fatal("a refused request set the return cookie")
	}

	// Things that must not move while looking.
	before := e.reload(u.ID)
	var events int64
	e.db.Model(&model.LoginEvent{}).Where("user_id = ?", u.ID).Count(&events)
	pub := model.Announcement{ID: uuid.NewString(), Title: "公告", Body: "正文", Status: model.AnnouncementPublished, CreatedBy: e.admin.ID, Version: 1, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	pub.PublishedAt = &pub.CreatedAt
	e.db.Create(&pub)
	pending := e.place(u, "starter", payment.TypeWeChat, "")

	w := e.impersonate(admin, u.ID)
	e.want(w, 200)
	byName := map[string]*http.Cookie{}
	for _, c := range w.Result().Cookies() {
		byName[c.Name] = c
	}
	ret := byName[returnCookieName]
	if ret == nil || !ret.HttpOnly || ret.SameSite != http.SameSiteStrictMode || ret.Path != returnPath || ret.Expires.IsZero() || ret.Value == "" {
		t.Fatalf("return cookie %+v", ret)
	}
	if byName["vpn_access"] == nil || !byName["vpn_access"].HttpOnly || byName["vpn_refresh"] == nil {
		t.Fatal("the user's session cookies were not set")
	}
	// The return cookie travels to the return path only.
	for _, other := range []string{"/api/v1/auth/me", "/api/v1/admin/console/users", "/api/v1/auth/refresh", "/api/v1/auth/logout"} {
		for _, c := range admin.sendable(other) {
			if c.Name == returnCookieName {
				t.Fatalf("return cookie would be sent to %s", other)
			}
		}
	}
	me, code := e.meOf(admin)
	if code != 200 || me.User.ID != u.ID || me.Impersonation == nil || me.Impersonation.AdminEmail != e.admin.Email {
		t.Fatalf("me: %d %+v", code, me)
	}
	if limit := e.clock.Now().Add(e.cfg.ImpersonationWindow() + time.Minute); me.Impersonation.ExpiresAt.After(limit) || !me.Impersonation.ExpiresAt.After(e.clock.Now()) {
		t.Fatalf("expiry %v", me.Impersonation.ExpiresAt)
	}
	var sess model.Session
	if err := e.db.Where("user_id = ? AND impersonator_id IS NOT NULL", u.ID).First(&sess).Error; err != nil || sess.HardExpiresAt == nil || !sess.HardExpiresAt.Equal(sess.ExpiresAt) || sess.Remember {
		t.Fatalf("impersonation session %+v %v", sess, err)
	}

	// Reading works, as the user.
	for _, path := range []string{"/me/dashboard", "/orders", "/me/devices", "/me/logins", "/me/usage-history", "/payments/orders/" + pending.Order.ID} {
		if w := admin.get(path); w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Everything that writes is refused, by the server.
	for _, r := range []struct {
		path string
		body any
	}{
		{"/me/password", map[string]any{"current_password": payPassword, "new_password": "Another-Password-2026"}},
		{"/me/account/delete", map[string]any{"password": payPassword, "email": u.Email}},
		{"/orders/test-purchase", map[string]any{"plan_id": "starter"}},
		{"/payments/orders", map[string]any{"plan_id": "starter", "pay_type": "wxpay"}},
		{"/payments/quote", map[string]any{"plan_id": "starter"}},
		{"/me/announcements/" + pub.ID + "/ack", nil},
		{"/admin/console/users/" + peer.ID + "/password", map[string]any{"admin_password": payPassword, "new_password": "Another-Password-2026"}},
	} {
		body := r.body
		if body == nil {
			body = map[string]any{}
		}
		e.code(admin.post(r.path, body), 403, "impersonation_read_only")
	}
	// The console is closed to it, even for reading.
	e.code(admin.get("/admin/console/users"), 403, "impersonation_read_only")
	e.code(admin.get("/admin/console/coupons"), 403, "impersonation_read_only")
	e.code(admin.get("/admin/console/announcements"), 403, "impersonation_read_only")

	// No announcements, no sign-in trail, no changes.
	var list struct {
		Announcements []announcementView `json:"announcements"`
	}
	e.decode(admin.get("/me/announcements"), 200, &list)
	if len(list.Announcements) != 0 {
		t.Fatalf("an impersonation saw announcements: %+v", list)
	}
	after := e.reload(u.ID)
	var eventsNow int64
	e.db.Model(&model.LoginEvent{}).Where("user_id = ?", u.ID).Count(&eventsNow)
	if (before.LastLoginAt == nil) != (after.LastLoginAt == nil) || before.LastLoginAt != nil && !before.LastLoginAt.Equal(*after.LastLoginAt) || eventsNow != events || after.AuthVersion != before.AuthVersion {
		t.Fatalf("the user's record changed: %+v -> %+v", before, after)
	}
	if o := e.order(pending.Order.ID); o.GatewayCheckedAt != nil || o.Status != model.OrderPending {
		t.Fatalf("looking at an order changed it: %+v", o)
	}
	var started model.AdminAudit
	if err := e.db.Where("action = ? AND actor_id = ? AND target_id = ?", AuditImpersonateStart, e.admin.ID, u.ID).First(&started).Error; err != nil || strings.Contains(started.Detail, payPassword) {
		t.Fatalf("start audit: %v %s", err, started.Detail)
	}
	// The user themself is unaffected and can still act.
	e.want(plain.get("/me/dashboard"), 200)
	// The real user still sees the announcement.
	e.decode(plain.get("/me/announcements"), 200, &list)
	if len(list.Announcements) != 1 {
		t.Fatalf("the real user lost the announcement: %+v", list)
	}
}

// R5-04, R5-05
func TestImpersonationReturn(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("visited", "user")
	admin := e.browser(e.admin.cookies)
	e.want(e.impersonate(admin, u.ID), 200)
	impCookies := []*http.Cookie{}
	for _, name := range []string{"vpn_access", "vpn_refresh"} {
		c := *admin.jar[name]
		impCookies = append(impCookies, &c)
	}
	returnValue := admin.jar[returnCookieName].Value

	// Stolen use: the cookie name means nothing elsewhere, and a plain user session cannot use the way back.
	if w := e.call([]*http.Cookie{{Name: returnCookieName, Value: returnValue}}, "GET", "/api/v1/auth/me", nil, ""); w.Code != 401 {
		t.Fatalf("return cookie authenticated a request: %d", w.Code)
	}
	if w := e.call([]*http.Cookie{{Name: returnCookieName, Value: returnValue}}, "GET", "/api/v1/admin/console/users", nil, ""); w.Code != 401 {
		t.Fatalf("return cookie opened the console: %d", w.Code)
	}
	other := e.user("bystander", "user")
	bystander := e.browser(other.cookies)
	e.code(bystander.post("/auth/impersonation/return", map[string]any{}), 403, "not_impersonating")
	bystander.jar[returnCookieName] = &http.Cookie{Name: returnCookieName, Value: returnValue, Path: returnPath}
	e.code(bystander.post("/auth/impersonation/return", map[string]any{}), 403, "not_impersonating")

	// A refresh keeps the session inside its hard limit.
	var sess model.Session
	e.db.Where("user_id = ? AND impersonator_id IS NOT NULL", u.ID).First(&sess)
	e.want(admin.post("/auth/refresh", map[string]any{}), 200)
	var again model.Session
	e.db.First(&again, "id = ?", sess.ID)
	if !again.ExpiresAt.Equal(*sess.HardExpiresAt) || again.HardExpiresAt == nil || !again.HardExpiresAt.Equal(*sess.HardExpiresAt) {
		t.Fatalf("refresh moved the end: %v -> %v", sess.ExpiresAt, again.ExpiresAt)
	}
	if c := admin.jar["vpn_access"]; c == nil || c.Expires.After(*sess.HardExpiresAt) {
		t.Fatalf("access cookie outlives the session: %+v", c)
	}

	// Back to the administrator.
	e.want(admin.post("/auth/impersonation/return", map[string]any{}), 200)
	if _, ok := admin.jar[returnCookieName]; ok {
		t.Fatal("the return cookie survived the return")
	}
	me, code := e.meOf(admin)
	if code != 200 || me.User.ID != e.admin.ID || me.Impersonation != nil {
		t.Fatalf("not the administrator again: %d %+v", code, me)
	}
	e.want(admin.get("/admin/console/users"), 200)
	// The old impersonation cookies are dead; the old return value is spent.
	old := e.browser(impCookies)
	if _, code := e.meOf(old); code != 401 {
		t.Fatalf("an old impersonation cookie works: %d", code)
	}
	old.jar[returnCookieName] = &http.Cookie{Name: returnCookieName, Value: returnValue, Path: returnPath}
	e.want(old.post("/auth/impersonation/return", map[string]any{}), 401)
	for _, ending := range []string{AuditImpersonateStart, AuditImpersonateEnd} {
		if e.countAudit(ending, e.admin.ID, u.ID) != 1 {
			t.Fatalf("expected one %s audit row", ending)
		}
	}
	var revoked model.Session
	e.db.First(&revoked, "id = ?", sess.ID)
	if revoked.RevokedAt == nil {
		t.Fatal("the impersonation session was not revoked")
	}
}

func (e *consoleEnv) countAudit(action, actor, target string) int64 {
	var n int64
	e.db.Model(&model.AdminAudit{}).Where("action = ? AND actor_id = ? AND target_id = ?", action, actor, target).Count(&n)
	return n
}

// R5-04: every way the way back can fail ends the impersonation and asks for a new login.
func TestImpersonationReturnFailures(t *testing.T) {
	// handled: the return endpoint itself judges it (and cleans up); otherwise the session
	// is already dead at authentication because its administrator no longer qualifies.
	for _, tc := range []struct {
		name    string
		handled bool
		break_  func(e *consoleEnv, b *browser, u payUser)
	}{
		{"missing return cookie", true, func(e *consoleEnv, b *browser, u payUser) { delete(b.jar, returnCookieName) }},
		{"forged return cookie", true, func(e *consoleEnv, b *browser, u payUser) { b.jar[returnCookieName].Value = "forged" }},
		{"administrator's session revoked", false, func(e *consoleEnv, b *browser, u payUser) {
			e.db.Model(&model.Session{}).Where("user_id = ? AND impersonator_id IS NULL", e.admin.ID).Update("revoked_at", e.clock.Now())
		}},
		{"administrator disabled", false, func(e *consoleEnv, b *browser, u payUser) {
			e.db.Model(&model.User{}).Where("id = ?", e.admin.ID).Update("status", "disabled")
		}},
		{"administrator demoted", false, func(e *consoleEnv, b *browser, u payUser) {
			e.db.Model(&model.User{}).Where("id = ?", e.admin.ID).Update("role", "user")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newConsoleEnv(t, nil)
			u := e.user("victim", "user")
			b := e.browser(e.admin.cookies)
			e.want(e.impersonate(b, u.ID), 200)
			tc.break_(e, b, u)
			w := b.post("/auth/impersonation/return", map[string]any{})
			if w.Code != 401 {
				t.Fatalf("return answered %d: %s", w.Code, w.Body.String())
			}
			if _, code := e.meOf(b); code != 401 {
				t.Fatal("still signed in")
			}
			if tc.handled {
				if len(b.jar) != 0 {
					t.Fatalf("cookies survived: %v", b.jar)
				}
				var n int64
				e.db.Model(&model.Session{}).Where("user_id = ? AND impersonator_id IS NOT NULL AND revoked_at IS NULL", u.ID).Count(&n)
				if n != 0 {
					t.Fatal("the impersonation session stayed open")
				}
			}
			if e.countAudit(AuditImpersonateEnd, e.admin.ID, u.ID) != 0 {
				t.Fatal("a failed return was audited as a return")
			}
		})
	}
}

// R5-04: the session ends on its own, and ends when its administrator's login changes.
func TestImpersonationEnds(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("timed", "user")
	b := e.browser(e.admin.cookies)
	e.want(e.impersonate(b, u.ID), 200)
	if _, code := e.meOf(b); code != 200 {
		t.Fatal("not signed in")
	}
	// Past the hard limit nothing works, refresh included.
	e.db.Model(&model.Session{}).Where("user_id = ? AND impersonator_id IS NOT NULL", u.ID).Updates(map[string]any{"hard_expires_at": time.Now().Add(-time.Second), "expires_at": time.Now().Add(time.Hour)})
	if _, code := e.meOf(b); code != 401 {
		t.Fatalf("an expired impersonation works: %d", code)
	}
	// Changing the administrator's password elsewhere ends a running one at once.
	b2 := e.browser(e.admin.cookies)
	spare := e.loginAgain(e.admin)
	e.want(e.impersonate(b2, u.ID), 200)
	e.want(spare.post("/me/password", map[string]any{"current_password": payPassword, "new_password": "A-Brand-New-Admin-Password-2026"}), 200)
	if _, code := e.meOf(b2); code != 401 {
		t.Fatalf("impersonation outlived the administrator's password change: %d", code)
	}
	// A password change or deletion of the visited user ends it too.
	e.admin = e.user("root2", "admin")
	b3, b4 := e.browser(e.admin.cookies), e.loginAgain(e.admin)
	e.want(e.impersonate(b3, u.ID), 200)
	e.want(b4.post("/admin/console/users/"+u.ID+"/password", map[string]any{"admin_password": payPassword, "new_password": "Visited-Users-New-Password-1"}), 200)
	if _, code := e.meOf(b3); code != 401 {
		t.Fatalf("impersonation outlived the user's password change: %d", code)
	}
	// Signing out of an impersonation leaves nothing behind, and drops the return cookie.
	e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("status", "active")
	b5 := e.browser(e.admin.cookies)
	e.want(e.impersonate(b5, u.ID), 200)
	e.want(b5.post("/auth/logout", map[string]any{}), 200)
	if len(b5.jar) != 0 {
		t.Fatalf("cookies after sign-out: %v", b5.jar)
	}
	var open int64
	e.db.Model(&model.Session{}).Where("user_id IN ? AND revoked_at IS NULL", []string{u.ID}).Count(&open)
	if open != 0 {
		t.Fatalf("%d sessions of the user are still open", open)
	}
}
