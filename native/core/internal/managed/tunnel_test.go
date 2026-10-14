package managed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

const mib = 1 << 20

// shiftClock lets a test move the wall clock, and optionally the monotonic one, to
// simulate time passing (both) or the machine sleeping (wall only).
type shiftClock struct {
	origin     time.Time
	wallOffset atomic.Int64
	monoOffset atomic.Int64
}

func newShiftClock() *shiftClock { return &shiftClock{origin: time.Now()} }

func (c *shiftClock) now() time.Time {
	return time.Now().Add(time.Duration(c.wallOffset.Load()))
}
func (c *shiftClock) mono() time.Duration {
	return time.Since(c.origin) + time.Duration(c.monoOffset.Load())
}

// pass: time genuinely elapses. sleep: only the wall clock moves.
func (c *shiftClock) pass(d time.Duration)  { c.wallOffset.Add(int64(d)); c.monoOffset.Add(int64(d)) }
func (c *shiftClock) sleep(d time.Duration) { c.wallOffset.Add(int64(d)) }

type harness struct {
	f      *fakeAPI
	engine *recordingEngine
	m      *Manager
	clock  *shiftClock
	sig    atomic.Value
}

func newHarness(t *testing.T, rate int64) *harness {
	t.Helper()
	f := newFakeAPI(t)
	f.mu.Lock()
	f.session.RatePermille = rate
	f.session.ReportInterval = 1
	f.mu.Unlock()
	h := &harness{f: f, engine: &recordingEngine{api: f}, clock: newShiftClock()}
	h.sig.Store("wifi")
	h.m = New(Options{
		CoreVersion: "t", Engine: h.engine, Now: h.clock.now, Mono: h.clock.mono,
		Signature:  func() string { return h.sig.Load().(string) },
		BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond,
		MonitorInterval: 10 * time.Millisecond, SettleTimeout: 2 * time.Second,
	})
	t.Cleanup(func() { h.m.Shutdown(context.Background()) })
	if _, err := h.m.Login(t.Context(), f.params(f.password)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catalog", func() bool { return h.m.Snapshot().CatalogRevision != "" })
	selectA(t, h.m)
	return h
}

func (h *harness) connect(t *testing.T) Snapshot {
	t.Helper()
	snap, err := h.m.Connect(t.Context())
	if err != nil || snap.State != StateConnected {
		t.Fatalf("connect: %v %+v", err, snap.LastError)
	}
	return snap
}

func (h *harness) ledger() (units, up, down, seq int64) {
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	s := h.f.session
	return s.UsedUnits, s.UploadBytes, s.DownloadBytes, s.LastSequence
}

func (h *harness) waitStopped(t *testing.T, reason string) Snapshot {
	t.Helper()
	waitFor(t, "tunnel to stop with "+reason, func() bool {
		s := h.m.Snapshot()
		return s.State != StateConnected && s.State != StateStopping && s.LastError != nil && s.LastError.Code == reason
	})
	return h.m.Snapshot()
}

// B01 through the whole state machine: identical bytes at 1x and 0.5x charge exactly
// 100 MiB and 50 MiB of quota, and the order is fixed: first metering ack, then TUN.
func TestMeteredSessionChargesExactIntegerUnits(t *testing.T) {
	for _, tc := range []struct{ rate, units int64 }{{1000, 104_857_600_000}, {500, 52_428_800_000}} {
		h := newHarness(t, tc.rate)
		snap := h.connect(t)
		if h.engine.trafficAtStart.Load() < 1 {
			t.Fatal("the tunnel was started before the first metering acknowledgement")
		}
		if h.engine.verifies.Load() != 1 || snap.TunnelHealth != "up" {
			t.Fatalf("connected without verification: %+v", snap)
		}
		h.engine.up.Store(20 * mib)
		h.engine.down.Store(80 * mib)
		snap = h.m.Disconnect(t.Context())
		if snap.State != StateReady || snap.LastError != nil {
			t.Fatalf("disconnect: %+v %+v", snap.State, snap.LastError)
		}
		if units, up, down, _ := h.ledger(); units != tc.units || up != 20*mib || down != 80*mib {
			t.Fatalf("rate %d: ledger units=%d up=%d down=%d", tc.rate, units, up, down)
		}
		if v := snap.Metering; v.UploadBytes != 20*mib || v.DownloadBytes != 80*mib || v.Pending || v.UploadRate != 0 {
			t.Fatalf("metering view: %+v", v)
		}
	}
}

func TestPeriodicReportsAreTheOnlyRenewalWhileConnected(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	time.Sleep(300 * time.Millisecond) // let login-time catalog sync settle
	sessionHits := h.f.sessionHits.Load()
	h.engine.down.Store(3 * mib)
	waitFor(t, "periodic /traffic", func() bool { _, _, down, _ := h.ledger(); return down == 3*mib })
	waitFor(t, "second report", func() bool { _, _, _, seq := h.ledger(); return seq >= 2 })
	if hits := h.f.sessionHits.Load(); hits != sessionHits {
		t.Fatalf("a second renewal writer ran while connected: %d extra /session calls", hits-sessionHits)
	}
	// The ledger moves before the acknowledgement is folded in; wait for the Core's view.
	waitFor(t, "acknowledged sequence", func() bool { return h.m.Snapshot().Metering.LastSequence >= 2 })
	if s := h.m.Snapshot(); s.State != StateConnected {
		t.Fatalf("%+v", s)
	}
}

// B02: the acknowledgement is lost; the identical batch is retried and charged once.
func TestLostAcknowledgementReplaysTheSameBatch(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.f.mu.Lock()
	h.f.loseAcks = 1
	h.f.mu.Unlock()
	h.engine.up.Store(1 * mib)
	waitFor(t, "lost ack then replay", func() bool {
		h.f.mu.Lock()
		defer h.f.mu.Unlock()
		n := len(h.f.bodies)
		return h.f.loseAcks == 0 && n >= 2 && h.f.bodies[n-1] == h.f.bodies[n-2]
	})
	snap := h.m.Disconnect(t.Context())
	units, up, _, _ := h.ledger()
	if units != 1*mib*1000 || up != 1*mib || snap.Metering.Pending {
		t.Fatalf("units=%d up=%d view=%+v", units, up, snap.Metering)
	}
}

func TestEngineStepsFailClosed(t *testing.T) {
	t.Run("conflicting vpn stops before any metering or tunnel", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.engine.preflightErr = ErrVPNConflict
		hits := h.f.trafficHits.Load()
		snap, err := h.m.Connect(t.Context())
		if code(t, err) != "vpn_conflict" || h.engine.starts.Load() != 0 || h.f.trafficHits.Load() != hits || snap.State != StateReady {
			t.Fatalf("%v starts=%d %+v", err, h.engine.starts.Load(), snap.State)
		}
		if snap.LastError.Code != "vpn_conflict" || snap.LastError.Message == "" {
			t.Fatalf("%+v", snap.LastError)
		}
	})
	t.Run("tunnel start failure leaves nothing running", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.engine.err = ErrTunnelFailed
		snap, err := h.m.Connect(t.Context())
		if code(t, err) != "tunnel_failed" || snap.State != StateReady || h.engine.stops.Load() == 0 {
			t.Fatalf("%v %+v", err, snap.State)
		}
	})
	t.Run("a tunnel that does not forward is torn down and its probe bytes are settled", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.engine.verifyErr = ErrTunnelUnhealthy
		h.engine.verifyHook = func() { h.engine.up.Store(500) } // the probe attempt itself moves bytes
		snap, err := h.m.Connect(t.Context())
		if code(t, err) != "tunnel_unhealthy" || snap.State == StateConnected || h.engine.stops.Load() == 0 {
			t.Fatalf("%v %+v", err, snap.State)
		}
		if _, up, _, _ := h.ledger(); up != 500 {
			t.Fatalf("probe bytes not settled: %d", up)
		}
	})
	t.Run("stage 2 engine still refuses honestly", func(t *testing.T) {
		f := newFakeAPI(t)
		m := login(t, f, UnavailableEngine())
		selectA(t, m)
		snap, err := m.Connect(t.Context())
		if code(t, err) != "tun_unavailable" || snap.State != StateReady || snap.TunnelHealth != "idle" {
			t.Fatalf("%v %+v", err, snap.State)
		}
	})
}

