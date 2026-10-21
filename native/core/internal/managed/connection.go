package managed

import (
	"context"
	"errors"
	"time"

	"asterlink/core/internal/apiclient"
)

// Online leases (contract native-v3, handoff_node_use_history_and_load_balancing_c.md
// 10.1). A device is online while it holds a lease: taken when it actually connects (POST
// /connections, or a grant in node-reported mode), renewed by the reports it already sends,
// and released when the data path is closed and its tail acknowledged. Selecting a line,
// signing in or reading the catalog never takes a slot.

// admission is the lease of the live (or starting) client-reported segment. serverID is
// the opaque server it was admitted to, used only as avoid_node_id.
type admission struct {
	id, groupID, serverID string
}

// pendingRelease is a lease whose release could not be delivered yet (or must wait for the
// ledger to settle first). keepAlive retries it; the server lets it lapse regardless.
type pendingRelease struct {
	id, reason string
}

// releaseTimeout bounds the release on the stop path (plan 10.1: 5 seconds).
const releaseTimeout = 5 * time.Second

// reconnectBudget bounds one automatic reconnect (reassignment, or an expired lease under
// ResumeGate): a full Connect, including its own probe retry.
const reconnectBudget = 2 * time.Minute

// releaseReason maps why a segment ended to the server's release allow-list. A failure of
// the probe through the tunnel is a health report against the server; everything else is a
// plain release.
func releaseReason(stop string) string {
	switch stop {
	case "":
		return apiclient.ReleaseUser
	case "network_changed":
		return apiclient.ReleaseNetworkChanged
	case "lifecycle_reconfirmation_required":
		return apiclient.ReleaseLifecycle
	case "tunnel_unhealthy":
		return apiclient.ReleaseTunnelUnhealthy
	case "tunnel_failed":
		return apiclient.ReleaseTunnelFailed
	}
	return apiclient.ReleaseStopped
}

// noOtherServer: the retry on another server of the group found none; the user is better
// served by the original failure than by "no server available".
func noOtherServer(code string) bool {
	return code == "group_unavailable" || code == "group_full" || code == "agent_unavailable"
}

// reconnects reports whether a segment stopped for reason is connected again by itself,
// once: the server moved this device off a withdrawn server, or (ResumeGate) its lease
// lapsed while the device slept and must be taken again.
func (m *Manager) reconnects(reason string) bool {
	return reason == "connection_reassigned" || reason == "connection_expired" && m.gating()
}

// bumpIntentLocked records a command of the user's own. An automatic reconnect proceeds
// only if none arrived since the segment it replaces was stopped.
func (m *Manager) bumpIntentLocked() { m.intent++ }

func (m *Manager) bumpIntent() {
	m.mu.Lock()
	m.bumpIntentLocked()
	m.mu.Unlock()
}

// admit asks for a lease, retrying transient failures with the same idempotency key so a
// lost reply can never leave two leases behind. requested is when the answering attempt
// was sent (the authorization lease starts there).
func (m *Manager) admit(ctx context.Context, client *apiclient.Client, token string, in apiclient.ConnectionRequest) (apiclient.ConnectionResponse, time.Time, error) {
	key := newRequestKey()
	began := time.Now()
	backoff := m.opts.BackoffMin
	for {
		requested := m.opts.Now()
		response, err := client.Connect(ctx, token, key, in)
		if err == nil {
			return response, requested, nil
		}
		// group_unavailable is a 503, but it is the server's answer about the group, not an
		// outage: asking again cannot change it within this attempt.
		if ae, terminal := isTerminal(err); terminal || ae != nil && noOtherServer(ae.Code) || ctx.Err() != nil || time.Since(began) >= m.opts.GrantTimeout {
			return response, requested, err
		}
		if !sleepCtx(ctx, backoff) {
			return response, requested, err
		}
		backoff = min(backoff*2, m.opts.BackoffMax)
	}
}

// endAdmission releases the current lease (if any) after the data path closed. The ledger
// must be acknowledged first (plan 10.1): with anything still unconfirmed the release waits
// for keepAlive, which settles the tail and then releases.
func (m *Manager) endAdmission(ctx context.Context, gen uint64, reason string) {
	m.mu.Lock()
	ref := m.admission
	m.admission = nil
	settled := m.meter == nil || m.meter.Settled()
	if ref != nil && !settled && m.generation == gen {
		m.unreleased = &pendingRelease{id: ref.id, reason: reason}
	}
	m.mu.Unlock()
	if ref != nil && settled {
		m.release(ctx, gen, pendingRelease{id: ref.id, reason: reason})
	}
}

