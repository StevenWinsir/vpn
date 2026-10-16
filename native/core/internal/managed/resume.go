package managed

import (
	"context"
	"errors"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/meter"
)

// ResumePolicy decides what a running segment does once its authorization can no longer
// be assumed: the machine slept or its clock moved, the physical network changed under
// it, or the lease lapsed while the control plane was unreachable.
type ResumePolicy int

const (
	// ResumeStop ends the segment, settles it and leaves reconnecting to the user. It is
	// the desktop behaviour (macOS, Windows): sleeping a computer is rare and deliberate.
	ResumeStop ResumePolicy = iota
	// ResumeGate closes an authorization gate instead (handoff_app_android.md 7.5): the
	// engine stops forwarding while the device and its routes stay, so traffic keeps
	// entering the tunnel and goes nowhere (no leak, no free use); the Core then
	// re-confirms online and opens the gate again, or stops exactly as ResumeStop would.
	// A phone's CPU sleeps whenever its screen is off, so stopping there would drop the
	// VPN minutes after every screen-off. The engine must implement Gate.
	ResumeGate
)

// DefaultGateLimit bounds how long a closed gate keeps retrying while the device is awake
// (the monotonic clock; time asleep does not count) before the segment is ended to save
// power. Authorization is never extended by it: nothing is forwarded meanwhile.
const DefaultGateLimit = 5 * time.Minute

// Gate is the engine capability ResumeGate needs.
type Gate interface {
	// Suspend stops forwarding without removing the device: new flows are refused and
	// every open flow is closed.
	Suspend() error
	// Resume forwards again. It fails when the engine is not running.
	Resume() error
}

// gateState describes a closed gate (nil while open).
type gateState struct {
	reason   string
	since    time.Time // wall clock from the moment the lapse was noticed (later sleeps count)
	waiting  bool      // the last attempt could not reach the network or the server
	attempts int
}

// GateView is the closed gate as the UI sees it: the tunnel is held but forwards nothing.
type GateView struct {
	// Reason is lifecycle_reconfirmation_required (woke from sleep, clock moved),
	// network_changed or lease_expired.
	Reason string `json:"reason"`
	// WaitingNetwork: the last re-confirmation could not reach the network or the server.
	WaitingNetwork bool  `json:"waiting_network"`
	ClosedSeconds  int64 `json:"closed_seconds"`
	// Attempts counts the re-confirmations that failed so far (0 while the first runs).
	Attempts int64 `json:"attempts"`
}

// GateRecord is the most recent gate that closed in this session and how it ended
// (outcome "reopened" or "stopped"), kept as evidence for the lifecycle acceptance.
type GateRecord struct {
	Reason       string `json:"reason"`
	Outcome      string `json:"outcome"`
	ClosedMillis int64  `json:"closed_ms"`
	// Attempts: failed re-confirmations before the outcome (0 = the first one decided).
	Attempts      int64 `json:"attempts"`
	TotalClosures int64 `json:"total_closures"`
}

// gateable reports whether reason is one ResumeGate re-confirms instead of stopping.
// Everything else (plan, quota, a changed or disabled line, a ledger problem) is a
// verdict, not a lapse, and stops the segment under either policy.
func gateable(reason string) bool {
	switch reason {
	case "lifecycle_reconfirmation_required", "network_changed", "lease_expired":
		return true
	}
	return false
}

// gating is true when lapses close a gate instead of ending the segment.
func (m *Manager) gating() bool {
	if m.opts.ResumePolicy != ResumeGate {
		return false
	}
	_, ok := m.opts.Engine.(Gate)
	return ok
}

type gateVerdict int

const (
	gateAuthorized gateVerdict = iota
	gateRefused                // the server said no: stop with that reason
	gateTransient              // no network or no answer: keep the gate closed and retry
	gateCanceled               // the segment or the session ended meanwhile
)

// reconfirm runs inside a watchdog: it closes the gate, re-confirms until the server
// answers, and either reopens (returning the network signature the decision was made
// on) or requests the stop itself. mt is the segment's ledger (nil for node-reported).
// The watchdog must not have a metering exchange in flight.
func (m *Manager) reconfirm(ctx context.Context, gen uint64, reason string, mt *meter.Meter) (string, bool) {
	gate := m.opts.Engine.(Gate)
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return "", false
	}
	m.gate = &gateState{reason: reason, since: m.opts.Now()}
	m.mu.Unlock()
	if err := gate.Suspend(); err != nil {
		// Forwarding cannot be held back: the segment must not continue unconfirmed.
		m.requestStop(gen, reason)
		return "", false
	}
	awakeSince := m.opts.Mono()
	backoff := m.opts.BackoffMin
	for attempt := 1; ; attempt++ {
		signature := m.opts.Signature()
		verdict, code := m.reauthorize(ctx, gen, mt)
		switch verdict {
		case gateCanceled:
			return "", false
		case gateRefused:
			m.requestStop(gen, code)
			return "", false
		case gateAuthorized:
			// Forwarding again is proven the same way connecting is: a real probe through
			// the tunnel (the network underneath may have changed).
			if gate.Resume() == nil {
				if m.opts.Engine.Verify(ctx) == nil {
					m.openGate(gen)
					return signature, true
				}
				_ = gate.Suspend()
			}
			if ctx.Err() != nil {
				return "", false
			}
			code = "tunnel_unhealthy"
		}
		m.mu.Lock()
		if m.generation != gen || m.gate == nil {
			m.mu.Unlock()
			return "", false
		}
		m.gate.waiting, m.gate.attempts = code != "tunnel_unhealthy", attempt
		m.mu.Unlock()
		if m.opts.Mono()-awakeSince >= m.opts.GateLimit {
			m.requestStop(gen, code)
			return "", false
		}
		if !m.sleepOrNudge(ctx, jitter(backoff)) {
			return "", false
		}
		backoff = min(backoff*2, m.opts.BackoffMax)
	}
}

