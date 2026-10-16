package managed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

// gatingEngine is a recordingEngine that can hold forwarding back (ResumeGate).
type gatingEngine struct {
	*recordingEngine
	suspends, resumes atomic.Int32
	suspended         atomic.Bool
	// verifiedWhileSuspended counts probes run while forwarding was held back: a reopened
	// gate must have resumed before proving that the tunnel forwards.
	verifiedWhileSuspended atomic.Int32
	suspendErr             error
}

func (e *gatingEngine) Suspend() error {
	if e.suspendErr != nil {
		return e.suspendErr
	}
	e.suspends.Add(1)
	e.suspended.Store(true)
	return nil
}

func (e *gatingEngine) Resume() error {
	e.resumes.Add(1)
	e.suspended.Store(false)
	return nil
}

func (e *gatingEngine) Verify(ctx context.Context) error {
	if e.suspended.Load() {
		e.verifiedWhileSuspended.Add(1)
	}
	return e.recordingEngine.Verify(ctx)
}

// newGateHarness is a metered harness under the Android policy. Reports are paced far
// apart so only the gate's own re-confirmation talks to the ledger.
func newGateHarness(t *testing.T, mutate func(*fakeAPI), limit time.Duration) (*harness, *gatingEngine) {
	t.Helper()
	return newGateHarnessWith(t, mutate, limit, nil)
}

func newGateHarnessWith(t *testing.T, mutate func(*fakeAPI), limit time.Duration, opts func(*Options)) (*harness, *gatingEngine) {
	t.Helper()
	var gate *gatingEngine
	h := newHarnessWith(t, 1000, func(f *fakeAPI) {
		f.session.ReportInterval = 30
		if mutate != nil {
			mutate(f)
		}
	}, func(h *harness, o *Options) {
		gate = &gatingEngine{recordingEngine: h.engine}
		o.Engine, o.ResumePolicy, o.GateLimit = gate, ResumeGate, limit
		if opts != nil {
			opts(o)
		}
	})
	return h, gate
}

func (h *harness) waitGate(t *testing.T, outcome string, closures int64) Snapshot {
	t.Helper()
	var s Snapshot
	waitFor(t, "gate "+outcome, func() bool {
		s = h.m.Snapshot()
		return s.LastGate != nil && s.LastGate.Outcome == outcome && s.LastGate.TotalClosures == closures
	})
	return s
}

func (h *harness) setTrafficErr(e *apiError) {
	h.f.mu.Lock()
	h.f.trafficErr = e
	h.f.mu.Unlock()
}

var unavailable = &apiError{503, "client_service_unavailable"}

// L01 in the Core: a wake from sleep closes the gate, re-confirms with a traffic batch
// (which also renews the lease) and reopens the same tunnel; nothing is torn down.
func TestGateReopensTheSameTunnelAfterAWake(t *testing.T) {
	h, gate := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.engine.up.Store(3 * mib)
	traffic, verifies, stops := h.f.trafficHits.Load(), h.engine.verifies.Load(), h.engine.stops.Load()

	h.clock.sleep(10 * time.Minute)
	s := h.waitGate(t, "reopened", 1)
	if s.State != StateConnected || s.TunnelHealth != "up" || s.Gate != nil {
		t.Fatalf("after reopening: %s %s %+v", s.State, s.TunnelHealth, s.Gate)
	}
	if s.LastGate.Reason != "lifecycle_reconfirmation_required" || s.LastGate.ClosedMillis < 0 {
		t.Fatalf("gate record: %+v", s.LastGate)
	}
	if gate.suspends.Load() != 1 || gate.resumes.Load() != 1 || h.engine.stops.Load() != stops || h.engine.starts.Load() != 1 {
		t.Fatalf("suspends %d resumes %d stops %d starts %d", gate.suspends.Load(), gate.resumes.Load(), h.engine.stops.Load()-stops, h.engine.starts.Load())
	}
	if h.f.trafficHits.Load() <= traffic {
		t.Fatal("re-confirmation did not go to the server")
	}
	if h.engine.verifies.Load() != verifies+1 || gate.verifiedWhileSuspended.Load() != 0 {
		t.Fatal("a reopened gate must prove forwarding through the resumed tunnel")
	}
	// The batch that re-confirmed carried what the tunnel had counted, at the bound rate.
	if units, up, _, _ := h.ledger(); up != 3*mib || units != 3*mib*1000 {
		t.Fatalf("ledger %d units, %d up", units, up)
	}
	if s.Entitlement == nil || s.Entitlement.LeaseRemainingSeconds <= 0 {
		t.Fatal("lease not renewed by the re-confirmation")
	}
	if len(h.stops) != 0 {
		t.Fatalf("stopped: %v", h.stops)
	}
}

