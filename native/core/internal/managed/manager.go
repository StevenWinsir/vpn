// Package managed is the Core's single owner of account, catalog and authorization
// state (handoff_app.md sections 5.2, 6.3). Everything the UI may see leaves through
// Snapshot; tokens, passwords and node material never do.
package managed

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/url"
	"sync"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/meter"
	"asterlink/core/internal/network"
)

// State is the user-visible lifecycle position.
type State string

const (
	StateSignedOut     State = "signed_out"
	StateAuthenticated State = "authenticating"
	StateReady         State = "ready"
	StatePreparing     State = "preparing"
	StateConnecting    State = "connecting"
	StateConnected     State = "connected"
	StateStopping      State = "stopping"
	StateBlocked       State = "blocked"
	StateError         State = "error"
)

// Options are injected by the entry point; tests replace the clock and the HTTP client.
type Options struct {
	CoreVersion string
	Engine      Engine
	Now         func() time.Time
	NewClient   func(base *url.URL) *apiclient.Client
	// Backoff bounds for transient failures; tests shrink them.
	BackoffMin, BackoffMax time.Duration
	// MonitorInterval is the connected-state watchdog tick (default 1s).
	MonitorInterval time.Duration
	// SettleTimeout bounds the final traffic settlement when a tunnel stops.
	SettleTimeout time.Duration
	// Mono is a monotonic reading; Now().Round(0) is the wall clock. Their divergence
	// reveals sleep or a clock change. Tests replace both.
	Mono func() time.Duration
	// Signature identifies the physical network; a change stops the tunnel.
	Signature func() string
	// Egress receives the physical interface while a tunnel is up.
	Egress *network.Egress
}

type LoginParams struct {
	Email      string
	Password   string
	APIBase    string
	Channel    string
	DeviceID   string
	AppVersion string
}

// Manager is safe for concurrent use. All mutable fields are guarded by mu, and every
// network exchange runs with mu released; its result is applied only if the generation
// that started it is still current, so a late reply can never revive an old session.
type Manager struct {
	opts Options

	mu         sync.Mutex
	generation uint64
	cancel     context.CancelFunc
	client     *apiclient.Client
	token      string

	state    State
	user     *apiclient.User
	session  *apiclient.Session
	lease    lease
	nodes    []apiclient.NodeChoice
	revision string
	// selected is what the user picked; profile is the validated material for it and
	// is nil whenever it may be stale.
	selected     string
	profile      *Profile
	lastError    *errorInfo
	profileNode  apiclient.NodeChoice
	catalogReady bool
	// catalogReason is the entitlement verdict carried by /nodes; it can change
	// before the next /session renewal and must immediately gate connecting.
	catalogReason string

	// meter is the session's single ledger writer; conn is the live forwarding
	// segment (nil whenever the data path is closed).
	meter *meter.Meter
	conn  *connection
	// opMu admits one Connect at a time; stopMu serialises every teardown of a segment.
	opMu, stopMu sync.Mutex
}

type errorInfo struct {
	Code    string
	Message string
}

func New(opts Options) *Manager {
	if opts.Engine == nil {
		opts.Engine = UnavailableEngine()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewClient == nil {
		opts.NewClient = apiclient.New
	}
	if opts.BackoffMin <= 0 {
		opts.BackoffMin = 2 * time.Second
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = 30 * time.Second
	}
	if opts.MonitorInterval <= 0 {
		opts.MonitorInterval = time.Second
	}
	if opts.SettleTimeout <= 0 {
		opts.SettleTimeout = 10 * time.Second
	}
	if opts.Mono == nil {
		origin := time.Now()
		opts.Mono = func() time.Duration { return time.Since(origin) }
	}
	if opts.Signature == nil {
		opts.Signature = network.Signature
	}
	if opts.Egress == nil {
		opts.Egress = network.Default
	}
	return &Manager{opts: opts, state: StateSignedOut}
}

// Generation is the current session generation; commands carry it back so a reply
// to a superseded operation is discarded.
func (m *Manager) Generation() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.generation
}

