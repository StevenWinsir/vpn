package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/payment"
	"vpn/backend/internal/referral"
)

// Round 1 of handoff_coupon.md: the referral backend (R1-02 … R1-11; R1-01 is in the
// store package, the pure rules in internal/referral and internal/billing).

const gibBytes = int64(1) << 30

// refEnv is a payment environment whose router also registers accounts through the
// real mailbox proof (capture mailer, test clock).
type refEnv struct {
	*payEnv
	mail *mailHarness
	ips  atomic.Int64
}

func newRefEnv(t *testing.T, mutate func(*config.Config)) *refEnv {
	t.Helper()
	e := newPayEnv(t, func(c *config.Config) {
		*c = withTestMail(*c)
		c.BcryptCost = 4
		if mutate != nil {
			mutate(c)
		}
	})
	h := newMailHarness(e.db, e.cfg, e.clock.Now)
	h.service.Limits.IPPerHour, h.service.Limits.IPPerDay, h.service.Limits.GlobalPerMinute = 1<<20, 1<<20, 1<<20
	e.router = newServerAt(e.db, e.cfg, h.service, e.clock.Now)
	// A plan at the requirement's own example price: 29.00 yuan.
	plan := model.Plan{ID: "e2e29", Name: "推荐测试计划", PriceCents: 2900, DurationDays: 30, TrafficBytes: 50 * gibBytes, MaxDevices: 2, Active: true, Sort: 9,
		YearlyEnabled: true, YearlyDurationDays: 365, Version: 1, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	if err := e.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	return &refEnv{payEnv: e, mail: h}
}

// post sends a JSON request from its own client address (so per-address limits of the
// mailbox proof never interfere).
func (e *refEnv) post(path string, body any) *httptest.ResponseRecorder {
	var data []byte
	if s, ok := body.(string); ok {
		data = []byte(s)
	} else {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", e.cfg.Origins[0])
	n := e.ips.Add(1)
	r.RemoteAddr = fmt.Sprintf("198.18.%d.%d:40000", n/250, n%250+1)
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w
}

// registration returns a ready /auth/register body for a fresh mailbox; extra fields
// (referral_code) are added by the caller.
func (e *refEnv) registration(email string) map[string]any {
	e.t.Helper()
	w := e.post("/api/v1/auth/register-code", map[string]string{"email": email})
	if w.Code != 202 {
		e.t.Fatalf("register-code %d %s", w.Code, w.Body.String())
	}
	var receipt struct {
		VerificationID string `json:"verification_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	return map[string]any{"name": "推荐测试", "email": email, "password": payPassword, "remember": false, "verification_id": receipt.VerificationID, "code": e.mail.latestCode(e.t, email)}
}

// register creates an account with an optional referral code (nil: field absent).
func (e *refEnv) register(email string, code any) (payUser, *httptest.ResponseRecorder) {
	e.t.Helper()
	body := e.registration(email)
	if code != nil {
		body["referral_code"] = code
	}
	w := e.post("/api/v1/auth/register", body)
	if w.Code != 201 {
		e.t.Fatalf("register %d %s", w.Code, w.Body.String())
	}
	var u model.User
	if err := e.db.First(&u, "email = ?", email).Error; err != nil {
		e.t.Fatal(err)
	}
	return payUser{User: u, cookies: w.Result().Cookies()}, w
}

func (e *refEnv) get(u payUser, path string) map[string]any {
	e.t.Helper()
	return e.want(e.call(u.cookies, "GET", path, nil, ""), 200)
}

// codeOf opens the referral page as u and returns the code.
func (e *refEnv) codeOf(u payUser) string {
	e.t.Helper()
	code, _ := e.get(u, "/api/v1/me/referral")["code"].(string)
	if !referral.Valid(code) {
		e.t.Fatalf("code %q", code)
	}
	return code
}

// buy pays one order of plan through the fake gateway.
func (e *refEnv) buy(u payUser, plan, coupon string) model.Order {
	e.t.Helper()
	p := e.place(u, plan, payment.TypeWeChat, coupon)
	o, status := e.gw.open(e.t, p.PayURL)
	if status != 200 {
		e.t.Fatalf("gateway %d", status)
	}
	e.want(e.pay(o), 200)
	order := e.order(p.Order.ID)
	if order.Status != model.OrderPaid {
		e.t.Fatalf("order %s", order.Status)
	}
	return order
}

// buyYearly pays one yearly order of plan through the fake gateway.
func (e *refEnv) buyYearly(u payUser, plan string) {
	e.t.Helper()
	w := e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": plan, "pay_type": payment.TypeWeChat, "billing_cycle": "yearly"}, uuid.NewString())
	e.want(w, 201)
	var p placed
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		e.t.Fatal(err)
	}
	o, status := e.gw.open(e.t, p.PayURL)
	if status != 200 {
		e.t.Fatalf("gateway %d", status)
	}
	e.want(e.pay(o), 200)
}

func (e *refEnv) referralOf(refereeID string) *model.Referral {
	e.t.Helper()
	var r model.Referral
	err := e.db.First(&r, "referee_id = ?", refereeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return &r
}

func (e *refEnv) reload(r *model.Referral) model.Referral {
	e.t.Helper()
	var out model.Referral
	if err := e.db.First(&out, "id = ?", r.ID).Error; err != nil {
		e.t.Fatal(err)
	}
	return out
}

// settle runs the reward worker until nothing is left to do now.
func (e *refEnv) settle() int {
	e.t.Helper()
	total := 0
	for {
		n, err := e.server().processReferrals(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if n == 0 {
			return total
		}
		total += n
	}
}

func (e *refEnv) subscribe(userID string, sub model.Subscription) model.Subscription {
	e.t.Helper()
	sub.ID, sub.UserID = uuid.NewString(), userID
	if sub.PlanID == "" {
		sub.PlanID, sub.PlanName, sub.MaxDevices = "starter", "轻量计划", 2
	}
	sub.Version = 1
	if err := e.db.Create(&sub).Error; err != nil {
		e.t.Fatal(err)
	}
	return sub
}

// qualified writes a row ready for the reward worker.
func (e *refEnv) qualified(referrerID string, reward int64) model.Referral {
	e.t.Helper()
	now := e.clock.Now()
	r := model.Referral{ID: uuid.NewString(), ReferrerID: referrerID, RefereeEmailMasked: "te***@example.com", CodeSnapshot: "ABCD2345", Status: model.ReferralQualified,
		CreatedAt: now, QualifiedAt: &now, FirstOrderCents: 2900, RewardBytes: reward, NextAttemptAt: &now, UpdatedAt: now}
	if err := e.db.Create(&r).Error; err != nil {
		e.t.Fatal(err)
	}
	return r
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// R1-05/R1-06, the requirement's own example: B registers with A's code and pays 29.00
// yuan; A, who has a valid plan, gains 29 GB once, and B's later purchases add nothing.
func TestReferralRequirementExample(t *testing.T) {
	e := newRefEnv(t, nil)
	a := e.user("alice", "user")
	e.buy(a, "starter", "")
	before := e.subscription(a.ID)
	if before.TrafficLimitBytes != 100*gibBytes {
		t.Fatalf("referrer allowance %d", before.TrafficLimitBytes)
	}
	// The node ledger of the referrer's current period exists (a grant was issued).
	period := model.SubscriptionPeriod{ID: uuid.NewString(), UserID: a.ID, SubscriptionID: before.ID, StartsAt: before.StartsAt, ExpiresAt: before.ExpiresAt, LimitBytes: before.TrafficLimitBytes}
	if err := e.db.Create(&period).Error; err != nil {
		t.Fatal(err)
	}
	code := e.codeOf(a)
	if again := e.codeOf(a); again != code {
		t.Fatalf("code changed %s -> %s", code, again)
	}
	b, _ := e.register("bob.example@gmail.com", strings.ToLower(code[:4])+"-"+code[4:])
	row := e.referralOf(b.ID)
	if row == nil || row.ReferrerID != a.ID || row.Status != model.ReferralRegistered || row.RefereeEmailMasked != "bo***@gmail.com" || row.CodeSnapshot != code {
		t.Fatalf("binding %+v", row)
	}
	items := e.get(a, "/api/v1/me/referrals")["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["email_masked"] != "bo***@gmail.com" || items[0].(map[string]any)["status"] != "registered" || items[0].(map[string]any)["reward_bytes"] != nil {
		t.Fatalf("referrer list %v", items)
	}

	first := e.buy(b, "e2e29", "")
	got := e.reload(row)
	if got.Status != model.ReferralQualified || got.FirstOrderID == nil || *got.FirstOrderID != first.ID || got.FirstOrderCents != 2900 || got.RewardBytes != 29*gibBytes || got.NextAttemptAt == nil {
		t.Fatalf("qualification %+v", got)
	}
	if e.subscription(a.ID).TrafficLimitBytes != 100*gibBytes {
		t.Fatal("the reward must not be granted inside the buyer's payment")
	}
	if n := e.settle(); n != 1 {
		t.Fatalf("settled %d", n)
	}
	got = e.reload(row)
	after := e.subscription(a.ID)
	if got.Status != model.ReferralRewarded || got.GrantedBytes != 29*gibBytes || got.GrantedAt == nil || got.GrantedSubscriptionID == nil || *got.GrantedSubscriptionID != before.ID || got.GrantedPlanName != "轻量计划" {
		t.Fatalf("reward %+v", got)
	}
	if after.TrafficLimitBytes != 129*gibBytes || after.BonusBytes != 29*gibBytes || after.BonusStartsAt == nil || !after.BonusStartsAt.Equal(before.StartsAt) ||
		after.Version != before.Version+1 || after.PlanID != before.PlanID || !after.StartsAt.Equal(before.StartsAt) || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("referrer subscription %+v (before %+v)", after, before)
	}
	if nativeEntitlementFingerprint(after) != nativeEntitlementFingerprint(before) {
		t.Fatal("the reward changed the client entitlement fingerprint (subscription_changed)")
	}
	if err := e.db.First(&period, "id = ?", period.ID).Error; err != nil || period.LimitBytes != 129*gibBytes {
		t.Fatalf("period ledger %d %v", period.LimitBytes, err)
	}
	summary := e.get(a, "/api/v1/me/referral")
	stats := summary["stats"].(map[string]any)
	if stats["registered_count"] != float64(1) || stats["purchased_count"] != float64(1) || stats["rewarded_count"] != float64(1) || stats["rewarded_bytes"] != float64(29*gibBytes) || stats["pending_count"] != float64(0) ||
		summary["current_bonus_bytes"] != float64(29*gibBytes) || summary["has_active_plan"] != true || summary["share_path"] != "/register?ref="+code {
		t.Fatalf("summary %v", summary)
	}
	if dash := e.get(a, "/api/v1/me/dashboard"); dash["bonus_bytes"] != float64(29*gibBytes) {
		t.Fatalf("dashboard bonus %v", dash["bonus_bytes"])
	}

	// Only the first purchase counts.
	e.buy(b, "e2e29", "")
	e.buy(b, "e2e29", "")
	if n := e.settle(); n != 0 {
		t.Fatalf("a later purchase was rewarded (%d)", n)
	}
	if again := e.reload(row); again.Status != model.ReferralRewarded || again.GrantedBytes != 29*gibBytes || *again.FirstOrderID != first.ID {
		t.Fatalf("row moved %+v", again)
	}
	if e.subscription(a.ID).TrafficLimitBytes != 129*gibBytes {
		t.Fatal("allowance moved after a later purchase")
	}
}

// R1-03, R1-11: whatever is entered in the referral field, registration answers the
// same way; only a correct code of an active account creates a binding.
func TestReferralRegistrationIsSilent(t *testing.T) {
	e := newRefEnv(t, nil)
	a := e.user("anna", "user")
	code := e.codeOf(a)
	deleting := e.user("dora", "user")
	deletingCode := e.codeOf(deleting)
	e.db.Model(&model.User{}).Where("id = ?", deleting.ID).Update("status", model.UserDeleting)
	disabled := e.user("dave", "user")
	disabledCode := e.codeOf(disabled)
	e.db.Model(&model.User{}).Where("id = ?", disabled.ID).Update("status", "disabled")
	unused := "ZZZZZZZZ"
	if unused == code {
		unused = "YYYYYYYY"
	}

	shape := func(w *httptest.ResponseRecorder) string {
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		user, _ := out["user"].(map[string]any)
		return strings.Join(keysOf(out), ",") + "|" + strings.Join(keysOf(user), ",")
	}
	_, base := e.register(newMailbox(), nil)
	want := shape(base)
	if strings.Contains(base.Body.String(), "referral") {
		t.Fatalf("registration response mentions referrals: %s", base.Body.String())
	}
	for _, tc := range []struct {
		name  string
		input any
		bound bool
	}{
		{"empty", "", false},
		{"garbage", "!!not a code!!", false},
		{"seven characters", code[:7], false},
		{"ambiguous characters", "O0I1LO0I", false},
		{"unknown code", unused, false},
		{"over 64 characters", strings.Repeat(" ", 60) + code, false},
		{"referrer being deleted", deletingCode, false},
		{"referrer disabled", disabledCode, false},
		{"valid, lower case with spaces", " " + strings.ToLower(code[:4]) + " " + strings.ToLower(code[4:]) + " ", true},
	} {
		u, w := e.register(newMailbox(), tc.input)
		if got := shape(w); got != want {
			t.Fatalf("%s: response shape %s, want %s", tc.name, got, want)
		}
		if row := e.referralOf(u.ID); (row != nil) != tc.bound {
			t.Fatalf("%s: binding %+v", tc.name, row)
		}
	}
	// Any other unknown field is still refused before anything happens.
	body := e.registration(newMailbox())
	body["referrer_id"] = a.ID
	if w := e.post("/api/v1/auth/register", body); w.Code != 400 {
		t.Fatalf("unknown field %d", w.Code)
	}
	// A wrong type for the field is a malformed request, as for every other field.
	body = e.registration(newMailbox())
	body["referral_code"] = 12345678
	if w := e.post("/api/v1/auth/register", body); w.Code != 400 {
		t.Fatalf("numeric referral code %d", w.Code)
	}

	// Switched off: the field is ignored, /meta says so.
	off := newRefEnv(t, func(c *config.Config) { c.Referral.Disabled = true })
	owner := off.user("olga", "user")
	offCode := off.codeOf(owner)
	u, _ := off.register(newMailbox(), offCode)
	if off.referralOf(u.ID) != nil {
		t.Fatal("a code was bound while the programme is off")
	}
	meta := off.want(off.call(nil, "GET", "/api/v1/meta", nil, ""), 200)
	if meta["referral_enabled"] != false {
		t.Fatalf("meta %v", meta["referral_enabled"])
	}
	if meta = e.want(e.call(nil, "GET", "/api/v1/meta", nil, ""), 200); meta["referral_enabled"] != true {
		t.Fatalf("meta %v", meta["referral_enabled"])
	}
}

// statementLog records the SQL of every statement the database runs while on.
type statementLog struct {
	mu   sync.Mutex
	on   bool
	sqls []string
	// failReferral makes the binding statement fail (fault injection).
	failReferral bool
	// failSubscription makes subscription writes fail.
	failSubscription bool
}

var errInjected = errors.New("injected failure")

func (l *statementLog) install(t *testing.T, db *gorm.DB) {
	t.Helper()
	record := func(tx *gorm.DB) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.on {
			l.sqls = append(l.sqls, tx.Statement.SQL.String())
		}
	}
	inject := func(tx *gorm.DB) {
		l.mu.Lock()
		defer l.mu.Unlock()
		sql := tx.Statement.SQL.String()
		if l.failReferral && strings.Contains(sql, "INSERT INTO") && strings.Contains(sql, ".referrals") {
			_ = tx.AddError(errInjected)
		}
	}
	injectUpdate := func(tx *gorm.DB) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.failSubscription && tx.Statement.Table == "subscriptions" {
			_ = tx.AddError(errInjected)
		}
	}
	cb := db.Callback()
	for _, err := range []error{
		cb.Raw().Before("gorm:raw").Register("referral_test:inject", inject),
		cb.Update().Before("gorm:update").Register("referral_test:inject_update", injectUpdate),
		cb.Create().After("gorm:create").Register("referral_test:create", record),
		cb.Query().After("gorm:query").Register("referral_test:query", record),
		cb.Update().After("gorm:update").Register("referral_test:update", record),
		cb.Delete().After("gorm:delete").Register("referral_test:delete", record),
		cb.Row().After("gorm:row").Register("referral_test:row", record),
		cb.Raw().After("gorm:raw").Register("referral_test:raw", record),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (l *statementLog) capture(f func()) []string {
	l.mu.Lock()
	l.on, l.sqls = true, nil
	l.mu.Unlock()
	f()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = false
	return l.sqls
}

func (l *statementLog) set(f func(*statementLog)) {
	l.mu.Lock()
	f(l)
	l.mu.Unlock()
}

// R1-04: the binding adds exactly a savepoint and one indexed statement to registration,
// costs nothing measurable, and a failing binding never fails the registration.
func TestReferralRegistrationCostAndRobustness(t *testing.T) {
	e := newRefEnv(t, nil)
	log := &statementLog{}
	log.install(t, e.db)
	a := e.user("amy", "user")
	code := e.codeOf(a)

	count := func(code any) []string {
		body := e.registration(newMailbox())
		if code != nil {
			body["referral_code"] = code
		}
		var w *httptest.ResponseRecorder
		sqls := log.capture(func() { w = e.post("/api/v1/auth/register", body) })
		if w.Code != 201 {
			t.Fatalf("register %d %s", w.Code, w.Body.String())
		}
		return sqls
	}
	plain, referred := count(nil), count(code)
	if len(referred) != len(plain)+2 {
		t.Fatalf("binding added %d statements, want 2 (savepoint + insert):\nplain %v\nreferred %v", len(referred)-len(plain), plain, referred)
	}
	extra := 0
	for _, sql := range referred {
		if strings.Contains(sql, "SAVEPOINT referral") || strings.Contains(sql, ".referrals") {
			extra++
		}
	}
	if extra != 2 {
		t.Fatalf("unexpected referral statements %v", referred)
	}
	// An invalid code does not reach the database at all.
	if invalid := count("not-a-code"); len(invalid) != len(plain) {
		t.Fatalf("an invalid code cost %d statements", len(invalid)-len(plain))
	}

	// The referrer lookup is an index scan.
	var plan []string
	if err := e.db.Raw(`EXPLAIN SELECT u.id FROM "`+e.cfg.Schema+`".users AS u WHERE u.referral_code = ? AND u.status = 'active'`, code).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "Index Scan") || !strings.Contains(joined, "referral_code") {
		t.Fatalf("referrer lookup plan:\n%s", joined)
	}

	// Fault injection: the binding statement fails; the account and its session exist,
	// there is no binding and the answer is the ordinary 201.
	log.set(func(l *statementLog) { l.failReferral = true })
	u, w := e.register(newMailbox(), code)
	log.set(func(l *statementLog) { l.failReferral = false })
	if e.referralOf(u.ID) != nil || len(w.Result().Cookies()) == 0 {
		t.Fatalf("failed binding left a row or no session")
	}
	if me := e.want(e.call(u.cookies, "GET", "/api/v1/auth/me", nil, ""), 200); me["user"] == nil {
		t.Fatal("the new account cannot sign in")
	}

	if testing.Short() {
		return
	}
	// Timing: 100 registrations each way, interleaved. The proof and bcrypt (cost 4 here)
	// dominate; the binding must not add anything measurable.
	var with, without []time.Duration
	for i := range 200 {
		body := e.registration(newMailbox())
		if i%2 == 1 {
			body["referral_code"] = code
		}
		start := time.Now()
		w := e.post("/api/v1/auth/register", body)
		took := time.Since(start)
		if w.Code != 201 {
			t.Fatalf("register %d", w.Code)
		}
		if i%2 == 1 {
			with = append(with, took)
		} else {
			without = append(without, took)
		}
	}
	pct := func(d []time.Duration, p int) time.Duration {
		slices.Sort(d)
		return d[(len(d)-1)*p/100]
	}
	t.Logf("registration p50 %v / %v, p95 %v / %v (without / with referral code, n=100 each)", pct(without, 50), pct(with, 50), pct(without, 95), pct(with, 95))
	if diff := pct(with, 50) - pct(without, 50); diff > 5*time.Millisecond {
		t.Fatalf("the referral binding adds %v to the median registration", diff)
	}
}

// R1-02: lazy code creation, retries on a collision, a clear failure after five, and a
// single code for concurrent first visits.
func TestReferralCodeCreation(t *testing.T) {
	e := newRefEnv(t, nil)
	a, b := e.user("ada", "user"), e.user("ben", "user")
	var user model.User
	e.db.First(&user, "id = ?", a.ID)
	if user.ReferralCode != nil {
		t.Fatal("a code exists before the page was opened")
	}
	if data, _ := json.Marshal(user); strings.Contains(string(data), "referral") {
		t.Fatalf("the user model serializes its code: %s", data)
	}
	taken := e.codeOf(a)
	s := e.server()
	draws := []string{taken, taken, "QQQQ2222"}
	s.referralCodes = func() (string, error) {
		next := draws[0]
		draws = draws[1:]
		return next, nil
	}
	code, err := s.ensureReferralCode(e.db, b.ID)
	if err != nil || code != "QQQQ2222" || len(draws) != 0 {
		t.Fatalf("collision retry: %q %v (left %v)", code, err, draws)
	}
	c := e.user("cid", "user")
	s.referralCodes = func() (string, error) { return taken, nil }
	if _, err = s.ensureReferralCode(e.db, c.ID); !errors.Is(err, errReferralUnavailable) {
		t.Fatalf("five collisions: %v", err)
	}
	// The endpoint reports the same as 503 referral_unavailable; the router's own
	// generator then succeeds on the next visit.
	if code = e.codeOf(c); code == taken {
		t.Fatal("duplicate code")
	}

	d := e.user("dan", "user")
	codes := make([]string, 8)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := e.call(d.cookies, "GET", "/api/v1/me/referral", nil, "")
			var out struct{ Code string }
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			codes[i] = out.Code
		}()
	}
	wg.Wait()
	for _, got := range codes {
		if got != codes[0] || !referral.Valid(got) {
			t.Fatalf("concurrent first visits returned %v", codes)
		}
	}
}

// R1-05: what counts as the first consumption and how much it is worth.
func TestReferralQualification(t *testing.T) {
	e := newRefEnv(t, func(c *config.Config) { c.TestPurchase = true })
	a := e.user("ava", "user")
	code := e.codeOf(a)

	// The amount is what was paid after the coupon.
	e.coupon(model.Coupon{Code: "REFTEN", DiscountType: model.DiscountPercent, PercentOff: 10, Enabled: true, BillingCycles: "all"})
	b, _ := e.register(newMailbox(), code)
	e.buy(b, "e2e29", "REFTEN")
	if r := e.referralOf(b.ID); r.Status != model.ReferralQualified || r.FirstOrderCents != 2610 || r.RewardBytes != 2610*gibBytes/100 {
		t.Fatalf("coupon order %+v", r)
	}

	// A test purchase is no consumption.
	c, _ := e.register(newMailbox(), code)
	w := e.call(c.cookies, "POST", "/api/v1/orders/test-purchase", map[string]any{"plan_id": "starter"}, uuid.NewString())
	e.want(w, 201)
	if r := e.referralOf(c.ID); r.Status != model.ReferralRegistered {
		t.Fatalf("test purchase qualified %+v", r)
	}

	// An order under review qualifies when the administrator confirms it.
	admin := e.user("root", "admin")
	d, _ := e.register(newMailbox(), code)
	p := e.place(d, "starter", payment.TypeWeChat, "")
	o, _ := e.gw.open(t, p.PayURL)
	e.gw.markPaid(o)
	e.want(e.notify(gatewayIP, fmt.Sprintf(`{"trade_no":%q,"name":%q,"money":0.01,"trade_status":%q}`, o.TradeNo, o.Name, payment.TradeSuccess)), 200)
	if e.order(p.Order.ID).Status != model.OrderReview || e.referralOf(d.ID).Status != model.ReferralRegistered {
		t.Fatal("an order under review qualified")
	}
	e.want(e.call(admin.cookies, "POST", "/api/v1/admin/console/orders/"+p.Order.ID+"/confirm", map[string]any{"admin_password": payPassword, "paid_cents": 1990, "note": "商户后台已核实"}, ""), 200)
	if r := e.referralOf(d.ID); r.Status != model.ReferralQualified || r.FirstOrderCents != 1990 || *r.FirstOrderID != p.Order.ID {
		t.Fatalf("confirmed order %+v", r)
	}

	// Two orders paid at once: one qualification, worth the order it names.
	f, _ := e.register(newMailbox(), code)
	p1, p2 := e.place(f, "starter", payment.TypeWeChat, ""), e.place(f, "starter", payment.TypeWeChat, "")
	o1, _ := e.gw.open(t, p1.PayURL)
	o2, _ := e.gw.open(t, p2.PayURL)
	var wg sync.WaitGroup
	for _, o := range []*fakeGatewayOrder{o1, o2} {
		wg.Add(1)
		go func() { defer wg.Done(); e.pay(o) }()
	}
	wg.Wait()
	r := e.referralOf(f.ID)
	if e.order(p1.Order.ID).Status != model.OrderPaid || e.order(p2.Order.ID).Status != model.OrderPaid || r.Status != model.ReferralQualified {
		t.Fatalf("concurrent payments: %+v", r)
	}
	named := e.order(*r.FirstOrderID)
	if named.UserID != f.ID || r.FirstOrderCents != named.AmountCents {
		t.Fatalf("qualification names %+v for %+v", named, r)
	}

	// The test price is real money: 0.01 yuan is worth 10.24 MB.
	tp := newRefEnv(t, func(c *config.Config) { c.Payment.TestPrice = true })
	owner := tp.user("tess", "user")
	g, _ := tp.register(newMailbox(), tp.codeOf(owner))
	tp.buy(g, "e2e29", "")
	if r := tp.referralOf(g.ID); r.FirstOrderCents != 1 || r.RewardBytes != gibBytes/100 {
		t.Fatalf("test price order %+v", r)
	}

	// The cap applies when the reward is computed.
	capped := newRefEnv(t, func(c *config.Config) { c.Referral.MaxRewardGiB = 10 })
	boss := capped.user("cap", "user")
	h, _ := capped.register(newMailbox(), capped.codeOf(boss))
	capped.buy(h, "e2e29", "")
	if r := capped.referralOf(h.ID); r.RewardBytes != 10*gibBytes {
		t.Fatalf("capped reward %d", r.RewardBytes)
	}
}

// R1-06: the referrer must hold a valid plan when the reward is settled (D7, D8).
func TestReferralGrantRules(t *testing.T) {
	e := newRefEnv(t, nil)
	now := e.clock.Now()

	// No plan: forfeited for good, even after buying one.
	none := e.user("nina", "user")
	r1 := e.qualified(none.ID, 29*gibBytes)
	e.settle()
	if got := e.reload(&r1); got.Status != model.ReferralForfeited || got.ForfeitReason != model.ForfeitNoPlan || got.NextAttemptAt != nil {
		t.Fatalf("no plan %+v", got)
	}
	e.subscribe(none.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: 100 * gibBytes})
	e.settle()
	if got := e.reload(&r1); got.Status != model.ReferralForfeited || e.subscription(none.ID).TrafficLimitBytes != 100*gibBytes {
		t.Fatal("a forfeited reward was granted later")
	}

	// Expired plan.
	old := e.user("olaf", "user")
	e.subscribe(old.ID, model.Subscription{StartsAt: now.AddDate(0, 0, -40), ExpiresAt: now.AddDate(0, 0, -10), TrafficLimitBytes: 100 * gibBytes})
	r2 := e.qualified(old.ID, 29*gibBytes)

	// Account being deleted.
	gone := e.user("gail", "user")
	e.subscribe(gone.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: 100 * gibBytes})
	e.db.Model(&model.User{}).Where("id = ?", gone.ID).Update("status", model.UserDeleting)
	r3 := e.qualified(gone.ID, 29*gibBytes)

	// Traffic used up but inside the validity window: the reward revives the account.
	dry := e.user("drew", "user")
	e.subscribe(dry.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: 100 * gibBytes, UsedUnits: 100 * gibBytes * 1000})
	if billing.Eligible(e.subscription(dry.ID), now, false) {
		t.Fatal("fixture: exhausted subscription is eligible")
	}
	r4 := e.qualified(dry.ID, 29*gibBytes)

	// A test subscription where test entitlements are not allowed.
	tester := e.user("toby", "user")
	e.subscribe(tester.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: 100 * gibBytes, IsTest: true})
	r5 := e.qualified(tester.ID, 29*gibBytes)

	// Near the 1 PiB ceiling the grant is clipped and the row records what was given.
	big := e.user("bigg", "user")
	e.subscribe(big.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: maxPlanBytes - 5*gibBytes})
	r6 := e.qualified(big.ID, 29*gibBytes)

	e.settle()
	for _, tc := range []struct {
		row            model.Referral
		status, reason string
		granted        int64
	}{
		{r2, model.ReferralForfeited, model.ForfeitNoPlan, 0},
		{r3, model.ReferralForfeited, model.ForfeitUnavailable, 0},
		{r4, model.ReferralRewarded, "", 29 * gibBytes},
		{r5, model.ReferralForfeited, model.ForfeitNoPlan, 0},
		{r6, model.ReferralRewarded, "", 5 * gibBytes},
	} {
		got := e.reload(&tc.row)
		if got.Status != tc.status || got.ForfeitReason != tc.reason || got.GrantedBytes != tc.granted {
			t.Fatalf("row of %s: %+v", tc.row.ReferrerID, got)
		}
	}
	if sub := e.subscription(dry.ID); !billing.Eligible(sub, now, false) || sub.TrafficLimitBytes != 129*gibBytes {
		t.Fatalf("the reward did not revive the exhausted plan: %+v", sub)
	}
	if sub := e.subscription(big.ID); sub.TrafficLimitBytes != maxPlanBytes || sub.BonusBytes != 5*gibBytes {
		t.Fatalf("clipped grant %+v", sub)
	}
	if e.subscription(tester.ID).TrafficLimitBytes != 100*gibBytes || e.subscription(old.ID).TrafficLimitBytes != 100*gibBytes {
		t.Fatal("a forfeited reward changed an allowance")
	}

	// Where test entitlements are allowed, a test subscription counts.
	dev := newRefEnv(t, func(c *config.Config) { c.TestPurchase = true })
	devUser := dev.user("devi", "user")
	dev.subscribe(devUser.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: 100 * gibBytes, IsTest: true})
	r7 := dev.qualified(devUser.ID, gibBytes)
	dev.settle()
	if got := dev.reload(&r7); got.Status != model.ReferralRewarded {
		t.Fatalf("development test subscription %+v", got)
	}
}

// R1-07: a yearly (refreshing) referrer keeps the reward across the 30-day refresh; a
// new period drops it; an administrator's edit clips it.
func TestReferralYearlyReferrer(t *testing.T) {
	e := newRefEnv(t, nil)
	a := e.user("yuri", "user")
	e.buyYearly(a, "starter")
	sub := e.subscription(a.ID)
	if !billing.QuotaCycleActive(sub) || sub.TrafficLimitBytes != 100*gibBytes {
		t.Fatalf("yearly fixture %+v", sub)
	}
	e.qualified(a.ID, 29*gibBytes)
	e.settle()
	if sub = e.subscription(a.ID); sub.TrafficLimitBytes != 129*gibBytes || sub.BonusBytes != 29*gibBytes {
		t.Fatalf("yearly grant %+v", sub)
	}
	// 110 GiB used in the first cycle: 19 GiB of the reward carries into the second.
	e.db.Model(&model.Subscription{}).Where("id = ?", sub.ID).Update("used_units", 110*gibBytes*1000)
	e.clock.Advance(31 * 24 * time.Hour)
	if sub = e.subscription(a.ID); sub.TrafficLimitBytes != 119*gibBytes || sub.BonusBytes != 19*gibBytes || sub.UsedUnits != 0 {
		t.Fatalf("after the refresh %+v", sub)
	}
	summary := e.get(a, "/api/v1/me/referral")
	if summary["current_bonus_bytes"] != float64(19*gibBytes) {
		t.Fatalf("summary bonus %v", summary["current_bonus_bytes"])
	}

	// An administrator lowering the allowance below the bonus clips it.
	admin := e.user("root", "admin")
	limit := 10 * gibBytes
	w := e.call(admin.cookies, "POST", "/api/v1/admin/console/users/"+a.ID+"/subscription", map[string]any{"version": sub.Version, "traffic_limit_bytes": limit}, "")
	e.want(w, 200)
	var stored model.Subscription
	e.db.First(&stored, "id = ?", sub.ID)
	if stored.BonusBytes != limit {
		t.Fatalf("admin edit left bonus %d over a %d allowance", stored.BonusBytes, limit)
	}

	// A monthly referrer whose plan expires and is bought again starts without bonus.
	m := e.user("mona", "user")
	e.buy(m, "starter", "")
	e.qualified(m.ID, 29*gibBytes)
	e.settle()
	e.clock.Advance(31 * 24 * time.Hour)
	e.buy(m, "starter", "")
	if sub = e.subscription(m.ID); sub.TrafficLimitBytes != 100*gibBytes || billing.CurrentBonus(sub) != 0 || sub.BonusBytes != 0 {
		t.Fatalf("new period kept the bonus %+v", sub)
	}
	// A monthly subscription with a bonus renewed as yearly keeps the bonus over refreshes.
	y := e.user("yara", "user")
	e.buy(y, "starter", "")
	e.qualified(y.ID, 29*gibBytes)
	e.settle()
	e.buyYearly(y, "starter")
	e.db.Model(&model.Subscription{}).Where("user_id = ?", y.ID).Update("used_units", 0)
	e.clock.Advance(31 * 24 * time.Hour)
	if sub = e.subscription(y.ID); sub.BonusBytes != 29*gibBytes || sub.TrafficLimitBytes != 129*gibBytes {
		t.Fatalf("monthly bonus renewed as yearly %+v", sub)
	}
}

// R1-08: the worker settles each reward once across instances, honours its lease,
// backs off on errors and pauses while the programme is off.
func TestReferralWorker(t *testing.T) {
	if referralBackoff(1) != 30*time.Second || referralBackoff(2) != time.Minute || referralBackoff(3) != 2*time.Minute || referralBackoff(7) != 30*time.Minute || referralBackoff(100) != 30*time.Minute {
		t.Fatal("backoff schedule")
	}
	e := newRefEnv(t, nil)
	now := e.clock.Now()
	a := e.user("wade", "user")
	e.subscribe(a.ID, model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 29), TrafficLimitBytes: 100 * gibBytes})
	for range 30 {
		e.qualified(a.ID, gibBytes)
	}
	var wg sync.WaitGroup
	var settled atomic.Int64
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := e.server()
			for {
				n, err := s.processReferrals(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
				settled.Add(int64(n))
			}
		}()
	}
	wg.Wait()
	var rewarded int64
	e.db.Model(&model.Referral{}).Where("referrer_id = ? AND status = ? AND granted_bytes = ?", a.ID, model.ReferralRewarded, gibBytes).Count(&rewarded)
	if sub := e.subscription(a.ID); settled.Load() != 30 || rewarded != 30 || sub.TrafficLimitBytes != 130*gibBytes || sub.BonusBytes != 30*gibBytes || sub.Version != 31 {
		t.Fatalf("three workers: settled %d rewarded %d sub %+v", settled.Load(), rewarded, sub)
	}

	// A claim left by a crashed instance is honoured until its lease runs out.
	r := e.qualified(a.ID, gibBytes)
	lease := now.Add(referralClaimLease)
	e.db.Model(&model.Referral{}).Where("id = ?", r.ID).Update("next_attempt_at", lease)
	if n := e.settle(); n != 0 {
		t.Fatal("a leased row was taken")
	}
	e.clock.Advance(referralClaimLease + time.Second)
	if n := e.settle(); n != 1 {
		t.Fatal("an expired lease was not taken over")
	}

	// A database error schedules a retry with back-off and a category, not a message.
	log := &statementLog{}
	log.install(t, e.db)
	r = e.qualified(a.ID, gibBytes)
	log.set(func(l *statementLog) { l.failSubscription = true })
	e.settle()
	log.set(func(l *statementLog) { l.failSubscription = false })
	got := e.reload(&r)
	if got.Status != model.ReferralQualified || got.Attempts != 1 || got.LastErrorKind != "database" || got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(e.clock.Now().Add(30*time.Second)) {
		t.Fatalf("retry %+v", got)
	}
	if n := e.settle(); n != 0 {
		t.Fatal("retried before the back-off")
	}
	e.clock.Advance(31 * time.Second)
	e.settle()
	if got = e.reload(&r); got.Status != model.ReferralRewarded || got.LastErrorKind != "" {
		t.Fatalf("after the retry %+v", got)
	}

	// Switched off: first purchases are still recorded, rewards wait, then resume.
	off := newRefEnv(t, func(c *config.Config) { c.Referral.Disabled = true })
	owner := off.user("otto", "user")
	off.buy(owner, "starter", "")
	b := off.user("bert", "user")
	stamp := off.clock.Now()
	row := model.Referral{ID: uuid.NewString(), ReferrerID: owner.ID, RefereeID: &b.ID, RefereeEmailMasked: "be***@example.com", CodeSnapshot: off.codeOf(owner), Status: model.ReferralRegistered, CreatedAt: stamp, UpdatedAt: stamp}
	if err := off.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	off.buy(b, "e2e29", "")
	if got = off.reload(&row); got.Status != model.ReferralQualified {
		t.Fatalf("switched off: first purchase not recorded %+v", got)
	}
	if n := off.settle(); n != 0 || off.reload(&row).Status != model.ReferralQualified {
		t.Fatal("rewards were granted while the programme is off")
	}
	on := off.server()
	on.cfg.Referral.Disabled = false
	if n, err := on.processReferrals(context.Background()); n != 1 || err != nil {
		t.Fatalf("switched back on: %d %v", n, err)
	}
	if off.subscription(owner.ID).TrafficLimitBytes != 129*gibBytes {
		t.Fatal("reward after switching back on")
	}
}

// R1-09: first purchases fulfilled without the check (an older binary) are found by the
// sweep, which names the earliest paid order.
func TestReferralSweep(t *testing.T) {
	e := newRefEnv(t, nil)
	a := e.user("sven", "user")
	b, _ := e.register(newMailbox(), e.codeOf(a))
	now := e.clock.Now()
	paid := func(cents int64, at time.Time, provider string) string {
		id := uuid.NewString()
		o := model.Order{ID: id, UserID: b.ID, IdempotencyKey: uuid.NewString(), PlanID: "starter", PlanName: "轻量计划", PriceCents: cents, DurationDays: 30, TrafficBytes: 100 * gibBytes,
			Status: model.OrderPaid, Provider: provider, CreatedAt: at, PaidAt: &at, AmountCents: cents, PaidCents: &cents}
		if err := e.db.Create(&o).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	paid(500, now.Add(-3*time.Hour), "test")
	earliest := paid(1500, now.Add(-2*time.Hour), "gateway")
	paid(2900, now.Add(-time.Hour), "gateway")
	n, err := e.server().sweepReferrals(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("sweep %d %v", n, err)
	}
	r := e.referralOf(b.ID)
	if r.Status != model.ReferralQualified || *r.FirstOrderID != earliest || r.FirstOrderCents != 1500 {
		t.Fatalf("sweep qualified %+v", r)
	}
	if n, err = e.server().sweepReferrals(context.Background()); err != nil || n != 0 {
		t.Fatalf("second sweep %d %v", n, err)
	}
	// Exported entry points used by cmd/api.
	if _, err = SweepReferrals(context.Background(), e.db, e.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = ProcessReferrals(context.Background(), e.db, e.cfg); err != nil {
		t.Fatal(err)
	}
}

// R1-10: purging a referee keeps the referrer's record anonymous; purging a referrer
// removes its records and leaves the referees alone.
func TestReferralPurge(t *testing.T) {
	e := newRefEnv(t, nil)
	a := e.user("pete", "user")
	e.buy(a, "starter", "")
	b, _ := e.register("purged.person@qq.com", e.codeOf(a))
	e.buy(b, "e2e29", "")
	e.settle()
	c, _ := e.register(newMailbox(), e.codeOf(a))
	row := *e.referralOf(b.ID)

	mark := func(id string) {
		now := e.clock.Now()
		e.db.Model(&model.User{}).Where("id = ?", id).Updates(map[string]any{"status": model.UserDeleting, "deletion_requested_at": now})
	}
	mark(b.ID)
	if res, err := PurgeDeletedAccounts(context.Background(), e.db, e.cfg); err != nil || res.Purged != 1 {
		t.Fatalf("purge %+v %v", res, err)
	}
	got := e.reload(&row)
	if got.RefereeID != nil || got.FirstOrderID != nil || got.RefereeEmailMasked != "pu***@qq.com" || got.Status != model.ReferralRewarded || got.GrantedBytes != 29*gibBytes {
		t.Fatalf("referee purge %+v", got)
	}
	items := e.get(a, "/api/v1/me/referrals")["items"].([]any)
	deleted := 0
	for _, it := range items {
		if it.(map[string]any)["referee_deleted"] == true {
			deleted++
		}
	}
	if len(items) != 2 || deleted != 1 {
		t.Fatalf("list after purge %v", items)
	}

	mark(a.ID)
	if res, err := PurgeDeletedAccounts(context.Background(), e.db, e.cfg); err != nil || res.Purged != 1 {
		t.Fatalf("purge %+v %v", res, err)
	}
	var left int64
	e.db.Model(&model.Referral{}).Count(&left)
	var survivor model.User
	if left != 0 || e.db.First(&survivor, "id = ?", c.ID).Error != nil || survivor.Status != "active" {
		t.Fatalf("referrer purge left %d rows, referee %+v", left, survivor)
	}
}

// R1-11: the endpoints show only the whitelisted fields, page correctly and keep to
// their audiences.
func TestReferralEndpoints(t *testing.T) {
	e := newRefEnv(t, nil)
	if w := e.call(nil, "GET", "/api/v1/me/referral", nil, ""); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}
	a := e.user("liam", "user")
	code := e.codeOf(a)
	summary := e.get(a, "/api/v1/me/referral")
	if got := strings.Join(keysOf(summary), ","); got != "code,current_bonus_bytes,enabled,has_active_plan,reward_rule,share_path,stats" {
		t.Fatalf("summary fields %s", got)
	}
	if got := strings.Join(keysOf(summary["stats"].(map[string]any)), ","); got != "forfeited_count,pending_count,purchased_count,registered_count,rewarded_bytes,rewarded_count" {
		t.Fatalf("stats fields %s", got)
	}
	if rule := summary["reward_rule"].(map[string]any); rule["gib_per_yuan"] != float64(1) || rule["max_gib"] != float64(0) || summary["has_active_plan"] != false {
		t.Fatalf("rule %v", summary)
	}
	var ids []string
	for i := range 25 {
		u, _ := e.register(fmt.Sprintf("member%02d.x@gmail.com", i), code)
		ids = append(ids, u.ID)
		e.clock.Advance(time.Second)
	}
	page1 := e.get(a, "/api/v1/me/referrals")
	page2 := e.get(a, "/api/v1/me/referrals?page=2")
	items1, items2 := page1["items"].([]any), page2["items"].([]any)
	if len(items1) != 20 || len(items2) != 5 || page1["total"] != float64(25) || page2["page_size"] != float64(20) {
		t.Fatalf("pages %d %d %v", len(items1), len(items2), page1["total"])
	}
	first := items1[0].(map[string]any)
	if got := strings.Join(keysOf(first), ","); got != "email_masked,forfeit_reason,granted_at,granted_bytes,referee_deleted,registered_at,reward_bytes,status" {
		t.Fatalf("item fields %s", got)
	}
	if first["email_masked"] != "me***@gmail.com" || strings.Contains(page1Body(e, a), "member24") {
		t.Fatalf("newest first and masked: %v", first)
	}
	for _, id := range ids {
		if strings.Contains(page1Body(e, a), id) {
			t.Fatal("a referee id leaked")
		}
	}
	if w := e.call(a.cookies, "GET", "/api/v1/me/referrals?page=10001", nil, ""); w.Code != 400 {
		t.Fatalf("page bound %d", w.Code)
	}
	// The user's own JSON never carries the code.
	if me := e.call(a.cookies, "GET", "/api/v1/auth/me", nil, ""); strings.Contains(me.Body.String(), code) {
		t.Fatal("/auth/me leaks the referral code")
	}
	// The dashboard's subscription object keeps its exact shape.
	e.buy(a, "starter", "")
	dash := e.get(a, "/api/v1/me/dashboard")
	if strings.Contains(fmt.Sprint(keysOf(dash["subscription"].(map[string]any))), "bonus") || dash["bonus_bytes"] != float64(0) {
		t.Fatalf("dashboard %v", dash)
	}

	// Administrators see the full picture, ordinary users nothing.
	console := &consoleEnv{payEnv: e.payEnv, admin: e.user("root", "admin")}
	if w := e.call(a.cookies, "GET", "/api/v1/admin/console/users/"+a.ID+"/referrals", nil, ""); w.Code != 403 {
		t.Fatalf("ordinary user %d", w.Code)
	}
	detail := e.want(console.get("/users/"+a.ID+"/referrals"), 200)
	if detail["code"] != code || detail["referrer"] != nil || len(detail["items"].([]any)) != 25 || detail["items"].([]any)[0].(map[string]any)["referee_email"] != "member24.x@gmail.com" {
		t.Fatalf("admin view %v", detail)
	}
	referee := e.want(console.get("/users/"+ids[0]+"/referrals"), 200)
	if ref, _ := referee["referrer"].(map[string]any); ref == nil || ref["email"] != a.Email || referee["code"] != nil {
		t.Fatalf("admin view of a referee %v", referee)
	}
	// An administrator signed in as the user may read the page, but not the console.
	b := console.browser(console.admin.cookies)
	e.want(console.impersonate(b, a.ID), 200)
	if w := b.get("/me/referral"); w.Code != 200 || !strings.Contains(w.Body.String(), code) {
		t.Fatalf("impersonated read %d %s", w.Code, w.Body.String())
	}
	if w := b.get("/admin/console/users/" + a.ID + "/referrals"); w.Code != 403 {
		t.Fatalf("impersonated console %d", w.Code)
	}
}

func page1Body(e *refEnv, u payUser) string {
	return e.call(u.cookies, "GET", "/api/v1/me/referrals", nil, "").Body.String()
}

// Doctor (handoff_coupon.md 14.3): switches and counts only, and a warning for a reward
// that has waited longer than ten minutes.
func TestReferralReadiness(t *testing.T) {
	e := newRefEnv(t, func(c *config.Config) { c.Referral.MaxRewardGiB = 50 })
	inspect := func() operations.ReadinessReport {
		t.Helper()
		report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	a := e.user("dora", "user")
	code := e.codeOf(a)
	e.register(newMailbox(), code)
	r := e.qualified(a.ID, gibBytes)
	report := inspect()
	if !report.Referral.Enabled || report.Referral.MaxRewardGiB != 50 || report.Referral.Registered != 1 || report.Referral.Qualified != 1 || report.Referral.StaleQualified != 0 || slices.Contains(report.Warnings, "referral_rewards_stale") {
		t.Fatalf("referral readiness %+v %v", report.Referral, report.Warnings)
	}
	old := time.Now().UTC().Add(-operations.ReferralStaleAfter - time.Minute)
	e.db.Model(&model.Referral{}).Where("id = ?", r.ID).Update("qualified_at", old)
	report = inspect()
	if report.Referral.StaleQualified != 1 || !slices.Contains(report.Warnings, "referral_rewards_stale") {
		t.Fatalf("stale reward not reported %+v %v", report.Referral, report.Warnings)
	}
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), code) || strings.Contains(string(data), "@") {
		t.Fatalf("doctor output carries a code or an address: %s", data)
	}
}
