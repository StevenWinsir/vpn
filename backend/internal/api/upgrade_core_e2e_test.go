package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// Mid-period upgrades and the clients (handoff_plans.md 7.4, 12, round 3: R3-03). An
// upgrade changes the entitlement fingerprint, so a signed-in client is told
// subscription_changed and asked to sign in again, while the period (and with it the
// ledger and every grant already issued) stays the same.

// upgradeNow places an upgrade of userID to planID at the price the server quotes now and
// fulfils it in the same transaction, exactly what a paid gateway callback does
// (placeUpgradeOrder, then fulfilOrder). It is shared by the tests below and the billing
// world's "upgrade" control (android_billing_world_test.go), which have no web session.
func upgradeNow(db *gorm.DB, cfg config.Config, userID, planID string) (model.Order, error) {
	s := &Server{db: db, cfg: cfg}
	var order model.Order
	err := db.Transaction(func(tx *gorm.DB) error {
		now := s.now()
		sub, settings, global, err := s.readUpgradeBase(tx, userID, now)
		if err != nil {
			return err
		}
		var user model.User
		if err = tx.First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		plan, err := loadPurchasablePlan(tx, planID, false)
		if err != nil {
			return err
		}
		ev, err := s.evaluateUpgrade(tx, user, sub, plan, settings, global, now, false)
		if err != nil {
			return err
		}
		if ev.Problem != nil {
			return ev.Problem
		}
		amount := ev.AmountCents
		order, _, _, err = s.placeUpgradeOrder(tx, userID, upgradeOrderInput{PlanID: planID, PayType: "alipay", ExpectedAmountCents: &amount}, uuid.NewString(), now)
		if err != nil {
			return err
		}
		ok, err := s.fulfilOrder(tx, &order, order.AmountCents, now)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("the upgrade could not be fulfilled")
		}
		return tx.First(&order, "id = ?", order.ID).Error
	})
	return order, err
}

