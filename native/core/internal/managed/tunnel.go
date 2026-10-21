package managed

import (
	"context"
	"errors"
	"sync"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/meter"
)

// connection is one forwarding segment: from the moment the tunnel verified until the
// data path closes. The monitor goroutine belongs to it.
type connection struct {
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup // in-flight report goroutines
	// metered: the client ledger (meter) is the renewal writer. False for node-reported
	// segments, where GET /session renews the lease.
	metered bool
}

// abortConnLocked cancels the live segment's workers without waiting. It is used by
// session-ending paths that close the engine themselves.
func (m *Manager) abortConnLocked() {
	if m.conn != nil {
		m.conn.cancel()
		m.conn = nil
	}
}

// classifyLocked sets Ready or Blocked from the latest server verdict.
func (m *Manager) classifyLocked() {
	if m.session == nil {
		return
	}
	s := m.session
	if m.readyLocked(*s) {
		m.state = StateReady
	} else {
		m.state = StateBlocked
	}
}

func (m *Manager) newMeterLocked(client *apiclient.Client, token string) *meter.Meter {
	return meter.New(meter.Config{
		Base:   *m.session,
		Totals: m.opts.Engine.Totals,
		Now:    m.opts.Now,
		Report: func(ctx context.Context, c apiclient.Counters) (apiclient.Session, error) {
			s, _, err := client.Traffic(ctx, token, c)
			return s, err
		},
	})
}

// settleIdle makes sure no earlier traffic batch is still unacknowledged. It is a
// precondition for starting, switching and exiting (sections 6.3 and 7.2).
func (m *Manager) settleIdle(ctx context.Context, gen uint64) error {
	m.mu.Lock()
	mt := m.meter
	m.mu.Unlock()
	if mt == nil || mt.Settled() {
		return nil
	}
	return m.settle(ctx, gen, mt)
}

// settle flushes the meter until everything counted is acknowledged, and folds the
// resulting session (lease, balance, authorization) in.
func (m *Manager) settle(ctx context.Context, gen uint64, mt *meter.Meter) error {
	sctx, cancel := context.WithTimeout(ctx, m.opts.SettleTimeout)
	ack, started, err := mt.Settle(sctx)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return codeError("stale_generation")
	}
	if ack.SessionID != "" {
		if apiclient.TerminatesSession(ack.Reason) {
			m.dropSessionLocked(ack.Reason)
			return codeError(ack.Reason)
		}
		m.applySessionLocked(ack, started)
		if meter.ErrorCode(err) == meter.CodeRateChanged {
			err = nil // the acknowledgement itself is valid and final for the old rate
		}
	}
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.dropSessionLocked(apiErr.Code)
			return err
		}
		if code := mt.Fatal(); code != "" {
			// The ledger cannot be trusted any more: only a fresh login starts a new one.
			m.dropSessionLocked(code)
			return codeError(code)
		}
		m.failLocked(err)
		return codeError(meter.CodeUnconfirmed)
	}
	return nil
}

// Connect is the only path to Engine.Start and re-checks every gate itself:
// signed in, a selected line, a fresh server authorization, an online lease admitted for
// this device (which brings the one server's config, validated before use), no conflicting
// VPN, and a first metering acknowledgement. Only after the tunnel verifiably forwards does
// it report Connected (connection.go, grant.go).
func (m *Manager) Connect(ctx context.Context) (Snapshot, error) {
	if !m.opMu.TryLock() {
		return m.Snapshot(), codeError("operation_in_progress")
	}
	defer m.opMu.Unlock()
	m.bumpIntent()
	return m.connect(ctx)
}

// failMeter reports a metering failure that happened before any tunnel existed.
func (m *Manager) failMeter(gen uint64, mt *meter.Meter, err error) (Snapshot, error) {
	code := meter.ErrorCode(err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return m.snapshotLocked(), codeError("stale_generation")
	}
	if fatal := mt.Fatal(); fatal != "" {
		m.dropSessionLocked(fatal)
		return m.snapshotLocked(), codeError(fatal)
	}
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
		m.dropSessionLocked(apiErr.Code)
		return m.snapshotLocked(), err
	}
	m.classifyLocked()
	if code == meter.CodeRateChanged {
		m.profile = nil
	}
	m.failLocked(err)
	return m.snapshotLocked(), codeError(code)
}

// Disconnect closes the data path, settles the tail and releases the online lease. It is
// idempotent.
func (m *Manager) Disconnect(ctx context.Context) Snapshot {
	m.bumpIntent()
	_ = m.stopTunnel(ctx, "")
	return m.Snapshot()
}