// L03: the network underneath changed. The gate closes, the decision is made on the new
// network, and the segment continues there without closing again for the same change.
func TestGateFollowsANetworkChange(t *testing.T) {
	h, gate := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.sig.Store("cellular")
	s := h.waitGate(t, "reopened", 1)
	if s.LastGate.Reason != "network_changed" || s.State != StateConnected {
		t.Fatalf("%+v %s", s.LastGate, s.State)
	}
	time.Sleep(100 * time.Millisecond) // ten watchdog ticks on the new network
	if gate.suspends.Load() != 1 || h.m.Snapshot().LastGate.TotalClosures != 1 {
		t.Fatal("the confirmed network must become the segment's network")
	}
}

// While the server cannot be reached the gate stays closed (tunnel held, nothing
// forwarded), the UI is told it is waiting for the network, and it reopens on its own.
func TestGateWaitsForTheNetworkWithoutForwarding(t *testing.T) {
	h, gate := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	stops := h.engine.stops.Load()
	h.setTrafficErr(unavailable)
	h.clock.sleep(time.Hour)
	var s Snapshot
	waitFor(t, "waiting gate", func() bool {
		s = h.m.Snapshot()
		return s.Gate != nil && s.Gate.WaitingNetwork && s.Gate.Attempts >= 2
	})
	if s.State != StateConnected || s.TunnelHealth != "gated" || s.Gate.Reason != "lifecycle_reconfirmation_required" {
		t.Fatalf("gated snapshot: %s %s %+v", s.State, s.TunnelHealth, s.Gate)
	}
	if !gate.suspended.Load() || h.engine.stops.Load() != stops {
		t.Fatal("forwarding must be held back without removing the tunnel")
	}
	// Time keeps counting while it waits (also asleep: it is wall time the user lived).
	h.clock.sleep(2 * time.Minute)
	waitFor(t, "closed time", func() bool { g := h.m.Snapshot().Gate; return g != nil && g.ClosedSeconds >= 120 })
	h.setTrafficErr(nil)
	s = h.waitGate(t, "reopened", 1)
	if s.State != StateConnected || gate.suspended.Load() || s.LastGate.Attempts < 2 || s.LastGate.ClosedMillis < 120_000 {
		t.Fatalf("after recovery: %s %+v", s.State, s.LastGate)
	}
}

// A verdict found while re-confirming ends the segment exactly as on the desktop, with
// the server's reason and the tail settled.
func TestGateStopsOnTheServersVerdict(t *testing.T) {
	h, _ := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.engine.down.Store(4 * mib)
	h.f.mu.Lock() // no catalog revision: only the re-confirmation can learn about it
	h.f.session.CanConnect, h.f.session.Reason = false, "subscription_expired"
	h.f.mu.Unlock()
	h.clock.sleep(time.Hour)
	h.waitStopped(t, "subscription_expired")
	s := h.waitGate(t, "stopped", 1)
	if s.Gate != nil || s.TunnelHealth != "idle" || h.engine.stops.Load() == 0 {
		t.Fatalf("after the verdict: %s %+v stops %d", s.TunnelHealth, s.Gate, h.engine.stops.Load())
	}
	if units, _, down, _ := h.ledger(); down != 4*mib || units != 4*mib*1000 {
		t.Fatalf("tail not settled: %d units %d down", units, down)
	}
}