// TestCoreUpgradeDisconnect runs the real Core binary (fake engine) against the real API
// and PostgreSQL with the real payment flow and the fake gateway: a customer connected on
// starter upgrades to pro on the website; the Core stops the tunnel and ends the session
// with "套餐已变更，请重新登录", after settling the bytes it carried since its last report
// (the token still works for /traffic in the same period) and releasing its online lease;
// a report still in flight from a second device after the upgrade is charged to the same
// period; signing in again gives the new plan's lines.
// scripts/test-native.py builds the Core and sets ASTERLINK_CORE_BIN.
func TestCoreUpgradeDisconnect(t *testing.T) {
	bin := os.Getenv("ASTERLINK_CORE_BIN")
	if bin == "" {
		t.Skip("set ASTERLINK_CORE_BIN (built by scripts/test-native.py)")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32))
	cfg.Origins = []string{"http://127.0.0.1:3000"}
	// Short client timings (the relations config.Load enforces still hold), as in the billing world.
	cfg.ClientReportInterval, cfg.ClientLease, cfg.ClientIdleTimeout, cfg.ClientNodeWatch = 2*time.Second, 6*time.Second, 30*time.Second, 2*time.Second
	realNow := func() time.Time { return time.Now().UTC() }
	gw := newFakeGateway(t, gatewayName, "test-only-gateway-secret", realNow)
	cfg.WebsiteBaseURL = "https://app.pay-test.example"
	cfg.Payment = config.Payment{Enabled: true, GatewayURL: gw.server.URL, Channel: gatewayName, Secret: "test-only-gateway-secret",
		NotifyURL: "https://api.pay-test.example/api/v1/payments/notify", NotifyAllowed: []netip.Prefix{netip.MustParsePrefix(gatewayIP + "/32")},
		ToleranceCents: 50, MaxPending: 5, PendingWindow: 180 * time.Second}
	router := newServerAt(db, cfg, authmail.NewService(db, cfg), realNow)
	server := httptest.NewServer(router)
	defer server.Close()
	world := seedNativeWorld(t, db, cfg, router)
	// The website side (fake gateway and callbacks) on the same router and real time.
	e := &payEnv{t: t, db: db, cfg: cfg, clock: &testClock{now: realNow()}, router: router, gw: gw}

	u := e.user("core-upgrade", "user")
	bought := e.buy(u, "starter", "", "")
	if bought.CoversFrom == nil || bought.CoversTo == nil {
		t.Fatalf("a new paid order carries its value window: %+v", bought)
	}
	readSub := func() model.Subscription {
		var sub model.Subscription
		if err := db.Where("user_id = ?", u.ID).First(&sub).Error; err != nil {
			t.Fatal(err)
		}
		return sub
	}
	before := readSub()
	if before.PlanID != "starter" || before.AllowDedicated {
		t.Fatalf("starter subscription: %+v", before)
	}

	// The Core signs in: starter has no dedicated access, so only the ordinary line (0.5x).
	core := startCore(t, bin, server.URL)
	core.login(u.Email, payPassword)
	snap, _ := core.waitSnapshot("starter catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 1 })
	if snap.Nodes[0].ID != world.ordinary.ID || snap.Entitlement == nil || snap.Entitlement.PlanName != "轻量计划" {
		t.Fatalf("starter sees %+v (%+v)", snap.Nodes, snap.Entitlement)
	}
	if reply, s := core.call("select_node", core.gen, map[string]string{"node_id": world.ordinary.ID, "catalog_revision": snap.CatalogRevision}); !reply.OK || s.ActionError != nil {
		t.Fatalf("select: %+v", s.ActionError)
	}
	if _, s := core.call("connect", core.gen, nil); s.ActionError != nil || s.State != "connected" || s.Metering == nil || s.Metering.RatePermille != 500 {
		t.Fatalf("connect on starter: %+v", s.ActionError)
	}
	// Traffic before the upgrade, settled by a real report.
	core.carry(3*mib, 9*mib)
	waitLedger := func(what string, ok func(model.Subscription) bool) model.Subscription {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if sub := readSub(); ok(sub) {
				return sub
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("ledger never showed %s: %+v", what, readSub())
		return model.Subscription{}
	}
	waitLedger("the Core's report", func(s model.Subscription) bool { return s.UploadBytes == 3*mib && s.DownloadBytes == 9*mib })
	var coreSession model.NativeSession
	if err := db.Where("user_id = ?", u.ID).First(&coreSession).Error; err != nil {
		t.Fatal(err)
	}
	readCoreSession := func() model.NativeSession {
		var sess model.NativeSession
		if err := db.First(&sess, "id = ?", coreSession.ID).Error; err != nil {
			t.Fatal(err)
		}
		return sess
	}

	// A second device of the same account with a report in flight across the upgrade
	// (the client API as an app speaks it).
	clientCall := func(method, path, token string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/v1/client"+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	login := clientCall("POST", "/login", "", map[string]string{"email": u.Email, "password": payPassword, "device_id": uuid.NewString(), "platform": "android", "app_version": "1.0.0"})
	var second struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(login.Body.Bytes(), &second)
	if w := clientCall("GET", "/config?node_id="+world.ordinary.ID, second.Token, nil); login.Code != 200 || w.Code != 200 {
		t.Fatalf("second device: login %d, config %d %s", login.Code, w.Code, w.Body.String())
	}
	if w := clientCall("POST", "/traffic", second.Token, map[string]int64{"sequence": 1, "upload_bytes": mib, "download_bytes": mib}); w.Code != 200 {
		t.Fatalf("second device report: %d %s", w.Code, w.Body.String())
	}
	// The period's ledger row (created on demand, by the first grant; made here so its
	// synchronisation by the upgrade is visible).
	var period *model.SubscriptionPeriod
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		period, err = ensurePeriod(tx, &before)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	settled := waitLedger("both devices", func(s model.Subscription) bool { return s.UploadBytes == 4*mib && s.DownloadBytes == 10*mib })

	// Bytes the Core carries just before the upgrade: right after one of its periodic reports
	// (every 2 s here) has landed, so the next is still most of an interval away, while the
	// catalog watch (1 s polls) tells the Core of the upgrade sooner. Before the fix the Core
	// dropped this tail; now its final report settles it with the still-valid token.
	reported := readCoreSession().LastSequence
	for deadline := time.Now().Add(10 * time.Second); readCoreSession().LastSequence == reported; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the Core sent no periodic report")
		}
	}
	tailFrom := readCoreSession()
	core.carry(4*mib, 11*mib)

	// The upgrade on the website: quote, order, gateway, trusted callback.
	upgradedAt := time.Now()
	order := e.upgrade(u, "pro")
	if order.Status != model.OrderPaid || order.Kind != model.OrderKindUpgrade || order.CoversFrom == nil || !order.CoversTo.Equal(before.ExpiresAt) {
		t.Fatalf("upgrade order: %+v", order)
	}
	after := readSub()
	if after.ID != before.ID || after.PlanID != "pro" || !after.AllowDedicated || after.MaxDevices != 5 || !after.StartsAt.Equal(before.StartsAt) || !after.ExpiresAt.Equal(before.ExpiresAt) ||
		after.TrafficLimitBytes != before.TrafficLimitBytes+order.UpgradeTrafficBytes || after.UsedUnits != settled.UsedUnits {
		t.Fatalf("upgraded subscription %+v (before %+v)", after, before)
	}

	// The Core learns it within one catalog watch or report: tunnel down, session gone,
	// the Core's own words for it.
	snap, took := core.waitSnapshot("subscription_changed", 15*time.Second, func(s snapshotView) bool { return s.State == "signed_out" })
	t.Logf("the Core ended the session %v after the upgrade was paid (%v after the order was placed)", took, time.Since(upgradedAt))
	if snap.LastError == nil || snap.LastError.Code != "subscription_changed" || snap.LastError.Message != "套餐已变更，请重新登录" || snap.TunnelHealth == "up" || snap.User != nil || len(snap.Nodes) != 0 {
		t.Fatalf("after the upgrade: %+v", snap)
	}
	if _, again := core.call("connect", core.gen, nil); again.State == "connected" {
		t.Fatal("a signed-out Core connected")
	}
	// The tail is charged at the line's rate to the same period, and the Core's online lease
	// is released rather than left to lapse.
	tailed := waitLedger("the Core's tail", func(s model.Subscription) bool { return s.UploadBytes == 5*mib && s.DownloadBytes == 12*mib })
	if want := settled.UsedUnits + 3*mib*500; tailed.UsedUnits != want {
		t.Fatalf("the tail charged %d units, want %d", tailed.UsedUnits-settled.UsedUnits, 3*mib*500)
	}
	if final := readCoreSession(); final.UploadBytes != 4*mib || final.DownloadBytes != 11*mib {
		t.Fatalf("the Core's session counters %d/%d", final.UploadBytes, final.DownloadBytes)
	} else {
		var tail model.ClientTrafficReport
		if err := db.Where("session_id = ? AND sequence = ?", coreSession.ID, final.LastSequence).First(&tail).Error; err != nil {
			t.Fatal(err)
		}
		t.Logf("the tail (sequence %d after %d) was reported %v after the upgrade was placed", final.LastSequence, tailFrom.LastSequence, tail.CreatedAt.Sub(upgradedAt))
	}
	var released bool
	for deadline := time.Now().Add(10 * time.Second); !released && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		var active int64
		if err := db.Model(&model.ConnectionLease{}).Where("session_id = ? AND state = ?", coreSession.ID, model.LeaseActive).Count(&active).Error; err != nil {
			t.Fatal(err)
		}
		var ended int64
		if err := db.Model(&model.ConnectionLease{}).Where("session_id = ? AND end_reason = ?", coreSession.ID, model.LeaseReleased).Count(&ended).Error; err != nil {
			t.Fatal(err)
		}
		released = active == 0 && ended > 0
	}
	if !released {
		t.Fatal("the Core's online lease was not released after the tail")
	}

	// The report in flight lands after the upgrade: charged at the session's own rate to
	// the same subscription and period, and the reply tells the device to sign in again.
	late := clientCall("POST", "/traffic", second.Token, map[string]int64{"sequence": 2, "upload_bytes": 3 * mib, "download_bytes": 2 * mib})
	var lateBody struct {
		Session nativeState `json:"session"`
	}
	_ = json.Unmarshal(late.Body.Bytes(), &lateBody)
	if late.Code != 200 || lateBody.Session.Reason != "subscription_changed" {
		t.Fatalf("late report: %d %s", late.Code, late.Body.String())
	}
	final := readSub()
	if want := tailed.UsedUnits + (2*mib+mib)*500; final.UsedUnits != want || final.UploadBytes != 7*mib || final.DownloadBytes != 13*mib {
		t.Fatalf("late report charged %d units (want %d): %+v", final.UsedUnits-tailed.UsedUnits, (3*mib)*500, final)
	}
	var reports []model.ClientTrafficReport
	if err := db.Where("user_id = ?", u.ID).Order("created_at").Find(&reports).Error; err != nil {
		t.Fatal(err)
	}
	var charged int64
	for _, r := range reports {
		if r.SubscriptionID != before.ID || r.RatePermille != 500 {
			t.Fatalf("report outside the period or at another rate: %+v", r)
		}
		charged += r.ChargedUnits
	}
	if charged != final.UsedUnits {
		t.Fatalf("%d reports charged %d, ledger %d", len(reports), charged, final.UsedUnits)
	}
	var periods []model.SubscriptionPeriod
	if err := db.Where("subscription_id = ?", before.ID).Find(&periods).Error; err != nil || len(periods) != 1 || periods[0].ID != period.ID || periods[0].LimitBytes != final.TrafficLimitBytes {
		t.Fatalf("ledger period %+v (before %+v) %v", periods, period, err)
	}
	// No new connection on the old authorization.
	if w := clientCall("GET", "/config?node_id="+world.ordinary.ID, second.Token, nil); w.Code != 409 || errorCode(w) != "subscription_changed" {
		t.Fatalf("admission on the old session: %d %s", w.Code, w.Body.String())
	}

	// Signing in again: the new plan, its dedicated line, connected at that line's rate.
	core.login(u.Email, payPassword)
	snap, _ = core.waitSnapshot("pro catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 2 })
	if snap.Entitlement == nil || snap.Entitlement.PlanName != "进阶计划" || !snap.Entitlement.AccountAuthorized {
		t.Fatalf("after signing in again: %+v", snap.Entitlement)
	}
	if reply, s := core.call("select_node", core.gen, map[string]string{"node_id": world.dedicated.ID, "catalog_revision": snap.CatalogRevision}); !reply.OK || s.ActionError != nil {
		t.Fatalf("select the dedicated line: %+v", s.ActionError)
	}
	if _, s := core.call("connect", core.gen, nil); s.ActionError != nil || s.State != "connected" || s.Metering.RatePermille != 1000 {
		t.Fatalf("connect on the dedicated line: %+v", s.ActionError)
	}
	core.call("disconnect", 0, nil)
}

