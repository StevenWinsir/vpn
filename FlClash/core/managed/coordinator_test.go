package managed

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type accountFixture struct {
	mu           sync.Mutex
	base         Status
	reason       string
	yaml         string
	mode         string
	sessionError string
	sessions     map[string]Status
	next         int
	logins       atomic.Int64
	refreshes    atomic.Int64
	reports      atomic.Int64
	logouts      atomic.Int64
	hook         func(http.ResponseWriter, *http.Request) bool
}

func accountParams() LoginParams {
	return LoginParams{Email: "fixture@example.invalid", Password: "fixture-password-not-a-secret", DeviceID: "12345678-1234-4234-8234-123456789abc", Platform: "macos", AppVersion: "test"}
}

func newAccountFixture(t *testing.T) (*accountFixture, *Coordinator, *atomic.Int64) {
	t.Helper()
	fixture := &accountFixture{base: fixtureStatus(t, readContract(t)["vip_unbound"]), reason: "profile_required", yaml: "proxies: []\nrules: [MATCH,DIRECT]\n", sessions: map[string]Status{}}
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	stops := new(atomic.Int64)
	coordinator := newCoordinator(server.URL, func() http.RoundTripper { return NewTransport(nil) }, func() { stops.Add(1) })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = coordinator.Reset(ctx)
		server.Close()
	})
	return fixture, coordinator, stops
}

func (f *accountFixture) fresh(status Status) Status {
	now := time.Now().UTC()
	status.ServerTime = now
	if status.ExpiresAt.Before(now) {
		status.ExpiresAt = now.Add(12 * time.Hour)
	}
	expires := now.Add(2 * time.Hour)
	status.SubscriptionExpiresAt = &expires
	status.AuthorizationExpiresAt = nil
	status.CanConnect = false
	status.Reason = f.reason
	if status.ProfileVersion != "" && f.reason == "profile_required" {
		lease := now.Add(90 * time.Second)
		status.AuthorizationExpiresAt = &lease
		status.CanConnect, status.Reason = true, ""
	}
	return status
}

func (f *accountFixture) serve(w http.ResponseWriter, r *http.Request) {
	if f.hook != nil && f.hook(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	fail := func(code string) {
		w.WriteHeader(401)
		write(map[string]any{"error": map[string]string{"code": code, "message": "private server detail"}})
	}
	if r.URL.Path == "/login" {
		f.logins.Add(1)
		var params LoginParams
		if r.Method != "POST" || r.Header.Get("Authorization") != "" || json.NewDecoder(r.Body).Decode(&params) != nil || !params.Valid() {
			fail("invalid_client_login")
			return
		}
		f.next++
		token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(string(rune('A'+f.next)), 32)))
		status := f.fresh(f.base)
		status.SessionID, status.ProfileVersion = fmt.Sprintf("session-%d", f.next), ""
		f.sessions[token] = status
		write(map[string]any{"token": token, "user": User{ID: fmt.Sprint(f.next), Email: params.Email, Name: "Fixture"}, "session": status})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	status, ok := f.sessions[token]
	if !ok {
		fail("native_session_expired")
		return
	}
	switch r.URL.Path {
	case "/session":
		f.refreshes.Add(1)
		if f.sessionError != "" {
			fail(f.sessionError)
			return
		}
		status = f.fresh(status)
		f.sessions[token] = status
		write(status)
	case "/config":
		if f.reason != "profile_required" {
			fail(f.reason)
			return
		}
		digest := sha256.Sum256([]byte(f.yaml))
		status.ProfileVersion = hex.EncodeToString(digest[:])
		status = f.fresh(status)
		f.sessions[token] = status
		profile := Profile{YAML: f.yaml, Version: status.ProfileVersion, Session: status}
		switch f.mode {
		case "hash":
			profile.Version = strings.Repeat("0", 64)
		case "session_version":
			profile.Session.ProfileVersion = strings.Repeat("0", 64)
		case "session_id":
			profile.Session.SessionID = "another-session"
		case "denied":
			profile.Session.CanConnect = false
			profile.Session.Reason = "quota_exhausted"
			profile.Session.AuthorizationExpiresAt = nil
		case "empty":
			profile.YAML = ""
		}
		write(profile)
	case "/traffic":
		f.reports.Add(1)
		fail("traffic_sequence")
	case "/logout":
		f.logouts.Add(1)
		delete(f.sessions, token)
		write(map[string]bool{"logged_out": true})
	default:
		w.WriteHeader(404)
	}
}

func mustAccountLogin(t *testing.T, c *Coordinator) AccountSnapshot {
	t.Helper()
	state, err := c.Login(context.Background(), accountParams())
	if err != nil || state.User == nil {
		t.Fatalf("fixture login failed: %v / %s", err, state.ErrorCode)
	}
	return state
}

