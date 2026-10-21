package managed

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

// ErrTunnelFailedForTest is an engine start failure without a specific cause.
var ErrTunnelFailedForTest = errors.New("tun device could not be created")

func snapshotJSON(t *testing.T, s Snapshot) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Online leases in the Core (contract native-v3, handoff_node_use_history_and_load_balancing_c.md
// round 5): one admission per Connect, release after the tail, retries, reassignment.

func (f *fakeAPI) leaseIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for id := range f.conns.leases {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (f *fakeAPI) avoidsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.conns.avoids...)
}

// Selecting takes nothing; Connect admits exactly once and uses the admitted document.
func TestConnectAdmitsOnceAndSelectionTakesNoSlot(t *testing.T) {
	h := newHarness(t, 1000)
	if h.f.conns.connects.Load() != 0 || h.f.configHits.Load() != 0 || h.f.activeLeases() != 0 {
		t.Fatal("selecting a line took a slot")
	}
	snap := h.connect(t)
	if h.f.conns.connects.Load() != 1 || h.f.configHits.Load() != 0 || h.f.activeLeases() != 1 {
		t.Fatalf("admissions %d config %d", h.f.conns.connects.Load(), h.f.configHits.Load())
	}
	// The document names its proxy after the group; the server never reaches the snapshot.
	if p := h.engine.last.Load(); p == nil || p.NodeID != nodeA || p.Proxy["name"] != "HK Dedicated" {
		t.Fatalf("engine profile: %+v", p)
	}
	if snap.SelectedNodeID != nodeA || !snap.ConfigReady || snap.ProfileVersion == "" {
		t.Fatalf("connected snapshot: %+v", snap)
	}
	for _, server := range h.f.conns.serversOf(nodeA) {
		if strings.Contains(snapshotJSON(t, snap), server) {
			t.Fatal("the snapshot names a server")
		}
	}
	// A second Connect while connected is idempotent: no second admission.
	if _, err := h.m.Connect(t.Context()); err != nil || h.f.conns.connects.Load() != 1 {
		t.Fatalf("repeat connect: %v admissions %d", err, h.f.conns.connects.Load())
	}
}

// Disconnect closes the data path, has the tail acknowledged, and only then releases.
func TestDisconnectReleasesAfterTheTail(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.engine.down.Store(7 * mib)
	snap := h.m.Disconnect(t.Context())
	_, _, down, seq := h.ledger()
	if snap.State != StateReady || snap.LastError != nil || down != 7*mib {
		t.Fatalf("disconnect: %s %+v ledger down %d", snap.State, snap.LastError, down)
	}
	r := h.f.releasesSeen()
	h.f.mu.Lock()
	at := append([]int64(nil), h.f.conns.atRelease...)
	h.f.mu.Unlock()
	if len(r) != 1 || r[0].reason != "user" || at[0] != seq || h.f.activeLeases() != 0 {
		t.Fatalf("release %+v at sequence %v, ledger at %d", r, at, seq)
	}
	// Idempotent: a second Disconnect releases nothing more.
	h.m.Disconnect(t.Context())
	if len(h.f.releasesSeen()) != 1 {
		t.Fatal("a second disconnect released again")
	}
}

// Guards release with their own reason: a network change is not a server failure.
func TestGuardStopsReleaseWithTheirReason(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.sig.Store("ethernet")
	h.waitStopped(t, "network_changed")
	waitFor(t, "release", func() bool { return len(h.f.releasesSeen()) == 1 })
	if r := h.f.releasesSeen(); r[0].reason != "network_changed" {
		t.Fatalf("%+v", r)
	}
}

