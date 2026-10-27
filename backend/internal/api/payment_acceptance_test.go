package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// shiftClock is real time plus an offset the browser test can move forward, so the gateway's
// 2-minute orders and the 3-minute local window can be crossed without waiting.
type shiftClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *shiftClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().UTC().Add(c.offset)
}

func (c *shiftClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

// TestPaymentAcceptanceFixture serves the real API with the payment gateway enabled against
// the fake gateway of payment_harness_test.go (the document's behaviour, reachable from a
// browser), plus control endpoints for the Playwright specs. It only runs under
// scripts/test-payment-web.py.
func TestPaymentAcceptanceFixture(t *testing.T) {
	dir := os.Getenv("PAYMENT_ACCEPTANCE_DIR")
	if dir == "" {
		t.Skip("only run with the private payment acceptance driver")
	}
	origin, err := url.Parse(os.Getenv("PAYMENT_WEB_ORIGIN"))
	if !strings.HasPrefix(dir, "/tmp/vpn-payment-") || err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.Path != "" {
		t.Fatal("private fixture directory and loopback origin required")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	clock := &shiftClock{}
	password, control := uuid.NewString(), uuid.NewString()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		t.Fatal(err)
	}
	emails := map[string]string{}
	// carol, dave and erin are for the account-settings specs (password change, deletion,
	// deletion blocked by a live order); root is an administrator, who cannot self-delete.
	// frank, gina and hank belong to the console spec (plan edit, deletion, manual confirmation);
	// ivan, judy, kyle, lena and boss (a second administrator) to the announcement, impersonation
	// and coupon specs, which publish announcements that must not reach the other specs' users.
	// themeroot (an administrator) and themea belong to the appearance spec (handoff_ui.md 6);
	// supportroot (an administrator), supporta and supportb to the support chat spec (handoff_ui.md 9).
	for _, kind := range []string{"alice", "bob", "tester", "carol", "dave", "erin", "frank", "gina", "hank", "ivan", "judy", "kyle", "lena", "boss", "root", "themeroot", "themea", "supportroot", "supporta", "supportb"} {
		role := "user"
		if kind == "root" || kind == "boss" || kind == "themeroot" || kind == "supportroot" {
			role = "admin"
		}
		u := model.User{ID: uuid.NewString(), Email: kind + "-" + uuid.NewString()[:8] + "@example.invalid", Name: "Pay " + kind, PasswordHash: string(hash), Role: role, Status: "active", AuthVersion: 1}
		if err = db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		emails[kind] = u.Email
	}
	// The shop spec (yearly checkout, renewal, price change, rebuy) and the referral spec
	// (refa: a referrer who buys a plan, refd: one who never has a plan) use accounts of their own,
	// as does the user-page copy spec (copya never buys, copyb buys a plan; handoff_ui.md 4.6) and the
	// home page spec (homea only signs in; handoff_ui.md 8).
	for _, kind := range []string{"shopa", "shopb", "shopc", "shopd", "refa", "refd", "copya", "copyb", "homea"} {
		u := model.User{ID: uuid.NewString(), Email: kind + "-" + uuid.NewString()[:8] + "@example.invalid", Name: "Pay " + kind, PasswordHash: string(hash), Role: "user", Status: "active", AuthVersion: 1}
		if err = db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		emails[kind] = u.Email
	}
	// The referral spec registers new accounts through the real code flow: mail goes to the
	// in-process capture Mailer and codes are read back only through the control-token endpoint.
	cfg = withTestMail(cfg)
	mail := newMailHarness(db, cfg, clock.Now)
	mail.worker.Poll = 200 * time.Millisecond
	mailCtx, stopMail := context.WithCancel(context.Background())
	mailDone := make(chan struct{})
	go func() { defer close(mailDone); mail.worker.Run(mailCtx) }()
	defer func() { stopMail(); <-mailDone }()
	gw := newFakeGateway(t, gatewayName, "test-only-gateway-secret", clock.Now)
	cfg.Origins = []string{origin.String()}
	cfg.WebsiteBaseURL = origin.String()
	cfg.Payment = config.Payment{Enabled: true, GatewayURL: gw.server.URL, Channel: gatewayName, Secret: "test-only-gateway-secret",
		NotifyURL: "https://api.pay-test.example/api/v1/payments/notify", NotifyAllowed: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		ToleranceCents: 50, MaxPending: 5, PendingWindow: 180 * time.Second,
		TestPrice: true, TestPriceCents: 1, TestEmails: []string{emails["tester"]}}
	now := clock.Now()
	past, future := now.Add(-24*time.Hour), now.Add(24*time.Hour)
	admin := uuid.NewString()
	for _, c := range []model.Coupon{
		{ID: uuid.NewString(), Code: "SAVE20", DiscountType: model.DiscountPercent, PercentOff: 20, PlanIDs: "[]", PerUserLimit: 1, Enabled: true},
		{ID: uuid.NewString(), Code: "ONLYMAX", DiscountType: model.DiscountPercent, PercentOff: 50, PlanIDs: `["max"]`, PerUserLimit: 1, Enabled: true},
		{ID: uuid.NewString(), Code: "USEDUP", DiscountType: model.DiscountAmount, AmountOffCents: 500, PlanIDs: "[]", MaxUses: 1, UsedCount: 1, PerUserLimit: 1, Enabled: true},
		{ID: uuid.NewString(), Code: "OLDCODE", DiscountType: model.DiscountAmount, AmountOffCents: 500, PlanIDs: "[]", EndsAt: &past, PerUserLimit: 1, Enabled: true},
		{ID: uuid.NewString(), Code: "FUTURE", DiscountType: model.DiscountAmount, AmountOffCents: 500, PlanIDs: "[]", StartsAt: &future, PerUserLimit: 1, Enabled: true},
	} {
		c.CreatedBy, c.Version, c.CreatedAt, c.UpdatedAt = admin, 1, now, now
		if err = db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := newServerAt(db, cfg, mail.service, clock.Now)
	stop := make(chan struct{})
	reconciler := &Server{db: db, cfg: cfg, clock: clock.Now}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_, _ = reconciler.reconcilePayments(t.Context())
				// The referral worker of cmd/api, driven on the fixture's clock.
				_, _ = reconciler.processReferrals(t.Context())
			}
		}
	}()
	defer close(stop)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/fixture/") {
			router.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Acceptance-Control") != control {
			w.WriteHeader(403)
			return
		}
		reply := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		switch r.URL.Path {
		case "/fixture/mail":
			msgs := mail.box.to(r.URL.Query().Get("to"))
			code, ok := "", false
			if len(msgs) > 0 {
				code, ok = codeIn(msgs[len(msgs)-1])
			}
			if !ok {
				w.WriteHeader(404)
				return
			}
			reply(map[string]any{"code": code, "count": len(msgs)})
		case "/fixture/advance":
			seconds, _ := strconv.Atoi(r.URL.Query().Get("seconds"))
			clock.Advance(time.Duration(seconds) * time.Second)
			w.WriteHeader(204)
		case "/fixture/orders":
			var rows []model.Order
			var owner model.User
			if e := db.First(&owner, "email = ?", r.URL.Query().Get("email")).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			if e := db.Where("user_id = ?", owner.ID).Order("created_at asc").Find(&rows).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			out := []map[string]any{}
			for _, o := range rows {
				trade := ""
				if o.TradeNo != nil {
					trade = *o.TradeNo
				}
				out = append(out, map[string]any{"id": o.ID, "trade_no": trade, "status": o.Status, "pay_type": o.PayType, "amount_cents": o.AmountCents, "paid_cents": o.PaidCents, "price_source": o.PriceSource, "coupon_code": o.CouponCode, "review_reason": o.ReviewReason})
			}
			reply(out)
		case "/fixture/subscription":
			var sub model.Subscription
			var owner model.User
			if e := db.First(&owner, "email = ?", r.URL.Query().Get("email")).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			if e := db.First(&sub, "user_id = ?", owner.ID).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			reply(map[string]any{"plan_id": sub.PlanID, "expires_at": sub.ExpiresAt, "traffic_limit_bytes": sub.TrafficLimitBytes, "is_test": sub.IsTest,
				"quota_cycle_days": sub.QuotaCycleDays, "quota_cycle_bytes": sub.QuotaCycleBytes, "quota_cycle_starts_at": sub.QuotaCycleStartsAt})
		case "/fixture/user":
			// Account state for the account specs: whether the row exists, its status and
			// how many rows of every account-owned table remain.
			var owner model.User
			email := r.URL.Query().Get("email")
			if e := db.First(&owner, "email = ?", email).Error; e != nil {
				reply(map[string]any{"exists": false})
				return
			}
			counts := map[string]int64{}
			for _, tbl := range accountTables {
				if tbl.name != "users" {
					var n int64
					db.Model(tbl.model).Where(tbl.where, owner.ID).Count(&n)
					counts[tbl.name] = n
				}
			}
			reply(map[string]any{"exists": true, "status": owner.Status, "auth_version": owner.AuthVersion, "rows": counts})
		case "/fixture/pending-order":
			// A live gateway order for the account, as if it had just been placed.
			var owner model.User
			if e := db.First(&owner, "email = ?", r.URL.Query().Get("email")).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			expires := clock.Now().Add(150 * time.Second)
			trade := clock.Now().Format("20060102") + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			order := model.Order{ID: uuid.NewString(), UserID: owner.ID, IdempotencyKey: uuid.NewString(), PlanID: "pro", PlanName: "进阶计划", PriceCents: 3990, DurationDays: 30, TrafficBytes: gib,
				Status: model.OrderPending, Provider: "gateway", CreatedAt: clock.Now(), TradeNo: &trade, PayType: payment.TypeWeChat, AmountCents: 3990, ListPriceCents: 3990, ExpiresAt: &expires}
			if e := db.Omit("User").Create(&order).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		case "/fixture/review-order":
			// An order waiting for an administrator, as after a callback from an unknown address.
			var owner model.User
			if e := db.First(&owner, "email = ?", r.URL.Query().Get("email")).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			trade := clock.Now().Format("20060102") + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			paid := int64(3990)
			order := model.Order{ID: uuid.NewString(), UserID: owner.ID, IdempotencyKey: uuid.NewString(), PlanID: "pro", PlanName: "进阶计划", PriceCents: 3990, DurationDays: 30, TrafficBytes: gib,
				Status: model.OrderReview, ReviewReason: model.ReviewUnverifiedSource, Provider: "gateway", CreatedAt: clock.Now(), TradeNo: &trade, PayType: payment.TypeWeChat, AmountCents: 3990, ListPriceCents: 3990,
				PaidCents: &paid, PriceSource: priceList}
			note := model.PaymentNotification{ID: uuid.NewString(), TradeNo: trade, OrderID: &order.ID, MoneyText: "39.90", TradeStatus: payment.TradeSuccess, Name: "进阶计划", SourceIP: "203.0.113.77", Result: model.NotifyReview, ReceivedAt: clock.Now()}
			if e := db.Omit("User").Create(&order).Error; e != nil || db.Create(&note).Error != nil {
				w.WriteHeader(500)
				return
			}
			reply(map[string]any{"id": order.ID, "trade_no": trade})
		case "/fixture/archive-announcements":
			// Cleanup for the announcement spec: a failed run must not leave notices that
			// block every later spec's pages.
			if e := db.Model(&model.Announcement{}).Where("status = ?", model.AnnouncementPublished).Update("status", model.AnnouncementArchived).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		case "/fixture/coupon-used":
			// A coupon an order has used: lena holds a paid order that references it.
			var coupon model.Coupon
			var owner model.User
			if e := db.First(&coupon, "code = ?", r.URL.Query().Get("code")).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			if e := db.First(&owner, "email = ?", r.URL.Query().Get("email")).Error; e != nil {
				w.WriteHeader(404)
				return
			}
			paid, trade := clock.Now(), clock.Now().Format("20060102")+strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			order := model.Order{ID: uuid.NewString(), UserID: owner.ID, IdempotencyKey: uuid.NewString(), PlanID: "pro", PlanName: "进阶计划", PriceCents: 3190, DurationDays: 30, TrafficBytes: gib,
				Status: model.OrderPaid, Provider: "gateway", CreatedAt: paid, PaidAt: &paid, TradeNo: &trade, PayType: payment.TypeWeChat, AmountCents: 3190, ListPriceCents: 3990, DiscountCents: 800,
				CouponID: &coupon.ID, CouponCode: coupon.Code, PriceSource: priceList}
			if e := db.Omit("User").Create(&order).Error; e != nil || db.Model(&coupon).Update("used_count", 1).Error != nil {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		case "/fixture/purge":
			res, e := (&Server{db: db, cfg: cfg, clock: clock.Now}).purgeDeletedAccounts(t.Context())
			if e != nil {
				w.WriteHeader(500)
				return
			}
			reply(map[string]any{"purged": res.Purged, "waiting": res.Waiting, "failed": len(res.Failed)})
		case "/fixture/pay":
			// The customer pays at the gateway and the gateway sends its callback to the
			// API from a loopback address (the fixture's allow-list). money overrides the
			// callback amount in cents to simulate a short payment.
			trade := r.URL.Query().Get("trade_no")
			gw.mu.Lock()
			o := gw.orders[trade]
			gw.mu.Unlock()
			if o == nil {
				w.WriteHeader(404)
				return
			}
			gw.markPaid(o)
			cents := o.Cents
			if v, e := strconv.ParseInt(r.URL.Query().Get("money"), 10, 64); e == nil {
				cents = v
			}
			body := fmt.Sprintf(`{"trade_no":%q,"name":%q,"money":%s,"trade_status":%q}`, o.TradeNo, o.Name, payment.FormatAmount(cents), payment.TradeSuccess)
			resp, e := http.Post(server.URL+"/api/v1/payments/notify", "application/json", strings.NewReader(body))
			if e != nil {
				w.WriteHeader(502)
				return
			}
			_ = resp.Body.Close()
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	metadata := map[string]any{"api": server.URL, "gateway": gw.server.URL, "password": password, "control": control, "emails": emails}
	data, _ := json.Marshal(metadata)
	if err = os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(20 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("payment acceptance driver did not stop fixture")
		}
	}
}
