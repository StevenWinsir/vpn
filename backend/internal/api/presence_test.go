package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// Round 2 (handoff_node_use_history_and_load_balancing_c.md 12): lease state machine.
func TestLeaseLifecyclePostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	u := e.user(5)
	jp, hk := e.server("JP1"), e.server("HK1")
	token := e.login(u, uuid.NewString())
	// Login, catalog reads and a session read take no slot.
	e.want(e.native("GET", "/nodes", token, nil), 200, "")
	e.want(e.native("GET", "/session", token, nil), 200, "")
	if e.live(u.ID) != 0 {
		t.Fatal("a lease exists before connecting")
	}
	start := e.now()
	w := e.connect(token, jp.ID, "life-key-0001")
	e.want(w, 201, "")
	first := e.connection(w)
	if first.Connection.State != "active" || first.Connection.GroupID != jp.ID || !first.Connection.ExpiresAt.Equal(start.Add(90*time.Second)) || first.NodeID != jp.ID ||
		first.Session.ConnectionID != first.Connection.ID || first.Session.Reason != "" || !first.Session.CanConnect || nativeProfileVersion([]byte(first.YAML)) != first.Version {
		t.Fatalf("connection: %+v", first)
	}
	contractShapeV3(t, "connection_create", "response", w.Body.Bytes())
	if l := e.lease(first.Connection.ID); l.Method != model.LeaseByConnections || l.IdempotencyKey != "life-key-0001" || l.Platform != "android" {
		t.Fatalf("stored lease: %+v", l)
	}
	// Idempotent replay; the same key for another group is a conflict.
	replay := e.connect(token, jp.ID, "life-key-0001")
	e.want(replay, 200, "")
	if e.connection(replay).Connection.ID != first.Connection.ID {
		t.Fatal("replay created a second lease")
	}
	e.want(e.connect(token, hk.ID, "life-key-0001"), 409, "idempotency_conflict")
	// Renewal rides on /traffic only.
	e.advance(60 * time.Second)
	e.want(e.native("GET", "/session", token, nil), 200, "")
	if l := e.lease(first.Connection.ID); !l.ExpiresAt.Equal(start.Add(90 * time.Second)) {
		t.Fatal("GET /session renewed a client_reported lease")
	}
	w = e.traffic(token, 1, 1000, 3000)
	e.want(w, 200, "")
	if l := e.lease(first.Connection.ID); !l.ExpiresAt.Equal(e.now().Add(90*time.Second)) || !l.RenewedAt.Equal(e.now()) {
		t.Fatalf("traffic did not renew: %+v", l)
	}
	if s := snapshotOf(t, w); s.ConnectionState != "active" || s.Reason != "" || s.ConnectionExpiresAt == nil {
		t.Fatalf("snapshot after renewal: %+v", s)
	}
	// Release is idempotent, allow-listed and scoped to the session.
	release := func(token, id, reason string) *httptest.ResponseRecorder {
		return e.native("POST", "/connections/"+id+"/release", token, map[string]string{"reason": reason})
	}
	e.want(release(token, first.Connection.ID, "because"), 400, "invalid_release")
	e.want(release(token, uuid.NewString(), "user"), 404, "connection_not_found")
	other := e.login(e.user(5), uuid.NewString())
	e.want(release(other, first.Connection.ID, "user"), 404, "connection_not_found")
	w = release(token, first.Connection.ID, "user")
	e.want(w, 200, "")
	contractShapeV3(t, "connection_release", "response", w.Body.Bytes())
	again := release(token, first.Connection.ID, "stopped")
	e.want(again, 200, "")
	if l := e.lease(first.Connection.ID); l.State != model.LeaseEnded || l.EndReason != model.LeaseReleased || !l.EndedAt.Equal(e.now()) {
		t.Fatalf("released lease: %+v", l)
	}
	if e.live(u.ID) != 0 {
		t.Fatal("released device still online")
	}
	// The tail batch after release is accounted and does not bring the lease back.
	w = e.traffic(token, 2, 1500, 3500)
	e.want(w, 200, "")
	if s := snapshotOf(t, w); s.ConnectionState != "ended" || s.Reason != "" || e.live(u.ID) != 0 {
		t.Fatalf("tail after release: %+v", s)
	}
	// A lapsed lease is not revived by a late report.
	w = e.connect(token, jp.ID, "life-key-0002")
	e.want(w, 201, "")
	second := e.connection(w)
	e.advance(90 * time.Second)
	status := snapshotOf(t, e.native("GET", "/session", token, nil))
	if status.Reason != "connection_expired" || status.CanConnect || status.ConnectionState != "expired" || status.AuthorizationExpiresAt != nil {
		t.Fatalf("expired snapshot: %+v", status)
	}
	contractShapeV3(t, "session_connection_expired", "response", mustJSON(t, status))
	w = e.traffic(token, 3, 1600, 3600)
	e.want(w, 200, "")
	if s := snapshotOf(t, w); s.Reason != "connection_expired" || e.lease(second.Connection.ID).ExpiresAt.After(e.now()) || e.live(u.ID) != 0 {
		t.Fatalf("late report revived the lease: %+v", s)
	}
	var reports int64
	e.db.Model(&model.ClientTrafficReport{}).Where("sequence = 3").Count(&reports)
	if reports != 1 {
		t.Fatal("report after expiry was not accounted")
	}
	// Connecting again on the same device replaces the old lease: one active row.
	w = e.connect(token, jp.ID, "life-key-0003")
	e.want(w, 201, "")
	if l := e.lease(second.Connection.ID); l.State != model.LeaseEnded || l.EndReason != model.LeaseReplaced {
		t.Fatalf("previous lease: %+v", l)
	}
	var active int64
	e.db.Model(&model.ConnectionLease{}).Where("user_id = ? AND state = ?", u.ID, model.LeaseActive).Count(&active)
	if active != 1 || e.live(u.ID) != 1 {
		t.Fatalf("%d active leases for one device", active)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Round 2: the online cap. Ten devices racing, one device reconnecting, a released
// slot reused at once and a hard-killed device's slot only after its lease lapses.
func TestDeviceLimitPostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	u := e.user(5) // D1: min(5, CLIENT_MAX_ONLINE_DEVICES=2) = 2
	g := e.server("JP1")
	tokens := make([]string, 10)
	for i := range tokens {
		tokens[i] = e.login(u, uuid.NewString())
	}
	results := make([]*httptest.ResponseRecorder, len(tokens))
	var wg sync.WaitGroup
	for i, token := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = e.connect(token, g.ID, fmt.Sprintf("race-key-%04d", i))
		}()
	}
	wg.Wait()
	var admitted []int
	for i, w := range results {
		switch {
		case w.Code == 201:
			admitted = append(admitted, i)
		case w.Code != 403 || errorCode(w) != "device_limit":
			t.Fatalf("device %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if len(admitted) != 2 || e.live(u.ID) != 2 {
		t.Fatalf("admitted %v, live %d", admitted, e.live(u.ID))
	}
	// One device connecting again and again keeps one slot.
	a, b := tokens[admitted[0]], tokens[admitted[1]]
	for i := range 5 {
		e.want(e.connect(a, g.ID, fmt.Sprintf("again-key-%04d", i)), 201, "")
	}
	if e.live(u.ID) != 2 {
		t.Fatalf("reconnecting device counted %d times", e.live(u.ID))
	}
	var rejected []string
	for i, token := range tokens {
		if !slices.Contains(admitted, i) {
			rejected = append(rejected, token)
		}
	}
	// A released slot is free immediately.
	var mine model.ConnectionLease
	e.db.Where("session_id = (SELECT id FROM "+e.cfg.Schema+".native_sessions WHERE token_hash = ?) AND state = ?", refreshHash(a), model.LeaseActive).Take(&mine)
	e.want(e.native("POST", "/connections/"+mine.ID+"/release", a, map[string]string{"reason": "user"}), 200, "")
	c := rejected[0]
	e.want(e.connect(c, g.ID, "freed-key-0001"), 201, "")
	// b is hard-killed (never reports again); c keeps reporting. d gets b's slot only
	// once b's lease has lapsed: at 90 s, not at 89 s.
	d := rejected[1]
	e.advance(60 * time.Second)
	e.want(e.traffic(c, 1, 10, 10), 200, "")
	e.want(e.connect(d, g.ID, "late-key-0001"), 403, "device_limit")
	e.advance(29 * time.Second)
	e.want(e.traffic(c, 2, 20, 20), 200, "")
	e.want(e.connect(d, g.ID, "late-key-0002"), 403, "device_limit")
	e.advance(time.Second)
	e.want(e.connect(d, g.ID, "late-key-0003"), 201, "")
	_ = b

	// D1: the smaller of the plan's devices and the global setting.
	for _, tc := range []struct {
		name         string
		global, plan int
		wantAdmitted int
	}{{"plan_lower", 2, 1, 1}, {"global_lower", 1, 5, 1}, {"both_three", 3, 3, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPresenceEnv(t, func(c *config.Config) { c.ClientMaxOnlineDevices = tc.global })
			u := e.user(tc.plan)
			g := e.server("JP1")
			admitted := 0
			for i := range 4 {
				if e.connect(e.login(u, uuid.NewString()), g.ID, fmt.Sprintf("d1-key-%04d", i)).Code == 201 {
					admitted++
				}
			}
			if admitted != tc.wantAdmitted {
				t.Fatalf("admitted %d want %d", admitted, tc.wantAdmitted)
			}
		})
	}
}

