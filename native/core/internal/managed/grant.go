package managed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/meter"
)

// Node-reported mode (handoff_app.md 6.3, 7.4). On a node governed by an agent, the
// Core never receives the shared node secret and never reports traffic. It asks for a
// grant (a per-user credential the node itself installs), connects with that, and
// tears the grant down at the node when the data path closes. The node is the ledger:
// the meter here only feeds the speed chart and an estimate.

type grantRef struct {
	id     string
	nodeID string
}

func (m *Manager) nodeReportedLocked(nodeID string) bool {
	for _, n := range m.nodes {
		if n.ID == nodeID {
			return n.MeteringSource == apiclient.MeteringNode
		}
	}
	return false
}

func (m *Manager) selectedNodeReportedLocked() bool {
	return m.selected != "" && m.nodeReportedLocked(m.selected)
}

// graceReason: server reasons that describe the previous grant or the missing binding
// of a node-reported node, and are resolved by minting a new grant, not by waiting.
func graceReason(reason string) bool {
	return reason == "profile_required" || reason == "profile_changed" || reason == "grant_expired"
}

// grantGateLocked is the verdict for pressing Connect on a node-reported node while no
// segment is live. There is no lease to check yet: the grant request itself is the
// server-side authorization, and the lease starts with the grant's session snapshot.
func (m *Manager) grantGateLocked() string {
	switch {
	case m.catalogReason != "":
		return m.catalogReason
	case m.session.Reason != "" && !graceReason(m.session.Reason):
		return m.session.Reason
	case m.session.RemainingBytes <= 0:
		return "quota_exhausted"
	case m.selected == "":
		return "profile_required"
	}
	return ""
}

func (m *Manager) selectGrantNode(gen uint64, nodeID string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return m.snapshotLocked(), codeError("stale_generation")
	}
	for i := range m.nodes {
		if m.nodes[i].ID == nodeID {
			m.selected, m.profile, m.profileNode = nodeID, nil, m.nodes[i]
			m.lastError = nil
			return m.snapshotLocked(), nil
		}
	}
	return m.snapshotLocked(), codeError("node_unavailable")
}

func newRequestKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "connect-" + hex.EncodeToString(b)
}

func isTerminal(err error) (*apiclient.Error, bool) {
	var ae *apiclient.Error
	if errors.As(err, &ae) {
		return ae, !ae.Transient
	}
	return nil, false
}

// mintGrant asks for a grant, retrying transient failures with the same idempotency
// key so a lost reply can never leave two grants behind.
func (m *Manager) mintGrant(ctx context.Context, client *apiclient.Client, token, nodeID, key string) (apiclient.GrantStatus, error) {
	began := time.Now()
	backoff := m.opts.BackoffMin
	for {
		status, err := client.CreateGrant(ctx, token, nodeID, key)
		if err == nil {
			return status, nil
		}
		ae, terminal := isTerminal(err)
		if terminal || ae != nil && ae.Code == "agent_unavailable" || ctx.Err() != nil || time.Since(began) >= m.opts.GrantTimeout {
			return status, err
		}
		if !sleepCtx(ctx, backoff) {
			return status, err
		}
		backoff = min(backoff*2, m.opts.BackoffMax)
	}
}

// awaitGrant polls until the node confirms the grant (state ready, credential
// present). The credential never exists before that confirmation.
func (m *Manager) awaitGrant(ctx context.Context, client *apiclient.Client, token, id string) (apiclient.GrantStatus, time.Time, error) {
	began := time.Now()
	for {
		requested := m.opts.Now()
		status, err := client.Grant(ctx, token, id)
		if err == nil {
			switch status.State {
			case apiclient.GrantReady:
				if status.YAML == "" || status.Session == nil {
					return status, requested, codeError("bad_response")
				}
				return status, requested, nil
			case apiclient.GrantPending:
			default:
				return status, requested, codeError("grant_expired")
			}
		} else if _, terminal := isTerminal(err); terminal {
			return status, requested, err
		}
		if ctx.Err() != nil {
			return status, requested, codeError("canceled")
		}
		if time.Since(began) >= m.opts.GrantTimeout {
			return status, requested, codeError("grant_timeout")
		}
		if !sleepCtx(ctx, m.opts.GrantPoll) {
			return status, requested, codeError("canceled")
		}
	}
}