// stopTunnel is the one way a segment ends, whether the user asked (reason "") or a
// guard fired. Order matters: the data path closes first, then the tail is
// acknowledged, and only then does the state move on. It never starts anything.
func (m *Manager) stopTunnel(ctx context.Context, reason string) error {
	return m.stopSegment(ctx, reason, false)
}

// stopSegment is stopTunnel; with bridge (an automatic reconnect follows at once) a segment
// that could reconnect ends in StatePreparing instead of Ready, so the apps, which treat a
// tunnel that left the busy states as "ended by itself" (Android stops its VPN service, the
// iOS extension cancels the tunnel), never see a gap. The reconnect owns the state from here
// (settleBridge).
func (m *Manager) stopSegment(ctx context.Context, reason string, bridge bool) error {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()

	m.mu.Lock()
	gen, conn, mt, grant, local := m.generation, m.conn, m.meter, m.grant, m.local
	active := conn != nil || m.state == StateConnected || m.state == StateConnecting
	held := m.admission != nil && active
	m.conn = nil
	m.closeGateRecordLocked("stopped")
	if active {
		m.state = StateStopping
	}
	m.mu.Unlock()

	if conn != nil {
		conn.cancel()
		<-conn.done
	}
	_ = m.opts.Engine.Stop()
	m.opts.Egress.Clear()

	// Node-reported: the data path is closed, so the grant can now be revoked at the node.
	if grant != nil {
		m.finishGrant(ctx, gen, grant, releaseReason(reason))
	}
	if local != nil {
		local.Idle()
	}

	var settleErr error
	if mt != nil {
		if active || !mt.Settled() {
			settleErr = m.settle(ctx, gen, mt)
		}
		mt.Idle() // after settlement: the final sample must not resurrect a displayed rate
	}
	// The device goes offline only now: forwarding has stopped and its tail is acknowledged
	// (or, if not, the release waits for keepAlive to settle it first).
	if held {
		m.endAdmission(ctx, gen, releaseReason(reason))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return settleErr
	}
	if m.state == StateStopping {
		m.classifyLocked()
		if m.state == StateStopping { // no session view (should not happen): fall back
			m.state = StateReady
		}
		if bridge && m.state == StateReady {
			m.state, m.bridge = StatePreparing, true
		}
	}
	switch {
	case reason != "":
		m.lastError = &errorInfo{Code: reason, Message: apiclient.Message(reason), stop: true}
	case settleErr != nil && active:
		code := meter.CodeUnconfirmed
		var apiErr *apiclient.Error
		if errors.As(settleErr, &apiErr) {
			code = apiErr.Code
		}
		m.lastError = &errorInfo{Code: code, Message: apiclient.Message(code), stop: true}
	case active:
		m.lastError = nil
	}
	if m.stopped != nil && reason != "" {
		m.stopped(reason)
	}
	return settleErr
}

// requestStop ends the segment from a watchdog without blocking it (stopTunnel waits
// for the watchdog to exit). A server-side move (connection_reassigned) is followed by one
// automatic reconnect to the same line, unless the user acted meanwhile.
func (m *Manager) requestStop(gen uint64, reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), m.opts.SettleTimeout+releaseTimeout+5*time.Second)
		defer cancel()
		m.mu.Lock()
		current, intent := m.generation == gen, m.intent
		m.mu.Unlock()
		if !current {
			return
		}
		again := m.reconnects(reason)
		_ = m.stopSegment(ctx, reason, again)
		if again {
			m.reconnect(gen, intent)
		}
	}()
}

// clockWatch detects sleep and clock changes. Two independent signals:
//   - the wall clock and the monotonic clock diverge (macOS/Linux: the monotonic clock
//     pauses while the machine sleeps; any platform: the wall clock was set);
//   - the watchdog itself was not scheduled for far longer than its tick (stallLimit).
//     This one does not depend on any clock's sleep semantics. It exists because Go's
//     monotonic clock on Windows is believed to keep counting through sleep and
//     hibernation (it is based on the system interrupt time), so the first signal alone
//     may never fire there and a resumed machine would keep a tunnel
//     whose network and authorization were never re-checked (handoff plan 7.4, T08).
//
// Either direction (a long sleep, a clock set back) invalidates the lease reasoning.
type clockWatch struct {
	wall time.Time
	mono time.Duration
	set  bool
}

const (
	clockJumpLimit = 5 * time.Second
	// stallLimit is far above the 1 s watchdog tick plus scheduler/GC noise and far
	// below any real sleep a user would notice.
	stallLimit = 10 * time.Second
)

func (c *clockWatch) jumped(wall time.Time, mono time.Duration) bool {
	defer func() { c.wall, c.mono, c.set = wall, mono, true }()
	if !c.set {
		return false
	}
	if wall.Sub(c.wall) > stallLimit {
		return true
	}
	diff := wall.Sub(c.wall) - (mono - c.mono)
	return diff > clockJumpLimit || diff < -clockJumpLimit
}

