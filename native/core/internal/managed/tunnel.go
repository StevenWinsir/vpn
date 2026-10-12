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
	if s.CanConnect || s.Reason == "profile_required" || s.Reason == "profile_changed" {
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
// signed in, a selected node, a fresh server authorization with an active lease, a
// config validated against the same version, no conflicting VPN, and a first metering
// acknowledgement. Only after the tunnel verifiably forwards does it report Connected.
func (m *Manager) Connect(ctx context.Context) (Snapshot, error) {
	if !m.opMu.TryLock() {
		return m.Snapshot(), codeError("operation_in_progress")
	}
	defer m.opMu.Unlock()

	gen, client, token, err := m.handles()
	if err != nil {
		return m.Snapshot(), err
	}
	m.mu.Lock()
	if m.conn != nil {
		defer m.mu.Unlock()
		return m.snapshotLocked(), nil // already connected: idempotent
	}
	selected := m.selected
	m.mu.Unlock()
	if selected == "" {
		return m.Snapshot(), codeError("profile_required")
	}

	if err = m.settleIdle(ctx, gen); err != nil {
		return m.Snapshot(), err
	}
	// Fresh authorization first: connecting never relies on a cached verdict.
	if err = m.refreshSession(ctx, gen, client, token); err != nil {
		return m.Snapshot(), err
	}
	// If the server says our binding is stale (or we hold nothing), bind once.
	m.mu.Lock()
	need := m.profile == nil
	m.mu.Unlock()
	if need {
		if err = m.bindNode(ctx, gen, client, token, selected); err != nil {
			return m.Snapshot(), err
		}
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_generation")
	}
	if code := m.gateLocked(); code != "" {
		m.failLocked(codeError(code))
		m.mu.Unlock()
		return m.Snapshot(), codeError(code)
	}
	profile, session := *m.profile, *m.session
	if m.meter == nil {
		m.meter = m.newMeterLocked(client, token)
	}
	mt := m.meter
	m.state = StatePreparing
	m.mu.Unlock()

	fail := func(code string, engineUp bool) (Snapshot, error) {
		if engineUp {
			_ = m.opts.Engine.Stop()
			m.opts.Egress.Clear()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.generation == gen {
			m.classifyLocked()
			m.failLocked(codeError(code))
		}
		return m.snapshotLocked(), codeError(code)
	}

	iface, err := m.opts.Engine.Preflight(ctx)
	if err != nil {
		return fail(engineCode(err), false)
	}
	signature := m.opts.Signature()
	if err = mt.Begin(session); err != nil {
		return m.failMeter(gen, mt, err)
	}
	// First acknowledgement before any tunnel exists: it proves the ledger and the
	// server agree and renews the lease. A zero-byte batch is legitimate.
	ack, started, err := mt.Flush(ctx)
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_generation")
	}
	if ack.SessionID != "" {
		if apiclient.TerminatesSession(ack.Reason) {
			m.dropSessionLocked(ack.Reason)
			m.mu.Unlock()
			return m.Snapshot(), codeError(ack.Reason)
		}
		m.applySessionLocked(ack, started)
	}
	m.mu.Unlock()
	if err != nil {
		return m.failMeter(gen, mt, err)
	}
	m.mu.Lock()
	code := m.gateLocked()
	if code == "" && mt.EstimatedRemaining() <= 0 {
		code = "quota_exhausted"
	}
	if code == "" {
		m.state = StateConnecting
	}
	m.mu.Unlock()
	if code != "" {
		return fail(code, false)
	}

	// From here the data path may exist, so every failure must close it.
	m.opts.Egress.Set(iface)
	if err = m.opts.Engine.Start(ctx, profile); err != nil {
		return fail(engineCode(err), true)
	}
	if err = m.opts.Engine.Verify(ctx); err != nil {
		snap, failure := fail(engineCode(err), true)
		// The probe itself used the tunnel and was counted; acknowledge that tail.
		_ = m.settle(ctx, gen, mt)
		return snap, failure
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		_ = m.opts.Engine.Stop()
		m.opts.Egress.Clear()
		return m.Snapshot(), codeError("stale_generation")
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	conn := &connection{cancel: cancel, done: make(chan struct{})}
	m.conn = conn
	m.state = StateConnected
	m.lastError = nil
	snap := m.snapshotLocked()
	m.mu.Unlock()
	// The clock watch is primed here, before the goroutine exists, so a sleep that
	// begins right after Connect is still seen.
	var watch clockWatch
	watch.jumped(m.opts.Now().Round(0), m.opts.Mono())
	go m.monitor(monitorCtx, gen, conn, mt, signature, watch)
	return snap, nil
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

// Disconnect closes the data path and settles the tail. It is idempotent.
func (m *Manager) Disconnect(ctx context.Context) Snapshot {
	_ = m.stopTunnel(ctx, "")
	return m.Snapshot()
}

// stopTunnel is the one way a segment ends, whether the user asked (reason "") or a
// guard fired. Order matters: the data path closes first, then the tail is
// acknowledged, and only then does the state move on. It never starts anything.
func (m *Manager) stopTunnel(ctx context.Context, reason string) error {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()

	m.mu.Lock()
	gen, conn, mt := m.generation, m.conn, m.meter
	active := conn != nil || m.state == StateConnected || m.state == StateConnecting
	m.conn = nil
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

	var settleErr error
	if mt != nil {
		if active || !mt.Settled() {
			settleErr = m.settle(ctx, gen, mt)
		}
		mt.Idle() // after settlement: the final sample must not resurrect a displayed rate
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
	}
	switch {
	case reason != "":
		m.lastError = &errorInfo{Code: reason, Message: apiclient.Message(reason)}
	case settleErr != nil && active:
		code := meter.CodeUnconfirmed
		var apiErr *apiclient.Error
		if errors.As(settleErr, &apiErr) {
			code = apiErr.Code
		}
		m.lastError = &errorInfo{Code: code, Message: apiclient.Message(code)}
	case active:
		m.lastError = nil
	}
	return settleErr
}

// requestStop ends the segment from a watchdog without blocking it (stopTunnel waits
// for the watchdog to exit).
func (m *Manager) requestStop(gen uint64, reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), m.opts.SettleTimeout+5*time.Second)
		defer cancel()
		if m.Generation() != gen {
			return
		}
		_ = m.stopTunnel(ctx, reason)
	}()
}

