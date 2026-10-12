// Package meter is the Core's single traffic ledger writer for the development
// (client_reported) accounting mode (handoff_app.md sections 5.2 and 7).
//
// The counters it reports are cumulative and monotonic for the whole login session:
// the server's own totals at bind time plus whatever the engine has forwarded since.
// There is at most one unconfirmed ("pending") report; it is retried verbatim until
// the server acknowledges it, so a lost reply can never double-charge or lose bytes.
package meter

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"asterlink/core/internal/apiclient"
)

// MaxCounter mirrors the server's hard ceiling (billing.MaxClientCounter).
const MaxCounter int64 = 1 << 50

// HistoryLen is the number of one-second rate samples kept for the UI chart.
const HistoryLen = 60

// Error codes the meter reports; they are also stable IPC error codes.
const (
	CodeInvalidCounters = "invalid_core_counters"
	CodeUnconfirmed     = "traffic_unconfirmed"
	CodeConflict        = "traffic_conflict"
	CodeSessionMismatch = "session_mismatch"
	CodeRateChanged     = "profile_changed"
)

// Error carries one of the codes above.
type Error struct{ Code string }

func (e *Error) Error() string { return "meter: " + e.Code }

func codeOf(err error) string {
	var m *Error
	if errors.As(err, &m) {
		return m.Code
	}
	var a *apiclient.Error
	if errors.As(err, &a) {
		return a.Code
	}
	return CodeUnconfirmed
}

// Config wires the meter to its two collaborators; tests replace both.
type Config struct {
	// Base is the server's confirmed state when the meter is created.
	Base   apiclient.Session
	Totals func() (upload, download int64, err error)
	Report func(ctx context.Context, c apiclient.Counters) (apiclient.Session, error)
	Now    func() time.Time
}

// Sample is one second of throughput.
type Sample struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

// View is the redacted state the snapshot exposes.
type View struct {
	// Local cumulative proxy bytes for this login session (confirmed + unreported).
	UploadBytes, DownloadBytes int64
	// What the server has acknowledged.
	ConfirmedUpload, ConfirmedDownload, LastSequence int64
	ConfirmedRemaining, EstimatedRemaining           int64
	RatePermille                                     int64
	UploadRate, DownloadRate                         int64
	Pending                                          bool
	History                                          []Sample
}

type Meter struct {
	cfg Config

	// reportMu serialises report attempts: exactly one writer, ever.
	reportMu chan struct{}

	mu sync.Mutex
	// rawUp/rawDown are the engine's last observed totals; cumUp/cumDown the ledger.
	rawUp, rawDown int64
	cumUp, cumDown int64
	confirmed      apiclient.Session
	pending        *apiclient.Counters
	fatal          string

	lastSample       time.Time
	upRate, downRate int64
	history          []Sample
	haveBaseline     bool
}

func New(cfg Config) *Meter {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Meter{cfg: cfg, reportMu: make(chan struct{}, 1), confirmed: cfg.Base, cumUp: cfg.Base.UploadBytes, cumDown: cfg.Base.DownloadBytes}
}

// Fatal is the reason the ledger can no longer be trusted ("" while healthy).
func (m *Meter) Fatal() string { m.mu.Lock(); defer m.mu.Unlock(); return m.fatal }

func (m *Meter) fail(code string) error {
	if m.fatal == "" {
		m.fatal = code
	}
	return &Error{Code: code}
}

// Settled reports that nothing is awaiting acknowledgement and every counted byte is
// confirmed (the precondition for rebinding, switching node or rate, or exiting).
func (m *Meter) Settled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending == nil && m.cumUp == m.confirmed.UploadBytes && m.cumDown == m.confirmed.DownloadBytes
}

// Begin starts a new forwarding segment. It is only legal when the previous segment
// is fully settled and the server still agrees with the ledger; the engine's counters
// are then re-baselined, which is the one controlled "epoch" the ledger accepts
// (section 7.2). Anything else is a conflict that forces a fresh login.
func (m *Meter) Begin(server apiclient.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal != "" {
		return &Error{Code: m.fatal}
	}
	if m.pending != nil || m.cumUp != m.confirmed.UploadBytes || m.cumDown != m.confirmed.DownloadBytes {
		return &Error{Code: CodeUnconfirmed}
	}
	if server.SessionID != m.confirmed.SessionID || server.LastSequence != m.confirmed.LastSequence ||
		server.UploadBytes != m.confirmed.UploadBytes || server.DownloadBytes != m.confirmed.DownloadBytes {
		return m.fail(CodeConflict)
	}
	up, down, err := m.cfg.Totals()
	if err != nil || up < 0 || down < 0 || up > MaxCounter || down > MaxCounter {
		return m.fail(CodeInvalidCounters)
	}
	m.confirmed = server
	m.rawUp, m.rawDown, m.haveBaseline = up, down, true
	m.lastSample, m.upRate, m.downRate, m.history = m.cfg.Now(), 0, 0, nil
	return nil
}

// Sample reads the engine's totals, extends the ledger and refreshes the rates.
// A counter that moves backwards, or would exceed the server ceiling, is fatal: the
// meter never patches over an inconsistency with an offset (section 7.2).
func (m *Meter) Sample() error {
	up, down, err := m.cfg.Totals()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal != "" {
		return &Error{Code: m.fatal}
	}
	if !m.haveBaseline {
		return &Error{Code: CodeInvalidCounters}
	}
	if err != nil || up < m.rawUp || down < m.rawDown || up < 0 || down < 0 ||
		up-m.rawUp > MaxCounter-m.cumUp || down-m.rawDown > MaxCounter-m.cumDown {
		return m.fail(CodeInvalidCounters)
	}
	now := m.cfg.Now()
	if elapsed := now.Sub(m.lastSample); elapsed > 0 {
		m.upRate = int64(float64(up-m.rawUp) / elapsed.Seconds())
		m.downRate = int64(float64(down-m.rawDown) / elapsed.Seconds())
		m.history = append(m.history, Sample{Upload: m.upRate, Download: m.downRate})
		if len(m.history) > HistoryLen {
			m.history = m.history[len(m.history)-HistoryLen:]
		}
	}
	m.lastSample = now
	m.cumUp += up - m.rawUp
	m.cumDown += down - m.rawDown
	m.rawUp, m.rawDown = up, down
	return nil
}

