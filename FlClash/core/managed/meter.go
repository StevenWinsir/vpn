package managed

import (
	"context"
	"sync"
	"time"
)

type ReadTotals func() (int64, int64)

type SampleTotals func(context.Context) (int64, int64, error)

type MeterView struct {
	Sequence             int64 `json:"sequence"`
	AcknowledgedUpload   int64 `json:"acknowledged_upload_bytes"`
	AcknowledgedDownload int64 `json:"acknowledged_download_bytes"`
	Upload               int64 `json:"upload_bytes"`
	Download             int64 `json:"download_bytes"`
	ConfirmedRemaining   int64 `json:"confirmed_remaining_bytes"`
	EstimatedRemaining   int64 `json:"estimated_remaining_bytes"`
	Pending              bool  `json:"pending"`
}

type Meter struct {
	mu                     sync.Mutex
	reportMu               chan struct{}
	sampleMu               chan struct{}
	closeOnce              sync.Once
	client                 *Client
	read                   SampleTotals
	stop                   func()
	status                 Status
	confirmed              Status
	confirmedStarted       time.Time
	lastFailure            string
	deadline               time.Time
	upload, download       int64
	rawUpload, rawDownload int64
	pending                *Counters
	cancel                 context.CancelFunc
	ctx                    context.Context
	done                   chan struct{}
	closeDone              chan struct{}
	closeErr               error
	reporters              sync.WaitGroup
	busy                   bool
	closed                 bool
	fatalReason            string
	clock                  func() time.Time
}

func Start(ctx context.Context, client *Client, read ReadTotals, stop func()) (*Meter, error) {
	if read == nil {
		return nil, &APIError{"meter_not_ready"}
	}
	return StartWithSampler(ctx, client, func(ctx context.Context) (int64, int64, error) {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		up, down := read()
		return up, down, nil
	}, stop)
}

func StartWithSampler(ctx context.Context, client *Client, read SampleTotals, stop func()) (*Meter, error) {
	if client == nil || read == nil || stop == nil {
		return nil, &APIError{"meter_not_ready"}
	}
	stop()
	started := time.Now()
	status, err := client.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !status.CanConnect || status.RemainingBytes <= 0 || !time.Now().Before(authorizationDeadline(status, started)) {
		if status.Reason == "" {
			status.Reason = "session_rejected"
		}
		return nil, &APIError{status.Reason}
	}
	up, down, sampleErr := read(ctx)
	if sampleErr != nil || up < 0 || down < 0 || up > MaxCounter || down > MaxCounter {
		return nil, &APIError{"invalid_core_counters"}
	}
	worker, cancel := context.WithCancel(context.Background())
	m := &Meter{client: client, read: read, stop: stop, status: status, confirmed: status, confirmedStarted: started,
		deadline: authorizationDeadline(status, started), upload: status.UploadBytes, download: status.DownloadBytes,
		rawUpload: up, rawDownload: down, ctx: worker, cancel: cancel, done: make(chan struct{}), closeDone: make(chan struct{}),
		reportMu: make(chan struct{}, 1), sampleMu: make(chan struct{}, 1), clock: time.Now}
	if err = m.Flush(ctx); err != nil {
		go m.run()
		return m, err
	}
	if !m.Status().CanConnect {
		go m.run()
		return m, &APIError{m.Status().Reason}
	}
	go m.run()
	return m, nil
}

func (m *Meter) snapshotLocked() Status {
	status := m.status
	unreported := m.upload - status.UploadBytes + m.download - status.DownloadBytes
	status.RemainingBytes = max(0, status.RemainingBytes-unreported)
	if m.closed {
		status.CanConnect = false
		status.Reason = "logged_out"
	} else if m.fatalReason != "" {
		status.CanConnect = false
		status.Reason = m.fatalReason
	} else if status.CanConnect && (!m.clock().Before(m.deadline) || !m.clock().Before(status.ExpiresAt)) {
		status.CanConnect = false
		status.Reason = "lease_expired"
	} else if status.RemainingBytes <= 0 {
		status.CanConnect = false
		status.Reason = "quota_exhausted"
	}
	return status
}

func (m *Meter) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshotLocked() }

func (m *Meter) sample() bool {
	return m.sampleContext(m.ctx)
}