// An undelivered release is retried by the keep-alive loop, exactly once more.
func TestUndeliveredReleaseIsRetriedOnce(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.conns.releaseFailures = 1
	h.f.mu.Unlock()
	snap := h.m.Disconnect(t.Context())
	if snap.State != StateReady || snap.LastError != nil || h.engine.stops.Load() == 0 {
		t.Fatalf("a failed release must not hold the user up: %s %+v", snap.State, snap.LastError)
	}
	if h.f.activeLeases() != 1 {
		t.Fatal("test premise: the first release must fail")
	}
	waitFor(t, "keep-alive to deliver the release", func() bool { return h.f.activeLeases() == 0 })
	hits := h.f.conns.releaseHits.Load()
	renewals := h.f.sessionHits.Load()
	waitFor(t, "two more renewals", func() bool { return h.f.sessionHits.Load() >= renewals+2 })
	if h.f.conns.releaseHits.Load() != hits || hits != 2 {
		t.Fatalf("released %d times", h.f.conns.releaseHits.Load())
	}
}

// A tail that cannot be acknowledged holds the release back until keep-alive settled it.
func TestReleaseWaitsForAnUnacknowledgedTail(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.engine.down.Store(3 * mib)
	h.f.mu.Lock()
	h.f.trafficErr = &apiError{503, "client_service_unavailable"}
	h.f.mu.Unlock()
	h.m.Disconnect(t.Context())
	if h.f.conns.releaseHits.Load() != 0 {
		t.Fatal("released before the tail was acknowledged")
	}
	h.f.mu.Lock()
	h.f.trafficErr = nil
	h.f.mu.Unlock()
	waitFor(t, "tail then release", func() bool { return h.f.activeLeases() == 0 })
	_, _, down, seq := h.ledger()
	h.f.mu.Lock()
	at := h.f.conns.atRelease[0]
	h.f.mu.Unlock()
	if down != 3*mib || at != seq {
		t.Fatalf("released at sequence %d, tail %d at %d", at, down, seq)
	}
}

// Logout and process exit go through the same stop path: the lease is released first.
func TestLogoutReleasesTheLease(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.m.Logout(t.Context())
	if r := h.f.releasesSeen(); len(r) != 1 || r[0].reason != "user" || h.f.logoutHits.Load() != 1 {
		t.Fatalf("logout: releases %+v logouts %d", r, h.f.logoutHits.Load())
	}
}

// The administrator withdraws the server: the Core stops, settles, and reconnects once to
// the same line on another server, with a continuous ledger.
func TestReassignmentReconnectsOnce(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	first := h.f.leaseIDs()
	h.engine.up.Store(1 * mib)
	starts := h.engine.starts.Load()
	h.f.reassign()
	var seenMessage bool
	waitFor(t, "automatic reconnect", func() bool {
		s := h.m.Snapshot()
		if s.LastError != nil && s.LastError.Code == "connection_reassigned" && strings.Contains(s.LastError.Message, "重新连接") {
			seenMessage = true
		}
		return s.State == StateConnected && h.engine.starts.Load() == starts+1
	})
	if !h.stoppedWith("connection_reassigned") {
		t.Fatal("the old segment did not stop for the reassignment")
	}
	s := h.m.Snapshot()
	if s.LastError != nil || h.f.conns.connects.Load() != 2 || len(h.f.leaseIDs()) != len(first)+1 {
		t.Fatalf("after reconnect: %+v admissions %d", s.LastError, h.f.conns.connects.Load())
	}
	t.Logf("reassignment message observed during the reconnect: %v", seenMessage)
	// The withdrawn lease is already ended at the server: nothing to release for it.
	if len(h.f.releasesSeen()) != 0 {
		t.Fatalf("released an ended lease: %+v", h.f.releasesSeen())
	}
	// The old segment's bytes were settled before the new admission; later ones continue.
	units, up, _, seq := h.ledger()
	if up != 1*mib || units != mib*1000 {
		t.Fatalf("ledger after reassignment: %d units %d up", units, up)
	}
	h.engine.up.Store(2 * mib) // one more MiB through the new segment (the engine counts on)
	h.m.Disconnect(t.Context())
	if units2, up2, _, seq2 := h.ledger(); up2 != 2*mib || units2 != 2*mib*1000 || seq2 <= seq {
		t.Fatalf("ledger continuity: %d units, %d up, sequence %d -> %d", units2, up2, seq, seq2)
	}
}