type reportResult struct {
	err  error
	done bool
}

func (m *Manager) reportEvery() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != nil && m.session.ReportInterval > 0 {
		return jitter(time.Duration(m.session.ReportInterval) * time.Second)
	}
	return jitter(60 * time.Second)
}

// monitor is the connected-state watchdog: one goroutine per segment that samples the
// counters, enforces the authorization gate, and paces the single metering writer.
func (m *Manager) monitor(ctx context.Context, gen uint64, conn *connection, mt *meter.Meter, signature string, watch clockWatch) {
	defer func() {
		conn.wg.Wait()
		close(conn.done)
	}()
	tick := time.NewTicker(m.opts.MonitorInterval)
	defer tick.Stop()
	nextReport := m.opts.Now().Add(m.reportEvery())
	retry := m.opts.BackoffMin
	results := make(chan reportResult, 1)
	reporting := false
	ticks := 0
	// settled folds one metering result in; false when the segment is over.
	settled := func(result reportResult) bool {
		reporting = false
		switch code := meter.ErrorCode(result.err); {
		case result.done:
			return false // the session ended inside the report; everything is already closed
		case result.err == nil:
			retry = m.opts.BackoffMin
			nextReport = m.opts.Now().Add(m.reportEvery())
		case code == meter.CodeConflict || code == meter.CodeInvalidCounters || code == meter.CodeSessionMismatch || code == meter.CodeRateChanged:
			m.requestStop(gen, code)
			return false
		default: // transient: retry the identical batch soon, with growing patience
			nextReport = m.opts.Now().Add(retry)
			retry = min(retry*2, 20*time.Second)
		}
		return true
	}
	// lapse handles a reason the segment can no longer assume authorization: the desktop
	// policy stops; the gate policy re-confirms (after the report in flight, if any, so
	// the ledger has one writer) and continues on the confirmed network. False when over.
	lapse := func(reason string) bool {
		if !m.gating() {
			m.requestStop(gen, reason)
			return false
		}
		if reporting {
			select {
			case <-ctx.Done():
				return false
			case result := <-results:
				if !settled(result) {
					return false
				}
			}
		}
		confirmed, ok := m.reconfirm(ctx, gen, reason, mt)
		if !ok {
			return false
		}
		signature = confirmed
		watch = clockWatch{}
		watch.jumped(m.opts.Now().Round(0), m.opts.Mono())
		retry = m.opts.BackoffMin
		nextReport = m.opts.Now().Add(m.reportEvery())
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-results:
			if !settled(result) {
				return
			}
			continue
		case <-tick.C:
		}
		ticks++
		if watch.jumped(m.opts.Now().Round(0), m.opts.Mono()) {
			if !lapse("lifecycle_reconfirmation_required") {
				return
			}
			continue
		}
		if ticks%3 == 0 && m.opts.Signature() != signature {
			if !lapse("network_changed") {
				return
			}
			continue
		}
		if err := mt.Sample(); err != nil {
			m.requestStop(gen, meter.ErrorCode(err))
			return
		}
		m.mu.Lock()
		if m.generation != gen {
			m.mu.Unlock()
			return
		}
		code := m.gateLocked()
		m.mu.Unlock()
		if code == "" && mt.EstimatedRemaining() <= 0 {
			code = "quota_exhausted"
		}
		if code == "lease_expired" && m.gating() {
			if !lapse(code) {
				return
			}
			continue
		}
		if code != "" {
			m.requestStop(gen, code)
			return
		}
		if !reporting && !m.opts.Now().Before(nextReport) {
			reporting = true
			conn.wg.Add(1)
			go func() {
				defer conn.wg.Done()
				results <- m.report(ctx, gen, mt)
			}()
		}
	}
}

// report performs one metering exchange and folds the acknowledgement in.
func (m *Manager) report(ctx context.Context, gen uint64, mt *meter.Meter) reportResult {
	ack, started, err := mt.Flush(ctx)
	if ctx.Err() != nil {
		return reportResult{err: err}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return reportResult{done: true}
	}
	if ack.SessionID != "" {
		if apiclient.TerminatesSession(ack.Reason) {
			m.dropSessionLocked(ack.Reason)
			return reportResult{done: true}
		}
		m.applySessionLocked(ack, started)
	}
	if err == nil {
		m.lastError = nil
		return reportResult{}
	}
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
		m.dropSessionLocked(apiErr.Code)
		return reportResult{done: true}
	}
	if meter.ErrorCode(err) != meter.CodeRateChanged {
		m.failLocked(err)
	}
	return reportResult{err: err}
}
