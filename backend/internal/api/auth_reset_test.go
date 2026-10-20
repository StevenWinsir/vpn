package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/model"
)

// Round 3 (handoff_auth.md 10, R3-01 … R3-10 except the browser-only R3-09): reset
// links, the atomic password change, revocation of both session kinds, and the
// login/refresh races. Interleavings are forced with barriers, never with sleeps.

const resetOldPassword = "Old-Password-2026!"

type requestOption func(*http.Request)

func withCookies(cookies []*http.Cookie) requestOption {
	return func(r *http.Request) {
		for _, c := range cookies {
			r.AddCookie(c)
		}
	}
}
func withBearer(token string) requestOption {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}
func withContext(ctx context.Context) requestOption {
	return func(r *http.Request) { *r = *r.WithContext(ctx) }
}
func fromIP(ip string) requestOption {
	return func(r *http.Request) { r.RemoteAddr = ip + ":40000" }
}

// send issues one request through a handler, as the Next.js proxy would.
func (e *regEnv) send(h http.Handler, method, path string, body any, options ...requestOption) *httptest.ResponseRecorder {
	var reader *strings.Reader
	switch b := body.(type) {
	case nil:
		reader = strings.NewReader("")
	case string:
		reader = strings.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		reader = strings.NewReader(string(data))
	}
	r := httptest.NewRequest(method, path, reader)
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Origin", e.cfg.Origins[0])
	r.RemoteAddr = "198.51.100.7:40000"
	for _, option := range options {
		option(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// historicalUser creates an account the way pre-round-2 accounts exist: no verified
// mailbox, AuthVersion 1, created directly in the database.
func (e *regEnv) historicalUser(t *testing.T, email, role, status string) model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(resetOldPassword), 4)
	if err != nil {
		t.Fatal(err)
	}
	u := model.User{ID: uuid.NewString(), Email: email, Name: "找回测试", PasswordHash: string(hash), Role: role, Status: status}
	if err = e.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *regEnv) reload(t *testing.T, id string) model.User {
	t.Helper()
	var u model.User
	if err := e.db.Where("id = ?", id).Take(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

// forgot requests a link and returns the token the recipient would read ("" if no
// mail arrived for this request).
func (e *regEnv) forgot(t *testing.T, email string) string {
	t.Helper()
	before := len(e.mail.box.to(email))
	w := e.send(e.handler, "POST", "/api/v1/auth/forgot-password", map[string]string{"email": email})
	expect(t, w, 202, "")
	e.mail.deliver(t)
	msgs := e.mail.box.to(email)
	if len(msgs) == before {
		return ""
	}
	return resetTokenIn(t, msgs[len(msgs)-1])
}

func resetTokenIn(t *testing.T, msg authmail.Message) string {
	t.Helper()
	const prefix = "https://app.example.com/reset-password#token="
	i := strings.Index(msg.Text, prefix)
	if i < 0 || len(msg.Text) < i+len(prefix)+43 {
		t.Fatalf("no reset link in mail %q", msg.Subject)
	}
	token := msg.Text[i+len(prefix) : i+len(prefix)+43]
	if !authmail.ValidTokenFormat(token) {
		t.Fatal("reset link carries a malformed token")
	}
	return token
}

func resetBody(token, password string) map[string]string {
	return map[string]string{"token": token, "password": password, "password_confirmation": password}
}

func (e *regEnv) webLogin(t *testing.T, h http.Handler, email, password string) []*http.Cookie {
	t.Helper()
	w := e.send(h, "POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password, "remember": true})
	expect(t, w, 200, "")
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("login set %d cookies", len(cookies))
	}
	return cookies
}

func nativeLoginBody(email, password, platform string) map[string]string {
	return map[string]string{"email": email, "password": password, "device_id": uuid.NewString(), "platform": platform, "app_version": "round3-test"}
}

func (e *regEnv) nativeLogin(t *testing.T, email, password, platform string) string {
	t.Helper()
	w := e.send(e.handler, "POST", "/api/v1/client/login", nativeLoginBody(email, password, platform))
	expect(t, w, 200, "")
	var out struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Token
}

func (e *regEnv) countWhere(t *testing.T, value any, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := e.db.Model(value).Where(query, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func refreshCookie(cookies []*http.Cookie) []*http.Cookie {
	for _, c := range cookies {
		if c.Name == "vpn_refresh" {
			return []*http.Cookie{c}
		}
	}
	return nil
}

func accessCookie(cookies []*http.Cookie) []*http.Cookie {
	for _, c := range cookies {
		if c.Name == "vpn_access" {
			return []*http.Cookie{c}
		}
	}
	return nil
}

// R3-01: every address gets the same answer; only an active account receives a link,
// and asking changes nothing about the account.
func TestResetRequestsRevealNothingAndChangeNothing(t *testing.T) {
	e := newRegEnv(t)
	active := e.historicalUser(t, "active-"+uuid.NewString()[:8]+"@gmail.com", "user", "active")
	legacy := e.historicalUser(t, "legacy-"+uuid.NewString()[:8]+"@corp.example", "user", "active")
	disabled := e.historicalUser(t, "disabled-"+uuid.NewString()[:8]+"@gmail.com", "user", "disabled")
	unknown := "nobody-" + uuid.NewString()[:8] + "@gmail.com"
	cookies := e.webLogin(t, e.handler, active.Email, resetOldPassword)

	type shape struct {
		Keys    []string
		Message string
		Resend  float64
	}
	var shapes []shape
	for i, email := range []string{active.Email, strings.ToUpper(legacy.Email), disabled.Email, unknown} {
		w := e.send(e.handler, "POST", "/api/v1/auth/forgot-password", map[string]string{"email": " " + email + " "}, fromIP(fmt.Sprintf("198.51.100.%d", 20+i)))
		expect(t, w, 202, "")
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		shapes = append(shapes, shape{keys, out["message"].(string), out["resend_after_seconds"].(float64)})
		if strings.Contains(w.Body.String(), "token") || strings.Contains(w.Body.String(), "user") {
			t.Fatalf("forgot-password response leaks details: %s", w.Body.String())
		}
	}
	for _, s := range shapes[1:] {
		if !reflect.DeepEqual(s, shapes[0]) {
			t.Fatalf("responses differ: %+v vs %+v", s, shapes[0])
		}
	}
	if !reflect.DeepEqual(shapes[0].Keys, []string{"message", "next_send_at", "resend_after_seconds"}) || shapes[0].Resend != 60 {
		t.Fatalf("unexpected response shape %+v", shapes[0])
	}
	e.mail.deliver(t)
	for email, want := range map[string]int{active.Email: 1, legacy.Email: 1, disabled.Email: 0, unknown: 0} {
		if got := len(e.mail.box.to(email)); got != want {
			t.Fatalf("%s received %d mails, want %d", authmail.MaskEmail(email), got, want)
		}
	}
	// The historical, non-allow-listed address can recover its account.
	if token := resetTokenIn(t, e.mail.box.to(legacy.Email)[0]); token == "" {
		t.Fatal("legacy account got no link")
	}
	for _, u := range []model.User{active, legacy, disabled} {
		after := e.reload(t, u.ID)
		if after.PasswordHash != u.PasswordHash || after.Status != u.Status || after.AuthVersion != 1 || after.PasswordChangedAt != nil || after.EmailVerifiedAt != nil {
			t.Fatal("requesting a link changed the account")
		}
	}
	if e.send(e.handler, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(cookies))).Code != 200 {
		t.Fatal("requesting a link signed the account out")
	}
	if n := e.countWhere(t, &model.User{}, "email = ?", unknown); n != 0 {
		t.Fatal("requesting a link created an account")
	}
	// Format errors are explicit and identical for every account.
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/forgot-password", map[string]string{"email": "not-an-address"}), 400, "invalid_email")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/forgot-password", map[string]any{"email": active.Email, "user_id": active.ID}), 400, "invalid_json")
	// The shared mailbox cooldown applies to resets too.
	w := e.send(e.handler, "POST", "/api/v1/auth/forgot-password", map[string]string{"email": active.Email})
	expect(t, w, 429, "rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit without Retry-After")
	}
}

// R3-02: validation is read-only no matter how often it runs; GET/HEAD do nothing.
func TestResetValidationNeverConsumes(t *testing.T) {
	e := newRegEnv(t)
	u := e.historicalUser(t, "validate-"+uuid.NewString()[:8]+"@qq.com", "user", "active")
	admin := e.historicalUser(t, "admin-"+uuid.NewString()[:8]+"@corp.example", "admin", "active")
	token := e.forgot(t, u.Email)
	for i := 0; i < 5; i++ {
		w := e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": token})
		expect(t, w, 200, "")
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out["valid"] != true || out["password_min_bytes"] != float64(10) || out["password_max_bytes"] != float64(72) || out["expires_in_seconds"] != float64(1800) {
			t.Fatalf("validate answer: %v", out)
		}
		if strings.Contains(w.Body.String(), u.Email) || strings.Contains(w.Body.String(), u.ID) {
			t.Fatal("validate leaks the account")
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, path := range []string{"/api/v1/auth/reset-password", "/api/v1/auth/reset-password/validate", "/api/v1/auth/reset-password?token=" + token} {
			if w := e.send(e.handler, method, path, nil); w.Code != 404 {
				t.Fatalf("%s %s answered %d", method, path, w.Code)
			}
		}
	}
	var c model.EmailAuthChallenge
	if err := e.db.Where("purpose = ? AND user_id = ?", authmail.PurposeReset, u.ID).Take(&c).Error; err != nil || c.Status != "active" || c.ConsumedAt != nil {
		t.Fatal("validation consumed the token")
	}
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "New-Password-2026!")), 200, "")
	// Administrators keep the 16-byte minimum.
	adminToken := e.forgot(t, admin.Email)
	w := e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": adminToken})
	expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"password_min_bytes":16`) {
		t.Fatalf("admin rule missing: %s", w.Body.String())
	}
}

// R3-03: unusable tokens are one answer; bad passwords never consume a usable token;
// byte rules match the website.
func TestResetTokenAndPasswordRules(t *testing.T) {
	e := newRegEnv(t)
	u := e.historicalUser(t, "rules-"+uuid.NewString()[:8]+"@163.com", "user", "active")
	invalid := func(token string) {
		t.Helper()
		expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": token}), 400, "reset_token_invalid")
		expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "Another-Password-1")), 400, "reset_token_invalid")
	}
	// Missing, malformed, oversized and random tokens.
	for _, token := range []string{"", "short", strings.Repeat("A", 42) + "=", strings.Repeat("A", 44), strings.Repeat("Z", 43) + "\n", uuid.NewString()} {
		invalid(token)
	}
	random, _ := authmail.NewResetToken()
	invalid(random)
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]any{"token": 12345}), 400, "invalid_json")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", map[string]any{"token": random, "password": "x", "password_confirmation": "x", "user_id": u.ID}), 400, "invalid_json")

	// Purpose mixing: a registration verification_id or code is never a reset token.
	regEmail := newMailbox()
	verificationID := e.requestCode(t, regEmail)
	invalid(verificationID)
	invalid(e.mail.latestCode(t, regEmail))

	first := e.forgot(t, u.Email)
	e.clock.Advance(61 * time.Second)
	second := e.forgot(t, u.Email)
	invalid(first) // superseded by the newer request

	// Field errors leave the token usable.
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", map[string]string{"token": second, "password": "New-Password-2026!", "password_confirmation": "New-Password-2026?"}), 400, "password_mismatch")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(second, resetOldPassword)), 400, "password_unchanged")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(second, "123456789")), 400, "invalid_password")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(second, strings.Repeat("密", 24)+"x")), 400, "invalid_password") // 73 bytes
	if e.reload(t, u.ID).AuthVersion != 1 {
		t.Fatal("a rejected password changed the account")
	}
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": second}), 200, "")
	// Four Chinese characters are 12 bytes: accepted although only 4 characters long.
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(second, "安全密码")), 200, "")
	invalid(second) // consumed
	e.webLogin(t, e.handler, u.Email, "安全密码")

	// Expiry: a link is usable until, and not at, 30 minutes.
	e.clock.Advance(61 * time.Second)
	late := e.forgot(t, u.Email)
	e.clock.Advance(30*time.Minute - time.Second)
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": late}), 200, "")
	e.clock.Advance(time.Second)
	invalid(late)

	// A disabled account cannot use a link issued while it was active.
	e.clock.Advance(time.Minute)
	pending := e.forgot(t, u.Email)
	if err := e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	invalid(pending)
	if e.reload(t, u.ID).Status != "disabled" {
		t.Fatal("a reset link re-enabled the account")
	}

	// Administrators: 16-byte minimum.
	admin := e.historicalUser(t, "admin-"+uuid.NewString()[:8]+"@corp.example", "admin", "active")
	adminToken := e.forgot(t, admin.Email)
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(adminToken, "fifteen-bytes!!")), 400, "invalid_password")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(adminToken, "sixteen-bytes!!!")), 200, "")
}

// R3-04 and R3-10: one reset revokes every web and client session of the account and
// changes nothing but the password state.
func TestResetRevokesAllSessionsAndKeepsAccount(t *testing.T) {
	e := newRegEnv(t)
	u := e.historicalUser(t, "sessions-"+uuid.NewString()[:8]+"@outlook.com", "user", "active")
	u = e.reload(t, u.ID) // database precision, for the before/after comparison
	other := e.historicalUser(t, "bystander-"+uuid.NewString()[:8]+"@outlook.com", "user", "active")
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 1 << 30, UsedUnits: 4242, MaxDevices: 8}
	order := model.Order{ID: uuid.NewString(), UserID: u.ID, IdempotencyKey: uuid.NewString(), PlanID: "pro", PlanName: "VIP", PriceCents: 100, DurationDays: 30, TrafficBytes: 1 << 30, Status: "paid", Provider: "test", PaidAt: now}
	for _, row := range []any{&sub, &order} {
		if err := e.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	web := [][]*http.Cookie{e.webLogin(t, e.handler, u.Email, resetOldPassword), e.webLogin(t, e.handler, u.Email, resetOldPassword)}
	native := map[string]string{}
	for _, platform := range []string{"macos", "windows", "android", "ios"} {
		native[platform] = e.nativeLogin(t, u.Email, resetOldPassword, platform)
	}
	otherWeb := e.webLogin(t, e.handler, other.Email, resetOldPassword)
	otherNative := e.nativeLogin(t, other.Email, resetOldPassword, "android")
	var beforeNative []model.NativeSession
	if err := e.db.Where("user_id = ?", u.ID).Order("id").Find(&beforeNative).Error; err != nil {
		t.Fatal(err)
	}

	token := e.forgot(t, u.Email)
	w := e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "Brand-New-Password-1"), withCookies(web[0]))
	expect(t, w, 200, "")
	cleared := map[string]bool{}
	for _, c := range w.Result().Cookies() {
		cleared[c.Name] = c.MaxAge < 0 && c.Value == ""
	}
	if !cleared["vpn_access"] || !cleared["vpn_refresh"] {
		t.Fatal("reset did not clear the browser's cookies")
	}

	for i, cookies := range web {
		expect(t, e.send(e.handler, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(cookies))), 401, "unauthorized")
		expect(t, e.send(e.handler, "POST", "/api/v1/auth/refresh", "{}", withCookies(refreshCookie(cookies))), 401, "session_expired")
		_ = i
	}
	for platform, token := range native {
		for _, path := range []string{"/api/v1/client/session", "/api/v1/client/nodes", "/api/v1/client/config"} {
			if w := e.send(e.handler, "GET", path, nil, withBearer(token)); w.Code != 401 || !strings.Contains(w.Body.String(), "native_session_expired") {
				t.Fatalf("%s token still works on %s: %d %s", platform, path, w.Code, w.Body.String())
			}
		}
		expect(t, e.send(e.handler, "POST", "/api/v1/client/traffic", map[string]int{"sequence": 1, "upload_bytes": 1, "download_bytes": 1}, withBearer(token)), 401, "native_session_expired")
	}
	if n := e.countWhere(t, &model.Session{}, "user_id = ? AND revoked_at IS NULL", u.ID); n != 0 {
		t.Fatalf("%d web sessions survived", n)
	}
	if n := e.countWhere(t, &model.NativeSession{}, "user_id = ? AND revoked_at IS NULL", u.ID); n != 0 {
		t.Fatalf("%d client sessions survived", n)
	}
	// Another account is untouched.
	expect(t, e.send(e.handler, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(otherWeb))), 200, "")
	expect(t, e.send(e.handler, "GET", "/api/v1/client/session", nil, withBearer(otherNative)), 200, "")

	// Old password fails everywhere; the new one works on the same account.
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": resetOldPassword}), 401, "invalid_credentials")
	expect(t, e.send(e.handler, "POST", "/api/v1/client/login", nativeLoginBody(u.Email, resetOldPassword, "macos")), 401, "invalid_credentials")
	e.webLogin(t, e.handler, u.Email, "Brand-New-Password-1")
	w = e.send(e.handler, "POST", "/api/v1/client/login", nativeLoginBody(u.Email, "Brand-New-Password-1", "ios"))
	expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), u.ID) {
		t.Fatal("new password signed into a different account")
	}

	// R3-10: only the password state changed.
	after := e.reload(t, u.ID)
	if after.ID != u.ID || after.Email != u.Email || after.Name != u.Name || after.Role != u.Role || after.Status != "active" || !after.CreatedAt.Equal(u.CreatedAt) {
		t.Fatal("identity fields changed")
	}
	if after.AuthVersion != 2 || after.PasswordChangedAt == nil || after.EmailVerifiedAt == nil || after.PasswordHash == u.PasswordHash {
		t.Fatalf("password state not updated: version=%d", after.AuthVersion)
	}
	var subAfter model.Subscription
	if err := e.db.Where("user_id = ?", u.ID).Take(&subAfter).Error; err != nil || subAfter.ID != sub.ID || subAfter.UsedUnits != 4242 || subAfter.TrafficLimitBytes != sub.TrafficLimitBytes || !subAfter.ExpiresAt.Equal(sub.ExpiresAt) || subAfter.PlanID != "pro" {
		t.Fatal("subscription changed")
	}
	if n := e.countWhere(t, &model.Order{}, "user_id = ?", u.ID); n != 1 {
		t.Fatal("orders changed")
	}
	var afterNative []model.NativeSession
	if err := e.db.Where("user_id = ? AND id IN ?", u.ID, ids(beforeNative)).Order("id").Find(&afterNative).Error; err != nil || len(afterNative) != len(beforeNative) {
		t.Fatal("client sessions were deleted instead of revoked")
	}
	for i := range afterNative {
		b, a := beforeNative[i], afterNative[i]
		if a.DeviceID != b.DeviceID || a.Platform != b.Platform || a.NodeID != b.NodeID || a.LastSequence != b.LastSequence || a.RevokedAt == nil {
			t.Fatal("client session history changed")
		}
	}
	var audit model.AdminAudit
	if err := e.db.Where("action = ? AND target_id = ?", authmail.AuditPasswordReset, u.ID).Take(&audit).Error; err != nil || audit.ActorID != u.ID || audit.Version != 2 {
		t.Fatal("reset audit missing")
	}
	// The notice arrives, carries no secret, and the link mail is the only one with a token.
	e.mail.deliver(t)
	msgs := e.mail.box.to(u.Email)
	notice := msgs[len(msgs)-1]
	if !strings.Contains(notice.Subject, "密码已修改") || strings.Contains(notice.Text, token) || strings.Contains(notice.Text, "Brand-New-Password-1") || strings.Contains(notice.HTML, "#token=") {
		t.Fatalf("change notice wrong: %q", notice.Subject)
	}
}

func ids(sessions []model.NativeSession) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}

// R3-05: concurrent submissions of one token change the password once; older links
// never come back.
func TestResetConcurrentSubmissions(t *testing.T) {
	e := newRegEnv(t)
	second := e.instance(e.cfg)
	u := e.historicalUser(t, "race-"+uuid.NewString()[:8]+"@icloud.com", "user", "active")
	token := e.forgot(t, u.Email)
	const n = 8
	results := make(chan *httptest.ResponseRecorder, n)
	var start sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		h := e.handler
		if i%2 == 1 {
			h = second
		}
		go func(i int, h http.Handler) {
			start.Wait()
			results <- e.send(h, "POST", "/api/v1/auth/reset-password", resetBody(token, fmt.Sprintf("Concurrent-Pass-%02d", i)))
		}(i, h)
	}
	start.Done()
	ok := 0
	for i := 0; i < n; i++ {
		w := <-results
		switch w.Code {
		case 200:
			ok++
		case 400:
			expect(t, w, 400, "reset_token_invalid")
		default:
			t.Fatalf("unexpected %d: %s", w.Code, w.Body.String())
		}
	}
	if ok != 1 || e.reload(t, u.ID).AuthVersion != 2 {
		t.Fatalf("%d successful resets", ok)
	}
	if n := e.countWhere(t, &model.AdminAudit{}, "action = ? AND target_id = ?", authmail.AuditPasswordReset, u.ID); n != 1 {
		t.Fatalf("%d audit rows", n)
	}

	// Two links in flight: the first is resolved, then a newer one is requested and used.
	e.clock.Advance(61 * time.Second)
	a := e.forgot(t, u.Email)
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": a}), 200, "")
	e.clock.Advance(61 * time.Second)
	b := e.forgot(t, u.Email)
	expect(t, e.send(second, "POST", "/api/v1/auth/reset-password", resetBody(b, "Second-Reset-Pass")), 200, "")
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(a, "Stale-Link-Pass-1")), 400, "reset_token_invalid")
	// A later link is bound to the new version; older ones stay dead after it is used.
	e.clock.Advance(61 * time.Second)
	c := e.forgot(t, u.Email)
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(c, "Third-Reset-Pass!")), 200, "")
	for _, old := range []string{token, a, b, c} {
		expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": old}), 400, "reset_token_invalid")
	}
	if v := e.reload(t, u.ID).AuthVersion; v != 4 {
		t.Fatalf("AuthVersion %d after three resets", v)
	}
	e.webLogin(t, e.handler, u.Email, "Third-Reset-Pass!")
}

// barrier pauses the first locking SELECT on one table made with a marked context, so
// a test can run another transaction at an exact point and then let the first go on.
type barrier struct {
	table    string
	arrived  chan struct{}
	release  chan struct{}
	once     sync.Once
	released sync.Once
}

type barrierKey struct{}

func newBarrier(table string) *barrier {
	return &barrier{table: table, arrived: make(chan struct{}), release: make(chan struct{})}
}

func (b *barrier) ctx() context.Context {
	return context.WithValue(context.Background(), barrierKey{}, b)
}
func (b *barrier) open() { b.released.Do(func() { close(b.release) }) }

func (b *barrier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-b.arrived:
	case <-time.After(20 * time.Second):
		t.Fatal("request never reached the barrier")
	}
}

// installBarriers registers the test-only GORM callback on this test's database.
func installBarriers(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Callback().Query().Before("gorm:query").Register("round3:barrier", func(tx *gorm.DB) {
		b, ok := tx.Statement.Context.Value(barrierKey{}).(*barrier)
		if !ok {
			return
		}
		table := tx.Statement.Table[strings.LastIndexByte(tx.Statement.Table, '.')+1:]
		if _, locking := tx.Statement.Clauses["FOR"]; !locking || table != b.table {
			return
		}
		b.once.Do(func() {
			close(b.arrived)
			<-b.release
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// R3-06: a login whose bcrypt check ran before a reset committed cannot create a session
// afterwards; a login that committed first is revoked by the reset. Web and client.
func TestResetLoginRaces(t *testing.T) {
	e := newRegEnv(t)
	installBarriers(t, e.db)
	for _, kind := range []string{"web", "native"} {
		t.Run(kind+"/reset_first", func(t *testing.T) {
			u := e.historicalUser(t, kind+"-rf-"+uuid.NewString()[:8]+"@yahoo.com", "user", "active")
			token := e.forgot(t, u.Email)
			b := newBarrier("users")
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				path, body := "/api/v1/auth/login", any(map[string]any{"email": u.Email, "password": resetOldPassword})
				if kind == "native" {
					path, body = "/api/v1/client/login", nativeLoginBody(u.Email, resetOldPassword, "windows")
				}
				done <- e.send(e.handler, "POST", path, body, withContext(b.ctx()))
			}()
			b.wait(t) // bcrypt accepted the old password; the session transaction has not locked the user yet
			expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "After-Race-Pass-1")), 200, "")
			b.open()
			w := <-done
			expect(t, w, 401, "invalid_credentials")
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("stale login set cookies")
			}
			if n := e.countWhere(t, &model.Session{}, "user_id = ?", u.ID) + e.countWhere(t, &model.NativeSession{}, "user_id = ?", u.ID); n != 0 {
				t.Fatalf("stale login created %d sessions", n)
			}
		})
		t.Run(kind+"/login_first", func(t *testing.T) {
			u := e.historicalUser(t, kind+"-lf-"+uuid.NewString()[:8]+"@yahoo.com", "user", "active")
			token := e.forgot(t, u.Email)
			b := newBarrier("users")
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "After-Race-Pass-2"), withContext(b.ctx()))
			}()
			b.wait(t) // the reset checked the token and hashed the password, but holds no lock yet
			var check func() *httptest.ResponseRecorder
			if kind == "web" {
				cookies := e.webLogin(t, e.handler, u.Email, resetOldPassword)
				check = func() *httptest.ResponseRecorder {
					return e.send(e.handler, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(cookies)))
				}
			} else {
				token := e.nativeLogin(t, u.Email, resetOldPassword, "android")
				check = func() *httptest.ResponseRecorder {
					return e.send(e.handler, "GET", "/api/v1/client/session", nil, withBearer(token))
				}
			}
			if check().Code != 200 {
				t.Fatal("login before the reset did not work")
			}
			b.open()
			expect(t, <-done, 200, "")
			if w := check(); w.Code != 401 {
				t.Fatalf("session created before the reset survived it: %d", w.Code)
			}
		})
	}
}

// bindTraffic prepares a client session that may report traffic (profile + paid plan).
func (e *regEnv) bindTraffic(t *testing.T, u model.User) string {
	t.Helper()
	profile := "proxies:\n  - name: Fixture\n    type: ss\n    server: 127.0.0.1\n    port: 18443\n    cipher: aes-128-gcm\n    password: test-only\nproxy-groups:\n  - name: VIP\n    type: select\n    proxies: [Fixture]\nrules: ['MATCH,VIP']\n"
	if err := os.WriteFile(filepath.Join(e.cfg.ClientProfileDir, "pro.yaml"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 1 << 30, MaxDevices: 4}
	if err := e.db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	token := e.nativeLogin(t, u.Email, resetOldPassword, "macos")
	expect(t, e.send(e.handler, "GET", "/api/v1/client/config", nil, withBearer(token)), 200, "")
	return token
}

func traffic(sequence int) map[string]int {
	return map[string]int{"sequence": sequence, "upload_bytes": sequence * 1000, "download_bytes": sequence * 2000}
}

// R3-07: refreshes, traffic reports and a reset contend for the user lock in every
// order without deadlock; nothing authenticated by an old session commits after the
// reset, and everything accepted before it stays in the ledger.
func TestResetRefreshAndTrafficOrdering(t *testing.T) {
	e := newRegEnv(t)
	installBarriers(t, e.db)
	usedUnits := func(u model.User) (int64, int64) {
		var sub model.Subscription
		if err := e.db.Where("user_id = ?", u.ID).Take(&sub).Error; err != nil {
			t.Fatal(err)
		}
		var reports int64
		if err := e.db.Model(&model.ClientTrafficReport{}).Where("user_id = ?", u.ID).Select("COALESCE(SUM(charged_units), 0)").Scan(&reports).Error; err != nil {
			t.Fatal(err)
		}
		return sub.UsedUnits, reports
	}

	t.Run("report_holds_lock_first", func(t *testing.T) {
		u := e.historicalUser(t, "order-a-"+uuid.NewString()[:8]+"@gmail.com", "user", "active")
		token := e.bindTraffic(t, u)
		expect(t, e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(1), withBearer(token)), 200, "")
		reset := e.forgot(t, u.Email)
		b := newBarrier("native_sessions") // after the report's user lock, before its session lock
		report := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			report <- e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(2), withBearer(token), withContext(b.ctx()))
		}()
		b.wait(t)
		resetDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			resetDone <- e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(reset, "Order-A-Pass-1"))
		}()
		select {
		case <-resetDone:
			t.Fatal("reset did not wait for the report holding the user lock")
		case <-time.After(300 * time.Millisecond):
		}
		b.open()
		expect(t, <-report, 200, "")
		expect(t, <-resetDone, 200, "")
		expect(t, e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(3), withBearer(token)), 401, "native_session_expired")
		if n := e.countWhere(t, &model.ClientTrafficReport{}, "user_id = ?", u.ID); n != 2 {
			t.Fatalf("%d reports, want the 2 accepted before the reset", n)
		}
		if used, sum := usedUnits(u); used != sum || used == 0 {
			t.Fatalf("ledger mismatch: used %d, reports %d", used, sum)
		}
	})

	t.Run("reset_holds_lock_first", func(t *testing.T) {
		u := e.historicalUser(t, "order-b-"+uuid.NewString()[:8]+"@gmail.com", "user", "active")
		token := e.bindTraffic(t, u)
		cookies := e.webLogin(t, e.handler, u.Email, resetOldPassword)
		expect(t, e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(1), withBearer(token)), 200, "")
		reset := e.forgot(t, u.Email)
		b := newBarrier("email_auth_challenges") // after the reset's user lock
		resetDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			resetDone <- e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(reset, "Order-B-Pass-1"), withContext(b.ctx()))
		}()
		b.wait(t)
		refresh := make(chan *httptest.ResponseRecorder, 1)
		report := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			refresh <- e.send(e.handler, "POST", "/api/v1/auth/refresh", "{}", withCookies(refreshCookie(cookies)))
		}()
		go func() {
			report <- e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(2), withBearer(token))
		}()
		select {
		case w := <-refresh:
			t.Fatalf("refresh did not wait for the reset: %d", w.Code)
		case w := <-report:
			t.Fatalf("report did not wait for the reset: %d", w.Code)
		case <-time.After(300 * time.Millisecond):
		}
		b.open()
		expect(t, <-resetDone, 200, "")
		expect(t, <-refresh, 401, "session_expired")
		expect(t, <-report, 401, "native_session_expired")
		if n := e.countWhere(t, &model.ClientTrafficReport{}, "user_id = ?", u.ID); n != 1 {
			t.Fatalf("%d reports, want only the one before the reset", n)
		}
		if used, sum := usedUnits(u); used != sum {
			t.Fatalf("ledger mismatch: used %d, reports %d", used, sum)
		}
	})

	t.Run("free_for_all", func(t *testing.T) {
		u := e.historicalUser(t, "order-c-"+uuid.NewString()[:8]+"@gmail.com", "user", "active")
		token := e.bindTraffic(t, u)
		var web [][]*http.Cookie
		for i := 0; i < 6; i++ {
			web = append(web, e.webLogin(t, e.handler, u.Email, resetOldPassword))
		}
		reset := e.forgot(t, u.Email)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var codes []int
		var refreshed [][]*http.Cookie
		accepted := 0
		start := make(chan struct{})
		record := func(code int) {
			mu.Lock()
			codes = append(codes, code)
			mu.Unlock()
		}
		for _, cookies := range web {
			wg.Add(1)
			go func(cookies []*http.Cookie) {
				defer wg.Done()
				<-start
				for i := 0; i < 3; i++ {
					w := e.send(e.handler, "POST", "/api/v1/auth/refresh", "{}", withCookies(refreshCookie(cookies)))
					record(w.Code)
					if w.Code != 200 {
						return
					}
					cookies = w.Result().Cookies()
					mu.Lock()
					refreshed = append(refreshed, cookies)
					mu.Unlock()
				}
			}(cookies)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for seq := 1; seq <= 20; seq++ {
				w := e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(seq), withBearer(token))
				record(w.Code)
				if w.Code != 200 {
					return
				}
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
		var resetCode int
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(5 * time.Millisecond)
			resetCode = e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(reset, "Order-C-Pass-1")).Code
		}()
		finished := make(chan struct{})
		close(start)
		go func() { wg.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(60 * time.Second):
			t.Fatal("contention did not finish: possible deadlock")
		}
		if resetCode != 200 {
			t.Fatalf("reset answered %d", resetCode)
		}
		for _, code := range codes {
			if code != 200 && code != 401 {
				t.Fatalf("unexpected status %d under contention (deadlock or failure)", code)
			}
		}
		for _, cookies := range append(web, refreshed...) {
			if w := e.send(e.handler, "POST", "/api/v1/auth/refresh", "{}", withCookies(refreshCookie(cookies))); w.Code != 401 {
				t.Fatal("a refresh token survived the reset")
			}
		}
		expect(t, e.send(e.handler, "POST", "/api/v1/client/traffic", traffic(accepted+1), withBearer(token)), 401, "native_session_expired")
		if n := e.countWhere(t, &model.ClientTrafficReport{}, "user_id = ?", u.ID); n != int64(accepted) {
			t.Fatalf("%d reports stored, %d accepted", n, accepted)
		}
		if used, sum := usedUnits(u); used != sum {
			t.Fatalf("ledger mismatch: used %d, reports %d", used, sum)
		}
	})
}

// R3-08: a database failure at any step of the password change rolls everything back.
func TestResetIsAtomic(t *testing.T) {
	e := newRegEnv(t)
	type failpointKey struct{}
	inject := errors.New("injected failure")
	hook := func(kind string) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			target, _ := tx.Statement.Context.Value(failpointKey{}).(string)
			if target != "" && target == kind+":"+tx.Statement.Table[strings.LastIndexByte(tx.Statement.Table, '.')+1:] {
				_ = tx.AddError(inject)
			}
		}
	}
	if err := e.db.Callback().Update().Before("gorm:update").Register("round3:fail_update", hook("update")); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Callback().Create().Before("gorm:create").Register("round3:fail_create", hook("create")); err != nil {
		t.Fatal(err)
	}
	u := e.historicalUser(t, "atomic-"+uuid.NewString()[:8]+"@126.com", "user", "active")
	web := e.webLogin(t, e.handler, u.Email, resetOldPassword)
	native := e.nativeLogin(t, u.Email, resetOldPassword, "android")
	token := e.forgot(t, u.Email)
	jobsBefore := e.countWhere(t, &model.AuthMailJob{}, "template = ?", authmail.TemplatePasswordChanged)
	steps := []string{
		"update:users",                 // new password
		"update:email_auth_challenges", // consume / supersede
		"update:sessions",              // revoke web sessions
		"update:native_sessions",       // revoke client sessions
		"update:auth_mail_jobs",        // cancel pending mail
		"create:admin_audits",          // audit
		"create:auth_mail_jobs",        // change notice
	}
	for _, step := range steps {
		ctx := context.WithValue(context.Background(), failpointKey{}, step)
		w := e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "Atomic-Pass-2026"), withContext(ctx))
		if w.Code != 503 {
			t.Fatalf("%s: expected 503, got %d %s", step, w.Code, w.Body.String())
		}
		after := e.reload(t, u.ID)
		if after.PasswordHash != u.PasswordHash || after.AuthVersion != 1 || after.PasswordChangedAt != nil || after.EmailVerifiedAt != nil {
			t.Fatalf("%s: password changed without the rest", step)
		}
		expect(t, e.send(e.handler, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(web))), 200, "")
		expect(t, e.send(e.handler, "GET", "/api/v1/client/session", nil, withBearer(native)), 200, "")
		expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password/validate", map[string]string{"token": token}), 200, "")
		if n := e.countWhere(t, &model.AdminAudit{}, "target_id = ?", u.ID); n != 0 {
			t.Fatalf("%s: audit row survived the rollback", step)
		}
		if n := e.countWhere(t, &model.AuthMailJob{}, "template = ?", authmail.TemplatePasswordChanged); n != jobsBefore {
			t.Fatalf("%s: change notice survived the rollback", step)
		}
	}
	expect(t, e.send(e.handler, "POST", "/api/v1/auth/reset-password", resetBody(token, "Atomic-Pass-2026")), 200, "")
	expect(t, e.send(e.handler, "GET", "/api/v1/auth/me", nil, withCookies(accessCookie(web))), 401, "unauthorized")
	expect(t, e.send(e.handler, "GET", "/api/v1/client/session", nil, withBearer(native)), 401, "native_session_expired")
	// A failing notice delivery afterwards does not undo the reset.
	e.mail.deliver(t)
	if e.reload(t, u.ID).AuthVersion != 2 {
		t.Fatal("reset not committed")
	}
}
