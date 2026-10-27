package api

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/referral"
)

// Round 3 of handoff_coupon.md (R3-04, R3-05): the security review of the referral
// programme, kept as executable checks. The per-endpoint access rules (401, 403 for
// ordinary users and impersonated sessions, whitelisted fields, no referee id or order)
// are pinned by TestReferralEndpoints; the pieces below are the ones only a review of the
// whole programme can ask for.

var timestamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT`)

// registrationFingerprint is what a client can observe of a registration response once the
// values that are different for every account (ids, address, times, cookie values) are
// blanked: status, header names, the JSON with its values masked, and the cookies'
// names and attributes.
func registrationFingerprint(w *httptest.ResponseRecorder, email string) string {
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	var mask func(v any) any
	mask = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				x[k] = mask(val)
			}
			return x
		case string:
			if x == email || timestamp.MatchString(x) || regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(x) {
				return "<per-account>"
			}
			return x
		default:
			return v
		}
	}
	body, _ := json.Marshal(mask(out))
	var headers []string
	for name := range w.Header() {
		switch name {
		case "Date", "Set-Cookie":
		case "X-Request-Id": // a fresh id per request; only its presence is compared
			headers = append(headers, name)
		default:
			headers = append(headers, name+"="+strings.Join(w.Header()[name], ","))
		}
	}
	slices.Sort(headers)
	var cookies []string
	for _, c := range w.Result().Cookies() {
		cookies = append(cookies, c.Name+"|"+c.Path+"|"+strconv.Itoa(int(c.SameSite))+"|"+boolText(c.HttpOnly)+"|"+boolText(c.Secure)+"|"+boolText(c.MaxAge != 0))
	}
	slices.Sort(cookies)
	return strings.Join([]string{boolText(w.Code == 201), string(body), strings.Join(headers, ";"), strings.Join(cookies, ";")}, "\n")
}

func boolText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// R3-04: nothing a visitor can observe tells a good code from a bad one, and no route
// looks codes up.
func TestReferralDoesNotRevealCodes(t *testing.T) {
	e := newRefEnv(t, nil)
	a := e.user("sec-anna", "user")
	code := e.codeOf(a)
	deleting := e.user("sec-dora", "user")
	deletingCode := e.codeOf(deleting)
	e.db.Model(&model.User{}).Where("id = ?", deleting.ID).Update("status", model.UserDeleting)

	// Every case answers with the same bytes (apart from the per-account values).
	fingerprint := func(input any) string {
		email := newMailbox()
		_, w := e.register(email, input)
		return registrationFingerprint(w, email)
	}
	want := fingerprint(nil)
	for name, input := range map[string]any{
		"empty": "", "garbage": "!!not a code!!", "unknown": "ZZZZZZZZ", "referrer being deleted": deletingCode, "valid": code,
		"valid lower case": strings.ToLower(code),
	} {
		if got := fingerprint(input); got != want {
			t.Fatalf("%s: the response differs from a registration without a code:\n%s\n---\n%s", name, got, want)
		}
	}

	// The only routes that mention the programme, and who may call them: nothing public,
	// no lookup by code.
	routes := map[string]bool{}
	for _, route := range e.engine().Routes() {
		if strings.Contains(route.Path, "referral") {
			routes[route.Method+" "+route.Path] = true
		}
	}
	expected := map[string]bool{
		"GET /api/v1/me/referral":                       true,
		"GET /api/v1/me/referrals":                      true,
		"GET /api/v1/admin/console/users/:id/referrals": true,
	}
	if len(routes) != len(expected) {
		t.Fatalf("referral routes %v", routes)
	}
	for route := range routes {
		if !expected[route] {
			t.Fatalf("unexpected referral route %s (all: %v)", route, routes)
		}
	}
	for _, path := range []string{"/api/v1/me/referral", "/api/v1/me/referrals", "/api/v1/admin/console/users/" + a.ID + "/referrals"} {
		if w := e.call(nil, "GET", path+"?code="+code, nil, ""); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
	}
	// The public answers do not change with a code in the query.
	plain := e.call(nil, "GET", "/api/v1/meta", nil, "").Body.String()
	for _, query := range []string{"?ref=" + code, "?referral_code=" + code, "?ref=ZZZZZZZZ"} {
		if got := e.call(nil, "GET", "/api/v1/meta"+query, nil, "").Body.String(); got != plain {
			t.Fatalf("/meta%s differs", query)
		}
	}
	// A code is never part of a user's own JSON, and a stranger cannot read another's page.
	if me := e.call(a.cookies, "GET", "/api/v1/auth/me", nil, ""); strings.Contains(me.Body.String(), code) {
		t.Fatal("/auth/me leaks the referral code")
	}
	stranger := e.user("sec-stranger", "user")
	if body := e.call(stranger.cookies, "GET", "/api/v1/me/referral", nil, "").Body.String(); strings.Contains(body, code) {
		t.Fatal("another account's page shows the code")
	}
}

// R3-04: text a referee controls is only ever shown masked and as plain text, and the
// responses carry the headers that keep a browser from reading it as markup.
func TestReferralTextAndHeaders(t *testing.T) {
	// The mask keeps two characters of the local part, whatever they are: the website
	// renders it as text (React escapes it), and this is the whole of the string.
	for in, want := range map[string]string{
		"<script>alert(1)</script>@evil.example": "<s***@evil.example",
		"a@b.co":                                 "a***@b.co",
		"zh.hao@qq.com":                          "zh***@qq.com",
	} {
		if got := referral.MaskEmail(in); got != want {
			t.Fatalf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
	e := newRefEnv(t, nil)
	a := e.user("sec-head", "user")
	code := e.codeOf(a)
	e.register("zh.hao.x@gmail.com", code)
	for _, path := range []string{"/api/v1/me/referral", "/api/v1/me/referrals"} {
		w := e.call(a.cookies, "GET", path, nil, "")
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s: %d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
		for _, header := range []struct{ name, want string }{{"X-Content-Type-Options", "nosniff"}, {"X-Frame-Options", "DENY"}, {"Cache-Control", "no-store"}, {"Referrer-Policy", "no-referrer"}} {
			if got := w.Header().Get(header.name); got != header.want {
				t.Fatalf("%s %s = %q, want %q", path, header.name, got, header.want)
			}
		}
		if strings.Contains(w.Body.String(), "zh.hao.x") {
			t.Fatalf("%s shows more than the masked address: %s", path, w.Body.String())
		}
	}
}

// R3-04: a whole run through binding, qualification, grants, a failing bind, a failing
// grant and the sweep leaves no code and no address in the logs.
func TestReferralLogsStayClean(t *testing.T) {
	logs := captureLogs(t)
	e := newRefEnv(t, nil)
	stmts := &statementLog{}
	stmts.install(t, e.db)
	a := e.user("sec-log-a", "user")
	code := e.codeOf(a)
	e.buy(a, "e2e29", "")

	var addresses []string
	join := func(failBind bool) payUser {
		email := newMailbox()
		addresses = append(addresses, email, strings.Split(email, "@")[0])
		stmts.set(func(l *statementLog) { l.failReferral = failBind })
		u, _ := e.register(email, code)
		stmts.set(func(l *statementLog) { l.failReferral = false })
		return u
	}
	b := join(false)
	join(true) // the binding fails and says so, in a category
	e.buy(b, "e2e29", "")
	// A grant that fails once and is retried.
	stmts.set(func(l *statementLog) { l.failSubscription = true })
	_, _ = e.server().processReferrals(t.Context())
	stmts.set(func(l *statementLog) { l.failSubscription = false })
	e.db.Model(&model.Referral{}).Where("status = ?", model.ReferralQualified).Update("next_attempt_at", e.clock.Now())
	e.settle()
	// A first purchase fulfilled without the check, found by the sweep.
	c := join(false)
	e.buy(c, "e2e29", "")
	if _, err := e.server().sweepReferrals(t.Context()); err != nil {
		t.Fatal(err)
	}

	text := logs.String()
	for _, bad := range append([]string{code, a.Email, strings.Split(a.Email, "@")[0]}, addresses...) {
		if strings.Contains(text, bad) {
			t.Fatalf("a log line carries %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "referral: binding skipped") {
		t.Fatalf("the failing binding logged nothing, so this check proves little:\n%s", text)
	}
}

// R3-05: the readiness report carries the programme in a development and in a production
// configuration, warns about a reward that waits too long, and shows only switches and
// counts.
func TestReferralReadinessInBothConfigurations(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"development": func(c *config.Config) { c.Referral.MaxRewardGiB = 20 },
		"production": func(c *config.Config) {
			c.Env, c.CookieSecure, c.SSLMode, c.TestPurchase = "production", true, "verify-full", false
			c.Payment.TestPrice, c.Payment.TestEmails = false, nil
			c.TrustedProxies = []string{"10.0.0.1"}
			c.Referral.MaxRewardGiB = 20
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRefEnv(t, mutate)
			a := e.user("rdy", "user")
			code := e.codeOf(a)
			e.register(newMailbox(), code)
			row := e.qualified(a.ID, gibBytes)
			inspect := func() (operations.ReadinessReport, string) {
				t.Helper()
				report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(report)
				return report, string(raw)
			}
			report, raw := inspect()
			if report.Referral != (operations.ReferralReadiness{Enabled: true, MaxRewardGiB: 20, Registered: 1, Qualified: 1}) || slices.Contains(report.Warnings, "referral_rewards_stale") {
				t.Fatalf("%s: %+v %v", name, report.Referral, report.Warnings)
			}
			if (report.Referral.Enabled) != e.cfg.ReferralEnabled() || (name == "production") != (e.cfg.Env == "production") {
				t.Fatalf("configuration mix-up in %s", name)
			}
			// A reward that has waited too long is reported, once, with its count.
			e.db.Model(&model.Referral{}).Where("id = ?", row.ID).Update("qualified_at", e.clock.Now().Add(-operations.ReferralStaleAfter-1))
			report, raw = inspect()
			if report.Referral.StaleQualified != 1 || !slices.Contains(report.Warnings, "referral_rewards_stale") {
				t.Fatalf("%s: stale reward not reported %+v %v", name, report.Referral, report.Warnings)
			}
			// Nothing but switches and counts: no address, no code, no id of anyone.
			var users []model.User
			e.db.Find(&users)
			leaks := []string{code, "@gmail.com", "@example"}
			for _, u := range users {
				leaks = append(leaks, u.ID, u.Email)
			}
			for _, leak := range leaks {
				if strings.Contains(raw, leak) {
					t.Fatalf("%s: the report carries %q", name, leak)
				}
			}
		})
	}
}