// release delivers one release (bounded). A transient failure is kept for keepAlive; a
// definite answer (done, or the server no longer knows the lease) ends the matter.
func (m *Manager) release(ctx context.Context, gen uint64, p pendingRelease) {
	m.mu.Lock()
	client, token, current, session := m.client, m.token, m.generation == gen, m.session
	m.mu.Unlock()
	if !current || client == nil || token == "" {
		return
	}
	if session != nil && session.ConnectionID == p.id && session.ConnectionState != "" && session.ConnectionState != apiclient.ConnectionActive {
		// The server already ended (or let lapse) this lease: nothing to release.
		m.clearRelease(gen, p.id)
		return
	}
	rctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	started := m.opts.Now()
	response, err := client.ReleaseConnection(rctx, token, p.id, p.reason)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return
	}
	if err == nil {
		if apiclient.TerminatesSession(response.Session.Reason) {
			m.dropSessionLocked(response.Session.Reason)
			return
		}
		m.applySessionLocked(response.Session, started)
		if m.unreleased != nil && m.unreleased.id == p.id {
			m.unreleased = nil
		}
		return
	}
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
		m.dropSessionLocked(apiErr.Code)
		return
	}
	if _, terminal := isTerminal(err); terminal {
		// connection_not_found, grant_required, invalid_release: retrying cannot help.
		if m.unreleased != nil && m.unreleased.id == p.id {
			m.unreleased = nil
		}
		return
	}
	pending := p
	m.unreleased = &pending
}

func (m *Manager) clearRelease(gen uint64, id string) {
	m.mu.Lock()
	if m.generation == gen && m.unreleased != nil && m.unreleased.id == id {
		m.unreleased = nil
	}
	m.mu.Unlock()
}

// retryRelease re-sends an undelivered release from keepAlive, once nothing counted is
// still unacknowledged.
func (m *Manager) retryRelease(ctx context.Context, gen uint64) {
	m.mu.Lock()
	p := m.unreleased
	settled := m.meter == nil || m.meter.Settled()
	busy := m.conn != nil
	m.mu.Unlock()
	if p == nil || !settled || busy {
		return
	}
	m.release(ctx, gen, *p)
}

// releaseStale ends a lease whose admission reply arrived for a superseded generation: it
// must not revive anything here, and it should not hold a slot for 90 seconds either.
func (m *Manager) releaseStale(client *apiclient.Client, token, id string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		defer cancel()
		_, _ = client.ReleaseConnection(ctx, token, id, apiclient.ReleaseStopped)
	}()
}

// reconnect is the one automatic reconnect after a server-side move. It gives way to any
// command of the user's (intent) and to a session change (generation).
func (m *Manager) reconnect(gen, intent uint64) {
	if !m.opMu.TryLock() {
		return // the user is connecting, switching or handing off right now
	}
	defer m.opMu.Unlock()
	m.mu.Lock()
	ok := m.generation == gen && m.intent == intent && m.conn == nil && m.selected != ""
	m.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reconnectBudget)
	defer cancel()
	if _, err := m.connect(ctx); err != nil {
		// The user did not ask for this attempt: its failure is why the VPN is off now, so a
		// later renewal must not erase it (like any stop reason).
		m.mu.Lock()
		if m.generation == gen && m.lastError != nil {
			m.lastError.stop = true
		}
		m.mu.Unlock()
	}
}

// connect is Connect without the operation lock: the selected line in its metering mode,
// and (only after the tunnel failed its probe) one retry on another server of the group.
func (m *Manager) connect(ctx context.Context) (Snapshot, error) {
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
	nodeMode := m.nodeReportedLocked(selected)
	m.mu.Unlock()
	if selected == "" {
		return m.Snapshot(), codeError("profile_required")
	}
	attempt := func(avoid string) (Snapshot, string, error) {
		if nodeMode {
			return m.connectGranted(ctx, gen, client, token, selected, avoid)
		}
		return m.connectClient(ctx, gen, client, token, selected, avoid)
	}
	snap, avoid, err := attempt("")
	if err == nil || avoid == "" || ctx.Err() != nil {
		return snap, err
	}
	first := codeOf(err)
	retried, _, retryErr := attempt(avoid)
	if retryErr != nil && noOtherServer(codeOf(retryErr)) {
		// The group has no other server to try: report what actually went wrong.
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.generation == gen {
			m.lastError = &errorInfo{Code: first, Message: apiclient.Message(first)}
		}
		return m.snapshotLocked(), err
	}
	return retried, retryErr
}

func codeOf(err error) string {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return "network_unreachable"
}

