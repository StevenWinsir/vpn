package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vpn/backend/internal/model"
)

// R3-06 / R3-07: login records, last sign-in, registration address.

func (a *acctEnv) events(t *testing.T, userID string) []model.LoginEvent {
	t.Helper()
	var out []model.LoginEvent
	if err := a.db.Where("user_id = ?", userID).Order("occurred_at, id").Find(&out).Error; err != nil {
		t.Fatal(err)
	}
	return out
}

// waitEvents polls for background (failure) records.
func (a *acctEnv) waitEvents(t *testing.T, userID string, n int) []model.LoginEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := a.events(t, userID); len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				t.Fatalf("expected %d login records, found %d", n, len(got))
			}
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (a *acctEnv) loginFrom(ip, ua string, email, password string, options ...requestOption) *httptest.ResponseRecorder {
	return a.send(a.h, "POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password},
		append([]requestOption{fromIP(ip), func(r *http.Request) { r.Header.Set("User-Agent", ua) }}, options...)...)
}

func TestLoginRecords(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	if got := a.reload(t, u.ID); got.LastLoginAt != nil || got.LastLoginIP != nil || got.RegisterIP != nil {
		t.Fatal("an account that predates login records must keep empty sign-in facts")
	}

	// Success: the record, the account's last sign-in, and the cookie all agree.
	expect(t, a.loginFrom("198.51.100.20", "Mozilla/5.0 (test)", u.Email, acctOldPassword), 200, "")
	got := a.reload(t, u.ID)
	if got.LastLoginIP == nil || *got.LastLoginIP != "198.51.100.20" || got.LastLoginAt == nil || !got.LastLoginAt.Equal(a.clock.Now()) {
		t.Fatalf("last sign-in not stamped: %+v", got)
	}
	ev := a.events(t, u.ID)
	if len(ev) != 1 || ev[0].Channel != model.LoginWeb || ev[0].Result != model.LoginSuccess || ev[0].IP != "198.51.100.20" || ev[0].UserAgent != "Mozilla/5.0 (test)" {
		t.Fatalf("success record: %+v", ev)
	}
	if ev[0].GeoStatus != model.GeoSkipped {
		t.Fatalf("no lookup service is configured, geo status %q", ev[0].GeoStatus)
	}

	// A private address is labelled at once and never queued for an external lookup.
	a.clock.Advance(time.Second)
	expect(t, a.loginFrom("10.1.2.3", "ua", u.Email, acctOldPassword), 200, "")
	ev = a.events(t, u.ID)
	if ev[1].GeoStatus != model.GeoDone || ev[1].GeoText != model.GeoLocal {
		t.Fatalf("private address: %+v", ev[1])
	}

	// Failure: recorded in the background, with the reason, and not as a last sign-in.
	a.clock.Advance(time.Second)
	expect(t, a.loginFrom("198.51.100.21", "ua", u.Email, "wrong-"+acctOldPassword), 401, "invalid_credentials")
	ev = a.waitEvents(t, u.ID, 3)
	if ev[2].Result != model.LoginFailure || ev[2].FailureReason != loginBadPassword || ev[2].IP != "198.51.100.21" {
		t.Fatalf("failure record: %+v", ev[2])
	}
	if after := a.reload(t, u.ID); after.LastLoginIP == nil || *after.LastLoginIP != "10.1.2.3" {
		t.Fatal("a failed attempt changed the last sign-in")
	}

	// An address that is not an account leaves nothing behind; neither does a token refresh.
	var total int64
	a.db.Model(&model.LoginEvent{}).Count(&total)
	expect(t, a.loginFrom("198.51.100.22", "ua", "nobody-"+u.Email, "whatever-password"), 401, "invalid_credentials")
	a.clock.Advance(time.Second)
	cookies := a.login(t, u)
	a.waitEvents(t, u.ID, 4)
	expect(t, a.send(a.h, "POST", "/api/v1/auth/refresh", nil, withCookies(refreshCookie(cookies))), 200, "")
	time.Sleep(100 * time.Millisecond)
	var after int64
	a.db.Model(&model.LoginEvent{}).Count(&after)
	if after != total+1 {
		t.Fatalf("expected exactly one new record (the login), got %d", after-total)
	}

	// The client login records its platform.
	a.clock.Advance(time.Second)
	a.native(t, u)
	a.clock.Advance(time.Second)
	nativeFailure := a.send(a.h, "POST", "/api/v1/client/login", nativeLoginBody(u.Email, "wrong-"+acctOldPassword, "android"))
	expect(t, nativeFailure, 401, "invalid_credentials")
	ev = a.waitEvents(t, u.ID, 6)
	var seen []string
	for _, e := range ev[4:] {
		seen = append(seen, e.Channel+"/"+e.Platform+"/"+e.Result)
	}
	if !(strings.Contains(strings.Join(seen, ","), "native/windows/success") && strings.Contains(strings.Join(seen, ","), "native/android/failure")) {
		t.Fatalf("client records: %v", seen)
	}
}

