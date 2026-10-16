package managed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

// grantFake is the grant half of the scriptable API: a miniature of the server's
// grants endpoints (handoff_app.md 7.4) where the "node" confirms after N polls.
type grantFake struct {
	items map[string]*fakeGrant
	byKey map[string]string
	seq   int

	pendingPolls   int // polls answered "pending" before the node confirms
	createErr      *apiError
	revokeFailures int  // revocations answered 503 before one succeeds
	revokeHold     bool // the node never confirms removal

	creates, polls, revokes atomic.Int32
}

type fakeGrant struct {
	id, node, state string
	polls           int
	yaml, version   string
	uuid            string
}

func grantYAML(name, uuid string) string {
	return "# catalog: 0000\nmode: rule\nproxies:\n    - name: " + name + "\n      type: vless\n      server: 192.0.2.10\n      port: 443\n      uuid: " + uuid + "\n      udp: true\nproxy-groups:\n    - name: VPN\n      type: select\n      proxies:\n        - " + name + "\nrules:\n    - MATCH,VPN\n"
}

func (f *fakeAPI) grantCreate(w http.ResponseWriter, r *http.Request) {
	if !f.auth(w, r) {
		return
	}
	f.grants.creates.Add(1)
	var in struct {
		NodeID string `json:"node_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	key := r.Header.Get("Idempotency-Key")
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &f.grants
	if g.createErr != nil {
		writeError(w, *g.createErr)
		return
	}
	if g.items == nil {
		g.items, g.byKey = map[string]*fakeGrant{}, map[string]string{}
	}
	if id, ok := g.byKey[key]; ok {
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(apiclient.GrantStatus{GrantID: id, State: g.items[id].state})
		return
	}
	var node *apiclient.NodeChoice
	for i := range f.nodes {
		if f.nodes[i].ID == in.NodeID && f.nodes[i].MeteringSource == apiclient.MeteringNode {
			node = &f.nodes[i]
		}
	}
	if node == nil || key == "" {
		writeError(w, apiError{400, "invalid_managed_selection"})
		return
	}
	g.seq++
	gr := &fakeGrant{id: fmt.Sprintf("55555555-5555-4555-8555-%012d", g.seq), node: node.ID, state: apiclient.GrantPending, uuid: fmt.Sprintf("44444444-4444-4444-8444-%012d", g.seq)}
	gr.yaml = grantYAML(node.Name, gr.uuid)
	gr.version = sha(gr.yaml)
	g.items[gr.id], g.byKey[key] = gr, gr.id
	s := &f.session
	s.MeteringSource, s.RatePermille, s.GrantID, s.GrantState = apiclient.MeteringNode, node.RatePermille, gr.id, gr.state
	s.ProfileVersion, s.Reason, s.CanConnect = gr.version, "", true
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(apiclient.GrantStatus{GrantID: gr.id, State: gr.state, Reason: "grant_pending"})
}

func (f *fakeAPI) grantByID(w http.ResponseWriter, r *http.Request) {
	if !f.auth(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/client/grants/")
	id, revoke := strings.CutSuffix(rest, "/revoke")
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &f.grants
	gr := g.items[id]
	if gr == nil {
		writeError(w, apiError{404, "grant_not_found"})
		return
	}
	if revoke {
		g.revokes.Add(1)
		if g.revokeFailures > 0 {
			g.revokeFailures--
			writeError(w, apiError{503, "client_service_unavailable"})
			return
		}
		if gr.state == apiclient.GrantPending || gr.state == apiclient.GrantReady {
			gr.state = apiclient.GrantRevoking
			if !g.revokeHold {
				gr.state = apiclient.GrantRevoked
			}
		}
		if f.session.GrantID == id {
			f.session.GrantID, f.session.GrantState = "", ""
		}
		json.NewEncoder(w).Encode(apiclient.GrantStatus{GrantID: id, State: gr.state})
		return
	}
	g.polls.Add(1)
	if gr.state == apiclient.GrantRevoking && !g.revokeHold {
		gr.state = apiclient.GrantRevoked
	}
	if gr.state == apiclient.GrantPending {
		gr.polls++
		if gr.polls > g.pendingPolls {
			gr.state = apiclient.GrantReady
			f.session.GrantState = gr.state
		}
	}
	switch gr.state {
	case apiclient.GrantPending:
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(apiclient.GrantStatus{GrantID: id, State: gr.state, Reason: "grant_pending"})
	case apiclient.GrantReady:
		snap := f.snapshotSession()
		json.NewEncoder(w).Encode(apiclient.GrantStatus{GrantID: id, State: gr.state, YAML: gr.yaml, Version: gr.version, NodeID: gr.node, Session: &snap})
	default:
		json.NewEncoder(w).Encode(apiclient.GrantStatus{GrantID: id, State: gr.state})
	}
}

// endFromServer simulates the control plane ending a grant (revocation, expiry, ...).
func (f *fakeAPI) endFromServer(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants.items[id].state = apiclient.GrantRevoking
	f.session.GrantState, f.session.Reason, f.session.CanConnect = apiclient.GrantRevoking, "grant_expired", false
}

func (f *fakeAPI) onlyGrant() *fakeGrant {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.grants.items {
		return g
	}
	return nil
}

func (f *fakeAPI) grantState(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.grants.items[id].state
}

func newGrantHarness(t *testing.T, mutate func(*fakeAPI), opts func(*Options)) *harness {
	t.Helper()
	f := newFakeAPI(t)
	f.mu.Lock()
	f.nodes[0].MeteringSource = apiclient.MeteringNode
	f.session.ReportInterval = 1
	if mutate != nil {
		mutate(f)
	}
	f.mu.Unlock()
	h := &harness{f: f, engine: &recordingEngine{api: f}, clock: newShiftClock()}
	h.sig.Store("wifi")
	o := Options{
		CoreVersion: "t", Engine: h.engine, Now: h.clock.now, Mono: h.clock.mono,
		Signature:  func() string { return h.sig.Load().(string) },
		BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond,
		MonitorInterval: 10 * time.Millisecond, SettleTimeout: 2 * time.Second,
		GrantPoll: 5 * time.Millisecond, GrantTimeout: 3 * time.Second,
	}
	if opts != nil {
		opts(&o)
	}
	h.m = New(o)
	h.observeStops()
	t.Cleanup(func() { h.m.Shutdown(t.Context()) })
	if _, err := h.m.Login(t.Context(), f.params(f.password)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catalog", func() bool { return h.m.Snapshot().CatalogRevision != "" })
	selectA(t, h.m)
	return h
}

func TestNodeReportedConnectUsesAGrantAndNeverTouchesTheClientLedger(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	snap := h.m.Snapshot()
	if h.f.configHits.Load() != 0 {
		t.Fatal("a node-reported node must not fetch the shared config")
	}
	if !snap.ConfigReady || snap.Entitlement == nil || !snap.Entitlement.CanConnect || snap.SelectedNodeID != nodeA {
		t.Fatalf("selection: %+v %+v", snap.ConfigReady, snap.Entitlement)
	}
	if snap.Nodes[0].MeteringSource != apiclient.MeteringNode {
		t.Fatal("snapshot hides which nodes are node-metered")
	}

	snap = h.connect(t)
	if got := h.f.grants.creates.Load(); got != 1 {
		t.Fatalf("grants created: %d", got)
	}
	grant := h.f.onlyGrant()
	profile := h.engine.last.Load()
	if profile == nil || profile.Proxy["uuid"] != grant.uuid || profile.Proxy["type"] != "vless" {
		t.Fatalf("the tunnel must start with the per-user credential, got %+v", profile)
	}
	if snap.Metering == nil || snap.Metering.Source != apiclient.MeteringNode || snap.Metering.Pending {
		t.Fatalf("metering view: %+v", snap.Metering)
	}

	// Bytes flow; the Core reports NOTHING (the node bills), yet the UI shows them.
	h.engine.up.Store(2 * mib)
	h.engine.down.Store(6 * mib)
	waitFor(t, "local session bytes in the snapshot", func() bool {
		v := h.m.Snapshot().Metering
		return v != nil && v.UploadBytes == 2*mib && v.DownloadBytes == 6*mib
	})
	hits := h.f.sessionHits.Load()
	waitFor(t, "keep-alive to renew authorization while connected", func() bool { return h.f.sessionHits.Load() >= hits+2 })
	if h.f.trafficHits.Load() != 0 {
		t.Fatalf("a node-reported session sent %d client traffic reports", h.f.trafficHits.Load())
	}
	if s := h.m.Snapshot(); s.State != StateConnected || s.Entitlement.LeaseRemainingSeconds <= 0 {
		t.Fatalf("lease not renewed: %+v", s.Entitlement)
	}

	// Disconnect closes the data path first, then revokes at the node and waits for it.
	stops := h.engine.stops.Load()
	snap = h.m.Disconnect(t.Context())
	if snap.State != StateReady || snap.LastError != nil || h.engine.stops.Load() == stops {
		t.Fatalf("disconnect: %+v %+v", snap.State, snap.LastError)
	}
	if st := h.f.grantState(grant.id); st != apiclient.GrantRevoked {
		t.Fatalf("grant after disconnect: %s", st)
	}
	if h.f.trafficHits.Load() != 0 {
		t.Fatal("disconnect settled a client ledger on a node-reported session")
	}
	// Reconnecting mints a fresh credential, never reusing the revoked one.
	h.connect(t)
	if h.f.grants.creates.Load() != 2 || h.engine.last.Load().Proxy["uuid"] == grant.uuid {
		t.Fatal("a revoked credential was reused")
	}
}

func TestCredentialAndDocumentNeverAppearInSnapshotsOrErrors(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	h.connect(t)
	uuid := h.f.onlyGrant().uuid
	data, _ := json.Marshal(h.m.Snapshot())
	for _, secret := range []string{uuid, "192.0.2.10", "type: vless", "proxies:", "Idempotency"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("snapshot leaked %q: %s", secret, data)
		}
	}
	if fmt.Sprintf("%v %#v", h.engine.last.Load(), h.engine.last.Load()) != "profile("+nodeA+") profile("+nodeA+")" {
		t.Fatal("profile prints its contents")
	}
}

func TestSlowNodeIsAwaitedButNeverConnectedBeforeItConfirms(t *testing.T) {
	h := newGrantHarness(t, func(f *fakeAPI) { f.grants.pendingPolls = 6 }, nil)
	h.connect(t)
	if got := h.f.grants.polls.Load(); got < 7 {
		t.Fatalf("connected after only %d polls; the node had not confirmed", got)
	}
	if h.engine.starts.Load() != 1 {
		t.Fatal("tunnel starts must follow the confirmation")
	}
}

func TestNodeThatNeverConfirmsTimesOutAndIsRevoked(t *testing.T) {
	h := newGrantHarness(t, func(f *fakeAPI) { f.grants.pendingPolls = 1 << 30 }, func(o *Options) { o.GrantTimeout = 150 * time.Millisecond })
	snap, err := h.m.Connect(t.Context())
	if code(t, err) != "grant_timeout" || snap.State != StateReady || h.engine.starts.Load() != 0 {
		t.Fatalf("%v %+v starts=%d", err, snap.State, h.engine.starts.Load())
	}
	if h.f.grants.revokes.Load() != 1 || h.f.grantState(h.f.onlyGrant().id) != apiclient.GrantRevoked {
		t.Fatal("an unconfirmed grant must be revoked, not left to linger")
	}
	if snap.LastError == nil || snap.LastError.Message == "" {
		t.Fatalf("user-facing error: %+v", snap.LastError)
	}
}

func TestUnavailableAgentFailsFastWithoutStartingAnything(t *testing.T) {
	h := newGrantHarness(t, func(f *fakeAPI) { f.grants.createErr = &apiError{503, "agent_unavailable"} }, nil)
	began := time.Now()
	snap, err := h.m.Connect(t.Context())
	if code(t, err) != "agent_unavailable" || snap.State != StateReady || h.engine.starts.Load() != 0 {
		t.Fatalf("%v %+v", err, snap.State)
	}
	if time.Since(began) > time.Second || h.f.grants.creates.Load() != 1 {
		t.Fatalf("agent_unavailable was retried for %v (%d calls)", time.Since(began), h.f.grants.creates.Load())
	}
}

func TestServerSideRevocationStopsTheTunnelAndAllowsAFreshGrant(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	h.connect(t)
	first := h.f.onlyGrant()
	h.f.endFromServer(first.id) // subscription ended, node removed, admin revoked, ...
	snap := h.waitStopped(t, "grant_expired")
	if snap.State != StateReady && snap.State != StateBlocked {
		t.Fatalf("state %s", snap.State)
	}
	if h.engine.stops.Load() == 0 {
		t.Fatal("the data path stayed open after the node ended the grant")
	}
	// The user can connect again: the dead grant is a grace reason, not a dead end.
	h.f.mu.Lock()
	h.f.session.Reason, h.f.session.CanConnect = "", true
	h.f.mu.Unlock()
	h.f.mutate(func() {}) // wake the catalog watcher so the session is refreshed
	h.connect(t)
	if h.f.grants.creates.Load() != 2 {
		t.Fatalf("grants created: %d", h.f.grants.creates.Load())
	}
}

func TestUndeliverableRevocationIsRetriedAfterTheDataPathCloses(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	h.connect(t)
	grant := h.f.onlyGrant()
	h.f.mu.Lock()
	h.f.grants.revokeFailures = 1 << 20 // the API is unreachable for revocations
	h.f.mu.Unlock()
	snap := h.m.Disconnect(t.Context())
	if h.engine.stops.Load() == 0 || snap.TunnelHealth != "idle" {
		t.Fatalf("data path must close even if the API cannot be reached: %+v", snap)
	}
	if h.f.grantState(grant.id) == apiclient.GrantRevoked {
		t.Fatal("test premise: revocation must not have been delivered yet")
	}
	h.f.mu.Lock()
	h.f.grants.revokeFailures = 0
	h.f.mu.Unlock()
	waitFor(t, "the keep-alive loop to deliver the revocation", func() bool { return h.f.grantState(grant.id) == apiclient.GrantRevoked })
}

func TestMeteringSourceMismatchEndsTheLoginSession(t *testing.T) {
	h := newGrantHarness(t, func(f *fakeAPI) { f.grants.createErr = &apiError{409, "metering_source_mismatch"} }, nil)
	snap, err := h.m.Connect(t.Context())
	if code(t, err) != "metering_source_mismatch" || snap.State != StateSignedOut || h.engine.starts.Load() != 0 {
		t.Fatalf("%v %+v", err, snap.State)
	}
	if snap.LastError == nil || snap.LastError.Code != "metering_source_mismatch" {
		t.Fatalf("%+v", snap.LastError)
	}
}

func TestEditingTheNodeWhileConnectedStopsAndRevokes(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	h.connect(t)
	grant := h.f.onlyGrant()
	h.f.mutate(func() { h.f.nodes[0].RatePermille = 250; h.f.nodes[0].Version++ })
	h.waitStopped(t, "profile_changed")
	waitFor(t, "grant revocation after the node changed", func() bool { return h.f.grantState(grant.id) == apiclient.GrantRevoked })
}

func TestNodeLosingItsAgentFallsBackToReselection(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	// The admin removes the agent: the node is still listed but no longer node-metered.
	h.f.mutate(func() { h.f.nodes[0].MeteringSource = "" })
	waitFor(t, "catalog to reflect the change", func() bool { return h.m.Snapshot().Nodes[0].MeteringSource == "" })
	// The held selection now follows the development path and binds a config on connect.
	snap := h.m.Snapshot()
	if snap.Entitlement == nil || snap.Entitlement.CanConnect {
		t.Fatalf("a changed node must not stay connectable on a stale mode: %+v", snap.Entitlement)
	}
	selectA(t, h.m)
	h.connect(t)
	if h.f.configHits.Load() == 0 || h.f.grants.creates.Load() != 0 {
		t.Fatal("a shared-credential node must use the config path")
	}
}

func TestTrafficReportingNeverRunsForAGrantEvenWhenQuotaEndsLocally(t *testing.T) {
	h := newGrantHarness(t, func(f *fakeAPI) { f.session.TotalBytes, f.session.RemainingBytes = 4*mib, 4*mib }, nil)
	h.connect(t)
	h.engine.down.Store(10 * mib) // far beyond the quota, as measured on this machine
	h.waitStopped(t, "quota_exhausted")
	if h.f.trafficHits.Load() != 0 {
		t.Fatal("client traffic reports were sent")
	}
}
