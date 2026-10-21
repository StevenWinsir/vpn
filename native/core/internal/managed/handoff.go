package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"asterlink/core/internal/apiclient"
)

// Session ownership handoff (handoff_app_ios.md 4.2, shared/contracts/handoff-v1.json).
//
// On iOS two processes run a Core: the app (signed in, browsing lines, while no VPN runs) and
// the packet tunnel extension (forwarding and metering, while the VPN runs). Only one of them
// may hold the bearer token and write to the server at any moment. ExportHandoff closes the
// data path, has every counted byte acknowledged, stops this process's workers WITHOUT
// revoking the token, and returns a ticket; AdoptHandoff in the other process confirms the
// ticket against GET /session and continues the very same server session. The ledger stays
// continuous because the receiver's meter starts from the server's confirmed state, which is
// what meter.Begin already requires of any new segment.
//
// A ticket carries the token, so it is a secret: it travels only in memory (the system's
// startVPNTunnel options and provider messages), is never written to disk or logged, and
// prints redacted. It is single-use within a process: a generation at or below the highest
// one this process adopted is refused, so a replayed or older ticket cannot create a second
// writer here.

// HandoffVersion is the ticket format of shared/contracts/handoff-v1.json.
const HandoffVersion = 1

// HandoffTTL bounds how long a ticket may be adopted after it was issued. It covers the
// tunnel start (connect budget 90 s) plus the app taking back its own ticket when the
// extension never adopted it; anything older means "sign in again".
const HandoffTTL = 5 * time.Minute

// handoffSkew tolerates a receiver clock slightly behind the sender's (same device, so
// this is only scheduling noise).
const handoffSkew = 30 * time.Second

// handoffOrigin is what the session was opened with, minus the password.
type handoffOrigin struct {
	APIBase  string
	Channel  string
	DeviceID string
}

// HandoffTicket moves one signed-in session from one process to another.
type HandoffTicket struct {
	Version        int       `json:"version"`
	APIBase        string    `json:"api_base"`
	Channel        string    `json:"channel"`
	SessionID      string    `json:"session_id"`
	Token          string    `json:"token"`
	DeviceID       string    `json:"device_id"`
	SelectedNodeID string    `json:"selected_node_id"`
	Generation     uint64    `json:"generation"`
	LastSequence   int64     `json:"last_sequence"`
	IssuedAt       time.Time `json:"issued_at"`
	User           UserView  `json:"user"`
}

// String never prints the token.
func (t HandoffTicket) String() string {
	return fmt.Sprintf("handoff{generation:%d session:%s sequence:%d}", t.Generation, t.SessionID, t.LastSequence)
}

// GoString keeps %#v redacted too.
func (t HandoffTicket) GoString() string { return t.String() }