// R3-07: a forged X-Forwarded-For changes nothing unless the sender is a trusted proxy.
func TestLoginRecordAddressIgnoresForgedHeaders(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	forged := func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		r.Header.Set("X-Real-IP", "203.0.113.98")
		r.Header.Set("Forwarded", "for=203.0.113.97")
	}
	expect(t, a.loginFrom("198.51.100.30", "ua", u.Email, acctOldPassword, forged), 200, "")
	expect(t, a.loginFrom("198.51.100.30", "ua", u.Email, "wrong-"+acctOldPassword, forged), 401, "invalid_credentials")
	for _, ev := range a.waitEvents(t, u.ID, 2) {
		if ev.IP != "198.51.100.30" {
			t.Fatalf("forged header reached the record: %s", ev.IP)
		}
	}
	if ip := a.reload(t, u.ID).LastLoginIP; ip == nil || *ip != "198.51.100.30" {
		t.Fatalf("forged header reached the account: %v", ip)
	}

	// Behind a configured proxy the forwarded client address is used.
	cfg := a.cfg
	cfg.TrustedProxies = []string{"198.51.100.0/24"}
	proxied := newServerAt(a.db, cfg, a.mail.service, a.clock.Now)
	w := a.send(proxied, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": acctOldPassword}, fromIP("198.51.100.30"), forged)
	expect(t, w, 200, "")
	if ip := a.reload(t, u.ID).LastLoginIP; ip == nil || *ip != "203.0.113.99" {
		t.Fatalf("trusted proxy address not honoured: %v", ip)
	}
}

func TestLoginRecordUserAgentIsClipped(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	long := strings.Repeat("浏览器/1.0 ", 60) // multi-byte, longer than 200 bytes
	expect(t, a.loginFrom("198.51.100.31", long, u.Email, acctOldPassword), 200, "")
	ev := a.events(t, u.ID)
	if len(ev[0].UserAgent) > 200 || !strings.HasPrefix(long, ev[0].UserAgent) || len(ev[0].UserAgent) < 190 {
		t.Fatalf("user agent stored with %d bytes", len(ev[0].UserAgent))
	}
	if strings.ContainsRune(ev[0].UserAgent, '�') {
		t.Fatal("user agent cut inside a character")
	}
}

// R3-06: the lookup never runs inside a login; a lookup service that hangs costs nothing.
func TestLoginDoesNotWaitForGeoLookup(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
	}))
	defer slow.Close()
	defer close(release)
	a := newAcctEnv(t)
	cfg := a.cfg
	cfg.Geo.URLTemplate = slow.URL + "/json/{ip}"
	cfg.Geo.RequestsPerMinute = 600
	h := newServerAt(a.db, cfg, a.mail.service, a.clock.Now)
	u := a.user(t, "user")
	start := time.Now()
	for i := 0; i < 3; i++ {
		w := a.send(h, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": acctOldPassword}, fromIP("198.51.100.40"))
		expect(t, w, 200, "")
	}
	if time.Since(start) > 3*time.Second || calls.Load() != 0 {
		t.Fatalf("login touched the lookup service: %d calls in %s", calls.Load(), time.Since(start))
	}
	for _, ev := range a.events(t, u.ID) {
		if ev.GeoStatus != model.GeoPending || ev.GeoText != "" {
			t.Fatalf("record resolved inside the login: %+v", ev)
		}
	}
}