// Power: after GateLimit of awake time without an answer the segment ends.
func TestGateGivesUpAfterTheAwakeLimit(t *testing.T) {
	h, _ := newGateHarness(t, nil, 50*time.Millisecond)
	h.connect(t)
	h.setTrafficErr(unavailable)
	h.clock.sleep(time.Hour)
	h.waitStopped(t, "network_unreachable")
	if s := h.waitGate(t, "stopped", 1); s.LastGate.Reason != "lifecycle_reconfirmation_required" {
		t.Fatalf("%+v", s.LastGate)
	}
}

// A lease that lapses while the control plane is unreachable closes the gate instead of
// forwarding unauthorized or dropping the VPN.
func TestLapsedLeaseClosesTheGate(t *testing.T) {
	h, gate := newGateHarness(t, func(f *fakeAPI) { f.session.LeaseSeconds = 2 }, time.Minute)
	h.connect(t)
	h.setTrafficErr(unavailable)
	h.clock.pass(5 * time.Second) // real elapsed time: no clock jump, just a lapsed lease
	waitFor(t, "lease gate", func() bool {
		s := h.m.Snapshot()
		return s.Gate != nil && s.Gate.Reason == "lease_expired"
	})
	if !gate.suspended.Load() {
		t.Fatal("an expired lease must stop forwarding")
	}
	h.setTrafficErr(nil)
	h.waitGate(t, "reopened", 1)
}

