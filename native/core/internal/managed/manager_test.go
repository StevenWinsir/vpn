package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

func code(t *testing.T, err error) string {
	t.Helper()
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *apiclient.Error, got %v", err)
	}
	return apiErr.Code
}

func login(t *testing.T, f *fakeAPI, engine Engine) *Manager {
	t.Helper()
	m := newTestManager(t, engine)
	if _, err := m.Login(t.Context(), f.params(f.password)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catalog", func() bool { return m.Snapshot().CatalogRevision != "" })
	return m
}

func selectA(t *testing.T, m *Manager) Snapshot {
	t.Helper()
	snap := m.Snapshot()
	out, err := m.SelectNode(t.Context(), nodeA, snap.CatalogRevision)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// C01: the shared fixture is the wire contract; every response must decode strictly.
func TestContractFixtureShapes(t *testing.T) {
	data, err := os.ReadFile("../../../../shared/contracts/native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Cases map[string]struct {
			Status   int             `json:"status"`
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	strictDecode := func(name string, raw json.RawMessage, into any) {
		t.Helper()
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(into); err != nil {
			t.Fatalf("%s does not match the Go types: %v", name, err)
		}
	}
	strictDecode("login", contract.Cases["login"].Response, &apiclient.LoginResponse{})
	for _, name := range []string{"free", "vip_unbound", "session_expired"} {
		if name == "session_expired" {
			continue
		}
		strictDecode(name, contract.Cases[name].Response, &apiclient.Session{})
	}
	var config apiclient.ConfigResponse
	strictDecode("vip_ready", contract.Cases["vip_ready"].Response, &config)
	for _, name := range []string{"traffic_ready", "quota_exhausted"} {
		strictDecode(name, contract.Cases[name].Response, &struct {
			Replayed bool              `json:"replayed"`
			Session  apiclient.Session `json:"session"`
		}{})
	}
	// The fixture's config must pass the real verification path, hash included.
	if _, err := ParseProfile("n", "Loopback Fixture", config.Version, config.YAML); err != nil {
		t.Fatalf("fixture config rejected: %v", err)
	}
	if apiErr := failureFromFixture(contract.Cases["session_expired"].Status, contract.Cases["session_expired"].Response); apiErr.Code != "native_session_expired" || apiclient.Message(apiErr.Code) == "" {
		t.Fatalf("error envelope mapping broken: %+v", apiErr)
	}
}

func failureFromFixture(status int, body []byte) *apiclient.Error {
	var envelope struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	return &apiclient.Error{Status: status, Code: envelope.Error.Code}
}

func TestLoginSuccessAndSnapshotRedaction(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	snap := selectA(t, m)
	if snap.State != StateReady || !snap.ConfigReady || snap.SelectedNodeID != nodeA || snap.Entitlement == nil || !snap.Entitlement.CanConnect {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.User == nil || snap.User.Role != "admin" || len(snap.Nodes) != 2 {
		t.Fatalf("user or nodes missing: %+v", snap)
	}
	if f.lastSeqSeen != "0" {
		t.Fatalf("last_sequence not sent: %q", f.lastSeqSeen)
	}
	raw, _ := json.Marshal(snap)
	for _, secret := range []string{fakeToken, secretPW, "192.0.2.10", "8388", "proxies:", "right-password", "aes-256-gcm", "password"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("snapshot leaks %q: %s", secret, raw)
		}
	}
	if strings.Contains(snap.Profile().String(), secretPW) {
		t.Fatal("profile prints secrets")
	}
}

// Profile is a test helper exposing the held value to check its formatting guards.
func (s Snapshot) Profile() Profile {
	return Profile{NodeID: "x", Proxy: map[string]any{"password": secretPW}}
}

func TestLoginFailures(t *testing.T) {
	f := newFakeAPI(t)
	m := newTestManager(t, nil)
	snap, err := m.Login(t.Context(), f.params("wrong"))
	if code(t, err) != "invalid_credentials" || snap.State != StateSignedOut || snap.LastError == nil || snap.LastError.Message != "邮箱或密码不正确" {
		t.Fatalf("wrong password: %v %+v", err, snap)
	}
	if strings.Contains(snap.LastError.Message, "server text") {
		t.Fatal("server message leaked to the UI")
	}
	for name, base := range map[string]string{
		"plain http non-loopback": "http://demo.example.invalid/api/v1/client",
		"wrong path":              f.server.URL + "/api/v2/client",
		"credentials in url":      "http://u:p@127.0.0.1:1/api/v1/client",
		"query":                   f.base() + "?x=1",
	} {
		p := f.params(f.password)
		p.APIBase = base
		if _, err := m.Login(t.Context(), p); code(t, err) != "invalid_api_base" {
			t.Fatalf("%s accepted", name)
		}
	}
	p := f.params(f.password)
	p.Channel = "release" // http loopback is development-only
	if _, err := m.Login(t.Context(), p); code(t, err) != "invalid_api_base" {
		t.Fatal("release channel accepted plain http")
	}
	if f.loginHits.Load() != 1 {
		t.Fatalf("credentials were sent to a rejected base: %d logins", f.loginHits.Load())
	}
}

func TestAPIUnavailable(t *testing.T) {
	f := newFakeAPI(t)
	p := f.params(f.password)
	f.server.Close()
	m := newTestManager(t, nil)
	snap, err := m.Login(t.Context(), p)
	if code(t, err) != "network_unreachable" || snap.State != StateSignedOut || snap.LastError.Code != "network_unreachable" {
		t.Fatalf("%v %+v", err, snap)
	}
}

// C04/C03: every refusal path must leave Engine.Start untouched.
func TestConnectGates(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *fakeAPI, m *Manager)
		expect string
	}{
		{"no plan (admin without subscription)", func(f *fakeAPI, _ *Manager) {
			f.mutate(func() { f.session.Reason, f.session.CanConnect = "upgrade_required", false })
		}, "upgrade_required"},
		{"subscription expired", func(f *fakeAPI, _ *Manager) {
			f.mutate(func() { f.session.Reason, f.session.CanConnect = "subscription_expired", false })
		}, "subscription_expired"},
		{"zero quota", func(f *fakeAPI, _ *Manager) {
			f.mutate(func() { f.session.Reason, f.session.CanConnect, f.session.RemainingBytes = "quota_exhausted", false, 0 })
		}, "quota_exhausted"},
		{"test entitlement not honoured", func(f *fakeAPI, _ *Manager) {
			f.mutate(func() { f.session.Reason, f.session.CanConnect = "paid_vip_required", false })
		}, "paid_vip_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			engine := &recordingEngine{}
			m := login(t, f, engine)
			selectA(t, m)
			tc.setup(f, m)
			snap, err := m.Connect(t.Context())
			if code(t, err) != tc.expect {
				t.Fatalf("got %v want %s", err, tc.expect)
			}
			if engine.starts.Load() != 0 {
				t.Fatal("tunnel started without authorization")
			}
			if snap.Entitlement == nil || snap.Entitlement.CanConnect || snap.State == StateConnected {
				t.Fatalf("snapshot still offers connection: %+v", snap)
			}
		})
	}
}