// Login replaces any existing session. The previous one is torn down first, so there
// is never more than one set of background workers.
func (m *Manager) Login(ctx context.Context, p LoginParams) (Snapshot, error) {
	base, err := apiclient.ParseBase(p.APIBase, p.Channel)
	if err != nil {
		return m.Snapshot(), err
	}
	// A previous account's tunnel is closed and its tail settled under that account
	// before anything else happens (section 6.3: old tail never charges a new login).
	_ = m.stopTunnel(ctx, "")
	m.teardown(true)

	m.mu.Lock()
	m.generation++
	gen := m.generation
	m.state = StateAuthenticated
	m.lastError = nil
	client := m.opts.NewClient(base)
	m.client = client
	m.mu.Unlock()

	start := m.opts.Now()
	response, err := client.Login(ctx, apiclient.LoginRequest{Email: p.Email, Password: p.Password, DeviceID: p.DeviceID, Platform: "macos", AppVersion: p.AppVersion})

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), &apiclient.Error{Code: "stale_generation"}
	}
	if err != nil {
		m.failLocked(err)
		m.state = StateSignedOut
		m.client = nil
		m.mu.Unlock()
		client.CloseIdle()
		return m.Snapshot(), err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.token = response.Token
	user := response.User
	m.user = &user
	m.applySessionLocked(response.Session, start)
	m.state = StateReady
	m.mu.Unlock()

	go m.keepAlive(runCtx, gen, client, response.Token)
	go m.watchCatalog(runCtx, gen, client, response.Token)
	return m.Snapshot(), nil
}

// Logout stops background work, makes a bounded best-effort server revocation and
// clears every temporary identity. It is idempotent.
func (m *Manager) Logout(ctx context.Context) Snapshot {
	// Close the data path and settle the tail while the bearer token is still valid.
	_ = m.stopTunnel(ctx, "")
	m.mu.Lock()
	client, token := m.client, m.token
	m.mu.Unlock()
	m.teardown(false)
	if client != nil && token != "" {
		revoke, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = client.Logout(revoke, token)
		cancel()
		client.CloseIdle()
	}
	return m.Snapshot()
}

// Shutdown is the process-exit path: same teardown, engine stopped.
func (m *Manager) Shutdown(ctx context.Context) {
	m.Logout(ctx) // stops the tunnel and settles before revoking
	_ = m.opts.Engine.Stop()
	m.opts.Egress.Clear()
}

// teardown drops all session state. keepError retains lastError for a replacement login.
func (m *Manager) teardown(keepError bool) {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.generation++
	m.abortConnLocked()
	m.resetLocked(keepError)
	m.mu.Unlock()
	_ = m.opts.Engine.Stop()
	m.opts.Egress.Clear()
}

func (m *Manager) resetLocked(keepError bool) {
	m.client, m.token = nil, ""
	m.user, m.session, m.lease = nil, nil, lease{}
	m.nodes, m.revision, m.selected, m.profile, m.catalogReady, m.catalogReason = nil, "", "", nil, false, ""
	m.profileNode = apiclient.NodeChoice{}
	m.meter = nil
	m.state = StateSignedOut
	if !keepError {
		m.lastError = nil
	}
}

// failLocked records err. Errors that invalidate the bearer token end the session.
func (m *Manager) failLocked(err error) {
	var apiErr *apiclient.Error
	var meterErr *meter.Error
	code := "network_unreachable"
	if errors.As(err, &apiErr) {
		code = apiErr.Code
	} else if errors.As(err, &meterErr) {
		code = meterErr.Code
	}
	m.lastError = &errorInfo{Code: code, Message: apiclient.Message(code)}
}

