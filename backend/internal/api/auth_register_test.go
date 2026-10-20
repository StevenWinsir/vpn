package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// regEnv is one isolated database with a mail harness on a controllable clock.
type regEnv struct {
	db      *gorm.DB
	cfg     config.Config
	clock   *testClock
	mail    *mailHarness
	handler *gin.Engine
}

func newRegEnv(t *testing.T) *regEnv {
	t.Helper()
	db, cfg := newIsolatedNativeDatabase(t)
	cfg = withTestMail(cfg)
	cfg.BcryptCost = 4
	clock := &testClock{now: time.Date(2026, 10, 20, 9, 0, 30, 0, time.UTC)}
	e := &regEnv{db: db, cfg: cfg, clock: clock}
	e.mail = newMailHarness(db, cfg, clock.Now)
	e.handler = newServer(db, cfg, e.mail.service)
	return e
}

// instance returns another API instance on the same database (e.g. after a restart,
// or behind a load balancer), optionally with different configuration.
func (e *regEnv) instance(cfg config.Config, limits ...func(*authmail.Limits)) *gin.Engine {
	service := authmail.NewService(e.db, cfg)
	service.Now = e.clock.Now
	for _, f := range limits {
		f(&service.Limits)
	}
	return newServer(e.db, cfg, service)
}