// The record is best effort: with the table gone, the login itself still works.
func TestLoginSucceedsWhenRecordCannotBeStored(t *testing.T) {
	a := newAcctEnv(t)
	u := a.user(t, "user")
	if err := a.db.Exec(`DROP TABLE "` + a.cfg.Schema + `".login_events`).Error; err != nil {
		t.Fatal(err)
	}
	expect(t, a.loginFrom("198.51.100.50", "ua", u.Email, acctOldPassword), 200, "")
	expect(t, a.loginFrom("198.51.100.50", "ua", u.Email, "wrong-"+acctOldPassword), 401, "invalid_credentials")
	if a.reload(t, u.ID).LastLoginIP == nil {
		t.Fatal("last sign-in not stamped")
	}
}

// Registration fixes the registration address and counts as the first sign-in.
func TestRegistrationRecordsAddress(t *testing.T) {
	a := newAcctEnv(t)
	email := "newcomer-" + strings.ReplaceAll(a.user(t, "user").ID[:8], "-", "") + "@yahoo.com"
	id := a.requestCode(t, email)
	code := a.mail.latestCode(t, email)
	w := a.send(a.h, "POST", "/api/v1/auth/register", registration(email, id, code), fromIP("198.51.100.60"), func(r *http.Request) { r.Header.Set("User-Agent", "register-ua") })
	expect(t, w, 201, "")
	var u model.User
	if err := a.db.Where("email = ?", email).Take(&u).Error; err != nil {
		t.Fatal(err)
	}
	if u.RegisterIP == nil || *u.RegisterIP != "198.51.100.60" || u.LastLoginIP == nil || u.LastLoginAt == nil {
		t.Fatalf("registration facts: %+v", u)
	}
	ev := a.events(t, u.ID)
	if len(ev) != 1 || ev[0].Result != model.LoginSuccess || ev[0].UserAgent != "register-ua" {
		t.Fatalf("registration record: %+v", ev)
	}
	// The response is unchanged: no sign-in facts leak into it.
	var body map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, key := range []string{"register_ip", "last_login_ip", "last_login_at", "deletion_requested_at"} {
		if _, leaked := body["user"][key]; leaked {
			t.Fatalf("user JSON exposes %s", key)
		}
	}
}

// The account page lists the caller's own latest ten, newest first.
func TestMyLogins(t *testing.T) {
	a := newAcctEnv(t)
	u, other := a.user(t, "user"), a.user(t, "user")
	var cookies []*http.Cookie
	for i := 0; i < 12; i++ {
		a.clock.Advance(time.Second)
		w := a.loginFrom("10.0.0."+string(rune('1'+i%9)), "ua", u.Email, acctOldPassword)
		expect(t, w, 200, "")
		cookies = w.Result().Cookies()
	}
	a.login(t, other)
	w := a.send(a.h, "GET", "/api/v1/me/logins", nil, withCookies(cookies))
	expect(t, w, 200, "")
	var out struct {
		Logins []struct {
			OccurredAt time.Time `json:"occurred_at"`
			IP         string    `json:"ip"`
			GeoText    string    `json:"geo_text"`
			Result     string    `json:"result"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Logins) != 10 {
		t.Fatalf("expected ten records: %v %s", err, w.Body.String())
	}
	for i := 1; i < len(out.Logins); i++ {
		if out.Logins[i].OccurredAt.After(out.Logins[i-1].OccurredAt) {
			t.Fatal("not newest first")
		}
	}
	if out.Logins[0].GeoText != model.GeoLocal || strings.Contains(w.Body.String(), "user_agent") || strings.Contains(w.Body.String(), "user_id") {
		t.Fatalf("unexpected content: %s", w.Body.String())
	}
	expect(t, a.send(a.h, "GET", "/api/v1/me/logins", nil), 401, "unauthorized")
}