// Between the stop and the automatic reconnect the snapshot never looks like a tunnel that
// ended by itself: the apps act on that (Android stops its VPN service and the Disconnect
// cancels the reconnect; the iOS extension cancels the tunnel). A slow GET /session (the
// reconnect's first request) used to leave the state at ready for a whole round trip.
func TestReassignmentStaysBusyUntilReconnected(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.sessionDelay = 300 * time.Millisecond
	h.f.mu.Unlock()
	h.f.reassign()
	var idle []State
	waitFor(t, "automatic reconnect", func() bool {
		s := h.m.Snapshot()
		switch s.State {
		case StateConnected:
			return h.f.conns.connects.Load() == 2
		case StatePreparing, StateConnecting, StateStopping:
		default:
			idle = append(idle, s.State)
		}
		return false
	})
	if len(idle) > 0 {
		t.Fatalf("the reconnect showed an idle tunnel %d times (%v)", len(idle), idle[0])
	}
}

// A reconnect that cannot get past its first request settles back to an idle state with its
// reason instead of staying "preparing".
func TestReassignmentWithoutASessionSettles(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.sessionErr = &apiError{503, "service_unavailable"}
	h.f.mu.Unlock()
	h.f.reassign()
	waitFor(t, "the reconnect to give up", func() bool {
		s := h.m.Snapshot()
		return (s.State == StateReady || s.State == StateBlocked) && s.LastError != nil
	})
	if n := h.f.conns.connects.Load(); n != 1 {
		t.Fatalf("admissions: %d", n)
	}
}

// The automatic reconnect is tried once: if it fails, the Core stays disconnected with the
// reason, and does not loop.
func TestFailedReconnectAfterReassignmentStops(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.configErr = &apiError{409, "group_full"}
	h.f.mu.Unlock()
	h.f.reassign()
	waitFor(t, "the failed reconnect", func() bool {
		s := h.m.Snapshot()
		return s.State == StateReady && s.LastError != nil && s.LastError.Code == "group_full"
	})
	// Renewals keep running (1 s here); none of them may erase why the VPN is off.
	renewals := h.f.sessionHits.Load()
	waitFor(t, "two more renewals", func() bool { return h.f.sessionHits.Load() >= renewals+2 })
	if n := h.f.conns.connects.Load(); n != 2 {
		t.Fatalf("admissions: %d", n)
	}
	if s := h.m.Snapshot(); s.LastError == nil || !strings.Contains(s.LastError.Message, "已满") || s.SelectedNodeID != nodeA {
		t.Fatalf("%+v %+v", s.LastError, s.SelectedNodeID)
	}
}

// A command of the user's wins over the automatic reconnect: requestStop captures the
// intent before stopping, and a Disconnect that lands meanwhile changes it.
func TestUserDisconnectCancelsTheReconnect(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.m.mu.Lock()
	gen, intent := h.m.generation, h.m.intent
	h.m.mu.Unlock()
	h.m.Disconnect(t.Context())
	h.m.reconnect(gen, intent) // what requestStop would do after a reassignment
	if h.f.conns.connects.Load() != 1 || h.m.Snapshot().State == StateConnected {
		t.Fatal("reconnected over the user's Disconnect")
	}
}

// A probe failure releases with the failure (a health report) and retries once on another
// server of the group.
func TestProbeFailureRetriesOnAnotherServer(t *testing.T) {
	h := newHarness(t, 1000)
	h.engine.verifyHook = func() {
		if h.engine.verifies.Load() == 1 {
			h.engine.verifyErr = ErrTunnelUnhealthy
		} else {
			h.engine.verifyErr = nil
		}
	}
	snap := h.connect(t)
	servers := h.f.conns.serversOf(nodeA)
	avoids := h.f.avoidsSeen()
	if len(avoids) != 2 || avoids[0] != "" || avoids[1] != servers[0] {
		t.Fatalf("avoid_node_id: %q", avoids)
	}
	if r := h.f.releasesSeen(); len(r) != 1 || r[0].reason != "tunnel_unhealthy" {
		t.Fatalf("release of the failed server: %+v", r)
	}
	if snap.LastError != nil || h.f.activeLeases() != 1 {
		t.Fatalf("%+v", snap.LastError)
	}
}