func (m *Meter) sampleContext(ctx context.Context) bool {
	select {
	case m.sampleMu <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	defer func() { <-m.sampleMu }()
	up, down, err := m.read(ctx)
	if ctx.Err() != nil {
		return false
	}
	m.mu.Lock()
	if err != nil || up < m.rawUpload || down < m.rawDownload || up < 0 || down < 0 || up-m.rawUpload > MaxCounter-m.upload || down-m.rawDownload > MaxCounter-m.download {
		m.status.CanConnect = false
		m.status.Reason = "invalid_core_counters"
		m.fatalReason = "invalid_core_counters"
		m.lastFailure = "invalid_core_counters"
	} else {
		m.upload += up - m.rawUpload
		m.download += down - m.rawDownload
		m.rawUpload, m.rawDownload = up, down
	}
	allowed := m.snapshotLocked().CanConnect
	m.mu.Unlock()
	if !allowed {
		m.stop()
	}
	return allowed
}

func (m *Meter) deny(reason string) {
	m.mu.Lock()
	m.status.CanConnect = false
	m.status.Reason = reason
	m.lastFailure = reason
	m.mu.Unlock()
	m.stop()
}

func (m *Meter) Flush(ctx context.Context) error {
	return m.flush(ctx, false)
}

func (m *Meter) flush(ctx context.Context, final bool) error {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	if !final {
		detach := context.AfterFunc(m.ctx, cancel)
		defer detach()
	}
	select {
	case m.reportMu <- struct{}{}:
	case <-work.Done():
		return &APIError{"traffic_unconfirmed"}
	}
	defer func() { <-m.reportMu }()
	return m.flushLocked(work, final)
}

func (m *Meter) flushLocked(work context.Context, final bool) error {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed && !final {
		return &APIError{"logged_out"}
	}
	m.sampleContext(work)
	if work.Err() != nil {
		return &APIError{"traffic_unconfirmed"}
	}
	m.mu.Lock()
	if m.pending == nil {
		m.pending = &Counters{Sequence: m.status.LastSequence + 1, UploadBytes: m.upload, DownloadBytes: m.download}
	}
	payload := *m.pending
	m.mu.Unlock()
	started := m.clock()
	status, err := m.client.Report(work, payload)
	if err != nil {
		m.deny(errorCode(err))
		return err
	}
	m.mu.Lock()
	if status.SessionID != m.status.SessionID {
		m.mu.Unlock()
		m.deny("session_mismatch")
		return &APIError{"session_mismatch"}
	}
	m.status = status
	m.confirmed = status
	m.confirmedStarted = started
	m.deadline = authorizationDeadline(status, started)
	m.pending = nil
	allowed := m.snapshotLocked().CanConnect
	m.mu.Unlock()
	if !allowed {
		m.stop()
	}
	return nil
}

func (m *Meter) launchReport() {
	m.mu.Lock()
	if m.busy || m.closed {
		m.mu.Unlock()
		return
	}
	m.busy = true
	m.reporters.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.reporters.Done()
		defer func() { m.mu.Lock(); m.busy = false; m.mu.Unlock() }()
		_ = m.Flush(m.ctx)
	}()
}

func (m *Meter) loadConfiguration(ctx context.Context, load func(context.Context) (Profile, error)) (Profile, error) {
	select {
	case m.reportMu <- struct{}{}:
	case <-ctx.Done():
		return Profile{}, &APIError{"traffic_unconfirmed"}
	}
	defer func() { <-m.reportMu }()
	settled := false
	for range 3 {
		if err := m.flushLocked(ctx, false); err != nil {
			return Profile{}, &APIError{"traffic_unconfirmed"}
		}
		m.mu.Lock()
		settled = m.pending == nil && m.upload == m.confirmed.UploadBytes && m.download == m.confirmed.DownloadBytes && m.fatalReason == ""
		m.mu.Unlock()
		if settled {
			break
		}
	}
	if !settled {
		return Profile{}, &APIError{"traffic_unconfirmed"}
	}
	started := m.clock()
	profile, err := load(ctx)
	if err != nil {
		return Profile{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status := profile.Session
	if status.SessionID != m.confirmed.SessionID || status.LastSequence != m.confirmed.LastSequence || status.UploadBytes != m.confirmed.UploadBytes || status.DownloadBytes != m.confirmed.DownloadBytes {
		return Profile{}, &APIError{"invalid_server_response"}
	}
	m.confirmed, m.status, m.confirmedStarted = status, status, started
	m.deadline = authorizationDeadline(status, started)
	return profile, nil
}

func (m *Meter) run() {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	next := m.clock().Add(time.Minute)
	previous := m.clock()
	wasAllowed := true
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			now := m.clock()
			if now.Round(0).Sub(previous.Round(0)) > 5*time.Second || now.Round(0).Before(previous.Round(0)) {
				m.deny("lifecycle_reconfirmation_required")
			}
			previous = now
			allowed := m.sample()
			m.mu.Lock()
			tail := m.pending == nil && (m.upload != m.confirmed.UploadBytes || m.download != m.confirmed.DownloadBytes)
			m.mu.Unlock()
			if !m.clock().Before(next) || (!allowed && (wasAllowed || tail)) {
				m.launchReport()
				next = m.clock().Add(time.Minute)
			}
			wasAllowed = allowed
		}
	}
}

func (m *Meter) Close(ctx context.Context, logout bool) error {
	owner := false
	m.closeOnce.Do(func() { owner = true })
	if !owner {
		select {
		case <-m.closeDone:
			return m.closeErr
		case <-ctx.Done():
			return &APIError{"traffic_unconfirmed"}
		}
	}
	defer close(m.closeDone)
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stop()
	m.cancel()
	defer m.client.Close()
	m.closeErr = m.settle(ctx, true)
	if logout && m.closeErr == nil {
		if m.client.Logout(ctx) != nil {
			m.closeErr = &APIError{"logout_unconfirmed"}
		}
	}
	return m.closeErr
}

func (m *Meter) Settle(ctx context.Context) error { return m.settle(ctx, false) }

func (m *Meter) settle(ctx context.Context, final bool) error {
	for range 3 {
		if err := m.flush(ctx, final); err != nil {
			return err
		}
		m.mu.Lock()
		settled := m.pending == nil && m.upload == m.confirmed.UploadBytes && m.download == m.confirmed.DownloadBytes
		fatal := m.fatalReason
		m.mu.Unlock()
		if fatal != "" {
			return &APIError{fatal}
		}
		if settled {
			return nil
		}
	}
	return &APIError{"traffic_unconfirmed"}
}

func (m *Meter) View() (MeterView, Status, time.Time, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MeterView{m.confirmed.LastSequence, m.confirmed.UploadBytes, m.confirmed.DownloadBytes,
			m.upload, m.download, m.confirmed.RemainingBytes, m.snapshotLocked().RemainingBytes, m.pending != nil},
		*copyStatus(m.confirmed), m.confirmedStarted, m.lastFailure
}

func (m *Meter) authorize(start func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status := m.snapshotLocked(); !status.CanConnect {
		return &APIError{status.Reason}
	}
	if err := start(); err != nil {
		return err
	}
	m.lastFailure = ""
	return nil
}
