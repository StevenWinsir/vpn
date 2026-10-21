package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

const (
	nodeDedicated = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	nodeOrdinary  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testToken     = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testDevice    = "33333333-3333-4333-8333-333333333333"
)

// fakeServer is a faithful miniature of the real native API for the harness's own tests:
// the same /traffic idempotency, rate binding and remaining-balance formula.
type fakeServer struct {
	server *httptest.Server

	mu          sync.Mutex
	session     apiclient.Session
	nodes       []apiclient.NodeChoice
	revision    int
	reports     map[int64]apiclient.Counters
	doubleBill  bool // a broken server that charges replays again
	trafficHits int
	// Online leases (contract native-v3).
	leases, releases int
}

func sha(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func newFakeServer(t *testing.T, total int64) *fakeServer {
	t.Helper()
	now := time.Now().UTC()
	exp := now.Add(time.Hour)
	f := &fakeServer{revision: 1, reports: map[int64]apiclient.Counters{}}
	f.session = apiclient.Session{ServerTime: now, SubscriptionExpiresAt: &exp, SessionIdleTimeout: 180, SessionID: "11111111-1111-4111-8111-111111111111",
		ExpiresAt: now.Add(12 * time.Hour), CanConnect: false, Reason: "profile_required", RemainingBytes: total, TotalBytes: total,
		PlanName: "VIP", ReportInterval: 1, LeaseSeconds: 90, MeteringSource: "client_reported", RatePermille: 1000}
	f.nodes = []apiclient.NodeChoice{
		{ID: nodeDedicated, Name: "HK Dedicated", Region: "HK", LineType: "dedicated", RatePermille: 1000, Version: 1},
		{ID: nodeOrdinary, Name: "JP Standard", Region: "JP", LineType: "direct", RatePermille: 500, Version: 1},
	}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			fail401(w)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v1/client/login", func(w http.ResponseWriter, r *http.Request) {
		var in apiclient.LoginRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if in.Password != "pw" || in.DeviceID != testDevice {
			fail401(w)
			return
		}
		_ = json.NewEncoder(w).Encode(apiclient.LoginResponse{Token: testToken, User: apiclient.User{ID: "u", Email: in.Email, Role: "admin"}, Session: f.snapshot()})
	})
	mux.HandleFunc("/api/v1/client/session", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			f.mu.Lock()
			defer f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(f.snapshot())
		}
	})
	mux.HandleFunc("/api/v1/client/nodes", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		deadline := time.Now().Add(100 * time.Millisecond)
		for {
			f.mu.Lock()
			c := apiclient.Catalog{SessionID: f.session.SessionID, Nodes: append([]apiclient.NodeChoice{}, f.nodes...), Revision: sha(fmt.Sprint(f.revision, f.nodes))}
			f.mu.Unlock()
			if r.URL.Query().Get("revision") != c.Revision || time.Now().After(deadline) {
				_ = json.NewEncoder(w).Encode(c)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	mux.HandleFunc("/api/v1/client/config", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.URL.Query().Get("node_id")
		for _, n := range f.nodes {
			if n.ID == id {
				doc := "# catalog: 0000\nmode: rule\nproxies:\n    - name: " + n.Name + "\n      type: ss\n      server: 192.0.2.10\n      port: 8388\n      cipher: aes-256-gcm\n      password: node-secret-password-1234\n      udp: true\nproxy-groups:\n    - name: VPN\n      type: select\n      proxies:\n        - " + n.Name + "\nrules:\n    - MATCH,VPN\n"
				f.session.RatePermille, f.session.Reason, f.session.CanConnect, f.session.ProfileVersion = n.RatePermille, "", true, sha(doc)
				_ = json.NewEncoder(w).Encode(apiclient.ConfigResponse{YAML: doc, Version: sha(doc), Session: f.snapshot(), Nodes: f.nodes, NodeID: id})
				return
			}
		}
		http.Error(w, `{"error":{"code":"client_config_unavailable"}}`, http.StatusServiceUnavailable)
	})
	// Contract native-v3: the Core is admitted when it connects and releases afterwards.
	mux.HandleFunc("/api/v1/client/connections", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var in apiclient.ConnectionRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, n := range f.nodes {
			if n.ID == in.GroupID {
				doc := "# catalog: 0000\nmode: rule\nproxies:\n    - name: " + n.Name + "\n      type: ss\n      server: 192.0.2.10\n      port: 8388\n      cipher: aes-256-gcm\n      password: node-secret-password-1234\n      udp: true\nproxy-groups:\n    - name: VPN\n      type: select\n      proxies:\n        - " + n.Name + "\nrules:\n    - MATCH,VPN\n"
				f.leases++
				id := fmt.Sprintf("77777777-7777-4777-8777-%012d", f.leases)
				f.session.RatePermille, f.session.Reason, f.session.CanConnect, f.session.ProfileVersion = n.RatePermille, "", true, sha(doc)
				f.session.ConnectionID, f.session.ConnectionState = id, apiclient.ConnectionActive
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(apiclient.ConnectionResponse{Connection: apiclient.Connection{ID: id, GroupID: n.ID, State: apiclient.ConnectionActive, ExpiresAt: time.Now().Add(90 * time.Second)},
					NodeID: "99999999-9999-4999-8999-999999999999", YAML: doc, Version: sha(doc), Nodes: f.nodes, Session: f.snapshot()})
				return
			}
		}
		http.Error(w, `{"error":{"code":"group_unavailable"}}`, http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/api/v1/client/connections/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id, _ := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/client/connections/"), "/release")
		f.mu.Lock()
		defer f.mu.Unlock()
		f.releases++
		if f.session.ConnectionID == id {
			f.session.ConnectionState = apiclient.ConnectionEnded
		}
		_ = json.NewEncoder(w).Encode(apiclient.ReleaseResponse{Connection: apiclient.Connection{ID: id, State: apiclient.ConnectionEnded}, Session: f.snapshot()})
	})
	mux.HandleFunc("/api/v1/client/traffic", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var in apiclient.Counters
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.trafficHits++
		s := &f.session
		replayed := in.Sequence <= s.LastSequence
		if replayed {
			if f.reports[in.Sequence] != in {
				http.Error(w, `{"error":{"code":"traffic_conflict"}}`, http.StatusConflict)
				return
			}
			if f.doubleBill {
				s.UsedUnits += (in.UploadBytes + in.DownloadBytes) * s.RatePermille / 100
			}
		} else {
			delta := (in.UploadBytes - s.UploadBytes) + (in.DownloadBytes - s.DownloadBytes)
			s.UsedUnits += delta * s.RatePermille
			s.UploadBytes, s.DownloadBytes, s.LastSequence = in.UploadBytes, in.DownloadBytes, in.Sequence
			f.reports[in.Sequence] = in
		}
		s.RemainingBytes = max(0, (s.TotalBytes*1000-s.UsedUnits)/1000)
		_ = json.NewEncoder(w).Encode(map[string]any{"session": f.snapshot(), "replayed": replayed})
	})
	mux.HandleFunc("/api/v1/client/logout", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"logged_out": true})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func fail401(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"invalid_credentials"}}`))
}

func (f *fakeServer) snapshot() apiclient.Session {
	s := f.session
	s.ServerTime = time.Now().UTC()
	if s.CanConnect {
		deadline := s.ServerTime.Add(time.Duration(s.LeaseSeconds) * time.Second)
		s.AuthorizationExpiresAt = &deadline
	}
	return s
}

// stubEngine stands in for Mihomo: counters move only when the transfer stub says so.
type stubEngine struct{ up, down atomic.Int64 }

func (e *stubEngine) Preflight(context.Context) (network.Interface, error) {
	return network.Interface{}, nil
}
func (e *stubEngine) Start(context.Context, managed.Profile) error { return nil }
func (e *stubEngine) Verify(context.Context) error                 { return nil }
func (e *stubEngine) Totals() (int64, int64, error)                { return e.up.Load(), e.down.Load(), nil }
func (e *stubEngine) Stop() error                                  { return nil }

type rig struct {
	h      *harness
	f      *fakeServer
	engine *stubEngine
}

// newRig wires the real Manager and the real lossy transport to the fake API. The transfer
// stub adds the body plus fixed framing to the engine counters, like a real flow would.
func newRig(t *testing.T, total int64) *rig {
	t.Helper()
	f := newFakeServer(t, total)
	eng := &stubEngine{}
	lossy := newLossy()
	m := managed.New(managed.Options{
		CoreVersion: "test", Engine: eng, BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond,
		MonitorInterval: 20 * time.Millisecond, SettleTimeout: 3 * time.Second,
		NewClient: func(base *url.URL) *apiclient.Client {
			c := apiclient.New(base)
			c.WrapTransport(lossy.wrap)
			return c
		},
	})
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	var sinkUp, sinkDown atomic.Int64
	h := &harness{
		m: m, lossy: lossy, rec: &recorder{},
		cfg: config{apiBase: f.server.URL + "/api/v1/client", channel: "development", email: "test@test.com", password: "pw", deviceID: testDevice,
			upload: 20 << 20, download: 80 << 20, background: 2048 << 10, settleWait: 100 * time.Millisecond},
		xfer: func(_ context.Context, up, down int64) (transferResult, error) {
			eng.up.Add(up + up/200)
			eng.down.Add(down + down/150)
			sinkUp.Add(up)
			sinkDown.Add(down)
			return transferResult{Upload: up, Download: down}, nil
		},
		stats: func(context.Context) (sinkStats, error) {
			return sinkStats{UpReceived: sinkUp.Load(), DownSent: sinkDown.Load()}, nil
		},
	}
	return &rig{h: h, f: f, engine: eng}
}

func (r *rig) results(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, c := range r.h.rec.cases {
		out[c.ID] = c.Result
		t.Logf("%-16s %-5s %s", c.ID, c.Result, c.Observed)
	}
	return out
}

func requirePass(t *testing.T, got map[string]string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if got[id] != "pass" {
			t.Errorf("%s = %q, want pass", id, got[id])
		}
	}
}

func TestBillingRunMeasuresEachRateToTheUnit(t *testing.T) {
	r := newRig(t, 10<<30)
	r.h.run(t.Context())
	got := r.results(t)
	requirePass(t, got, "A-LOGIN", "B01-n1-connect", "B01-n1-live", "B01-n1-sink", "B01-n1-ledger", "B01-n1-body",
		"B01-n2-connect", "B01-n2-ledger", "B01-n2-body", "B01-rates")
	if failed, blocked := r.h.rec.counts(); failed+blocked != 0 {
		t.Fatalf("%d failed, %d blocked", failed, blocked)
	}
	// The two cycles ran at different rates and both charged exactly their own rate.
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if r.f.session.UsedUnits == 0 {
		t.Fatal("nothing was charged")
	}
}

func TestBillingRunFailsAgainstAServerThatChargesTheWrongAmount(t *testing.T) {
	r := newRig(t, 10<<30)
	r.f.mu.Lock()
	r.f.nodes[0].RatePermille = 1000
	r.f.mu.Unlock()
	// A server that bills 1.5x while advertising 1x must be caught by the ledger check.
	r.h.cfg.nodes = []string{nodeDedicated}
	r.h.run(t.Context())
	if got := r.results(t); got["B01-n1-ledger"] != "pass" {
		t.Fatalf("control run: %v", got)
	}
	bad := newRig(t, 10<<30)
	bad.f.mu.Lock()
	bad.f.session.RatePermille = 1000
	bad.f.mu.Unlock()
	bad.f.nodes[0].RatePermille = 1000
	bad.h.cfg.nodes = []string{nodeDedicated}
	bad.h.cfg.upload, bad.h.cfg.download = 3<<20, 5<<20
	// Tamper after login: the catalog says 1000 but the ledger will charge double.
	original := bad.h.xfer
	bad.h.xfer = func(ctx context.Context, up, down int64) (transferResult, error) {
		bad.f.mu.Lock()
		bad.f.session.RatePermille = 2000
		bad.f.mu.Unlock()
		return original(ctx, up, down)
	}
	bad.h.run(t.Context())
	if got := bad.results(t); got["B01-n1-ledger"] != "fail" {
		t.Fatalf("a mischarging server passed: %v", got)
	}
}

func TestAckLossIsRetriedIdenticallyAndChargedOnce(t *testing.T) {
	r := newRig(t, 10<<30)
	r.h.cfg.ackLoss = true
	r.h.run(t.Context())
	got := r.results(t)
	requirePass(t, got, "B02-connect", "B02-baseline", "B02-lost-connect", "B02-retry", "B02-once")
	bodies, dropped := r.h.lossy.trafficBodies()
	if dropped < 0 || dropped+1 >= len(bodies) || bodies[dropped] != bodies[dropped+1] {
		t.Fatalf("retry not identical: dropped=%d bodies=%v", dropped, bodies)
	}
}

func TestAckLossCatchesAServerThatChargesReplaysAgain(t *testing.T) {
	r := newRig(t, 10<<30)
	r.f.doubleBill = true
	r.h.cfg.ackLoss = true
	r.h.run(t.Context())
	if got := r.results(t); got["B02-once"] != "fail" {
		t.Fatalf("a double charge passed: %v", got)
	}
}

func TestBlockedWhenQuotaIsTooSmallForTheTransfer(t *testing.T) {
	r := newRig(t, 50<<20)
	r.h.run(t.Context())
	if got := r.results(t); got["B01-n1-quota"] != "blocked" {
		t.Fatalf("%v", got)
	}
}

func TestWatchStopSeesTheTunnelCloseWhenTheNodeIsEdited(t *testing.T) {
	r := newRig(t, 10<<30)
	r.h.cfg.watchStop, r.h.cfg.expectStop = 15*time.Second, "profile_changed"
	r.h.cfg.nodes = []string{nodeDedicated}
	go func() {
		time.Sleep(700 * time.Millisecond)
		r.f.mu.Lock()
		r.f.nodes[0].RatePermille, r.f.nodes[0].Version = 2000, 2
		r.f.revision++
		r.f.mu.Unlock()
	}()
	r.h.run(t.Context())
	got := r.results(t)
	requirePass(t, got, "A04-watch", "A04-rebind")
}

func TestWatchStopFailsWhenNothingStopsTheTunnel(t *testing.T) {
	r := newRig(t, 10<<30)
	r.h.cfg.watchStop = 1500 * time.Millisecond
	r.h.cfg.nodes = []string{nodeDedicated}
	r.h.run(t.Context())
	if got := r.results(t); got["A04-watch"] != "fail" {
		t.Fatalf("%v", got)
	}
}

func TestWatchDriveExhaustsASmallQuotaAndStopsTheTunnel(t *testing.T) {
	r := newRig(t, 10<<20)
	r.h.cfg.watchStop, r.h.cfg.watchDrive, r.h.cfg.expectStop = 30*time.Second, true, "quota_exhausted"
	r.h.cfg.nodes = []string{nodeDedicated}
	r.h.run(t.Context())
	requirePass(t, r.results(t), "A04-watch")
}

func TestWatchDriveReportsWhatItDroveWhenNothingStops(t *testing.T) {
	r := newRig(t, 10<<30)
	r.h.cfg.watchStop, r.h.cfg.watchDrive = 600*time.Millisecond, true
	r.h.cfg.nodes = []string{nodeDedicated}
	fast := r.h.xfer
	r.h.xfer = func(ctx context.Context, up, down int64) (transferResult, error) { // a line is not instantaneous
		time.Sleep(100 * time.Millisecond)
		return fast(ctx, up, down)
	}
	r.h.run(t.Context())
	r.results(t)
	for _, c := range r.h.rec.cases {
		if c.ID == "A04-watch" {
			if c.Result != "fail" || !strings.Contains(c.Observed, "drove ") || !strings.Contains(c.Observed, "estimated remaining") {
				t.Fatalf("%+v", c)
			}
			return
		}
	}
	t.Fatal("no A04-watch case")
}

func TestLoginFailureIsBlockedNotFailed(t *testing.T) {
	r := newRig(t, 10<<30)
	r.h.cfg.password = "wrong"
	r.h.run(t.Context())
	if got := r.results(t); got["A-LOGIN"] != "blocked" {
		t.Fatalf("%v", got)
	}
}

func TestChooseNodesTakesOnePerDistinctRate(t *testing.T) {
	nodes := []managed.NodeView{{ID: "a", Name: "A", RatePermille: 500}, {ID: "b", Name: "B", RatePermille: 1000}, {ID: "c", Name: "C", RatePermille: 1000}, {ID: "d", Name: "D", RatePermille: 2000}}
	got := chooseNodes(nodes, nil)
	if len(got) != 2 || got[0].ID != "d" || got[1].ID != "b" {
		t.Fatalf("%+v", got)
	}
	if got := chooseNodes(nodes, []string{"C", "a"}); len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Fatalf("%+v", got)
	}
	if got := chooseNodes(nodes, []string{"missing"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