func TestConnectRequiresSelectionAndSession(t *testing.T) {
	f := newFakeAPI(t)
	engine := &recordingEngine{}
	m := newTestManager(t, engine)
	if _, err := m.Connect(t.Context()); code(t, err) != "not_signed_in" {
		t.Fatal("connect without login")
	}
	if _, err := m.Login(t.Context(), f.params(f.password)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect(t.Context()); code(t, err) != "profile_required" {
		t.Fatal("connect without a selected node")
	}
	if engine.starts.Load() != 0 {
		t.Fatal("Start called")
	}
}

func TestConnectHappyPathAndStageTwoEngine(t *testing.T) {
	f := newFakeAPI(t)
	engine := &recordingEngine{}
	m := login(t, f, engine)
	selectA(t, m)
	snap, err := m.Connect(t.Context())
	if err != nil || snap.State != StateConnected || snap.TunnelHealth != "up" {
		t.Fatalf("connect: %v %+v", err, snap)
	}
	profile := engine.last.Load()
	if engine.starts.Load() != 1 || profile == nil || profile.Proxy["server"] != "192.0.2.10" || profile.NodeID != nodeA {
		t.Fatal("engine did not receive the validated profile")
	}
	if snap = m.Disconnect(); snap.State != StateReady || engine.stops.Load() == 0 {
		t.Fatalf("disconnect: %+v", snap)
	}
	// The shipped engine refuses: a passing gate must not pretend a tunnel exists.
	m2 := login(t, f, UnavailableEngine())
	selectA(t, m2)
	snap, err = m2.Connect(t.Context())
	if code(t, err) != "tun_unavailable" || snap.State != StateReady || snap.TunnelHealth != "idle" {
		t.Fatalf("stage-2 engine: %v %+v", err, snap)
	}
}

// Config verification: hash, shape, unknown keys and the shared policy.
func TestConfigRejection(t *testing.T) {
	bad := map[string]func(name string) string{
		"extra top-level key": func(n string) string { return profileYAML(n) + "external-controller: 0.0.0.0:9090\n" },
		"two proxies": func(n string) string {
			return strings.Replace(profileYAML(n), "proxy-groups:", "    - name: Other\n      type: ss\n      server: 192.0.2.11\n      port: 1\n      cipher: aes-256-gcm\n      password: x\nproxy-groups:", 1)
		},
		"unsupported option": func(n string) string {
			return strings.Replace(profileYAML(n), "udp: true", "udp: true\n      plugin-file: /etc/passwd", 1)
		},
		"unsupported type": func(n string) string { return strings.Replace(profileYAML(n), "type: ss", "type: script", 1) },
		"DIRECT fallback":  func(n string) string { return strings.Replace(profileYAML(n), "MATCH,VPN", "MATCH,DIRECT", 1) },
		"wrong node name":  func(string) string { return profileYAML("Someone Else") },
	}
	for name, build := range bad {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.configYAML = build
			m := login(t, f, nil)
			out, err := m.SelectNode(t.Context(), nodeA, m.Snapshot().CatalogRevision)
			if code(t, err) != "config_rejected" || out.ConfigReady {
				t.Fatalf("accepted: %v %+v", err, out)
			}
			if _, err = m.Connect(t.Context()); err == nil {
				t.Fatal("connected with a rejected config")
			}
		})
	}
	t.Run("version hash mismatch", func(t *testing.T) {
		if _, err := ParseProfile("n", "HK Dedicated", strings.Repeat("0", 64), profileYAML("HK Dedicated")); err == nil {
			t.Fatal("hash mismatch accepted")
		}
	})
}