// Twice failing: one retry only, and the user sees the probe failure.
func TestProbeFailureRetriesOnlyOnce(t *testing.T) {
	h := newHarness(t, 1000)
	h.engine.verifyErr = ErrTunnelUnhealthy
	snap, err := h.m.Connect(t.Context())
	if code(t, err) != "tunnel_unhealthy" || snap.State == StateConnected || h.f.conns.connects.Load() != 2 {
		t.Fatalf("%v %s admissions %d", err, snap.State, h.f.conns.connects.Load())
	}
	if r := h.f.releasesSeen(); len(r) != 2 || r[0].reason != "tunnel_unhealthy" || r[1].reason != "tunnel_unhealthy" || h.f.activeLeases() != 0 {
		t.Fatalf("releases: %+v", r)
	}
}

// A group with a single server has nothing else to try: the original failure is reported.
func TestProbeFailureOnALoneServerReportsTheProbe(t *testing.T) {
	h := newHarnessWith(t, 1000, func(f *fakeAPI) {
		f.conns.servers = map[string][]string{nodeA: {serverOf(nodeA, 1)}}
	}, nil)
	h.engine.verifyErr = ErrTunnelUnhealthy
	snap, err := h.m.Connect(t.Context())
	if code(t, err) != "tunnel_unhealthy" || snap.LastError == nil || snap.LastError.Code != "tunnel_unhealthy" {
		t.Fatalf("%v %+v", err, snap.LastError)
	}
	if h.f.conns.connects.Load() != 2 {
		t.Fatalf("admissions: %d", h.f.conns.connects.Load())
	}
}

// Starting the engine is local: no health report and no retry on another server.
func TestStartFailureIsNotAServerReport(t *testing.T) {
	h := newHarness(t, 1000)
	h.engine.err = ErrTunnelFailedForTest
	if _, err := h.m.Connect(t.Context()); code(t, err) != "tunnel_failed" {
		t.Fatal(err)
	}
	if r := h.f.releasesSeen(); len(r) != 1 || r[0].reason != "stopped" || h.f.conns.connects.Load() != 1 {
		t.Fatalf("releases %+v admissions %d", r, h.f.conns.connects.Load())
	}
}

// Refusals are shown with the Core's own text, and nothing starts.
func TestAdmissionRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  apiError
		text string
	}{
		{apiError{403, "device_limit"}, "同时在线设备上限"},
		{apiError{409, "group_full"}, "该线路当前已满"},
		{apiError{503, "group_unavailable"}, "暂无可用服务器"},
		{apiError{409, "grant_required"}, "节点授权"},
	} {
		t.Run(tc.err.code, func(t *testing.T) {
			h := newHarness(t, 1000)
			h.f.mu.Lock()
			h.f.configErr = &tc.err
			h.f.mu.Unlock()
			snap, err := h.m.Connect(t.Context())
			if code(t, err) != tc.err.code || h.engine.starts.Load() != 0 || snap.State == StateConnected {
				t.Fatalf("%v %s", err, snap.State)
			}
			if snap.LastError == nil || !strings.Contains(snap.LastError.Message, tc.text) {
				t.Fatalf("%+v", snap.LastError)
			}
			if h.f.conns.connects.Load() != 1 {
				t.Fatalf("a definite refusal was asked again: %d admissions", h.f.conns.connects.Load())
			}
		})
	}
}