// TestUpgradeLateNodeUsageStaysInPeriod is the production metering (node_reported): a grant
// issued before the upgrade keeps reporting to the same period afterwards and every byte
// is charged at the grant's rate, while the session is told subscription_changed and no
// new grant is issued on the old authorization.
func TestUpgradeLateNodeUsageStaysInPeriod(t *testing.T) {
	e := newGrantEnv(t, func(c *config.Config) { c.GrantBudgetBytes = 256 * mib })
	u := e.newUser(100<<30, 2)
	now := time.Now().UTC().Truncate(time.Microsecond)
	// The customer holds starter for another 20 days (no dedicated access, 2 devices).
	if err := e.db.Model(&model.Subscription{}).Where("id = ?", u.subID).Updates(map[string]any{"plan_id": "starter", "plan_name": "轻量计划",
		"allow_dedicated": false, "max_devices": 2, "starts_at": now.Add(-10 * 24 * time.Hour), "expires_at": now.Add(20 * 24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	node := e.addNode("Authority-Upgrade", 500, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	token := e.login(u)
	w := e.createGrant(token, node.ID, "key-upgrade-0001")
	if w.Code != 202 {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	id := grantID(t, w)
	e.sync(agent, nil, nil)
	if orders := e.sync(agent, []string{id}, nil); orders[id].Action != "keep" {
		t.Fatalf("install: %+v", orders[id])
	}
	if got := e.client("GET", "/grants/"+id, token, nil, nil); got.Code != 200 {
		t.Fatalf("ready: %d %s", got.Code, got.Body.String())
	}
	e.report(agent, usage{id, 1, 1, 2 * mib, 6 * mib, false}, 200)
	before := e.subscription(u.subID)
	period := e.period(u.subID)

	order, err := upgradeNow(e.db, e.cfg, u.ID, "pro")
	if err != nil || order.Status != model.OrderPaid {
		t.Fatalf("upgrade: %v %+v", err, order)
	}
	after := e.subscription(u.subID)
	if after.PlanID != "pro" || !after.StartsAt.Equal(before.StartsAt) || !after.ExpiresAt.Equal(before.ExpiresAt) || after.UsedUnits != before.UsedUnits {
		t.Fatalf("upgraded %+v", after)
	}
	// The node keeps the grant (same period) and its late usage is charged.
	if orders := e.sync(agent, []string{id}, nil); orders[id].Action != "keep" {
		t.Fatalf("the upgrade withdrew a grant of the same period: %+v", orders[id])
	}
	e.report(agent, usage{id, 1, 2, 5 * mib, 10 * mib, false}, 200)
	e.report(agent, usage{id, 1, 3, 6 * mib, 12 * mib, true}, 200)
	settled := e.subscription(u.subID)
	if want := before.UsedUnits + (4*mib+6*mib)*500; settled.UsedUnits != want || settled.UploadBytes != 6*mib || settled.DownloadBytes != 12*mib {
		t.Fatalf("late node usage: used %d want %d (%+v)", settled.UsedUnits, want, settled)
	}
	if p := e.period(u.subID); p.ID != period.ID || p.UsedUnits != settled.UsedUnits || p.LimitBytes != settled.TrafficLimitBytes {
		t.Fatalf("period %+v (before %+v)", p, period)
	}
	// The session says subscription_changed; no new grant on the old authorization.
	var state nativeState
	_ = json.Unmarshal(e.client("GET", "/session", token, nil, nil).Body.Bytes(), &state)
	if state.Reason != "subscription_changed" {
		t.Fatalf("session after the upgrade: %+v", state)
	}
	if w := e.createGrant(token, node.ID, "key-upgrade-0002"); w.Code != 409 || errorCode(w) != "subscription_changed" {
		t.Fatalf("new grant on the old authorization: %d %s", w.Code, w.Body.String())
	}
	// A fresh sign-in is granted on the new plan.
	fresh := e.login(u)
	if w := e.createGrant(fresh, node.ID, "key-upgrade-0003"); w.Code != 202 {
		t.Fatalf("grant after signing in again: %d %s", w.Code, w.Body.String())
	}
}