func TestStaleCatalogAndUnknownNode(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	if _, err := m.SelectNode(t.Context(), nodeA, strings.Repeat("0", 64)); code(t, err) != "stale_catalog" {
		t.Fatal("stale revision accepted")
	}
	if _, err := m.SelectNode(t.Context(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc", m.Snapshot().CatalogRevision); code(t, err) != "node_unavailable" {
		t.Fatal("unknown node accepted")
	}
	if f.configHits.Load() != 0 {
		t.Fatal("config fetched for an invalid selection")
	}
}

// C06 / stage-2 acceptance: admin edits reach the client and invalidate old config.
func TestAdminChangesSyncAndInvalidate(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	selectA(t, m)
	started := time.Now()

	f.mutate(func() { f.nodes[0].Name, f.nodes[0].RatePermille, f.nodes[0].Version = "HK Premium", 2000, 2 })
	waitFor(t, "rename + rate to reach the client", func() bool {
		s := m.Snapshot()
		return len(s.Nodes) == 2 && s.Nodes[0].Name == "HK Premium" && s.Nodes[0].RatePermille == 2000
	})
	t.Logf("admin change observed after %v", time.Since(started))
	snap := m.Snapshot()
	if snap.ConfigReady || snap.Entitlement.CanConnect {
		t.Fatalf("old config survived a node edit: %+v", snap)
	}
	if snap.LastError == nil || snap.LastError.Code != "profile_changed" {
		t.Fatalf("no profile_changed notice: %+v", snap.LastError)
	}

	// Re-select, then the admin disables the node: selection must clear.
	if _, err := m.SelectNode(t.Context(), nodeA, m.Snapshot().CatalogRevision); err != nil {
		t.Fatal(err)
	}
	f.mutate(func() { f.nodes = f.nodes[1:] })
	waitFor(t, "node removal", func() bool { return len(m.Snapshot().Nodes) == 1 })
	if s := m.Snapshot(); s.SelectedNodeID != "" || s.ConfigReady {
		t.Fatalf("removed node still selected: %+v", s)
	}

	// Plan revoked: the catalog empties and connecting is refused straight away.
	f.mutate(func() { f.catalogRsn = "subscription_expired" })
	waitFor(t, "entitlement loss", func() bool { return len(m.Snapshot().Nodes) == 0 })
	if _, err := m.Connect(t.Context()); err == nil {
		t.Fatal("connect allowed after entitlement loss")
	}
}

// Only one watcher per session, and none from a previous session survives.
func TestSingleWatcherAcrossRelogin(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	for i := 0; i < 3; i++ {
		if _, err := m.Login(t.Context(), f.params(f.password)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	if peak := f.nodesPeak.Load(); peak > 2 {
		// 2 allows the instant an old request is still unwinding after cancellation.
		t.Fatalf("concurrent /nodes requests peaked at %d", peak)
	}
	m.Logout(t.Context())
	waitFor(t, "watchers to stop", func() bool { return f.nodesNow.Load() == 0 })
	hits := f.nodesSeen.Load()
	time.Sleep(200 * time.Millisecond)
	if f.nodesSeen.Load() != hits {
		t.Fatal("watcher kept polling after logout")
	}
}

// A reply that arrives after the session was replaced must not change state.
func TestLateLoginReplyDiscarded(t *testing.T) {
	f := newFakeAPI(t)
	f.loginGate = make(chan struct{})
	f.loginEntered = make(chan struct{}, 1)
	m := newTestManager(t, nil)
	done := make(chan error, 1)
	go func() { _, err := m.Login(t.Context(), f.params(f.password)); done <- err }()
	<-f.loginEntered
	m.Logout(t.Context()) // the user gave up (or switched account) while the request was in flight
	close(f.loginGate)
	err := <-done
	if code(t, err) != "stale_generation" {
		t.Fatalf("late reply applied: %v", err)
	}
	if s := m.Snapshot(); s.State != StateSignedOut || s.User != nil {
		t.Fatalf("late reply revived the session: %+v", s)
	}
}

func TestSessionTerminationClearsEverything(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	selectA(t, m)
	f.mu.Lock()
	f.sessionErr = &apiError{401, "native_session_expired"}
	f.mu.Unlock()
	waitFor(t, "sign-out", func() bool { return m.Snapshot().State == StateSignedOut })
	s := m.Snapshot()
	if s.LastError == nil || s.LastError.Code != "native_session_expired" || s.User != nil || len(s.Nodes) != 0 || s.ConfigReady {
		t.Fatalf("temporary identity survived: %+v", s)
	}
	if _, err := m.Connect(t.Context()); code(t, err) != "not_signed_in" {
		t.Fatal("connect after session loss")
	}
}

// /nodes success must never extend authorization (section 5.2).
func TestCatalogDoesNotRenewLease(t *testing.T) {
	f := newFakeAPI(t)
	f.session.ReportInterval = 3600 // no periodic /session renewal during the test
	now := time.Now()
	clock := &now
	m := New(Options{CoreVersion: "t", Now: func() time.Time { return *clock }, BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond})
	t.Cleanup(func() { m.Shutdown(t.Context()) })
	if _, err := m.Login(t.Context(), f.params(f.password)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catalog", func() bool { return m.Snapshot().CatalogRevision != "" })
	selectA(t, m)
	if !m.Snapshot().Entitlement.CanConnect {
		t.Fatal("expected authorization right after bind")
	}
	// /session starts failing (e.g. the API's session store is down) while /nodes keeps
	// answering; time passes beyond the 90s lease.
	f.mu.Lock()
	f.sessionErr = &apiError{503, "client_service_unavailable"}
	f.mu.Unlock()
	m.mu.Lock()
	*clock = clock.Add(91 * time.Second)
	m.mu.Unlock()
	before := m.Snapshot().CatalogRevision
	f.mutate(func() {}) // force /nodes to answer with a fresh revision
	waitFor(t, "catalog update", func() bool { return m.Snapshot().CatalogRevision != before })
	snap := m.Snapshot()
	if snap.Entitlement.CanConnect || snap.Entitlement.Reason != "lease_expired" || snap.Entitlement.LeaseRemainingSeconds != 0 {
		t.Fatalf("catalog traffic extended the lease: %+v", snap.Entitlement)
	}
}

func TestDeviceLimitSurfaced(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	f.mu.Lock()
	f.configErr = &apiError{403, "device_limit"}
	f.mu.Unlock()
	_, err := m.SelectNode(t.Context(), nodeA, m.Snapshot().CatalogRevision)
	if code(t, err) != "device_limit" {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.LastError.Code != "device_limit" || !strings.Contains(s.LastError.Message, "设备上限") {
		t.Fatalf("%+v", s.LastError)
	}
}

func TestTransientNodeErrorsRetry(t *testing.T) {
	f := newFakeAPI(t)
	f.mu.Lock()
	f.nodesErr = &apiError{429, "ignored"}
	f.mu.Unlock()
	m := newTestManager(t, nil)
	if _, err := m.Login(t.Context(), f.params(f.password)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "several retries", func() bool { return f.nodesSeen.Load() >= 3 })
	if s := m.Snapshot(); s.State == StateSignedOut || s.LastError == nil || s.LastError.Code != "rate_limited" {
		t.Fatalf("429 handling: %+v", s)
	}
	f.mu.Lock()
	f.nodesErr = nil
	f.mu.Unlock()
	waitFor(t, "recovery", func() bool { return len(m.Snapshot().Nodes) == 2 })
}

func TestKeepAliveRenewsLeaseIndependently(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	selectA(t, m)
	before := f.sessionHits.Load()
	waitFor(t, "periodic /session", func() bool { return f.sessionHits.Load() >= before+2 })
}

func TestLogoutRevokesAndIsIdempotent(t *testing.T) {
	f := newFakeAPI(t)
	m := login(t, f, nil)
	selectA(t, m)
	s := m.Logout(t.Context())
	if s.State != StateSignedOut || s.User != nil || len(s.Nodes) != 0 || f.logoutHits.Load() != 1 {
		t.Fatalf("%+v hits=%d", s, f.logoutHits.Load())
	}
	m.Logout(t.Context())
	if f.logoutHits.Load() != 1 {
		t.Fatal("second logout contacted the server")
	}
}

func TestLeaseClocks(t *testing.T) {
	start := time.Now()
	server := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := server.Add(90 * time.Second)
	l := newLease(start, server, &deadline, 90)
	if !l.active(start.Add(89 * time.Second)) {
		t.Fatal("lease expired early")
	}
	if l.active(start.Add(90 * time.Second)) {
		t.Fatal("lease outlived its length")
	}
	// A server claiming a longer window than lease_seconds is capped.
	far := server.Add(time.Hour)
	if l = newLease(start, server, &far, 90); l.active(start.Add(91 * time.Second)) {
		t.Fatal("lease exceeded lease_seconds")
	}
	// Sleep: the wall clock jumps but the monotonic reading does not advance.
	slept := start.Round(0).Add(200 * time.Second) // wall only
	if l = newLease(start, server, &deadline, 90); l.active(slept) {
		t.Fatal("wall-clock advance during sleep did not close the lease")
	}
	// Clock rolled back: the monotonic deadline still holds.
	if l.active(start.Add(95*time.Second)) || !l.active(start.Add(10*time.Second)) {
		t.Fatal("monotonic boundary wrong")
	}
	if newLease(start, server, nil, 90).active(start) {
		t.Fatal("no authorization must mean no lease")
	}
}