// Round 2: every path that revokes a native session ends its lease in the same
// transaction (plan 5.4); a disabled account's devices stop counting at once.
func TestRevocationEndsLeasesPostgres(t *testing.T) {
	e := newPresenceEnv(t, func(c *config.Config) { c.ClientMaxOnlineDevices = 5 })
	u := e.user(5)
	g := e.server("JP1")
	connectNew := func(device string) (string, string) {
		token := e.login(u, device)
		w := e.connect(token, g.ID, "rev-"+uuid.NewString()[:8])
		e.want(w, 201, "")
		return token, e.connection(w).Connection.ID
	}
	deviceA := uuid.NewString()
	_, leaseA := connectNew(deviceA)
	tokenB, leaseB := connectNew(uuid.NewString())
	e.login(u, deviceA) // signing in again on device A
	if l := e.lease(leaseA); l.State != model.LeaseEnded || l.EndReason != model.LeaseSessionRevoked {
		t.Fatalf("same-device login: %+v", l)
	}
	e.want(e.native("POST", "/logout", tokenB, map[string]any{}), 200, "")
	if l := e.lease(leaseB); l.State != model.LeaseEnded || l.EndReason != model.LeaseSessionRevoked || e.live(u.ID) != 0 {
		t.Fatalf("logout: %+v", l)
	}
	// Password reset revokes every session through authmail.RevokeAllSessions.
	_, leaseC := connectNew(uuid.NewString())
	_, leaseD := connectNew(uuid.NewString())
	cookie := e.web(u)
	if online := e.webGet(cookie, "/me/devices")["online"].([]any); len(online) != 2 {
		t.Fatalf("before reset: %v", online)
	}
	if err := e.db.Transaction(func(tx *gorm.DB) error { return authmail.RevokeAllSessions(tx, u.ID, e.now()) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{leaseC, leaseD} {
		if l := e.lease(id); l.State != model.LeaseEnded || l.EndReason != model.LeaseSessionRevoked {
			t.Fatalf("reset: %+v", l)
		}
	}
	// The web session was revoked too; a fresh one sees nothing online.
	devices := e.webGet(e.web(u), "/me/devices")
	if len(devices["online"].([]any)) != 0 || len(devices["signed_in"].([]any)) != 0 || devices["online_count"].(float64) != 0 {
		t.Fatalf("after reset: %v", devices)
	}
	// Disabling the account takes its devices offline without any sweep.
	connectNew(uuid.NewString())
	if e.live(u.ID) != 1 {
		t.Fatal("setup")
	}
	if err := e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if e.live(u.ID) != 0 {
		t.Fatal("a disabled account's device still counts")
	}
	// A revoked session's lease never counts even if a path forgot to end it.
	e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("status", "active")
	token, leaseF := connectNew(uuid.NewString())
	if e.live(u.ID) != 2 {
		t.Fatalf("re-enabled account: %d online", e.live(u.ID))
	}
	e.db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Update("revoked_at", e.now())
	if l := e.lease(leaseF); l.State != model.LeaseActive || e.live(u.ID) != 1 {
		t.Fatal("revoked session's lease counted")
	}
}

// Round 2 compatibility: an older Core uses GET /config + POST /traffic only.
func TestLegacyCoreLeasesPostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	u := e.user(5)
	g := e.server("JP1")
	tokenA := e.login(u, uuid.NewString())
	w := e.native("GET", "/config?node_id="+g.ID, tokenA, nil)
	e.want(w, 200, "")
	// The v1 response stays a superset of what the old Core decodes.
	var live, fixture struct {
		Session map[string]any `json:"session"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &live)
	_ = json.Unmarshal(readNativeContract(t)["vip_ready"].Response, &fixture)
	for key := range fixture.Session {
		if _, ok := live.Session[key]; !ok {
			t.Fatalf("v1 session field %s missing", key)
		}
	}
	leaseA := snapshotOf(t, w).ConnectionID
	if l := e.lease(leaseA); l.Method != model.LeaseByLegacy || l.State != model.LeaseActive {
		t.Fatalf("legacy lease: %+v", l)
	}
	e.advance(30 * time.Second)
	e.want(e.traffic(tokenA, 1, 100, 100), 200, "")
	if !e.lease(leaseA).ExpiresAt.Equal(e.now().Add(90 * time.Second)) {
		t.Fatal("legacy lease not renewed by /traffic")
	}
	// Paused past its lease: no reason that would disturb an idle old app...
	e.advance(91 * time.Second)
	status := snapshotOf(t, e.native("GET", "/session", tokenA, nil))
	if status.Reason != "" || !status.CanConnect || status.ConnectionState != "expired" {
		t.Fatalf("legacy idle snapshot: %+v", status)
	}
	// ...and its next report (the old Core's first flush on connecting) admits it anew.
	w = e.traffic(tokenA, 2, 200, 200)
	e.want(w, 200, "")
	readmitted := snapshotOf(t, w)
	if readmitted.Reason != "" || readmitted.ConnectionID == leaseA || readmitted.ConnectionState != "active" || e.live(u.ID) != 1 {
		t.Fatalf("legacy re-admission: %+v", readmitted)
	}
	// Two more old devices: the cap applies to /config.
	tokenB := e.login(u, uuid.NewString())
	e.want(e.native("GET", "/config?node_id="+g.ID, tokenB, nil), 200, "")
	tokenC := e.login(u, uuid.NewString())
	e.want(e.native("GET", "/config?node_id="+g.ID, tokenC, nil), 403, "device_limit")
	// A pauses again, C takes the slot, and A's next report is still charged but A is
	// told device_limit instead of being admitted.
	e.advance(60 * time.Second)
	e.want(e.traffic(tokenB, 1, 10, 10), 200, "")
	e.advance(31 * time.Second)
	e.want(e.native("GET", "/config?node_id="+g.ID+"&last_sequence=0", tokenC, nil), 200, "")
	w = e.traffic(tokenA, 3, 300, 300)
	e.want(w, 200, "")
	if s := snapshotOf(t, w); s.Reason != "device_limit" || s.CanConnect {
		t.Fatalf("over-cap legacy report: %+v", s)
	}
	var charged int64
	e.db.Model(&model.ClientTrafficReport{}).Where("sequence = 3 AND user_id = ?", u.ID).Count(&charged)
	if charged != 1 || e.live(u.ID) != 2 {
		t.Fatal("refused legacy report not accounted, or admitted anyway")
	}
	// A server-side end (e.g. the server was withdrawn) tells the old Core profile_changed.
	var leaseB model.ConnectionLease
	e.db.Where("user_id = ? AND state = ? AND session_id = (SELECT id FROM "+e.cfg.Schema+".native_sessions WHERE token_hash = ?)", u.ID, model.LeaseActive, refreshHash(tokenB)).Take(&leaseB)
	e.db.Model(&leaseB).Updates(map[string]any{"state": model.LeaseEnded, "ended_at": e.now(), "end_reason": model.LeaseNodeUnavailable})
	if s := snapshotOf(t, e.traffic(tokenB, 2, 20, 20)); s.Reason != "profile_changed" {
		t.Fatalf("legacy server-side end: %+v", s)
	}
}

type presenceFault struct{}

// Round 2: history is written in the billing transaction and follows it exactly.
func TestUsageHistoryPostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	u := e.user(5)
	g := e.server("JP1")
	token := e.login(u, uuid.NewString())
	e.want(e.connect(token, g.ID, "hist-key-0001"), 201, "")
	// 10:07:30 -> bucket 10:05; replay adds nothing; 10:10:00 -> bucket 10:10.
	e.want(e.traffic(token, 1, 1000, 3000), 200, "")
	e.want(e.traffic(token, 1, 1000, 3000), 200, "")
	e.advance(150 * time.Second)
	e.want(e.traffic(token, 2, 1500, 3500), 200, "")
	var buckets []model.UsageBucket
	e.db.Where("user_id = ?", u.ID).Order("bucket_start").Find(&buckets)
	if len(buckets) != 2 || !buckets[0].BucketStart.Equal(time.Date(2030, 1, 2, 10, 5, 0, 0, time.UTC)) || buckets[0].UploadBytes != 1000 || buckets[0].DownloadBytes != 3000 || buckets[0].ChargedUnits != 4000*1000 ||
		!buckets[1].BucketStart.Equal(time.Date(2030, 1, 2, 10, 10, 0, 0, time.UTC)) || buckets[1].UploadBytes != 500 || buckets[1].DownloadBytes != 500 {
		t.Fatalf("buckets: %+v", buckets)
	}
	// A failing history write rolls the whole batch back: no charge, no ledger row.
	if err := e.db.Callback().Row().Before("gorm:row").Register("presence:fault", func(tx *gorm.DB) {
		if tx.Statement.Context.Value(presenceFault{}) != nil && strings.Contains(tx.Statement.SQL.String(), "usage_buckets") {
			_ = tx.AddError(errors.New("injected history failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.db.Callback().Row().Remove("presence:fault") })
	before := e.subscriptionOf(u)
	fault := e.do(presenceCall{method: "POST", path: "/client/traffic", token: token, body: map[string]int64{"sequence": 3, "upload_bytes": 2500, "download_bytes": 4500}, ctx: context.WithValue(context.Background(), presenceFault{}, true)})
	e.want(fault, 503, "client_service_unavailable")
	var ledger int64
	e.db.Model(&model.ClientTrafficReport{}).Where("user_id = ? AND sequence = 3", u.ID).Count(&ledger)
	if after := e.subscriptionOf(u); after.UsedUnits != before.UsedUnits || ledger != 0 || e.sequence(token) != 2 {
		t.Fatalf("history failure left a charge: %d -> %d, ledger %d", before.UsedUnits, after.UsedUnits, ledger)
	}
	e.want(e.traffic(token, 3, 2500, 4500), 200, "")
	if after := e.subscriptionOf(u); after.UsedUnits != before.UsedUnits+2000*1000 {
		t.Fatal("retry not charged")
	}

	// The window: T = 10:10 (now 10:10:00); intervals start at T-24h+5m.
	other := e.user(5)
	T := time.Date(2030, 1, 2, 10, 10, 0, 0, time.UTC)
	windowStart := T.Add(-24*time.Hour + 5*time.Minute)
	e.db.Where("user_id = ?", u.ID).Delete(&model.UsageBucket{})
	insert := func(userID string, at time.Time, up int64) {
		if err := e.db.Omit("User").Create(&model.UsageBucket{UserID: userID, BucketStart: at, UploadBytes: up, DownloadBytes: 2 * up, ChargedUnits: 3000 * up, UpdatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	insert(u.ID, T.Add(-30*time.Hour), 1<<20)                   // long gone (the sweep may lag)
	insert(u.ID, T.Add(-24*time.Hour), 1<<19)                   // exactly T-24h: kept by the sweep, outside the window
	insert(u.ID, windowStart, 1)                                // first interval, first bucket
	insert(u.ID, windowStart.Add(3*time.Hour-5*time.Minute), 2) // first interval, last bucket
	insert(u.ID, windowStart.Add(3*time.Hour), 4)               // second interval
	insert(u.ID, T.Add(-3*time.Hour+5*time.Minute), 8)          // last interval, first bucket
	insert(u.ID, T, 16)                                         // the bucket in progress
	insert(other.ID, T, 1000)                                   // someone else's
	history := e.webGet(e.web(u), "/me/usage-history")
	if history["bucket_seconds"].(float64) != 10800 || history["granularity_seconds"].(float64) != 300 ||
		history["window_start"] != windowStart.Format(time.RFC3339) || history["window_end"] != T.Add(5*time.Minute).Format(time.RFC3339) {
		t.Fatalf("window: %v", history)
	}
	intervals := history["buckets"].([]any)
	want := []float64{3, 4, 0, 0, 0, 0, 0, 24}
	if len(intervals) != 8 {
		t.Fatalf("%d intervals", len(intervals))
	}
	for i, raw := range intervals {
		interval := raw.(map[string]any)
		start := windowStart.Add(time.Duration(i) * 3 * time.Hour)
		if interval["upload_bytes"].(float64) != want[i] || interval["download_bytes"].(float64) != 2*want[i] || interval["charged_units"].(float64) != 3000*want[i] ||
			interval["start"] != start.Format(time.RFC3339) || interval["end"] != start.Add(3*time.Hour).Format(time.RFC3339) {
			t.Fatalf("interval %d: %v", i, interval)
		}
	}
	if history["total_upload_bytes"].(float64) != 31 || history["total_download_bytes"].(float64) != 62 || history["total_charged_units"].(float64) != 93000 {
		t.Fatalf("totals: %v", history)
	}
}

func (e *presenceEnv) subscriptionOf(u presenceUser) model.Subscription {
	var sub model.Subscription
	e.db.Take(&sub, "id = ?", u.sub.ID)
	return sub
}

// Round 2: /me/devices, /me/usage-history and /me/dashboard expose an explicit
// allow-list: no server, session, lease or grant id, token or address.
func TestPresenceResponsesAreAllowListedPostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	u := e.user(5)
	g := e.server("JP1")
	connected := e.login(u, uuid.NewString())
	w := e.connect(connected, g.ID, "white-key-0001")
	e.want(w, 201, "")
	lease := e.connection(w)
	e.advance(20 * time.Second)
	e.want(e.traffic(connected, 1, 700, 1300), 200, "")
	idle := e.login(u, uuid.NewString())
	cookie := e.web(u)
	devicesRaw := e.do(presenceCall{method: "GET", path: "/me/devices", token: cookie})
	e.want(devicesRaw, 200, "")
	historyRaw := e.do(presenceCall{method: "GET", path: "/me/usage-history", token: cookie})
	dashboardRaw := e.do(presenceCall{method: "GET", path: "/me/dashboard", token: cookie})
	var sessions []model.NativeSession
	e.db.Where("user_id = ?", u.ID).Find(&sessions)
	for _, body := range []string{devicesRaw.Body.String(), historyRaw.Body.String()} {
		for _, secret := range []string{g.ID, lease.Connection.ID, connected, idle, "127.0.0.1", "presence-secret", sessions[0].ID, sessions[1].ID, sessions[0].DeviceID, sessions[1].DeviceID} {
			if strings.Contains(body, secret) {
				t.Fatalf("response leaked %q: %s", secret, body)
			}
		}
	}
	keys := func(m map[string]any) string {
		out := []string{}
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	var devices map[string]any
	_ = json.Unmarshal(devicesRaw.Body.Bytes(), &devices)
	if keys(devices) != "device_limit,lease_seconds,online,online_count,server_time,signed_in" || devices["device_limit"].(float64) != 2 || devices["online_count"].(float64) != 1 {
		t.Fatalf("devices: %v", devices)
	}
	online := devices["online"].([]any)[0].(map[string]any)
	if keys(online) != "app_version,connected_at,device,download_bytes,last_heartbeat_at,line,platform,upload_bytes" ||
		online["line"] != "JP1" || online["upload_bytes"].(float64) != 700 || online["download_bytes"].(float64) != 1300 || len(online["device"].(string)) != 6 ||
		online["last_heartbeat_at"] != e.now().Format(time.RFC3339) || online["connected_at"] != e.now().Add(-20*time.Second).Format(time.RFC3339) {
		t.Fatalf("online device: %v", online)
	}
	signedIn := devices["signed_in"].([]any)
	if len(signedIn) != 1 || keys(signedIn[0].(map[string]any)) != "app_version,device,last_seen_at,platform" {
		t.Fatalf("signed in: %v", signedIn)
	}
	var history map[string]any
	_ = json.Unmarshal(historyRaw.Body.Bytes(), &history)
	if keys(history) != "bucket_seconds,buckets,granularity_seconds,server_time,total_charged_units,total_download_bytes,total_upload_bytes,window_end,window_start" ||
		keys(history["buckets"].([]any)[0].(map[string]any)) != "charged_units,download_bytes,end,start,upload_bytes" || history["total_upload_bytes"].(float64) != 700 {
		t.Fatalf("history: %v", history)
	}
	var dashboard map[string]any
	_ = json.Unmarshal(dashboardRaw.Body.Bytes(), &dashboard)
	if dashboard["metering_connected"] != true || dashboard["online_devices"].(float64) != 1 || dashboard["device_limit"].(float64) != 2 {
		t.Fatalf("dashboard: %v", dashboard)
	}
	// Another user sees none of it.
	strangerDevices := e.webGet(e.web(e.user(5)), "/me/devices")
	if strangerDevices["online_count"].(float64) != 0 || len(strangerDevices["signed_in"].([]any)) != 0 {
		t.Fatalf("cross-user devices: %v", strangerDevices)
	}
}

// Round 2: admission gates of POST /connections and GET /config.
func TestAdmissionGatesPostgres(t *testing.T) {
	e := newPresenceEnv(t, func(c *config.Config) { c.ClientMinAppVersion = "2.1.0" })
	u := e.user(5)
	g := e.server("JP1")
	old := e.login(u, uuid.NewString()) // app_version 2.0.0
	e.want(e.connect(old, g.ID, "gate-key-0001"), 426, "client_upgrade_required")
	e.want(e.native("GET", "/config?node_id="+g.ID, old, nil), 426, "client_upgrade_required")

	e = newPresenceEnv(t, nil)
	u = e.user(5)
	g = e.server("JP1")
	token := e.login(u, uuid.NewString())
	// The only server is avoided, disabled or unknown: nothing to admit to.
	e.want(e.do(presenceCall{method: "POST", path: "/client/connections", token: token, headers: map[string]string{"Idempotency-Key": "gate-key-0002"},
		body: map[string]any{"group_id": g.ID, "last_sequence": 0, "avoid_node_id": g.ID}}), 503, "group_unavailable")
	e.want(e.connect(token, uuid.NewString(), "gate-key-0003"), 503, "group_unavailable")
	e.want(e.do(presenceCall{method: "POST", path: "/client/connections", token: token, body: map[string]any{"group_id": g.ID, "last_sequence": 0}}), 400, "invalid_idempotency_key")
	e.want(e.do(presenceCall{method: "POST", path: "/client/connections", token: token, headers: map[string]string{"Idempotency-Key": "gate-key-0004"}, body: map[string]any{"group_id": g.ID, "extra": 1}}), 400, "invalid_json")
	// Capacity (the single-server allocator until round 3).
	e.db.Model(&model.Node{}).Where("id = ?", g.ID).Update("max_connections", 1)
	e.want(e.connect(token, g.ID, "gate-key-0005"), 201, "")
	stranger := e.login(e.user(5), uuid.NewString())
	e.want(e.connect(stranger, g.ID, "gate-key-0006"), 409, "group_full")
	// A node-reported server is admitted through /grants only.
	agentNode := e.server("Agent1")
	if err := e.db.Create(&model.NodeAgent{ID: uuid.NewString(), NodeID: agentNode.ID, Name: "agent", CertFingerprint: strings.Repeat("c", 64), Enabled: true, Protocols: `["ss"]`}).Error; err != nil {
		t.Fatal(err)
	}
	e.want(e.connect(e.login(e.user(5), uuid.NewString()), agentNode.ID, "gate-key-0007"), 409, "grant_required")
	e.db.Model(&model.Node{}).Where("id = ?", g.ID).Update("enabled", false)
	e.want(e.connect(e.login(e.user(5), uuid.NewString()), g.ID, "gate-key-0008"), 503, "group_unavailable")
}

// Round 2: node_reported leases come and go with their grant, and node usage
// enters the same history.
func TestGrantLeaseAndHistoryPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	u := e.newUser(1<<30, 5) // room for two 64 MiB grant budgets
	node := e.addNode("N", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, node.ID, "grant-lease-0001"))
	var lease model.ConnectionLease
	if err := e.db.Take(&lease, "grant_id = ?", id).Error; err != nil || lease.Method != model.LeaseByGrant || lease.State != model.LeaseActive || lease.GroupID != node.ID {
		t.Fatalf("grant lease: %+v %v", lease, err)
	}
	e.sync(agent, []string{id}, nil)
	before := lease.ExpiresAt
	time.Sleep(20 * time.Millisecond)
	status := e.client("GET", "/session", token, nil, nil)
	var state nativeState
	_ = json.Unmarshal(status.Body.Bytes(), &state)
	_ = e.db.Take(&lease, "id = ?", lease.ID).Error
	if !lease.ExpiresAt.After(before) || state.ConnectionID != lease.ID || state.ConnectionState != "active" {
		t.Fatalf("GET /session did not renew the grant lease: %+v %+v", lease, state)
	}
	e.report(agent, usage{id, 1, 1, 3 << 20, 5 << 20, false}, 200)
	var history struct{ Up, Down, Units int64 }
	e.db.Model(&model.UsageBucket{}).Select("SUM(upload_bytes) AS up, SUM(download_bytes) AS down, SUM(charged_units) AS units").Where("user_id = ?", u.ID).Scan(&history)
	if history.Up != 3<<20 || history.Down != 5<<20 || history.Units != 8<<20*1000 {
		t.Fatalf("node usage history: %+v", history)
	}
	e.client("POST", "/grants/"+id+"/revoke", token, nil, map[string]any{})
	_ = e.db.Take(&lease, "id = ?", lease.ID).Error
	if lease.State != model.LeaseEnded || lease.EndReason != model.LeaseReleased {
		t.Fatalf("lease after revoke: %+v", lease)
	}
	// A grant ended by the control plane (session revoked) ends its lease in the same sync.
	second := e.login(u)
	id2 := grantID(t, e.createGrant(second, node.ID, "grant-lease-0002"))
	e.sync(agent, []string{id2}, nil)
	e.client("POST", "/logout", second, nil, map[string]any{})
	e.sync(agent, []string{id2}, nil)
	var ended model.ConnectionLease
	_ = e.db.Take(&ended, "grant_id = ?", id2).Error
	if ended.State != model.LeaseEnded || ended.EndReason != model.LeaseSessionRevoked {
		t.Fatalf("lease after the grant's session ended: %+v", ended)
	}
}