func TestCoordinatorLoginLoadAndNoPrematureAuthorization(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	state := mustAccountLogin(t, c)
	if state.Phase != "profile_required" || state.CanConnect {
		t.Fatal("VIP was not staged safely")
	}
	state, err := c.LoadConfig(context.Background(), state.Generation)
	if err != nil || state.Phase != "configuration_staged" || state.CanConnect || state.ProfileVersion == "" {
		t.Fatal("configuration gate opened too early")
	}
	encoded, err := json.Marshal(state)
	if err != nil || strings.Contains(string(encoded), "token") || strings.Contains(string(encoded), fixture.yaml) || strings.Contains(string(encoded), "password") {
		t.Fatal("snapshot contains private material")
	}
	if _, err = c.Flush(context.Background(), state.Generation); PublicError(err) != "meter_not_ready" || fixture.reports.Load() != 0 {
		t.Fatal("pre-meter flush fabricated a report")
	}
	state, err = c.Logout(context.Background(), state.Generation)
	if err != nil || state.User != nil || state.Session != nil || state.ProfileVersion != "" || state.CanConnect || fixture.logouts.Load() != 1 {
		t.Fatal("logout did not clear the account")
	}
}

func TestCoordinatorAccessStatesStayDistinct(t *testing.T) {
	for _, reason := range []string{"upgrade_required", "paid_vip_required", "subscription_expired", "quota_exhausted", "device_limit", "profile_required"} {
		t.Run(reason, func(t *testing.T) {
			fixture, c, _ := newAccountFixture(t)
			fixture.reason = reason
			state := mustAccountLogin(t, c)
			if state.CanConnect || state.Session.Reason != reason {
				t.Fatal("access reason was lost")
			}
			if reason == "profile_required" {
				if state.Phase != "profile_required" {
					t.Fatal("VIP misclassified as free")
				}
			} else if state.Phase != "restricted" || state.ErrorCode != reason {
				t.Fatal("restriction not exposed")
			}
		})
	}
}

func TestCoordinatorExplicitRefreshFindsPurchasedAccess(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	fixture.reason = "upgrade_required"
	state := mustAccountLogin(t, c)
	fixture.mu.Lock()
	fixture.reason = "profile_required"
	fixture.mu.Unlock()
	state, err := c.Refresh(context.Background(), state.Generation)
	if err != nil || state.Phase != "profile_required" || state.CanConnect {
		t.Fatal("purchase refresh failed")
	}
	state, err = c.LoadConfig(context.Background(), state.Generation)
	if err != nil || state.Phase != "configuration_staged" || state.CanConnect {
		t.Fatal("purchase configuration not safely staged")
	}
}

func TestCoordinatorRejectsBadConfigAndNeverReusesOldBytes(t *testing.T) {
	for _, mode := range []string{"hash", "session_version", "session_id", "denied", "empty"} {
		t.Run(mode, func(t *testing.T) {
			fixture, c, _ := newAccountFixture(t)
			state := mustAccountLogin(t, c)
			state, _ = c.LoadConfig(context.Background(), state.Generation)
			fixture.mu.Lock()
			fixture.mode = mode
			fixture.mu.Unlock()
			state, err := c.LoadConfig(context.Background(), state.Generation)
			if err != nil || state.ErrorCode != "invalid_server_response" || state.CanConnect || state.ProfileVersion != "" || c.profile != nil {
				t.Fatal("bad config retained a usable profile")
			}
		})
	}
}

func TestCoordinatorLargeConfigLimits(t *testing.T) {
	for _, size := range []int{70 << 10, 1 << 20, (1 << 20) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			fixture, c, _ := newAccountFixture(t)
			fixture.yaml = "#" + strings.Repeat("x", size-2) + "\n"
			state := mustAccountLogin(t, c)
			state, err := c.LoadConfig(context.Background(), state.Generation)
			if err != nil {
				t.Fatal(err)
			}
			if size <= 1<<20 && state.Phase != "configuration_staged" {
				t.Fatal("legal large config rejected")
			}
			if size > 1<<20 && (state.ErrorCode != "invalid_server_response" || c.profile != nil) {
				t.Fatal("oversized decoded config accepted")
			}
		})
	}
}