// connectGranted is Connect for a node-reported node. Order: fresh authorization,
// physical network check, grant (node confirms) -> verified document -> tunnel ->
// real forwarding probe. Any failure revokes the grant and leaves no data path.
func (m *Manager) connectGranted(ctx context.Context, gen uint64, client *apiclient.Client, token, nodeID string) (Snapshot, error) {
	if err := m.settleIdle(ctx, gen); err != nil {
		return m.Snapshot(), err
	}
	if err := m.refreshSession(ctx, gen, client, token); err != nil {
		return m.Snapshot(), err
	}
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_generation")
	}
	var node apiclient.NodeChoice
	found := false
	for _, n := range m.nodes {
		if n.ID == nodeID {
			node, found = n, true
		}
	}
	if !found || node.MeteringSource != apiclient.MeteringNode {
		m.selected, m.profile = "", nil
		m.failLocked(codeError("node_unavailable"))
		m.mu.Unlock()
		return m.Snapshot(), codeError("node_unavailable")
	}
	if code := m.gateLocked(); code != "" {
		m.failLocked(codeError(code))
		m.mu.Unlock()
		return m.Snapshot(), codeError(code)
	}
	m.state = StatePreparing
	m.mu.Unlock()

	var ref *grantRef
	fail := func(code string, engineUp bool) (Snapshot, error) {
		if engineUp {
			_ = m.opts.Engine.Stop()
			m.opts.Egress.Clear()
		}
		if ref != nil {
			m.finishGrant(ctx, gen, ref)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.generation == gen {
			m.local = nil
			m.classifyLocked()
			m.failLocked(codeError(code))
		}
		return m.snapshotLocked(), codeError(code)
	}
	failErr := func(err error, engineUp bool) (Snapshot, error) {
		if ae, ok := isAPIError(err); ok && apiclient.TerminatesSession(ae.Code) {
			if ref != nil {
				m.finishGrant(ctx, gen, ref)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.generation == gen {
				m.dropSessionLocked(ae.Code)
			}
			return m.snapshotLocked(), err
		}
		code := "network_unreachable"
		if ae, ok := isAPIError(err); ok {
			code = ae.Code
		}
		return fail(code, engineUp)
	}

	iface, err := m.opts.Engine.Preflight(ctx)
	if err != nil {
		return fail(engineCode(err), false)
	}
	signature := m.opts.Signature()

	status, err := m.mintGrant(ctx, client, token, nodeID, newRequestKey())
	if err != nil {
		return failErr(err, false)
	}
	ref = &grantRef{id: status.GrantID, nodeID: nodeID}
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		m.finishGrant(ctx, gen, ref)
		return m.Snapshot(), codeError("stale_generation")
	}
	m.grant = ref // from here every teardown path revokes it
	m.mu.Unlock()

	ready, requested, err := m.awaitGrant(ctx, client, token, status.GrantID)
	if err != nil {
		return failErr(err, false)
	}
	profile, parseErr := ParseProfile(nodeID, node.Name, ready.Version, ready.YAML)
	if parseErr != nil || ready.NodeID != nodeID || ready.Session.GrantID != ready.GrantID {
		return fail("config_rejected", false)
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		m.finishGrant(ctx, gen, ref)
		return m.Snapshot(), codeError("stale_generation")
	}
	m.applySessionLocked(*ready.Session, requested)
	if ready.Session.Reason != "" || !ready.Session.CanConnect || ready.Session.MeteringSource != apiclient.MeteringNode {
		code := ready.Session.Reason
		if code == "" {
			code = "metering_source_mismatch"
		}
		m.mu.Unlock()
		return fail(code, false)
	}
	m.profile, m.profileNode = &profile, node
	local := meter.New(meter.Config{Base: *m.session, Totals: m.opts.Engine.Totals, Now: m.opts.Now})
	if local.Begin(*m.session) == nil {
		m.local = local
	}
	m.state = StateConnecting
	m.mu.Unlock()

	m.opts.Egress.Set(iface)
	if err = m.opts.Engine.Start(ctx, profile); err != nil {
		return fail(engineCode(err), true)
	}
	if err = m.opts.Engine.Verify(ctx); err != nil {
		return fail(engineCode(err), true)
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		_ = m.opts.Engine.Stop()
		m.opts.Egress.Clear()
		m.finishGrant(ctx, gen, ref)
		return m.Snapshot(), codeError("stale_generation")
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	conn := &connection{cancel: cancel, done: make(chan struct{})}
	m.conn = conn
	m.state = StateConnected
	m.lastError = nil
	snap := m.snapshotLocked()
	m.mu.Unlock()
	var watch clockWatch
	watch.jumped(m.opts.Now().Round(0), m.opts.Mono())
	go m.monitorGranted(monitorCtx, gen, conn, signature, watch)
	return snap, nil
}

func isAPIError(err error) (*apiclient.Error, bool) {
	var ae *apiclient.Error
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

// monitorGranted is the connected-state watchdog of a node-reported segment. It does
// not write to the ledger; it only watches the gate, the clock and the network.
func (m *Manager) monitorGranted(ctx context.Context, gen uint64, conn *connection, signature string, watch clockWatch) {
	defer close(conn.done)
	tick := time.NewTicker(m.opts.MonitorInterval)
	defer tick.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
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
		m.mu.Lock()
		if m.generation != gen {
			m.mu.Unlock()
			return
		}
		if m.local != nil {
			_ = m.local.Sample() // display only: a counter oddity must not cut a paid session
		}
		code := m.gateLocked()
		exhausted := m.local != nil && m.local.EstimatedRemaining() <= 0
		m.mu.Unlock()
		if code == "" && exhausted {
			code = "quota_exhausted"
		}
		if code != "" {
			m.requestStop(gen, code)
			return
		}
	}
}

// finishGrant revokes a grant after the data path is closed, then waits (bounded) for
// the node's confirmation. A revocation that cannot be delivered is retried from the
// keep-alive loop; the node's own deadline ends the grant regardless.
func (m *Manager) finishGrant(ctx context.Context, gen uint64, g *grantRef) {
	m.mu.Lock()
	client, token, current := m.client, m.token, m.generation == gen
	m.mu.Unlock()
	if client == nil || token == "" || !current {
		m.mu.Lock()
		if m.grant == g {
			m.grant = nil
		}
		m.mu.Unlock()
		return
	}
	rctx, cancel := context.WithTimeout(ctx, m.opts.SettleTimeout)
	defer cancel()
	var rerr error
	backoff := m.opts.BackoffMin
	for {
		_, rerr = client.RevokeGrant(rctx, token, g.id)
		if rerr == nil {
			break
		}
		if _, terminal := isTerminal(rerr); terminal || !sleepCtx(rctx, backoff) {
			break
		}
		backoff = min(backoff*2, m.opts.BackoffMax)
	}
	if rerr == nil {
		for {
			status, err := client.Grant(rctx, token, g.id)
			if err == nil && (status.State == apiclient.GrantRevoked || status.State == apiclient.GrantExpired) {
				break
			}
			if _, terminal := isTerminal(err); terminal && err != nil || !sleepCtx(rctx, m.opts.GrantPoll) {
				break
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return
	}
	if m.grant == g {
		m.grant = nil
	}
	if _, terminal := isTerminal(rerr); rerr != nil && !terminal {
		m.orphan = g.id // not delivered: keep trying while the session lives
	}
}

// retryOrphan re-sends an undelivered revocation. It runs from the keep-alive loop.
func (m *Manager) retryOrphan(ctx context.Context, gen uint64, client *apiclient.Client, token string) {
	m.mu.Lock()
	id := m.orphan
	m.mu.Unlock()
	if id == "" {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := client.RevokeGrant(rctx, token, id)
	if _, terminal := isTerminal(err); err != nil && !terminal {
		return
	}
	m.mu.Lock()
	if m.generation == gen && m.orphan == id {
		m.orphan = ""
	}
	m.mu.Unlock()
}