// connectClient is Connect for a client-reported group. Order: old tail acknowledged, fresh
// session, physical network check, admission (lease + the one server's document), verified
// document, first metering acknowledgement, tunnel, real forwarding probe. Every failure
// after the admission releases the lease once the data path is closed and the tail settled.
// The string result is the server to avoid on a retry: set only when the probe failed.
func (m *Manager) connectClient(ctx context.Context, gen uint64, client *apiclient.Client, token, groupID, avoid string) (Snapshot, string, error) {
	if err := m.settleIdle(ctx, gen); err != nil {
		return m.Snapshot(), "", err
	}
	// Fresh authorization first: connecting never relies on a cached verdict.
	if err := m.refreshSession(ctx, gen, client, token); err != nil {
		return m.Snapshot(), "", err
	}
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), "", codeError("stale_generation")
	}
	var node apiclient.NodeChoice
	found := false
	for _, n := range m.nodes {
		if n.ID == groupID {
			node, found = n, true
		}
	}
	if !found || node.MeteringSource == apiclient.MeteringNode {
		m.selected, m.profile = "", nil
		m.failLocked(codeError("node_unavailable"))
		m.mu.Unlock()
		return m.Snapshot(), "", codeError("node_unavailable")
	}
	if code := m.gateLocked(); code != "" {
		m.failLocked(codeError(code))
		m.mu.Unlock()
		return m.Snapshot(), "", codeError(code)
	}
	lastSequence := m.session.LastSequence
	m.profile = nil
	m.state = StatePreparing
	m.mu.Unlock()

	// fail ends an attempt: the engine is closed, the probe's tail (if any) acknowledged, and
	// only then the lease released (plan 10.1).
	fail := func(code string, engineUp, settleTail bool, release string) (Snapshot, error) {
		if engineUp {
			_ = m.opts.Engine.Stop()
			m.opts.Egress.Clear()
		}
		m.mu.Lock()
		mt := m.meter
		m.mu.Unlock()
		if settleTail && mt != nil {
			_ = m.settle(ctx, gen, mt)
		}
		m.endAdmission(ctx, gen, release)
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
		snap, failure := fail(engineCode(err), false, false, apiclient.ReleaseStopped)
		return snap, "", failure
	}
	signature := m.opts.Signature()

	response, requested, err := m.admit(ctx, client, token, apiclient.ConnectionRequest{GroupID: groupID, LastSequence: lastSequence, AvoidNodeID: avoid})
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.mu.Lock()
			if m.generation == gen {
				m.dropSessionLocked(apiErr.Code)
			}
			m.mu.Unlock()
			return m.Snapshot(), "", err
		}
		snap, failure := fail(codeOf(err), false, false, apiclient.ReleaseStopped)
		return snap, "", failure
	}
	ref := &admission{id: response.Connection.ID, groupID: groupID, serverID: response.NodeID}
	profile, parseErr := ParseProfile(groupID, node.Name, response.Version, response.YAML)
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		m.releaseStale(client, token, ref.id) // a late reply never revives an old lease here
		return m.Snapshot(), "", codeError("stale_generation")
	}
	m.admission = ref  // from here every teardown path releases it
	m.unreleased = nil // this device's older lease was replaced by the admission
	if parseErr != nil || response.Session.ConnectionID != ref.id {
		m.mu.Unlock()
		snap, failure := fail("config_rejected", false, false, apiclient.ReleaseStopped)
		return snap, "", failure
	}
	if apiclient.TerminatesSession(response.Session.Reason) {
		m.dropSessionLocked(response.Session.Reason)
		m.mu.Unlock()
		return m.Snapshot(), "", codeError(response.Session.Reason)
	}
	m.applySessionLocked(response.Session, requested)
	if response.Session.Reason != "" {
		code := response.Session.Reason
		m.mu.Unlock()
		snap, failure := fail(code, false, false, apiclient.ReleaseStopped)
		return snap, "", failure
	}
	m.profile, m.profileNode = &profile, node
	if m.meter == nil {
		m.meter = m.newMeterLocked(client, token)
	}
	mt, session := m.meter, *m.session
	m.mu.Unlock()

	if err = mt.Begin(session); err != nil {
		m.endAdmission(ctx, gen, apiclient.ReleaseStopped)
		snap, failure := m.failMeter(gen, mt, err)
		return snap, "", failure
	}
	// First acknowledgement before any tunnel exists: it proves the ledger and the server
	// agree and renews the lease. A zero-byte batch is legitimate.
	ack, started, err := mt.Flush(ctx)
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), "", codeError("stale_generation")
	}
	if ack.SessionID != "" {
		if apiclient.TerminatesSession(ack.Reason) {
			m.dropSessionLocked(ack.Reason)
			m.mu.Unlock()
			return m.Snapshot(), "", codeError(ack.Reason)
		}
		m.applySessionLocked(ack, started)
	}
	m.mu.Unlock()
	if err != nil {
		m.endAdmission(ctx, gen, apiclient.ReleaseStopped)
		snap, failure := m.failMeter(gen, mt, err)
		return snap, "", failure
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
		snap, failure := fail(code, false, false, apiclient.ReleaseStopped)
		return snap, "", failure
	}

	// From here the data path may exist, so every failure must close it.
	m.opts.Egress.Set(iface)
	if err = m.opts.Engine.Start(ctx, profile); err != nil {
		// Starting is local (TUN, routes): not evidence against the server.
		snap, failure := fail(engineCode(err), true, false, apiclient.ReleaseStopped)
		return snap, "", failure
	}
	if err = m.opts.Engine.Verify(ctx); err != nil {
		// The probe itself used the tunnel and was counted; that tail is acknowledged before
		// the lease is released with the failure, which reports the server.
		probe := engineCode(err)
		snap, failure := fail(probe, true, true, releaseReason(probe))
		return snap, ref.serverID, failure
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		_ = m.opts.Engine.Stop()
		m.opts.Egress.Clear()
		return m.Snapshot(), "", codeError("stale_generation")
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	conn := &connection{cancel: cancel, done: make(chan struct{}), metered: true}
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
	return snap, "", nil
}
