package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// fakeGateway behaves as 支付接入技术文档.md describes the real one, and nothing more:
// GET /submit checks the channel, type and signature (which covers trade_no only),
// refuses a reused trade_no, shifts an amount by 0.01 while another unexpired unpaid
// order of the same type holds it, and cancels unpaid orders after 2 minutes;
// GET /api/order/status/:trade_no reports pending, paid or timeout. Callbacks are
// sent by the test (pay), to the API router, from a chosen source address.
type fakeGateway struct {
	mu      sync.Mutex
	channel string
	secret  string
	now     func() time.Time
	orders  map[string]*fakeGatewayOrder
	server  *httptest.Server
	// statusFailure makes the status endpoint answer 500 (gateway unreachable).
	statusFailure bool
	statusCalls   int
}

type fakeGatewayOrder struct {
	TradeNo, Type, NotifyURL, ReturnURL, Name string
	Cents                                     int64
	Created                                   time.Time
	Paid                                      bool
}

func newFakeGateway(t *testing.T, channel, secret string, now func() time.Time) *fakeGateway {
	g := &fakeGateway{channel: channel, secret: secret, now: now, orders: map[string]*fakeGatewayOrder{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /submit", g.submit)
	mux.HandleFunc("GET /api/order/status/{trade}", g.status)
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func gatewayError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (g *fakeGateway) submit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for _, key := range []string{"channel", "trade_no", "notify_url", "return_url", "name", "money", "sign"} {
		if q.Get(key) == "" {
			gatewayError(w, 400, "缺少必要参数")
			return
		}
	}
	if q.Get("channel") != g.channel {
		gatewayError(w, 400, "无效的商户号")
		return
	}
	kind := q.Get("type")
	if kind == "" {
		kind = payment.TypeWeChat
	}
	if !payment.ValidType(kind) {
		gatewayError(w, 400, "无效的支付类型，仅支持 wxpay 或 alipay")
		return
	}
	cents, err := payment.ParseAmount(q.Get("money"))
	if err != nil || cents <= 0 {
		gatewayError(w, 400, "无效的金额格式")
		return
	}
	if q.Get("sign") != payment.Sign(q.Get("trade_no"), g.secret) {
		gatewayError(w, 401, "签名验证失败")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, taken := g.orders[q.Get("trade_no")]; taken {
		gatewayError(w, 400, "订单号已存在")
		return
	}
	now := g.now()
	for clash := true; clash; {
		clash = false
		for _, o := range g.orders {
			if o.Type == kind && o.Cents == cents && !o.Paid && now.Sub(o.Created) < payment.OrderLifetime {
				cents++
				clash = true
			}
		}
	}
	g.orders[q.Get("trade_no")] = &fakeGatewayOrder{TradeNo: q.Get("trade_no"), Type: kind, NotifyURL: q.Get("notify_url"), ReturnURL: q.Get("return_url"), Name: q.Get("name"), Cents: cents, Created: now}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<html><body>%s %s</body></html>", q.Get("name"), payment.FormatAmount(cents))
}

func (g *fakeGateway) status(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.statusCalls++
	if g.statusFailure {
		w.WriteHeader(500)
		return
	}
	o, ok := g.orders[r.PathValue("trade")]
	w.Header().Set("Content-Type", "application/json")
	switch {
	case !ok:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"status":"not_found","expired":true}`))
	case o.Paid:
		_, _ = w.Write([]byte(`{"status":"paid","expired":false}`))
	case g.now().Sub(o.Created) >= payment.OrderLifetime:
		_, _ = w.Write([]byte(`{"status":"timeout","expired":true}`))
	default:
		_, _ = w.Write([]byte(`{"status":"pending","expired":false}`))
	}
}

// open follows a pay_url the way a browser would; it returns the gateway order or the
// gateway's HTTP status on refusal.
func (g *fakeGateway) open(t *testing.T, payURL string) (*fakeGatewayOrder, int) {
	t.Helper()
	if !strings.HasPrefix(payURL, g.server.URL+"/submit?") {
		t.Fatalf("pay URL does not target the gateway submit endpoint")
	}
	resp, err := http.Get(payURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, resp.StatusCode
	}
	u, _ := url.Parse(payURL)
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.orders[u.Query().Get("trade_no")], 200
}

// markPaid records the customer's payment at the gateway (no callback yet).
func (g *fakeGateway) markPaid(o *fakeGatewayOrder) {
	g.mu.Lock()
	o.Paid = true
	g.mu.Unlock()
}

func (g *fakeGateway) calls() int { g.mu.Lock(); defer g.mu.Unlock(); return g.statusCalls }

const (
	gatewayIP   = "203.0.113.10"
	gatewayName = "1001"
)

// payEnv is one isolated database, API router, test clock and fake gateway.
type payEnv struct {
	t      *testing.T
	db     *gorm.DB
	cfg    config.Config
	clock  *testClock
	router http.Handler
	gw     *fakeGateway
}

func newPayEnv(t *testing.T, mutate func(*config.Config)) *payEnv {
	t.Helper()
	db, cfg := newIsolatedNativeDatabase(t)
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	gw := newFakeGateway(t, gatewayName, "test-only-gateway-secret", clock.Now)
	cfg.WebsiteBaseURL = "https://app.pay-test.example"
	cfg.Payment = config.Payment{Enabled: true, GatewayURL: gw.server.URL, Channel: gatewayName, Secret: "test-only-gateway-secret",
		NotifyURL: "https://api.pay-test.example/api/v1/payments/notify", NotifyAllowed: []netip.Prefix{netip.MustParsePrefix(gatewayIP + "/32")},
		ToleranceCents: 50, MaxPending: 5, PendingWindow: 180 * time.Second, TestPriceCents: 1}
	if mutate != nil {
		mutate(&cfg)
	}
	router := newServerAt(db, cfg, authmail.NewService(db, cfg), clock.Now)
	return &payEnv{t: t, db: db, cfg: cfg, clock: clock, router: router, gw: gw}
}

func (e *payEnv) server() *Server { return &Server{db: e.db, cfg: e.cfg, clock: e.clock.Now} }

type payUser struct {
	model.User
	cookies []*http.Cookie
}

const payPassword = "Payment-Test-Password!"

var payHash = func() string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(payPassword), 10)
	return string(hash)
}()

func (e *payEnv) user(name, role string) payUser {
	e.t.Helper()
	u := model.User{ID: uuid.NewString(), Email: name + "@example.com", Name: name, PasswordHash: payHash, Role: role, Status: "active", AuthVersion: 1}
	if err := e.db.Create(&u).Error; err != nil {
		e.t.Fatal(err)
	}
	w := e.call(nil, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": payPassword}, "")
	if w.Code != 200 {
		e.t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	return payUser{User: u, cookies: w.Result().Cookies()}
}

func (e *payEnv) call(cookies []*http.Cookie, method, path string, body any, key string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", e.cfg.Origins[0])
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *payEnv) want(w *httptest.ResponseRecorder, status int) map[string]any {
	e.t.Helper()
	if w.Code != status {
		e.t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func payErrorCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		return code
	}
	return ""
}

type placed struct {
	Order  model.Order `json:"order"`
	PayURL string      `json:"pay_url"`
	Others int         `json:"other_pending_orders"`
}

func (e *payEnv) place(u payUser, plan, kind, coupon string) placed {
	e.t.Helper()
	w := e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": plan, "pay_type": kind, "coupon_code": coupon}, uuid.NewString())
	if w.Code != 201 {
		e.t.Fatalf("order %d %s", w.Code, w.Body.String())
	}
	var out placed
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// notify posts a callback to the API as the gateway would, from source ip. The clock
// moves on by a millisecond so callbacks are ordered by received_at.
func (e *payEnv) notify(ip, body string) *httptest.ResponseRecorder {
	return e.notifyWith(ip, body, nil)
}

func (e *payEnv) notifyWith(ip, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/payments/notify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.RemoteAddr = ip + ":443"
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	e.clock.Advance(time.Millisecond)
	return w
}

func callbackBody(o *fakeGatewayOrder, status string) string {
	return fmt.Sprintf(`{"trade_no":%q,"name":%q,"money":%s,"trade_status":%q}`, o.TradeNo, o.Name, payment.FormatAmount(o.Cents), status)
}

// pay completes a gateway order and delivers its callback from the gateway address.
func (e *payEnv) pay(o *fakeGatewayOrder) *httptest.ResponseRecorder {
	e.gw.markPaid(o)
	return e.notify(gatewayIP, callbackBody(o, payment.TradeSuccess))
}

func (e *payEnv) order(id string) model.Order {
	e.t.Helper()
	var o model.Order
	if err := e.db.First(&o, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return o
}

func (e *payEnv) subscription(userID string) *model.Subscription {
	e.t.Helper()
	sub, err := readSubscription(e.db, userID)
	if err != nil {
		e.t.Fatal(err)
	}
	return sub
}

func (e *payEnv) lastNotification() model.PaymentNotification {
	e.t.Helper()
	var n model.PaymentNotification
	if err := e.db.Order("received_at desc, id desc").First(&n).Error; err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *payEnv) coupon(c model.Coupon) model.Coupon {
	e.t.Helper()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.PlanIDs == "" {
		c.PlanIDs = "[]"
	}
	if c.PerUserLimit == 0 {
		c.PerUserLimit = 1
	}
	c.CreatedBy, c.Version, c.CreatedAt, c.UpdatedAt = uuid.NewString(), 1, e.clock.Now(), e.clock.Now()
	if err := e.db.Create(&c).Error; err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *payEnv) reconcile() int {
	e.t.Helper()
	n, err := e.server().reconcilePayments(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

const gib = int64(1) << 30