// dropSessionLocked ends the session after the server refused the token.
func (m *Manager) dropSessionLocked(code string) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.generation++
	client := m.client
	m.abortConnLocked()
	m.resetLocked(false)
	m.lastError = &errorInfo{Code: code, Message: apiclient.Message(code)}
	if client != nil {
		go client.CloseIdle()
	}
	// The token is dead, so no settlement is possible: closing the data path is all
	// that remains to be done.
	_ = m.opts.Engine.Stop()
	m.opts.Egress.Clear()
}

// applySessionLocked folds a fresh server session in. requestStart is the local time
// the request was sent (lease accounting starts there, not at reply time).
func (m *Manager) applySessionLocked(s apiclient.Session, requestStart time.Time) {
	m.session = &s
	m.lease = newLease(requestStart, s.ServerTime, s.AuthorizationExpiresAt, s.LeaseSeconds)
	// A held profile is trusted only while the server reports no problem AND names the
	// very version we validated; anything else means it may be stale.
	if m.profile != nil && (s.Reason != "" || s.ProfileVersion != m.profile.Version) {
		m.profile = nil
	}
	if m.state == StateReady || m.state == StateBlocked {
		if s.CanConnect || s.Reason == "profile_required" || s.Reason == "profile_changed" {
			m.state = StateReady
		} else {
			m.state = StateBlocked
		}
	}
}

// jitter returns d reduced by up to 10% so renewals land early, never late.
func jitter(d time.Duration) time.Duration {
	return d - time.Duration(rand.Int64N(int64(d)/10+1))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// keepAlive is the one renewal writer while no meter runs (section 5.2): it polls
// GET /session at the server-provided interval. /nodes never substitutes for it.
func (m *Manager) keepAlive(ctx context.Context, gen uint64, client *apiclient.Client, token string) {
	backoff := m.opts.BackoffMin
	for {
		m.mu.Lock()
		interval := 60 * time.Second
		if m.generation == gen && m.session != nil && m.session.ReportInterval > 0 {
			interval = time.Duration(m.session.ReportInterval) * time.Second
		}
		m.mu.Unlock()
		if !sleepCtx(ctx, jitter(interval)) {
			return
		}
		for {
			m.mu.Lock()
			active, ledger := m.conn != nil, m.meter
			m.mu.Unlock()
			if active {
				// The meter is the only renewal writer while forwarding (section 5.2).
				break
			}
			start := m.opts.Now()
			var s apiclient.Session
			var err error
			if ledger != nil && ledger.Pending() {
				// An unacknowledged traffic batch outranks a plain session poll: its
				// replay settles the tail and renews the lease in one request.
				s, start, err = ledger.Flush(ctx)
				if err != nil && s.SessionID != "" {
					err = nil // e.g. a changed rate: the acknowledgement itself is valid
				}
			} else {
				s, err = client.Session(ctx, token)
			}
			if ctx.Err() != nil {
				return
			}
			m.mu.Lock()
			if m.generation != gen {
				m.mu.Unlock()
				return
			}
			if err == nil {
				if apiclient.TerminatesSession(s.Reason) {
					m.dropSessionLocked(s.Reason)
					m.mu.Unlock()
					return
				}
				m.applySessionLocked(s, start)
				m.lastError = nil
				m.mu.Unlock()
				backoff = m.opts.BackoffMin
				break
			}
			var apiErr *apiclient.Error
			if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
				m.dropSessionLocked(apiErr.Code)
				m.mu.Unlock()
				return
			}
			if ledger != nil && ledger.Fatal() != "" {
				// The ledger can no longer be trusted; only a fresh login starts a new one.
				m.dropSessionLocked(ledger.Fatal())
				m.mu.Unlock()
				return
			}
			m.failLocked(err)
			m.mu.Unlock()
			// Transient failure: retry sooner than a full interval, but with backoff.
			// The lease keeps running meanwhile and lapses on its own deadline.
			if !sleepCtx(ctx, jitter(backoff)) {
				return
			}
			backoff = min(backoff*2, m.opts.BackoffMax)
		}
	}
}