func TestCoordinatorSessionFailureClearsIdentityAndSecrets(t *testing.T) {
	for _, reason := range []string{"native_session_required", "native_session_expired", "subscription_changed"} {
		t.Run(reason, func(t *testing.T) {
			fixture, c, _ := newAccountFixture(t)
			state := mustAccountLogin(t, c)
			state, _ = c.LoadConfig(context.Background(), state.Generation)
			fixture.mu.Lock()
			fixture.sessionError = reason
			fixture.mu.Unlock()
			state, err := c.Refresh(context.Background(), state.Generation)
			if err != nil || state.User != nil || c.client != nil || c.profile != nil || state.ErrorCode != reason || state.CanConnect {
				t.Fatal("invalid session survived")
			}
		})
	}
}

func TestCoordinatorCachedSnapshotDoesNotRenewLease(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	state := mustAccountLogin(t, c)
	for index := 0; index < 100; index++ {
		c.Snapshot()
	}
	if fixture.refreshes.Load() != 0 {
		t.Fatal("UI snapshots renewed the session")
	}
	if c.Snapshot().Generation != state.Generation {
		t.Fatal("reads changed generation")
	}
}

func TestCoordinatorOwnsKeepAliveAndCancelsIt(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	c.tickInterval, c.heartbeatInterval = 5*time.Millisecond, 15*time.Millisecond
	state := mustAccountLogin(t, c)
	deadline := time.Now().Add(time.Second)
	for fixture.refreshes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fixture.refreshes.Load() < 2 {
		t.Fatal("Core keepalive did not run")
	}
	_, _ = c.Logout(context.Background(), state.Generation)
	count := fixture.refreshes.Load()
	time.Sleep(40 * time.Millisecond)
	if fixture.refreshes.Load() != count {
		t.Fatal("old keepalive survived logout")
	}
}

func TestCoordinatorConservativeIdleDeadline(t *testing.T) {
	_, c, stops := newAccountFixture(t)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c.clock = func() time.Time { return time.Unix(0, now.Load()) }
	state := mustAccountLogin(t, c)
	now.Add(int64(180 * time.Second))
	state = c.Snapshot()
	if state.User != nil || state.ErrorCode != "native_session_expired" || state.CanConnect || stops.Load() < 2 {
		t.Fatal("cached session survived its idle deadline")
	}
}

func TestCoordinatorLogoutCancelsLoginAndRejectsOldGeneration(t *testing.T) {
	fixture, c, stops := newAccountFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var first atomic.Bool
	fixture.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/login" && first.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return false
	}
	done := make(chan error, 1)
	go func() { _, err := c.Login(context.Background(), accountParams()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("login not started")
	}
	old := c.Snapshot().Generation
	if _, err := c.Login(context.Background(), accountParams()); PublicError(err) != "operation_in_progress" {
		t.Fatal("duplicate login was not coalesced")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := c.Reset(ctx)
	unblock()
	if err != nil || state.User != nil {
		t.Fatal("reset did not cancel login")
	}
	select {
	case err = <-done:
		if PublicError(err) != "operation_superseded" {
			t.Fatal("late login not rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("login cancellation hung")
	}
	state = mustAccountLogin(t, c)
	before := stops.Load()
	if _, err = c.Logout(context.Background(), old); PublicError(err) != "operation_superseded" {
		t.Fatal("stale logout accepted")
	}
	if _, err = c.LoadConfig(context.Background(), old); PublicError(err) != "operation_superseded" {
		t.Fatal("stale load accepted")
	}
	if c.Snapshot().Generation != state.Generation || c.Snapshot().User == nil || stops.Load() != before {
		t.Fatal("old request stopped the new account")
	}
}

func TestCoordinatorCallerCancellationRestoresLoginForm(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	fixture.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/login" {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return true
		}
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _, _ = c.Login(ctx, accountParams()); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("login not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled login hung")
	}
	state := c.Snapshot()
	if state.Busy || state.Phase != "signed_out" || state.User != nil {
		t.Fatal("cancelled request left login form locked")
	}
}

func TestCoordinatorReplacesAccountAfterOldCleanup(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	first := mustAccountLogin(t, c)
	first, _ = c.LoadConfig(context.Background(), first.Generation)
	second := mustAccountLogin(t, c)
	if second.User.ID == first.User.ID || second.Generation <= first.Generation || second.ProfileVersion != "" || second.CanConnect || fixture.logouts.Load() != 1 {
		t.Fatal("account replacement retained old authorization")
	}
}

func TestCoordinatorResponseCopiesCannotMutateOwner(t *testing.T) {
	_, c, _ := newAccountFixture(t)
	state := mustAccountLogin(t, c)
	state.User.Email = "changed@example.invalid"
	*state.Session.SubscriptionExpiresAt = time.Time{}
	if c.Snapshot().User.Email == state.User.Email || c.Snapshot().Session.SubscriptionExpiresAt.IsZero() {
		t.Fatal("public state aliases coordinator storage")
	}
}