func TestConnectIsIdempotentAndSerialised(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	if snap, err := h.m.Connect(t.Context()); err != nil || snap.State != StateConnected || h.engine.starts.Load() != 1 {
		t.Fatalf("second connect: %v starts=%d", err, h.engine.starts.Load())
	}
	h.m.Disconnect(t.Context())
	h.m.Disconnect(t.Context())
	if h.engine.starts.Load() != 1 {
		t.Fatal("disconnect restarted the engine")
	}
}

// Authorization loss is enforced by the official client (section 6.3 / C04).
func TestAuthorizationLossStopsTheTunnel(t *testing.T) {
	t.Run("quota exhausted by the local estimate", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.f.mu.Lock()
		h.f.session.TotalBytes, h.f.session.RemainingBytes = 10*mib, 10*mib
		h.f.mu.Unlock()
		h.m.Disconnect(t.Context()) // no-op; re-read session for the smaller quota
		if _, err := h.m.RefreshCatalog(t.Context()); err != nil {
			t.Fatal(err)
		}
		h.connect(t)
		h.engine.down.Store(11 * mib)
		snap := h.waitStopped(t, "quota_exhausted")
		if snap.State == StateConnected || h.engine.stops.Load() == 0 {
			t.Fatalf("%+v", snap.State)
		}
		if _, _, down, _ := h.ledger(); down != 11*mib {
			t.Fatalf("tail not settled after the stop: %d", down)
		}
	})
	t.Run("lease expiry when the API is unreachable", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.f.mu.Lock()
		h.f.trafficErr = &apiError{503, "client_service_unavailable"}
		h.f.mu.Unlock()
		// Time must advance in watchdog-sized steps: one 91 s leap is, by design, a
		// suspended machine (stallLimit) and stops with a different reason.
		for range 11 {
			h.clock.pass(9 * time.Second)
			time.Sleep(60 * time.Millisecond)
		}
		snap := h.waitStopped(t, "lease_expired")
		if snap.State == StateConnected {
			t.Fatal("still connected on an expired lease")
		}
	})
	t.Run("the server revokes the subscription", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.f.mu.Lock()
		h.f.session.CanConnect, h.f.session.Reason = false, "subscription_expired"
		h.f.mu.Unlock()
		h.waitStopped(t, "subscription_expired")
		if s := h.m.Snapshot(); s.State != StateBlocked {
			t.Fatalf("state %s", s.State)
		}
	})
	t.Run("the bearer token is revoked", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.f.mu.Lock()
		h.f.trafficErr = &apiError{401, "native_session_expired"}
		h.f.mu.Unlock()
		waitFor(t, "sign-out", func() bool { return h.m.Snapshot().State == StateSignedOut })
		if h.engine.stops.Load() == 0 {
			t.Fatal("tunnel survived the loss of the session")
		}
	})
	t.Run("admin edits the node while connected", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.f.mutate(func() { h.f.nodes[0].RatePermille, h.f.nodes[0].Version = 1500, 2 })
		h.waitStopped(t, "profile_changed")
		if s := h.m.Snapshot(); s.ConfigReady || s.State == StateConnected {
			t.Fatalf("%+v", s)
		}
	})
	// A04 on a live tunnel: the operator disables/deletes the node, or the plan stops
	// covering it. Whatever reason the user sees, the data path must close, the flow
	// already carried must be charged at the OLD rate, and the node must not stay selected.
	for name, change := range map[string]func(h *harness){
		"admin deletes or disables the node": func(h *harness) { h.f.mutate(func() { h.f.nodes = h.f.nodes[1:] }) },
		"the plan no longer covers the node": func(h *harness) { h.f.mutate(func() { h.f.catalogRsn = "subscription_expired" }) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, 1000)
			h.connect(t)
			h.engine.down.Store(7 * mib)
			change(h)
			waitFor(t, "the tunnel to close", func() bool {
				s := h.m.Snapshot()
				return s.State != StateConnected && s.State != StateStopping
			})
			if h.engine.stops.Load() == 0 {
				t.Fatal("engine never stopped")
			}
			waitFor(t, "the tail to be acknowledged", func() bool { _, _, down, _ := h.ledger(); return down == 7*mib })
			if units, _, _, _ := h.ledger(); units != 7*mib*1000 {
				t.Fatalf("tail charged %d units, want %d", units, 7*mib*1000)
			}
			if s := h.m.Snapshot(); s.LastError == nil || s.LastError.Code == "" || (s.Metering != nil && s.Metering.Pending) {
				t.Fatalf("no reason or unsettled tail: %+v", s)
			}
			if _, err := h.m.Connect(t.Context()); err == nil {
				t.Fatal("connect allowed after the node left the plan")
			}
		})
	}
}