func (e *regEnv) call(h http.Handler, path string, body any, ip string) *httptest.ResponseRecorder {
	var data []byte
	if s, ok := body.(string); ok {
		data = []byte(s)
	} else {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest("POST", "/api/v1/auth/"+path, strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", e.cfg.Origins[0])
	if ip == "" {
		ip = "198.51.100.7"
	}
	r.RemoteAddr = ip + ":40000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (e *regEnv) requestCode(t *testing.T, email string) string {
	t.Helper()
	w := e.call(e.handler, "register-code", map[string]string{"email": email}, "")
	expect(t, w, 202, "")
	var out struct {
		VerificationID string `json:"verification_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.VerificationID
}

func registration(email, id, code string) map[string]any {
	return map[string]any{"name": "注册测试", "email": email, "password": "Test-Only-Password-2026!", "remember": false, "verification_id": id, "code": code}
}

func expect(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("expected %d %s, got %d: %s", status, code, w.Code, w.Body.String())
	}
	if code != "" {
		var out struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Error.Code != code {
			t.Fatalf("expected error %s, got %s", code, w.Body.String())
		}
	}
}

func (e *regEnv) userCount(t *testing.T, email string) int64 {
	t.Helper()
	var n int64
	if err := e.db.Model(&model.User{}).Where("email = ?", email).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// setCode replaces a challenge's code, so cases such as leading zeros are deterministic.
func (e *regEnv) setCode(t *testing.T, id, email, code string) {
	t.Helper()
	digest := e.mail.service.Keys().CodeDigest(authmail.PurposeRegister, email, id, code)
	if err := e.db.Model(&model.EmailAuthChallenge{}).Where("id = ?", id).Update("digest", digest).Error; err != nil {
		t.Fatal(err)
	}
}

func newMailbox() string { return "reg-" + uuid.NewString()[:13] + "@gmail.com" }

// R2-01: a delivered code completes registration with the existing cookie session.
func TestRegistrationCodeFlow(t *testing.T) {
	e := newRegEnv(t)
	email := newMailbox()
	w := e.call(e.handler, "register-code", map[string]string{"email": "  " + strings.ToUpper(email) + " "}, "")
	expect(t, w, 202, "")
	var receipt map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	if receipt["expires_in_seconds"] != float64(600) || receipt["resend_after_seconds"] != float64(60) || receipt["expires_at"] != "2026-10-20T09:10:30Z" {
		t.Fatalf("receipt timing wrong: %v", receipt)
	}
	if e.userCount(t, email) != 0 {
		t.Fatal("requesting a code must not create a user")
	}
	id := receipt["verification_id"].(string)
	code := e.mail.latestCode(t, email)
	if strings.Contains(w.Body.String(), code) {
		t.Fatal("code leaked in the response")
	}
	w = e.call(e.handler, "register", registration(email, id, code), "")
	expect(t, w, 201, "")
	var out struct {
		User map[string]any `json:"user"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	for _, hidden := range []string{"password_hash", "email_verified_at", "auth_version", "PasswordHash"} {
		if _, ok := out.User[hidden]; ok {
			t.Fatalf("user response exposes %s", hidden)
		}
	}
	if out.User["role"] != "user" || out.User["email"] != email {
		t.Fatalf("unexpected user: %v", out.User)
	}
	cookies := w.Result().Cookies()
	names := map[string]bool{}
	for _, c := range cookies {
		names[c.Name] = c.HttpOnly && c.Path == "/api/v1" && c.SameSite == http.SameSiteLaxMode
	}
	if !names["vpn_access"] || !names["vpn_refresh"] || len(cookies) != 2 {
		t.Fatal("registration must set the two existing session cookies")
	}
	var user model.User
	if err := e.db.Where("email = ?", email).Take(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.EmailVerifiedAt == nil || !user.EmailVerifiedAt.Equal(e.clock.Now()) || user.AuthVersion != 1 || user.Role != "user" || user.Status != "active" {
		t.Fatalf("user not created as a verified ordinary account: %+v", user)
	}
	me := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	for _, c := range cookies {
		me.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, me)
	expect(t, rec, 200, "")
	expect(t, e.call(e.handler, "login", map[string]any{"email": email, "password": "Test-Only-Password-2026!"}, ""), 200, "")
	var challenge model.EmailAuthChallenge
	if err := e.db.Where("id = ?", id).Take(&challenge).Error; err != nil || challenge.Status != "consumed" || challenge.ConsumedAt == nil {
		t.Fatal("challenge not consumed with the account")
	}
}

// R2-02: only six ASCII digits are a code; nothing converts into one.
func TestRegistrationCodeFormat(t *testing.T) {
	e := newRegEnv(t)
	for _, code := range []string{"000000", "012345"} {
		email := newMailbox()
		id := e.requestCode(t, email)
		e.setCode(t, id, email, code)
		expect(t, e.call(e.handler, "register", registration(email, id, code), ""), 201, "")
	}
	email := newMailbox()
	id := e.requestCode(t, email)
	e.setCode(t, id, email, "012345")
	for _, bad := range []string{"12345", "0123456", "１２３４５６", " 012345", "012345\n", "01234a", "+12345", "01 345", "12345\u0000", ""} {
		expect(t, e.call(e.handler, "register", registration(email, id, bad), ""), 400, "invalid_verification_code")
	}
	// A number is not a string: the strict decoder refuses it, leading zero or not.
	raw := fmt.Sprintf(`{"name":"注册测试","email":%q,"password":"Test-Only-Password-2026!","remember":false,"verification_id":%q,"code":12345}`, email, id)
	expect(t, e.call(e.handler, "register", raw, ""), 400, "invalid_json")
	var c model.EmailAuthChallenge
	_ = e.db.Where("id = ?", id).Take(&c).Error
	if c.Failures != 0 || e.userCount(t, email) != 0 {
		t.Fatal("malformed codes must not count as guesses or create users")
	}
	expect(t, e.call(e.handler, "register", registration(email, id, "012345"), ""), 201, "")
}

// R2-03: five wrong codes lock a challenge; failures are committed and shared across
// instances and resends, and a correct code cannot bypass either limit.
func TestRegistrationGuessingLimits(t *testing.T) {
	e := newRegEnv(t)
	// Part 1, with a roomy mailbox budget: the per-challenge lock alone.
	e.mail.service.Limits.EmailVerifyFailures = 50
	other := e.instance(e.cfg, func(l *authmail.Limits) { l.EmailVerifyFailures = 50 })
	email := newMailbox()
	id := e.requestCode(t, email)
	code := e.mail.latestCode(t, email)
	wrong := fmt.Sprintf("%06d", (mustAtoi(code)+1)%1000000)
	for i := 1; i <= 4; i++ {
		h := e.handler
		if i%2 == 0 {
			h = other
		}
		expect(t, e.call(h, "register", registration(email, id, wrong), ""), 400, "verification_invalid")
	}
	var c model.EmailAuthChallenge
	_ = e.db.Where("id = ?", id).Take(&c).Error
	if c.Failures != 4 {
		t.Fatalf("failures not committed: %d", c.Failures)
	}
	expect(t, e.call(other, "register", registration(email, id, wrong), ""), 400, "verification_locked")
	expect(t, e.call(e.handler, "register", registration(email, id, code), ""), 400, "verification_locked")
	if e.userCount(t, email) != 0 {
		t.Fatal("a locked challenge registered")
	}

	// Part 2, default budget (5 per 30 minutes per mailbox): a resend, a new challenge,
	// another instance or a restart never reset it.
	e.mail.service.Limits.EmailVerifyFailures = 5
	email = newMailbox()
	id = e.requestCode(t, email)
	code = e.mail.latestCode(t, email)
	wrong = fmt.Sprintf("%06d", (mustAtoi(code)+1)%1000000)
	for i := 0; i < 4; i++ {
		expect(t, e.call(e.handler, "register", registration(email, id, wrong), ""), 400, "verification_invalid")
	}
	e.clock.Advance(61 * time.Second)
	id2 := e.requestCode(t, email)
	code2 := e.mail.latestCode(t, email)
	restarted := e.instance(e.cfg)
	expect(t, e.call(restarted, "register", registration(email, id2, wrong), ""), 400, "verification_invalid")
	w := e.call(restarted, "register", registration(email, id2, code2), "")
	expect(t, w, 429, "rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("budget exhaustion must carry Retry-After")
	}
	// The budget window outlives the code: no bypass before the code expires.
	e.clock.Advance(9 * time.Minute)
	expect(t, e.call(e.handler, "register", registration(email, id2, code2), ""), 429, "rate_limited")
	var c2 model.EmailAuthChallenge
	_ = e.db.Where("id = ?", id2).Take(&c2).Error
	if e.userCount(t, email) != 0 || c2.Status != "active" || c2.Failures != 1 {
		t.Fatalf("budget bypassed or miscounted: users=%d challenge=%s/%d", e.userCount(t, email), c2.Status, c2.Failures)
	}
}

// R2-04: expiry and resend boundaries follow the server clock.
func TestRegistrationExpiryAndResend(t *testing.T) {
	e := newRegEnv(t)
	email := newMailbox()
	id := e.requestCode(t, email)
	code := e.mail.latestCode(t, email)
	e.clock.Advance(59 * time.Second)
	w := e.call(e.handler, "register-code", map[string]string{"email": email}, "")
	expect(t, w, 429, "rate_limited")
	if w.Header().Get("Retry-After") != "1" {
		t.Fatalf("cooldown Retry-After = %q", w.Header().Get("Retry-After"))
	}
	e.clock.Advance(time.Second)
	id2 := e.requestCode(t, email)
	code2 := e.mail.latestCode(t, email)
	// The first code is superseded by the new request, even inside its own lifetime.
	expect(t, e.call(e.handler, "register", registration(email, id, code), ""), 400, "verification_expired")
	// The new challenge has its own 10 minutes, measured from its creation.
	e.clock.Advance(10 * time.Minute)
	expect(t, e.call(e.handler, "register", registration(email, id2, code2), ""), 400, "verification_expired")

	email = newMailbox()
	id = e.requestCode(t, email)
	code = e.mail.latestCode(t, email)
	e.clock.Advance(10*time.Minute - time.Second)
	expect(t, e.call(e.handler, "register", registration(email, id, code), ""), 201, "")
}

// R2-05: the proof is bound to its mailbox, and the final step re-reads configuration.
func TestRegistrationBindingAndConfiguration(t *testing.T) {
	e := newRegEnv(t)
	email, other := newMailbox(), newMailbox()
	id := e.requestCode(t, email)
	code := e.mail.latestCode(t, email)
	expect(t, e.call(e.handler, "register", registration(other, id, code), ""), 400, "verification_invalid")
	expect(t, e.call(e.handler, "register", registration(email, uuid.NewString(), code), ""), 400, "verification_invalid")
	expect(t, e.call(e.handler, "register", registration(email, "not-a-uuid", code), ""), 400, "verification_invalid")

	closed := e.cfg
	closed.AllowRegistration = false
	expect(t, e.call(e.instance(closed), "register", registration(email, id, code), ""), 403, "registration_disabled")
	expect(t, e.call(e.instance(closed), "register-code", map[string]string{"email": newMailbox()}, ""), 403, "registration_disabled")
	narrowed := e.cfg
	narrowed.RegistrationDomains = []string{"qq.com"}
	expect(t, e.call(e.instance(narrowed), "register", registration(email, id, code), ""), 400, "email_domain_not_allowed")
	if e.userCount(t, email)+e.userCount(t, other) != 0 {
		t.Fatal("configuration change bypassed")
	}
	expect(t, e.call(e.handler, "register-code", map[string]string{"email": "user@mail.qq.com"}, ""), 400, "email_domain_not_allowed")
	expect(t, e.call(e.handler, "register-code", map[string]string{"email": "Name <user@qq.com>"}, ""), 400, "invalid_email")
	// Without mail the code cannot be sent, so registration reports itself closed.
	noMail := e.cfg
	noMail.SMTPPass = ""
	expect(t, e.call(e.instance(noMail), "register", registration(email, id, code), ""), 403, "registration_disabled")
	// The original challenge is still usable once configuration allows it again.
	expect(t, e.call(e.handler, "register", registration(email, id, code), ""), 201, "")
}

// R2-06: concurrent requests create at most one account; a lost response is not a
// second registration.
func TestRegistrationConcurrency(t *testing.T) {
	e := newRegEnv(t)
	email := newMailbox()
	codes := parallel(10, func(i int) int {
		return e.call(e.handler, "register-code", map[string]string{"email": email}, fmt.Sprintf("198.51.100.%d", i+10)).Code
	})
	if codes[202] != 1 || codes[429] != 9 {
		t.Fatalf("concurrent code requests: %v", codes)
	}
	var active int64
	_ = e.db.Model(&model.EmailAuthChallenge{}).Where("email = ? AND status = ?", email, "active").Count(&active).Error
	if active != 1 {
		t.Fatalf("active challenges = %d", active)
	}
	var challenge model.EmailAuthChallenge
	_ = e.db.Where("email = ? AND status = ?", email, "active").Take(&challenge).Error
	code := e.mail.latestCode(t, email)
	other := e.instance(e.cfg)
	results := parallel(8, func(i int) int {
		h := e.handler
		if i%2 == 1 {
			h = other
		}
		return e.call(h, "register", registration(email, challenge.ID, code), "").Code
	})
	if results[201] != 1 || results[400] != 7 {
		t.Fatalf("concurrent registrations: %v", results)
	}
	expect(t, e.call(e.handler, "register", registration(email, challenge.ID, code), ""), 400, "verification_used")
	var sessions int64
	_ = e.db.Model(&model.Session{}).Joins("JOIN "+e.cfg.Schema+".users u ON u.id = sessions.user_id").Where("u.email = ?", email).Count(&sessions).Error
	if e.userCount(t, email) != 1 || sessions != 1 {
		t.Fatalf("users=%d sessions=%d", e.userCount(t, email), sessions)
	}
	var subs int64
	_ = e.db.Model(&model.Subscription{}).Count(&subs).Error
	if subs != 0 {
		t.Fatal("registration must not grant a plan")
	}
	// An address that already has an account: the proof is checked first, then 409.
	e.clock.Advance(time.Minute)
	id := e.requestCode(t, email)
	e.setCode(t, id, email, "424242")
	expect(t, e.call(e.handler, "register", registration(email, id, "000001"), ""), 400, "verification_invalid")
	expect(t, e.call(e.handler, "register", registration(email, id, "424242"), ""), 409, "email_exists")
}

func parallel(n int, f func(i int) int) map[int]int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	start := make(chan struct{})
	out := map[int]int{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			code := f(i)
			mu.Lock()
			out[code]++
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

// R2-07: the original endpoint has no compatibility bypass.
func TestRegistrationHasNoBypass(t *testing.T) {
	e := newRegEnv(t)
	email := newMailbox()
	legacy := map[string]any{"name": "旧接口", "email": email, "password": "Test-Only-Password-2026!", "remember": true}
	expect(t, e.call(e.handler, "register", legacy, ""), 400, "verification_required")
	id := e.requestCode(t, email)
	code := e.mail.latestCode(t, email)
	for _, field := range []string{"role", "status", "verified", "email_verified", "user_id", "plan_id", "id"} {
		body := registration(email, id, code)
		body[field] = "admin"
		expect(t, e.call(e.handler, "register", body, ""), 400, "invalid_json")
	}
	token, _ := authmail.NewResetToken()
	expect(t, e.call(e.handler, "register", registration(email, id, token), ""), 400, "invalid_verification_code")
	expect(t, e.call(e.handler, "register", registration(email, token, code), ""), 400, "verification_invalid")
	// Disallowed domains are refused at the final step too, not only when sending.
	expect(t, e.call(e.handler, "register", registration("someone@example.invalid", id, code), ""), 400, "email_domain_not_allowed")
	if e.userCount(t, email) != 0 {
		t.Fatal("user created without a valid proof")
	}
	var users int64
	_ = e.db.Model(&model.User{}).Count(&users).Error
	if users != 0 {
		t.Fatal("bypass attempts created users")
	}
	expect(t, e.call(e.handler, "register", registration(email, id, code), ""), 201, "")
}

// R2-08: registered and unregistered addresses get the same public answer.
func TestRegistrationCodeDoesNotRevealAccounts(t *testing.T) {
	e := newRegEnv(t)
	e.mail.service.Limits.IPPerHour, e.mail.service.Limits.IPPerDay, e.mail.service.Limits.GlobalPerMinute = 1000, 1000, 1000
	const rounds = 15
	var existing, fresh []string
	for i := 0; i < rounds; i++ {
		u := model.User{ID: uuid.NewString(), Email: newMailbox(), Name: "Existing", PasswordHash: "x", Role: "user", Status: "active"}
		if err := e.db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		existing = append(existing, u.Email)
		fresh = append(fresh, newMailbox())
	}
	shape := func(w *httptest.ResponseRecorder) string {
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		keys := make([]string, 0, len(body))
		for k, v := range body {
			kind := reflect.TypeOf(v).Kind().String()
			if k == "verification_id" {
				if _, err := uuid.Parse(v.(string)); err != nil {
					kind = "not-uuid"
				}
			}
			if k == "message" {
				kind = v.(string)
			}
			keys = append(keys, k+":"+kind)
		}
		sort.Strings(keys)
		return fmt.Sprint(w.Code, keys)
	}
	var durations [2][]time.Duration
	var shapes [2]map[string]bool
	for i := 0; i < rounds; i++ {
		for k, email := range []string{existing[i], fresh[i]} {
			start := time.Now()
			w := e.call(e.handler, "register-code", map[string]string{"email": email}, "")
			durations[k] = append(durations[k], time.Since(start))
			if shapes[k] == nil {
				shapes[k] = map[string]bool{}
			}
			shapes[k][shape(w)] = true
		}
	}
	if len(shapes[0]) != 1 || !reflect.DeepEqual(shapes[0], shapes[1]) {
		t.Fatalf("response shapes differ: existing=%v new=%v", shapes[0], shapes[1])
	}
	median := func(d []time.Duration) time.Duration {
		s := append([]time.Duration(nil), d...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return s[len(s)/2]
	}
	a, b := median(durations[0]), median(durations[1])
	t.Logf("register-code median latency: existing=%v new=%v", a, b)
	if a > 3*b || b > 3*a {
		t.Fatalf("latency separates existing accounts: existing=%v new=%v", a, b)
	}
	e.mail.deliver(t)
	for i := 0; i < rounds; i++ {
		if len(e.mail.box.to(existing[i])) != 0 || len(e.mail.box.to(fresh[i])) != 1 {
			t.Fatal("only eligible new mailboxes may receive registration mail")
		}
	}
}

// Password and name rules are measured like the backend has always measured them:
// bytes for passwords, Unicode characters for names.
func TestRegistrationFieldRules(t *testing.T) {
	e := newRegEnv(t)
	email := newMailbox()
	id := e.requestCode(t, email)
	code := e.mail.latestCode(t, email)
	try := func(name, password string) *httptest.ResponseRecorder {
		body := registration(email, id, code)
		body["name"], body["password"] = name, password
		return e.call(e.handler, "register", body, "")
	}
	expect(t, try("测", "Test-Only-Password-2026!"), 400, "invalid_registration")
	expect(t, try(strings.Repeat("名", 41), "Test-Only-Password-2026!"), 400, "invalid_registration")
	expect(t, try("测试", strings.Repeat("密", 25)), 400, "invalid_registration") // 75 bytes
	expect(t, try("测试", "短密码"), 400, "invalid_registration")                   // 9 bytes
	expect(t, try("  ", "Test-Only-Password-2026!"), 400, "invalid_registration")
	var c model.EmailAuthChallenge
	_ = e.db.Where("id = ?", id).Take(&c).Error
	if c.Failures != 0 {
		t.Fatal("field errors must not consume the verification budget")
	}
	// 4 Chinese characters = 12 bytes: valid although shorter than 10 characters.
	expect(t, try(strings.Repeat("名", 40), "安全密码"), 201, "")
}

func mustAtoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