// A transient failure (an outage, not a verdict) is retried with the same idempotency key:
// one lease, not two.
func TestAdmissionRetrySameKey(t *testing.T) {
	h := newHarness(t, 1000)
	h.f.mu.Lock()
	h.f.conns.failFirst = 2
	h.f.mu.Unlock()
	h.connect(t)
	h.f.mu.Lock()
	keys := append([]string(nil), h.f.conns.keys...)
	h.f.mu.Unlock()
	if len(keys) != 3 || keys[0] != keys[1] || keys[1] != keys[2] || h.f.activeLeases() != 1 || len(h.f.leaseIDs()) != 1 {
		t.Fatalf("keys %q leases %d", keys, len(h.f.leaseIDs()))
	}
}

// An admission reply that arrives after the session moved on never revives anything here,
// and the stray lease is given back.
func TestLateAdmissionReplyIsDiscarded(t *testing.T) {
	h := newHarness(t, 1000)
	h.f.mu.Lock()
	h.f.conns.connectGate, h.f.conns.connectEntered = make(chan struct{}), make(chan struct{}, 1)
	h.f.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := h.m.Connect(t.Context()); done <- err }()
	<-h.f.conns.connectEntered
	// The session is replaced while the admission is in flight (same device logs in again).
	h.m.mu.Lock()
	h.m.generation++
	h.m.mu.Unlock()
	close(h.f.conns.connectGate)
	if err := <-done; code(t, err) != "stale_generation" {
		t.Fatalf("late reply applied: %v", err)
	}
	if h.engine.starts.Load() != 0 {
		t.Fatal("a stale admission started the tunnel")
	}
	h.m.mu.Lock()
	held := h.m.admission
	h.m.mu.Unlock()
	if held != nil {
		t.Fatal("a stale admission was installed")
	}
	waitFor(t, "the stray lease to be released", func() bool { return h.f.activeLeases() == 0 })
}

// iOS handoff: a lease that is held or not yet released stays with this process.
func TestHandoffRefusesAnUnreleasedLease(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.conns.releaseFailures = 1 << 20
	h.f.mu.Unlock()
	if _, err := h.m.ExportHandoff(t.Context()); code(t, err) != "handoff_unsettled" {
		t.Fatalf("export with an unreleased lease: %v", err)
	}
	if s := h.m.Snapshot(); s.User == nil || s.State == StateReleased {
		t.Fatalf("the session must stay here: %+v", s.State)
	}
	h.f.mu.Lock()
	h.f.conns.releaseFailures = 0
	h.f.mu.Unlock()
	ticket, err := h.m.ExportHandoff(t.Context())
	if err != nil || ticket.SelectedNodeID != nodeA || h.f.activeLeases() != 0 {
		t.Fatalf("export once released: %v %q", err, ticket.SelectedNodeID)
	}
}

// Desktop policy: a lapsed lease stops the tunnel and asks the user to connect again.
func TestExpiredLeaseStopsOnTheDesktop(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.session.Reason, h.f.session.CanConnect, h.f.session.ConnectionState = "connection_expired", false, apiclient.ConnectionExpired
	h.f.mu.Unlock()
	s := h.waitStopped(t, "connection_expired")
	time.Sleep(100 * time.Millisecond)
	if h.f.conns.connects.Load() != 1 || s.State != StateReady || !s.Entitlement.CanConnect {
		t.Fatalf("desktop must not reconnect by itself: admissions %d state %s %+v", h.f.conns.connects.Load(), s.State, s.Entitlement)
	}
	// The verdict only describes the old lease: connecting again is allowed and works.
	h.connect(t)
}