// T06: sleeping or changing the network closes the data path before anything else.
func TestLifecycleGuards(t *testing.T) {
	t.Run("wake from sleep", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.clock.sleep(60 * time.Second)
		h.waitStopped(t, "lifecycle_reconfirmation_required")
	})
	t.Run("resume when the monotonic clock counts sleep (Windows)", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.clock.pass(45 * time.Second) // wall and monotonic both advance across the suspend
		h.waitStopped(t, "lifecycle_reconfirmation_required")
	})
	t.Run("clock set back", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.clock.sleep(-60 * time.Second)
		h.waitStopped(t, "lifecycle_reconfirmation_required")
	})
	t.Run("network change", func(t *testing.T) {
		h := newHarness(t, 1000)
		h.connect(t)
		h.sig.Store("ethernet")
		h.waitStopped(t, "network_changed")
	})
	t.Run("reconnect works after a guard fired and the tail is acknowledged", func(t *testing.T) {
		h := newHarness(t, 500)
		h.connect(t)
		h.engine.up.Store(2 * mib)
		h.sig.Store("ethernet")
		h.waitStopped(t, "network_changed")
		if units, _, _, _ := h.ledger(); units != 2*mib*500 {
			t.Fatalf("tail %d", units)
		}
		h.connect(t)
	})
}