// clockWatch detects sleep and clock changes. Go's monotonic clock pauses while the
// machine sleeps, the wall clock does not, so their divergence is the signal. Either
// direction (a long sleep, a clock set back) invalidates the lease reasoning.
type clockWatch struct {
	wall time.Time
	mono time.Duration
	set  bool
}

const clockJumpLimit = 5 * time.Second

func (c *clockWatch) jumped(wall time.Time, mono time.Duration) bool {
	defer func() { c.wall, c.mono, c.set = wall, mono, true }()
	if !c.set {
		return false
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
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-results:
			reporting = false
			switch code := meter.ErrorCode(result.err); {
			case result.done:
				return // the session ended inside the report; everything is already closed
			case result.err == nil:
				retry = m.opts.BackoffMin
				nextReport = m.opts.Now().Add(m.reportEvery())
			case code == meter.CodeConflict || code == meter.CodeInvalidCounters || code == meter.CodeSessionMismatch || code == meter.CodeRateChanged:
				m.requestStop(gen, code)
				return
			default: // transient: retry the identical batch soon, with growing patience
				nextReport = m.opts.Now().Add(retry)
				retry = min(retry*2, 20*time.Second)
			}
			continue
		case <-tick.C:
		}
		ticks++
		if watch.jumped(m.opts.Now().Round(0), m.opts.Mono()) {
			m.requestStop(gen, "lifecycle_reconfirmation_required")
			return
		}
		if ticks%3 == 0 && m.opts.Signature() != signature {
			m.requestStop(gen, "network_changed")
			return
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