// Android (ResumeGate): a lease that lapsed while the phone slept is taken again by the
// re-confirmation path; when the slots are gone meanwhile, the Core stops and says so.
func TestGateReadmitsAnExpiredLease(t *testing.T) {
	h, _ := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.f.mu.Lock()
	h.f.session.Reason, h.f.session.CanConnect, h.f.session.ConnectionState = "connection_expired", false, apiclient.ConnectionExpired
	h.f.mu.Unlock()
	h.clock.sleep(10 * time.Minute)
	// Same server, same document: the gate reopens the very same tunnel (no restart).
	s := h.waitGate(t, "reopened", 1)
	if h.f.conns.connects.Load() != 2 || s.State != StateConnected || s.LastError != nil || h.f.activeLeases() != 1 || h.engine.starts.Load() != 1 {
		t.Fatalf("admissions %d state %s %+v starts %d", h.f.conns.connects.Load(), s.State, s.LastError, h.engine.starts.Load())
	}

	h.f.mu.Lock()
	h.f.session.Reason, h.f.session.CanConnect, h.f.session.ConnectionState = "connection_expired", false, apiclient.ConnectionExpired
	h.f.configErr = &apiError{403, "device_limit"}
	h.f.mu.Unlock()
	h.clock.sleep(10 * time.Minute)
	waitFor(t, "the device limit to stop it", func() bool {
		s := h.m.Snapshot()
		return s.State != StateConnected && s.State != StateStopping && s.LastError != nil && s.LastError.Code == "device_limit"
	})
}

// ResumeGate: re-admitted onto another server (the old one went away meanwhile), the held
// tunnel cannot carry on; it is stopped and connected again once, on the new server.
func TestGateReadmissionElsewhereReconnects(t *testing.T) {
	h, _ := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.f.mu.Lock()
	h.f.session.Reason, h.f.session.CanConnect, h.f.session.ConnectionState = "connection_expired", false, apiclient.ConnectionExpired
	h.f.conns.down = map[string]bool{serverOf(nodeA, 1): true}
	h.f.mu.Unlock()
	h.clock.sleep(10 * time.Minute)
	waitFor(t, "reconnect on the other server", func() bool {
		return h.engine.starts.Load() == 2 && h.m.Snapshot().State == StateConnected
	})
	if !h.stoppedWith("connection_expired") || h.f.activeLeases() != 1 {
		t.Fatalf("stops %v active %d", h.stops, h.f.activeLeases())
	}
}

// Node-reported: the grant path asks to avoid the failed server and reports the failure.
func TestGrantProbeFailureRetriesWithAvoid(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	h.engine.verifyHook = func() {
		if h.engine.verifies.Load() == 1 {
			h.engine.verifyErr = ErrTunnelUnhealthy
		} else {
			h.engine.verifyErr = nil
		}
	}
	h.connect(t)
	h.f.mu.Lock()
	avoids, reasons := append([]string(nil), h.f.grants.avoids...), append([]string(nil), h.f.grants.revokeReasons...)
	h.f.mu.Unlock()
	if len(avoids) != 2 || avoids[0] != "" || avoids[1] != serverOf(nodeA, 1) {
		t.Fatalf("avoids %q", avoids)
	}
	if len(reasons) != 1 || reasons[0] != "tunnel_unhealthy" {
		t.Fatalf("revocation reasons %q", reasons)
	}
	h.m.Disconnect(t.Context())
	h.f.mu.Lock()
	last := h.f.grants.revokeReasons[len(h.f.grants.revokeReasons)-1]
	h.f.mu.Unlock()
	if last != "user" {
		t.Fatalf("disconnect revocation reason %q", last)
	}
}

// Node-reported reassignment: the session renewal reports it; the Core reconnects once.
func TestGrantReassignmentReconnects(t *testing.T) {
	h := newGrantHarness(t, nil, nil)
	h.connect(t)
	first := h.f.onlyGrant()
	h.f.mu.Lock()
	h.f.grants.items[first.id].state = apiclient.GrantRevoking
	h.f.conns.down = map[string]bool{first.server: true}
	h.f.session.Reason, h.f.session.CanConnect = "connection_reassigned", false
	h.f.mu.Unlock()
	waitFor(t, "reconnect on another server", func() bool {
		return h.f.grants.creates.Load() == 2 && h.m.Snapshot().State == StateConnected
	})
	if !h.stoppedWith("connection_reassigned") {
		t.Fatal("no reassignment stop")
	}
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	for _, g := range h.f.grants.items {
		if g.id != first.id && g.server == first.server {
			t.Fatal("reconnected to the withdrawn server")
		}
	}
}