func TestCounterRegressionEndsTheSession(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.engine.up.Store(1000)
	waitFor(t, "sample", func() bool { return h.m.Snapshot().Metering.UploadBytes == 1000 })
	h.engine.up.Store(10) // impossible: counters never decrease inside a segment
	waitFor(t, "forced re-login", func() bool { return h.m.Snapshot().State == StateSignedOut })
	if s := h.m.Snapshot(); s.LastError == nil || s.LastError.Code != "invalid_core_counters" {
		t.Fatalf("%+v", s.LastError)
	}
	if h.engine.stops.Load() == 0 {
		t.Fatal("tunnel kept running on a corrupt ledger")
	}
}

// Section 6.3: changing node waits for the old tail, then binds using the confirmed sequence.
func TestSwitchingNodeSettlesBeforeRebinding(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.engine.down.Store(5 * mib)
	snap := h.m.Snapshot()
	out, err := h.m.SelectNode(t.Context(), nodeB, snap.CatalogRevision)
	if err != nil || out.SelectedNodeID != nodeB || out.State == StateConnected {
		t.Fatalf("%v %+v", err, out.State)
	}
	_, _, down, seq := h.ledger()
	if down != 5*mib {
		t.Fatalf("old tail not settled before the new bind: %d", down)
	}
	h.f.mu.Lock()
	seen := h.f.lastSeqSeen
	h.f.mu.Unlock()
	if seen != itoa(seq) || h.engine.stops.Load() == 0 {
		t.Fatalf("config requested with last_sequence=%s, ledger at %d", seen, seq)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

func TestLogoutSettlesTailBeforeRevoking(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.engine.up.Store(7 * mib)
	snap := h.m.Logout(t.Context())
	if snap.State != StateSignedOut || h.f.logoutHits.Load() != 1 {
		t.Fatalf("%+v", snap.State)
	}
	if _, up, _, _ := h.ledger(); up != 7*mib {
		t.Fatalf("logout dropped the tail: %d", up)
	}
}

// Unconfirmed bytes are never discarded: they block reconnecting until acknowledged,
// and the idle keep-alive replays them on its own.
func TestUnconfirmedTailBlocksReconnectUntilAcknowledged(t *testing.T) {
	h := newHarness(t, 1000)
	h.connect(t)
	h.engine.up.Store(3 * mib)
	h.f.mu.Lock()
	h.f.trafficErr = &apiError{503, "client_service_unavailable"}
	h.f.mu.Unlock()
	snap := h.m.Disconnect(t.Context())
	if snap.State != StateReady || !snap.Metering.Pending || snap.LastError == nil || snap.LastError.Code != "client_service_unavailable" && snap.LastError.Code != "traffic_unconfirmed" {
		t.Fatalf("%+v %+v", snap.State, snap.LastError)
	}
	if _, err := h.m.Connect(t.Context()); code(t, err) != "traffic_unconfirmed" {
		t.Fatalf("reconnect with an unacknowledged tail: %v", err)
	}
	if h.engine.starts.Load() != 1 {
		t.Fatal("tunnel restarted over an unconfirmed ledger")
	}
	h.f.mu.Lock()
	h.f.trafficErr = nil
	h.f.mu.Unlock()
	waitFor(t, "idle replay", func() bool { return !h.m.Snapshot().Metering.Pending })
	if _, up, _, _ := h.ledger(); up != 3*mib {
		t.Fatalf("%d", up)
	}
	h.connect(t)
}

func TestMeterErrorsAreStableCodes(t *testing.T) {
	err := &apiclient.Error{Code: "traffic_conflict"}
	var target *apiclient.Error
	if !errors.As(err, &target) {
		t.Fatal("sanity")
	}
	for _, code := range []string{"vpn_conflict", "no_network", "tunnel_failed", "tunnel_unhealthy", "traffic_unconfirmed", "invalid_core_counters", "lifecycle_reconfirmation_required", "network_changed"} {
		if apiclient.Message(code) == "操作失败，请稍后重试" {
			t.Fatalf("no localized message for %s", code)
		}
	}
}

func TestClockWatch(t *testing.T) {
	var w clockWatch
	wall := time.Unix(1_700_000_000, 0)
	if w.jumped(wall, 0) {
		t.Fatal("first observation cannot be a jump")
	}
	if w.jumped(wall.Add(time.Second), time.Second) {
		t.Fatal("normal tick flagged")
	}
	if !w.jumped(wall.Add(61*time.Second), 2*time.Second) {
		t.Fatal("sleep not detected")
	}
	if !w.jumped(wall.Add(-100*time.Second), 3*time.Second) {
		t.Fatal("clock set back not detected")
	}
}

// Windows: the monotonic clock keeps counting through sleep, so wall and monotonic move
// together. The stalled watchdog must still be recognised.
func TestClockWatchDetectsSuspendWhenMonotonicCountsSleep(t *testing.T) {
	var w clockWatch
	wall := time.Unix(1_700_000_000, 0)
	w.jumped(wall, 0)
	if w.jumped(wall.Add(3*time.Second), 3*time.Second) {
		t.Fatal("a slow tick (3 s) must not be treated as a suspend")
	}
	if !w.jumped(wall.Add(3*time.Second+30*time.Minute), 3*time.Second+30*time.Minute) {
		t.Fatal("a 30 minute stall with a sleep-inclusive monotonic clock went unnoticed")
	}
	if w.jumped(wall.Add(3*time.Second+30*time.Minute+time.Second), 3*time.Second+30*time.Minute+time.Second) {
		t.Fatal("the watch must re-arm after reporting")
	}
}
