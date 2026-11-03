package managed

import (
	"context"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

// A session that ends while a tunnel runs (dropSessionLocked). subscription_changed (a
// mid-period upgrade, an administrator's plan edit) leaves the token valid for /traffic, so
// the tail counted since the last report is settled with it and the online lease released;
// a dead token (native_session_*) only closes the data path.

// quietHarness connects with no periodic report and no watchdog tick inside the test, so
// every byte the engine counts after the connect is a tail only the session's end can settle.
func quietHarness(t *testing.T, rate int64) *harness {
	t.Helper()
	h := newHarnessWith(t, rate, func(f *fakeAPI) { f.session.ReportInterval = 600 }, func(_ *harness, o *Options) { o.MonitorInterval = time.Hour })
	h.connect(t)
	if h.f.activeLeases() != 1 {
		t.Fatalf("%d leases after connect", h.f.activeLeases())
	}
	return h
}

// The plan changed under the session: the server's catalog and session both say so, as the
// real server does once the entitlement fingerprint no longer matches.
func (h *harness) changePlan() {
	h.f.mutate(func() {
		h.f.catalogRsn = "subscription_changed"
		h.f.session.Reason, h.f.session.CanConnect = "subscription_changed", false
	})
}

func (h *harness) waitSignedOut(t *testing.T, reason string) Snapshot {
	t.Helper()
	var s Snapshot
	waitFor(t, "sign-out with "+reason, func() bool {
		s = h.m.Snapshot()
		return s.State == StateSignedOut && s.LastError != nil && s.LastError.Code == reason
	})
	if h.engine.stops.Load() == 0 || s.User != nil || len(s.Nodes) != 0 {
		t.Fatalf("session not torn down: stops %d, %+v", h.engine.stops.Load(), s)
	}
	return s
}

func TestSubscriptionChangedSettlesTheTail(t *testing.T) {
	t.Run("seen by the catalog watch", func(t *testing.T) {
		h := quietHarness(t, 500)
		_, _, _, seq := h.ledger()
		h.engine.up.Store(mib)
		h.engine.down.Store(3 * mib)
		h.changePlan()
		h.waitSignedOut(t, "subscription_changed")
		waitFor(t, "the tail to be charged", func() bool { _, up, down, _ := h.ledger(); return up == mib && down == 3*mib })
		if units, _, _, last := h.ledger(); units != 4*mib*500 || last != seq+1 {
			t.Fatalf("tail charged %d units in %d reports, want %d in one", units, last-seq, 4*mib*500)
		}
		waitFor(t, "the lease to be released", func() bool { return len(h.f.releasesSeen()) == 1 })
		if r := h.f.releasesSeen()[0]; r.reason != apiclient.ReleaseStopped || h.f.activeLeases() != 0 {
			t.Fatalf("release %+v, %d leases active", r, h.f.activeLeases())
		}
		// Nothing more: the old session writes no further batch and cannot reconnect.
		hits := h.f.trafficHits.Load()
		if _, err := h.m.Connect(t.Context()); code(t, err) != "not_signed_in" {
			t.Fatalf("connect after the plan changed: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		if h.f.trafficHits.Load() != hits {
			t.Fatal("the dropped session reported again")
		}
	})
	t.Run("carried by a report acknowledgement", func(t *testing.T) {
		h := newHarness(t, 500)
		h.connect(t)
		// No catalog change: the next periodic report is the first to hear of it.
		h.f.mu.Lock()
		h.f.session.Reason, h.f.session.CanConnect = "subscription_changed", false
		h.f.mu.Unlock()
		h.engine.down.Store(5 * mib)
		h.waitSignedOut(t, "subscription_changed")
		waitFor(t, "the lease to be released", func() bool { return len(h.f.releasesSeen()) == 1 })
		if units, _, down, _ := h.ledger(); down != 5*mib || units != 5*mib*500 {
			t.Fatalf("charged %d units for %d bytes, want each byte once at 500", units, down)
		}
	})
	t.Run("a report in flight is replayed, not lost", func(t *testing.T) {
		h := quietHarness(t, 1000)
		h.engine.down.Store(2 * mib)
		// The batch reaches the server but its reply is lost; then the plan changes.
		h.f.mu.Lock()
		h.f.loseAcks = 1
		h.f.mu.Unlock()
		h.m.mu.Lock()
		mt := h.m.meter
		h.m.mu.Unlock()
		if _, _, err := mt.Flush(t.Context()); err == nil || !mt.Pending() {
			t.Fatalf("lost acknowledgement left no pending batch: %v", err)
		}
		h.engine.down.Store(3 * mib)
		h.changePlan()
		h.waitSignedOut(t, "subscription_changed")
		waitFor(t, "the tail to be charged", func() bool { _, _, down, _ := h.ledger(); return down == 3*mib })
		if units, _, _, _ := h.ledger(); units != 3*mib*1000 {
			t.Fatalf("charged %d units, want %d (the replay must not charge twice)", units, 3*mib*1000)
		}
	})
	t.Run("a new period refuses the tail", func(t *testing.T) {
		h := quietHarness(t, 1000)
		h.engine.down.Store(3 * mib)
		h.f.mu.Lock()
		h.f.trafficErr = &apiError{409, "subscription_changed"}
		h.f.mu.Unlock()
		hits := h.f.trafficHits.Load()
		h.changePlan()
		h.waitSignedOut(t, "subscription_changed")
		waitFor(t, "the tail to be offered", func() bool { return h.f.trafficHits.Load() > hits })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { h.m.Shutdown(ctx); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Shutdown waited on a refused tail")
		}
		if _, _, down, _ := h.ledger(); down != 0 || len(h.f.releasesSeen()) != 0 {
			t.Fatalf("a refused tail was charged (%d) or its lease released before it (%v)", down, h.f.releasesSeen())
		}
	})
}

// A dead token cannot settle anything: the data path closes and nothing more is sent.
func TestDeadTokenDropsTheTailUnsent(t *testing.T) {
	for _, reason := range []string{"native_session_expired", "native_session_required"} {
		t.Run(reason, func(t *testing.T) {
			h := quietHarness(t, 1000)
			h.engine.down.Store(3 * mib)
			traffic, releases := h.f.trafficHits.Load(), h.f.conns.releaseHits.Load()
			h.f.mu.Lock()
			h.f.nodesErr = &apiError{401, reason}
			h.f.mu.Unlock()
			h.waitSignedOut(t, reason)
			time.Sleep(100 * time.Millisecond)
			if h.f.trafficHits.Load() != traffic || h.f.conns.releaseHits.Load() != releases {
				t.Fatalf("a dead token was used: %d traffic, %d release requests after the drop",
					h.f.trafficHits.Load()-traffic, h.f.conns.releaseHits.Load()-releases)
			}
			if _, _, down, _ := h.ledger(); down != 0 {
				t.Fatalf("charged %d bytes", down)
			}
		})
	}
}

// Shutdown right after the plan changed still delivers the tail before the process exits.
func TestShutdownWaitsForTheDroppedTail(t *testing.T) {
	h := quietHarness(t, 1000)
	h.engine.down.Store(4 * mib)
	h.f.mu.Lock()
	h.f.conns.releaseGate = make(chan struct{})
	gate := h.f.conns.releaseGate
	h.f.mu.Unlock()
	h.changePlan()
	h.waitSignedOut(t, "subscription_changed")
	waitFor(t, "the release to be on its way", func() bool { return h.f.conns.releaseHits.Load() == 1 })
	done := make(chan struct{})
	go func() { h.m.Shutdown(context.Background()); close(done) }()
	select {
	case <-done:
		t.Fatal("Shutdown returned before the dropped session was finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	<-done
	if _, _, down, _ := h.ledger(); down != 4*mib || len(h.f.releasesSeen()) != 1 {
		t.Fatalf("down %d, releases %v", down, h.f.releasesSeen())
	}
}