var (
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{16,512}$`)
)

// maxHandoffTicket bounds the document; a real one is about 600 bytes.
const maxHandoffTicket = 4 << 10

// ParseHandoff strictly decodes and validates a ticket as of now. Unknown fields, trailing
// data, an API base the channel does not allow and an expired or future ticket are refused
// with handoff_invalid / handoff_expired.
func ParseHandoff(raw []byte, now time.Time) (HandoffTicket, error) {
	var t HandoffTicket
	if len(raw) == 0 || len(raw) > maxHandoffTicket {
		return HandoffTicket{}, codeError("handoff_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&t) != nil || decoder.More() {
		return HandoffTicket{}, codeError("handoff_invalid")
	}
	if _, err := apiclient.ParseBase(t.APIBase, t.Channel); err != nil ||
		t.Version != HandoffVersion || t.Generation == 0 || t.LastSequence < 0 ||
		!uuidPattern.MatchString(t.SessionID) || !tokenPattern.MatchString(t.Token) || !uuidPattern.MatchString(t.DeviceID) ||
		(t.SelectedNodeID != "" && !uuidPattern.MatchString(t.SelectedNodeID)) ||
		t.User.ID == "" || len(t.User.ID) > 64 || len(t.User.Email) > 254 || len(t.User.Name) > 256 || len(t.User.Role) > 32 ||
		t.IssuedAt.IsZero() {
		return HandoffTicket{}, codeError("handoff_invalid")
	}
	if age := now.Sub(t.IssuedAt); age > HandoffTTL || age < -handoffSkew {
		return HandoffTicket{}, codeError("handoff_expired")
	}
	return t, nil
}

// ExportHandoff hands the signed-in session to another process. The data path is closed
// and every counted byte acknowledged first; then this process stops its workers and forgets
// the token without revoking it, and reports StateReleased. The caller must deliver the ticket
// or adopt it back itself; until one process adopts it, nobody renews the session (the
// server's idle timeout ends it if the ticket is lost).
func (m *Manager) ExportHandoff(ctx context.Context) (HandoffTicket, error) {
	if !m.opMu.TryLock() {
		return HandoffTicket{}, codeError("operation_in_progress")
	}
	defer m.opMu.Unlock()
	if _, _, _, err := m.handles(); err != nil {
		return HandoffTicket{}, err
	}
	m.bumpIntent()
	if err := m.stopTunnel(ctx, ""); err != nil {
		return HandoffTicket{}, codeError("handoff_unsettled")
	}
	m.mu.Lock()
	gen := m.generation
	m.mu.Unlock()
	if err := m.settleIdle(ctx, gen); err != nil {
		return HandoffTicket{}, codeError("handoff_unsettled")
	}
	// A release that waited for that tail goes out now; if it still cannot, the lease
	// stays with this process (below).
	m.retryRelease(ctx, gen)

	m.mu.Lock()
	if m.generation != gen || m.client == nil || m.session == nil || m.user == nil {
		m.mu.Unlock()
		return HandoffTicket{}, codeError("stale_generation")
	}
	// A revocation not yet delivered to a node, an online lease still held or not yet
	// released, or a ledger with anything unacknowledged, stays with the process that knows
	// about it.
	if m.grant != nil || m.orphan != "" || m.admission != nil || m.unreleased != nil || (m.meter != nil && !m.meter.Settled()) {
		m.mu.Unlock()
		return HandoffTicket{}, codeError("handoff_unsettled")
	}
	ticket := HandoffTicket{
		Version: HandoffVersion, APIBase: m.login.APIBase, Channel: m.login.Channel,
		SessionID: m.session.SessionID, Token: m.token, DeviceID: m.login.DeviceID,
		SelectedNodeID: m.selected, Generation: m.handoffGen + 1, LastSequence: m.session.LastSequence,
		IssuedAt: m.opts.Now().UTC(),
		User:     UserView{ID: m.user.ID, Email: m.user.Email, Name: m.user.Name, Role: m.user.Role},
	}
	client := m.client
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.generation++
	m.abortConnLocked()
	m.resetLocked(false)
	m.state = StateReleased
	m.mu.Unlock()
	client.CloseIdle()
	_ = m.opts.Engine.Stop()
	m.opts.Egress.Clear()
	return ticket, nil
}

// AdoptHandoff continues the session a ticket describes. It refuses while this process holds
// a session of its own, and refuses a generation at or below one it already adopted. The
// server must still know the session under the ticket's id and must not be behind the ledger
// the sender confirmed. On success the session runs here exactly as after a login (renewal,
// catalog watcher) and the ticket's line is selected again; a failed selection is reported
// but leaves the session adopted.
func (m *Manager) AdoptHandoff(ctx context.Context, t HandoffTicket) (Snapshot, error) {
	if !m.opMu.TryLock() {
		return m.Snapshot(), codeError("operation_in_progress")
	}
	defer m.opMu.Unlock()
	base, err := apiclient.ParseBase(t.APIBase, t.Channel)
	if err != nil {
		return m.Snapshot(), codeError("handoff_invalid")
	}
	m.mu.Lock()
	if m.client != nil {
		m.mu.Unlock()
		return m.Snapshot(), codeError("handoff_busy")
	}
	if t.Generation <= m.adopted {
		m.mu.Unlock()
		return m.Snapshot(), codeError("handoff_stale")
	}
	m.generation++
	gen := m.generation
	m.state = StateAuthenticated
	m.lastError = nil
	client := m.opts.NewClient(base)
	m.client = client
	m.mu.Unlock()

	start := m.opts.Now()
	s, err := client.Session(ctx, t.Token)

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		client.CloseIdle()
		return m.Snapshot(), codeError("stale_generation")
	}
	refuse := func(code string) (Snapshot, error) {
		m.resetLocked(false)
		m.lastError = &errorInfo{Code: code, Message: apiclient.Message(code)}
		snap := m.snapshotLocked()
		m.mu.Unlock()
		client.CloseIdle()
		return snap, codeError(code)
	}
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && !apiErr.Transient {
			return refuse(apiErr.Code)
		}
		// Transient: the ticket stays usable (it was not consumed), so the caller may retry.
		m.resetLocked(false)
		m.failLocked(err)
		snap := m.snapshotLocked()
		m.mu.Unlock()
		client.CloseIdle()
		return snap, err
	}
	switch {
	case apiclient.TerminatesSession(s.Reason):
		return refuse(s.Reason)
	case s.SessionID != t.SessionID:
		return refuse("handoff_mismatch")
	case s.LastSequence < t.LastSequence:
		return refuse("handoff_conflict")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.token = t.Token
	m.login = handoffOrigin{APIBase: t.APIBase, Channel: t.Channel, DeviceID: t.DeviceID}
	m.handoffGen, m.adopted = t.Generation, t.Generation
	m.user = &apiclient.User{ID: t.User.ID, Email: t.User.Email, Name: t.User.Name, Role: t.User.Role}
	m.state = StateReady
	m.applySessionLocked(s, start)
	m.mu.Unlock()

	go m.keepAlive(runCtx, gen, client, t.Token)
	go m.watchCatalog(runCtx, gen, client, t.Token)

	if err := m.syncCatalog(ctx, gen, client, t.Token); err != nil {
		return m.Snapshot(), err
	}
	if t.SelectedNodeID == "" {
		return m.Snapshot(), nil
	}
	return m.selectLine(gen, t.SelectedNodeID)
}