// The user can always end a closed gate, and the teardown records it.
func TestDisconnectWhileTheGateIsClosed(t *testing.T) {
	h, _ := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.setTrafficErr(unavailable)
	h.clock.sleep(time.Hour)
	waitFor(t, "gate", func() bool { return h.m.Snapshot().Gate != nil })
	h.setTrafficErr(nil)
	done := make(chan Snapshot, 1)
	go func() { done <- h.m.Disconnect(context.Background()) }()
	select {
	case s := <-done:
		if s.State != StateReady || s.Gate != nil || s.LastGate == nil || s.LastGate.Outcome != "stopped" {
			t.Fatalf("disconnect: %s %+v %+v", s.State, s.Gate, s.LastGate)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("disconnect waited on the closed gate")
	}
}

// ResumeGate with an engine that cannot hold traffic back keeps the stop behaviour, and
// so does a failing Suspend: an unconfirmed segment never keeps forwarding.
func TestGateFallsBackToStopping(t *testing.T) {
	t.Run("engine without a gate", func(t *testing.T) {
		h := newHarnessWith(t, 1000, nil, func(_ *harness, o *Options) { o.ResumePolicy = ResumeGate })
		h.connect(t)
		h.clock.sleep(time.Hour)
		h.waitStopped(t, "lifecycle_reconfirmation_required")
	})
	t.Run("suspend fails", func(t *testing.T) {
		h, gate := newGateHarness(t, nil, time.Minute)
		gate.suspendErr = errors.New("cannot")
		h.connect(t)
		h.sig.Store("cellular")
		h.waitStopped(t, "network_changed")
	})
}

// Node-reported segments re-confirm with GET /session (they have no ledger of their own).
func TestGateReconfirmsANodeReportedSegment(t *testing.T) {
	var gate *gatingEngine
	h := newGrantHarness(t, nil, func(o *Options) {
		gate = &gatingEngine{recordingEngine: o.Engine.(*recordingEngine)}
		o.Engine, o.ResumePolicy = gate, ResumeGate
	})
	h.connect(t)
	hits := h.f.sessionHits.Load()
	h.clock.sleep(time.Hour)
	s := h.waitGate(t, "reopened", 1)
	if s.State != StateConnected || h.f.sessionHits.Load() <= hits || h.f.trafficHits.Load() != 0 || gate.resumes.Load() != 1 {
		t.Fatalf("state %s session hits %d traffic %d", s.State, h.f.sessionHits.Load(), h.f.trafficHits.Load())
	}
}

// 7.6: in the background the catalog long-poll pauses; in the foreground it resumes at
// once. Desktop builds never call SetForeground.
func TestBackgroundPausesTheCatalogWatch(t *testing.T) {
	h := newHarness(t, 1000)
	h.m.SetForeground(false)
	time.Sleep(150 * time.Millisecond) // let a long-poll in flight (50 ms) finish
	before := h.f.nodesSeen.Load()
	time.Sleep(300 * time.Millisecond)
	if got := h.f.nodesSeen.Load(); got != before {
		t.Fatalf("the watcher polled %d times in the background", got-before)
	}
	h.m.SetForeground(false) // idempotent
	h.m.SetForeground(true)
	waitFor(t, "watch to resume", func() bool { return h.f.nodesSeen.Load() > before })
	h.m.SetForeground(true)
}

// The server's verdict still reaches a connected, backgrounded segment through its
// traffic acknowledgements, so pausing the watcher never extends access.
func TestBackgroundStillStopsOnTheServersVerdict(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.m.SetForeground(false)
	h.f.mutate(func() { h.f.session.CanConnect, h.f.session.Reason = false, "subscription_expired" })
	h.waitStopped(t, "subscription_expired")
}

func TestGateSnapshotsCarryNoSecrets(t *testing.T) {
	h, _ := newGateHarness(t, nil, time.Minute)
	h.connect(t)
	h.setTrafficErr(unavailable)
	h.clock.sleep(time.Hour)
	waitFor(t, "gate", func() bool { return h.m.Snapshot().Gate != nil })
	if g := h.m.Snapshot().Gate; g.Reason == "" || apiclient.Message(g.Reason) == "" {
		t.Fatalf("gate reason must be a known code: %+v", g)
	}
}

// A nudge (the network came back, the app was opened) makes a waiting gate retry at once
// instead of after its backoff.
func TestANudgeRetriesAWaitingGateAtOnce(t *testing.T) {
	h, _ := newGateHarnessWith(t, nil, time.Minute, func(o *Options) { o.BackoffMin, o.BackoffMax = time.Hour, time.Hour })
	h.connect(t)
	h.setTrafficErr(unavailable)
	h.clock.sleep(time.Hour)
	waitFor(t, "waiting gate", func() bool { g := h.m.Snapshot().Gate; return g != nil && g.WaitingNetwork })
	h.setTrafficErr(nil)
	time.Sleep(50 * time.Millisecond)
	if h.m.Snapshot().Gate == nil {
		t.Fatal("the gate retried before its hour-long backoff without a nudge")
	}
	h.m.Nudge()
	h.waitGate(t, "reopened", 1)
}

// A node bound while the catalog long-poll is paused (Android background, e.g. a connect from
// the Quick Settings tile) is bound against the current catalog, so the catalog that arrives
// in the foreground does not invalidate it as "changed".
func TestBindingWhileTheWatchIsPausedReadsTheCatalogFirst(t *testing.T) {
	h := newHarness(t, 1000)
	h.m.SetForeground(false)
	time.Sleep(150 * time.Millisecond) // a long-poll in flight (50 ms) ends; none follows
	h.f.mutate(func() { h.f.nodes[0].Name, h.f.nodes[0].Version = "HK Dedicated 2", 2 })
	if got := h.m.Snapshot().Nodes[0].Name; got != "HK Dedicated" {
		t.Fatalf("the paused watcher already saw the edit: %s", got)
	}
	snap, err := h.m.SelectNode(t.Context(), nodeA, h.m.Snapshot().CatalogRevision)
	if err != nil || !snap.ConfigReady || snap.Nodes[0].Name != "HK Dedicated 2" {
		t.Fatalf("select: %v ready=%v name=%s", err, snap.ConfigReady, snap.Nodes[0].Name)
	}
	h.m.SetForeground(true)
	time.Sleep(200 * time.Millisecond) // the resumed watcher delivers the same catalog
	if s := h.m.Snapshot(); !s.ConfigReady || s.LastError != nil {
		t.Fatalf("the binding was invalidated after the foreground: ready=%v %+v", s.ConfigReady, s.LastError)
	}
	h.connect(t)
}
