package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/network"
)

const (
	nodeA     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	nodeB     = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	fakeToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	secretPW  = "node-secret-password-1234"
	deviceUID = "33333333-3333-4333-8333-333333333333"
)

func sha(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

// profileYAML builds the exact shape the server's catalog emits for one node.
func profileYAML(name string) string {
	return "# catalog: 0000\nmode: rule\nproxies:\n    - name: " + name + "\n      type: ss\n      server: 192.0.2.10\n      port: 8388\n      cipher: aes-256-gcm\n      password: " + secretPW + "\n      udp: true\nproxy-groups:\n    - name: VPN\n      type: select\n      proxies:\n        - " + name + "\nrules:\n    - MATCH,VPN\n"
}

// fakeAPI is a scriptable stand-in for the v1 native API. Handlers read fields under mu.
type fakeAPI struct {
	t      *testing.T
	server *httptest.Server

	mu           sync.Mutex
	password     string
	session      apiclient.Session
	nodes        []apiclient.NodeChoice
	catalogRsn   string
	revision     int
	configYAML   func(name string) string // override to corrupt documents
	configErr    *apiError
	sessionErr   *apiError
	nodesErr     *apiError
	loginGate    chan struct{} // when set, /login blocks until closed
	loginEntered chan struct{}
	lastSeqSeen  string
	nodesSeen    atomic.Int32
	nodesNow     atomic.Int32
	nodesPeak    atomic.Int32
	sessionHits  atomic.Int32
	loginHits    atomic.Int32
	logoutHits   atomic.Int32
	configHits   atomic.Int32
	trafficHits  atomic.Int32
	longPoll     time.Duration

	// Ledger state for /traffic.
	trafficErr *apiError
	loseAcks   int // apply the next n batches, then fail the reply (a lost acknowledgement)
	reports    map[int64]apiclient.Counters
	bodies     []apiclient.Counters
}

type apiError struct {
	status int
	code   string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, password: "right-password", longPoll: 50 * time.Millisecond, configYAML: profileYAML, revision: 1, reports: map[int64]apiclient.Counters{}}
	now := time.Now().UTC()
	exp := now.Add(time.Hour)
	f.session = apiclient.Session{
		ServerTime: now, SubscriptionExpiresAt: &exp, SessionIdleTimeout: 180, SessionID: "11111111-1111-4111-8111-111111111111",
		ExpiresAt: now.Add(12 * time.Hour), CanConnect: false, Reason: "profile_required", RemainingBytes: 1 << 30, TotalBytes: 1 << 30,
		PlanName: "VIP", ReportInterval: 1, LeaseSeconds: 90, MeteringSource: "client_reported", RatePermille: 1000,
	}
	f.nodes = []apiclient.NodeChoice{
		{ID: nodeA, Name: "HK Dedicated", Region: "HK", LineType: "dedicated", RatePermille: 1000, Version: 1},
		{ID: nodeB, Name: "JP Standard", Region: "JP", LineType: "direct", RatePermille: 500, Version: 1},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/client/login", f.login)
	mux.HandleFunc("/api/v1/client/session", f.sessionHandler)
	mux.HandleFunc("/api/v1/client/nodes", f.nodesHandler)
	mux.HandleFunc("/api/v1/client/config", f.config)
	mux.HandleFunc("/api/v1/client/traffic", f.traffic)
	mux.HandleFunc("/api/v1/client/logout", func(w http.ResponseWriter, r *http.Request) {
		f.logoutHits.Add(1)
		f.auth(w, r)
		json.NewEncoder(w).Encode(map[string]bool{"logged_out": true})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAPI) base() string { return f.server.URL + "/api/v1/client" }

func writeError(w http.ResponseWriter, e apiError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": e.code, "message": "server text must never be shown"}})
}

func (f *fakeAPI) auth(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		writeError(w, apiError{401, "native_session_required"})
		return false
	}
	return true
}

func (f *fakeAPI) snapshotSession() apiclient.Session {
	s := f.session
	s.ServerTime = time.Now().UTC()
	if s.CanConnect {
		deadline := s.ServerTime.Add(time.Duration(s.LeaseSeconds) * time.Second)
		s.AuthorizationExpiresAt = &deadline
	} else {
		s.AuthorizationExpiresAt = nil
	}
	return s
}

func (f *fakeAPI) login(w http.ResponseWriter, r *http.Request) {
	f.loginHits.Add(1)
	var in apiclient.LoginRequest
	_ = json.NewDecoder(r.Body).Decode(&in)
	if f.loginEntered != nil {
		select {
		case f.loginEntered <- struct{}{}:
		default:
		}
	}
	if f.loginGate != nil {
		<-f.loginGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.Password != f.password || in.Platform != "macos" || in.DeviceID != deviceUID {
		writeError(w, apiError{401, "invalid_credentials"})
		return
	}
	json.NewEncoder(w).Encode(apiclient.LoginResponse{Token: fakeToken, User: apiclient.User{ID: "22222222-2222-4222-8222-222222222222", Email: in.Email, Name: "T", Role: "admin"}, Session: f.snapshotSession()})
}

func (f *fakeAPI) sessionHandler(w http.ResponseWriter, r *http.Request) {
	f.sessionHits.Add(1)
	if !f.auth(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionErr != nil {
		writeError(w, *f.sessionErr)
		return
	}
	json.NewEncoder(w).Encode(f.snapshotSession())
}

func (f *fakeAPI) catalogJSON() apiclient.Catalog {
	c := apiclient.Catalog{SessionID: f.session.SessionID, Reason: f.catalogRsn, Nodes: []apiclient.NodeChoice{}}
	if f.catalogRsn == "" {
		c.Nodes = append(c.Nodes, f.nodes...)
	}
	c.Revision = sha(fmt.Sprintf("%d|%s", f.revision, f.catalogRsn))
	return c
}

func (f *fakeAPI) nodesHandler(w http.ResponseWriter, r *http.Request) {
	f.nodesSeen.Add(1)
	now := f.nodesNow.Add(1)
	defer f.nodesNow.Add(-1)
	for {
		peak := f.nodesPeak.Load()
		if now <= peak || f.nodesPeak.CompareAndSwap(peak, now) {
			break
		}
	}
	if !f.auth(w, r) {
		return
	}
	deadline := time.Now().Add(f.longPoll)
	for {
		f.mu.Lock()
		if f.nodesErr != nil {
			e := *f.nodesErr
			f.mu.Unlock()
			writeError(w, e)
			return
		}
		c := f.catalogJSON()
		f.mu.Unlock()
		if r.URL.Query().Get("revision") != c.Revision || time.Now().After(deadline) {
			json.NewEncoder(w).Encode(c)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (f *fakeAPI) config(w http.ResponseWriter, r *http.Request) {
	f.configHits.Add(1)
	if !f.auth(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSeqSeen = r.URL.Query().Get("last_sequence")
	if _, err := strconv.ParseInt(f.lastSeqSeen, 10, 64); err != nil {
		writeError(w, apiError{409, "traffic_sequence"})
		return
	}
	if f.configErr != nil {
		writeError(w, *f.configErr)
		return
	}
	switch f.session.Reason {
	case "", "profile_required", "profile_changed":
	default: // like the real endpoint: an entitlement problem is a 403 carrying the reason
		writeError(w, apiError{403, f.session.Reason})
		return
	}
	id := r.URL.Query().Get("node_id")
	var chosen *apiclient.NodeChoice
	for i := range f.nodes {
		if f.nodes[i].ID == id {
			chosen = &f.nodes[i]
		}
	}
	if chosen == nil {
		writeError(w, apiError{503, "client_config_unavailable"})
		return
	}
	document := f.configYAML(chosen.Name)
	f.session.ProfileVersion = sha(document)
	f.session.Reason, f.session.CanConnect = "", true
	json.NewEncoder(w).Encode(apiclient.ConfigResponse{YAML: document, Version: sha(document), Session: f.snapshotSession(), Nodes: f.nodes, NodeID: id})
}

// traffic is a faithful miniature of the server's nativeTraffic handler.
func (f *fakeAPI) traffic(w http.ResponseWriter, r *http.Request) {
	f.trafficHits.Add(1)
	if !f.auth(w, r) {
		return
	}
	var in apiclient.Counters
	_ = json.NewDecoder(r.Body).Decode(&in)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.trafficErr != nil {
		writeError(w, *f.trafficErr)
		return
	}
	f.bodies = append(f.bodies, in)
	s := &f.session
	replayed := false
	if in.Sequence <= s.LastSequence {
		if f.reports[in.Sequence] != in {
			writeError(w, apiError{409, "traffic_conflict"})
			return
		}
		replayed = true
	} else {
		if in.Sequence != s.LastSequence+1 || in.UploadBytes < s.UploadBytes || in.DownloadBytes < s.DownloadBytes {
			writeError(w, apiError{409, "traffic_sequence"})
			return
		}
		delta := (in.UploadBytes - s.UploadBytes) + (in.DownloadBytes - s.DownloadBytes)
		s.UsedUnits += delta * s.RatePermille
		s.UploadBytes, s.DownloadBytes, s.LastSequence = in.UploadBytes, in.DownloadBytes, in.Sequence
		s.RemainingBytes = max(0, (s.TotalBytes*1000-s.UsedUnits)/1000)
		if s.RemainingBytes == 0 {
			s.CanConnect, s.Reason = false, "quota_exhausted"
		}
		f.reports[in.Sequence] = in
		if f.loseAcks > 0 {
			f.loseAcks--
			writeError(w, apiError{503, "client_service_unavailable"})
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"session": f.snapshotSession(), "replayed": replayed})
}

// Helpers for tests.

func (f *fakeAPI) mutate(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
	f.revision++
}

func newTestManager(t *testing.T, engine Engine) *Manager {
	t.Helper()
	m := New(Options{CoreVersion: "test", Engine: engine, BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond})
	t.Cleanup(func() { m.Shutdown(t.Context()) })
	return m
}

func (f *fakeAPI) params(password string) LoginParams {
	return LoginParams{Email: "test@test.com", Password: password, APIBase: f.base(), Channel: "development", DeviceID: deviceUID, AppVersion: "test"}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type recordingEngine struct {
	starts, stops, verifies, preflights atomic.Int32
	last                                atomic.Pointer[Profile]
	err                                 error // Start failure
	preflightErr, verifyErr             error
	verifyHook                          func()
	up, down                            atomic.Int64
	// trafficAtStart records how many /traffic requests had been made when Start ran.
	api            *fakeAPI
	trafficAtStart atomic.Int32
}

func (e *recordingEngine) Preflight(context.Context) (network.Interface, error) {
	e.preflights.Add(1)
	return network.Interface{}, e.preflightErr
}
func (e *recordingEngine) Start(_ context.Context, p Profile) error {
	e.starts.Add(1)
	e.last.Store(&p)
	if e.api != nil {
		e.trafficAtStart.Store(e.api.trafficHits.Load())
	}
	return e.err
}
func (e *recordingEngine) Verify(context.Context) error {
	e.verifies.Add(1)
	if e.verifyHook != nil {
		e.verifyHook()
	}
	return e.verifyErr
}
func (e *recordingEngine) Totals() (int64, int64, error) { return e.up.Load(), e.down.Load(), nil }
func (e *recordingEngine) Stop() error                   { e.stops.Add(1); return nil }