// reauthorize is one online re-confirmation. A metered segment replays or sends a traffic
// batch (its acknowledgement settles what was carried and renews the lease, exactly as a
// periodic report does); a node-reported one asks GET /session.
func (m *Manager) reauthorize(ctx context.Context, gen uint64, mt *meter.Meter) (gateVerdict, string) {
	iface, err := m.opts.Engine.Preflight(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return gateCanceled, ""
		}
		return gateTransient, engineCode(err)
	}
	m.opts.Egress.Set(iface)
	var ack apiclient.Session
	var started time.Time
	if mt != nil {
		ack, started, err = mt.Flush(ctx)
	} else {
		_, client, token, herr := m.handles()
		if herr != nil {
			return gateCanceled, ""
		}
		started = m.opts.Now()
		ack, err = client.Session(ctx, token)
	}
	if ctx.Err() != nil {
		return gateCanceled, ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return gateCanceled, ""
	}
	if ack.SessionID != "" {
		if apiclient.TerminatesSession(ack.Reason) {
			m.dropSessionLocked(ack.Reason)
			return gateCanceled, ""
		}
		m.applySessionLocked(ack, started)
	}
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.dropSessionLocked(apiErr.Code)
			return gateCanceled, ""
		}
		if mt != nil {
			if fatal := mt.Fatal(); fatal != "" {
				m.dropSessionLocked(fatal)
				return gateCanceled, ""
			}
			switch code := meter.ErrorCode(err); code {
			case meter.CodeConflict, meter.CodeInvalidCounters, meter.CodeSessionMismatch, meter.CodeRateChanged:
				return gateRefused, code
			}
		}
		if errors.As(err, &apiErr) && !apiErr.Transient && apiErr.Code != "network_unreachable" {
			// A definite refusal (not a transport failure) is the server's verdict.
			return gateRefused, apiErr.Code
		}
		return gateTransient, "network_unreachable"
	}
	code := m.gateLocked()
	if code == "" && mt != nil && mt.EstimatedRemaining() <= 0 {
		code = "quota_exhausted"
	}
	if code == "" && m.local != nil && m.local.EstimatedRemaining() <= 0 {
		code = "quota_exhausted"
	}
	if code == "lease_expired" {
		// Answered, but without a usable lease (should not happen with a valid verdict).
		return gateTransient, "network_unreachable"
	}
	if code != "" {
		return gateRefused, code
	}
	return gateAuthorized, ""
}

// openGate records the reopened gate.
func (m *Manager) openGate(gen uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation == gen {
		m.closeGateRecordLocked("reopened")
	}
}

// closeGateRecordLocked clears a closed gate, keeping its record. Every teardown calls it.
func (m *Manager) closeGateRecordLocked(outcome string) {
	g := m.gate
	if g == nil {
		return
	}
	m.gate = nil
	m.gateClosures++
	m.lastGate = &GateRecord{
		Reason: g.reason, Outcome: outcome, Attempts: int64(g.attempts), TotalClosures: m.gateClosures,
		ClosedMillis: max(0, m.opts.Now().Round(0).Sub(g.since.Round(0)).Milliseconds()),
	}
}

// sleepOrNudge waits for d, or less when nudged; false when ctx ended.
func (m *Manager) sleepOrNudge(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-m.nudge:
	}
	return true
}

// Nudge asks a closed gate to retry now: the host saw the network change (Android:
// ConnectivityManager) or the user opened the app. Harmless at any other time.
func (m *Manager) Nudge() {
	select {
	case m.nudge <- struct{}{}:
	default:
	}
}

// SetForeground tells the Core whether any of the app's screens is visible
// (handoff_app_android.md 7.6). In the background the catalog long-poll pauses, which keeps
// a phone's radio from waking every few seconds; it resumes at once in the foreground.
// Authorization does not depend on it: the server's verdict still arrives with every
// traffic acknowledgement (metered) or session renewal (node-reported), and those continue.
// Desktop entry points never call it and keep watching.
func (m *Manager) SetForeground(visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if visible == !m.background {
		return
	}
	m.background = !visible
	if visible {
		close(m.foreground)
		m.Nudge()
	} else {
		m.foreground = make(chan struct{})
		m.catalogStale = true
		m.pauses++
	}
}

// awaitForeground blocks while the app is in the background; false when ctx ended.
func (m *Manager) awaitForeground(ctx context.Context) bool {
	m.mu.Lock()
	wait := m.foreground
	background := m.background
	m.mu.Unlock()
	if !background {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-wait:
		return true
	}
}