// Flush performs exactly one report attempt. It returns the acknowledged session and
// the local time the request was sent (lease accounting starts there). On any failure
// the pending batch is kept byte-for-byte for the next attempt.
func (m *Meter) Flush(ctx context.Context) (apiclient.Session, time.Time, error) {
	select {
	case m.reportMu <- struct{}{}:
	case <-ctx.Done():
		return apiclient.Session{}, time.Time{}, &Error{Code: CodeUnconfirmed}
	}
	defer func() { <-m.reportMu }()

	m.mu.Lock()
	if m.fatal != "" {
		code := m.fatal
		m.mu.Unlock()
		return apiclient.Session{}, time.Time{}, &Error{Code: code}
	}
	pendingExists := m.pending != nil
	m.mu.Unlock()
	if !pendingExists {
		// A new batch may include fresh bytes. An existing pending one is never altered.
		if err := m.Sample(); err != nil {
			return apiclient.Session{}, time.Time{}, err
		}
	}
	m.mu.Lock()
	if m.pending == nil {
		m.pending = &apiclient.Counters{Sequence: m.confirmed.LastSequence + 1, UploadBytes: m.cumUp, DownloadBytes: m.cumDown}
	}
	batch := *m.pending
	rate := m.confirmed.RatePermille
	m.mu.Unlock()

	started := m.cfg.Now()
	ack, err := m.cfg.Report(ctx, batch)
	if err != nil {
		var api *apiclient.Error
		if errors.As(err, &api) && (api.Code == "traffic_conflict" || api.Code == "traffic_sequence") {
			m.mu.Lock()
			_ = m.fail(CodeConflict)
			m.mu.Unlock()
			return apiclient.Session{}, started, &Error{Code: CodeConflict}
		}
		return apiclient.Session{}, started, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case ack.SessionID != m.confirmed.SessionID:
		return apiclient.Session{}, started, m.fail(CodeSessionMismatch)
	case ack.LastSequence != batch.Sequence || ack.UploadBytes != batch.UploadBytes || ack.DownloadBytes != batch.DownloadBytes:
		return apiclient.Session{}, started, m.fail(CodeConflict)
	case ack.RatePermille != rate:
		// The multiplier moved underneath a running segment. Bytes counted so far are
		// settled at the bound rate; a new rate needs a new bind (section 6.3).
		m.confirmed, m.pending = ack, nil
		return ack, started, &Error{Code: CodeRateChanged}
	}
	m.confirmed, m.pending = ack, nil
	return ack, started, nil
}

// Settle flushes until every counted byte is acknowledged or ctx ends. It is what
// disconnect, node switch and logout call after the data path is closed.
func (m *Meter) Settle(ctx context.Context) (apiclient.Session, time.Time, error) {
	var (
		ack     apiclient.Session
		started time.Time
		err     error
	)
	for range 3 {
		ack, started, err = m.Flush(ctx)
		if err != nil {
			return ack, started, err
		}
		if m.Settled() {
			return ack, started, nil
		}
	}
	return ack, started, &Error{Code: CodeUnconfirmed}
}

// ErrorCode extracts the stable code from any meter or API failure.
func ErrorCode(err error) string { return codeOf(err) }

// Pending reports whether an unacknowledged batch exists.
func (m *Meter) Pending() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.pending != nil }

// Unreported is the number of counted bytes the server has not acknowledged yet.
func (m *Meter) Unreported() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cumUp - m.confirmed.UploadBytes + m.cumDown - m.confirmed.DownloadBytes
}

// EstimatedRemaining is the confirmed balance minus the not-yet-acknowledged usage at
// the bound rate, all in integer 1/1000-byte units (section 7.1). It is an estimate:
// only the server's figure is ever final.
func (m *Meter) EstimatedRemaining() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.estimatedLocked()
}

func (m *Meter) estimatedLocked() int64 {
	s := m.confirmed
	unreported := m.cumUp - s.UploadBytes + m.cumDown - s.DownloadBytes
	if unreported < 0 {
		return 0
	}
	if s.RatePermille > 0 && unreported > math.MaxInt64/s.RatePermille {
		return 0
	}
	charge := unreported * s.RatePermille
	available := s.TotalBytes*1000 - s.UsedUnits - charge
	if available <= 0 {
		return 0
	}
	return available / 1000
}

func (m *Meter) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return View{
		UploadBytes: m.cumUp, DownloadBytes: m.cumDown,
		ConfirmedUpload: m.confirmed.UploadBytes, ConfirmedDownload: m.confirmed.DownloadBytes, LastSequence: m.confirmed.LastSequence,
		ConfirmedRemaining: m.confirmed.RemainingBytes, EstimatedRemaining: m.estimatedLocked(), RatePermille: m.confirmed.RatePermille,
		UploadRate: m.upRate, DownloadRate: m.downRate, Pending: m.pending != nil,
		History: append([]Sample(nil), m.history...),
	}
}

// Idle zeroes the displayed rates once the data path is closed.
func (m *Meter) Idle() {
	m.mu.Lock()
	m.upRate, m.downRate = 0, 0
	m.mu.Unlock()
}
