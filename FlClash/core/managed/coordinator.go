package managed

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type AccountSnapshot struct {
	APIBase         string             `json:"api_base"`
	Generation      uint64             `json:"generation"`
	RuntimeRevision uint64             `json:"runtime_revision"`
	Phase           string             `json:"phase"`
	Busy            bool               `json:"busy"`
	CanConnect      bool               `json:"can_connect"`
	User            *User              `json:"user"`
	Session         *Status            `json:"session"`
	ProfileVersion  string             `json:"profile_version"`
	ErrorCode       string             `json:"error_code"`
	Configuration   *ConfigurationView `json:"configuration,omitempty"`
	Metering        *MeterView         `json:"metering,omitempty"`
}

type Coordinator struct {
	mu                    sync.Mutex
	serial                chan struct{}
	base                  string
	transport             func() http.RoundTripper
	stop                  func()
	clock                 func() time.Time
	heartbeatInterval     time.Duration
	tickInterval          time.Duration
	snapshot              AccountSnapshot
	client                *Client
	profile               *Profile
	configuration         ConfigurationEngine
	configurationDeadline time.Time
	deadline              time.Time
	requestCancel         context.CancelFunc
	lifeCancel            context.CancelFunc
	meter                 *Meter
	sampler               SampleTotals
	runtime               *runtimeLease
	runRevision           uint64
	meterSyncedAt         time.Time
	retirement            <-chan struct{}
}

func NewCoordinator(transport func() http.RoundTripper, stop func(), configuration ConfigurationEngine, sampler ...SampleTotals) *Coordinator {
	if configuration == nil {
		panic("managed coordinator requires a configuration owner")
	}
	c := newCoordinator(APIBase, transport, stop)
	c.configuration = configuration
	if len(sampler) == 1 {
		c.sampler = sampler[0]
	}
	return c
}

func newCoordinator(base string, transport func() http.RoundTripper, stop func()) *Coordinator {
	if transport == nil || stop == nil {
		panic("managed coordinator requires transport and stop owners")
	}
	return &Coordinator{base: base, transport: transport, stop: stop, clock: time.Now,
		serial: make(chan struct{}, 1), heartbeatInterval: time.Minute, tickInterval: time.Second,
		runtime:  &runtimeLease{stop: stop},
		snapshot: AccountSnapshot{Phase: "signed_out"}}
}

func (c *Coordinator) lock(ctx context.Context) error {
	select {
	case c.serial <- struct{}{}:
		return nil
	case <-ctx.Done():
		return &APIError{"operation_superseded"}
	}
}

func (c *Coordinator) unlock() { <-c.serial }

func copyStatus(status Status) *Status {
	copyTime := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	status.SubscriptionExpiresAt = copyTime(status.SubscriptionExpiresAt)
	status.AuthorizationExpiresAt = copyTime(status.AuthorizationExpiresAt)
	return &status
}

func (c *Coordinator) snapshotLocked() AccountSnapshot {
	if c.meter != nil {
		view, confirmed, started, failure := c.meter.View()
		if !c.snapshot.Busy && !started.Equal(c.meterSyncedAt) {
			c.setStatusLocked(confirmed, started)
			c.meterSyncedAt = started
		}
		c.snapshot.Metering = &view
		if failure != "" {
			c.snapshot.ErrorCode = PublicError(&APIError{failure})
		}
		if status := c.meter.Status(); !status.CanConnect {
			c.runtime.Stop()
			if status.Reason != "" {
				c.snapshot.ErrorCode = PublicError(&APIError{status.Reason})
			}
		}
	}
	if c.client != nil && !c.clock().Before(c.deadline) {
		account := c.clearLocked("native_session_expired")
		go func() { _ = releaseAccount(account, false) }()
	}
	if c.profile != nil && !c.configurationDeadline.IsZero() && !c.clock().Before(c.configurationDeadline) {
		err := c.discardConfigurationLocked()
		c.snapshot.Phase, c.snapshot.ErrorCode = "unavailable", "configuration_expired"
		if err != nil {
			c.snapshot.ErrorCode = PublicError(err)
		}
	}
	c.snapshot.CanConnect = c.meter != nil && c.snapshot.Configuration != nil && c.meter.Status().CanConnect && c.runtime.Running()
	c.snapshot.RuntimeRevision = c.runRevision
	result := c.snapshot
	result.APIBase = c.base
	if result.Metering != nil {
		view := *result.Metering
		result.Metering = &view
	}
	result.Configuration = CopyConfiguration(c.snapshot.Configuration)
	if result.User != nil {
		user := *result.User
		result.User = &user
	}
	if result.Session != nil {
		result.Session = copyStatus(*result.Session)
	}
	return result
}

func (c *Coordinator) Snapshot() AccountSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Coordinator) clearLocked(reason string) *retiredAccount {
	if c.requestCancel != nil {
		c.requestCancel()
		c.requestCancel = nil
	}
	if c.lifeCancel != nil {
		c.lifeCancel()
		c.lifeCancel = nil
	}
	account := &retiredAccount{client: c.client, meter: c.meter, done: make(chan struct{})}
	if engine, ok := c.configuration.(RuntimeEngine); ok {
		account.drain = engine.Drain
	}
	if c.meter != nil {
		c.meter.cancel()
		c.retirement = account.done
	}
	c.meter = nil
	c.meterSyncedAt = time.Time{}
	c.client = nil
	c.runtime.retire()
	c.runtime = &runtimeLease{stop: c.stop}
	c.runRevision++
	if err := c.discardConfigurationLocked(); err != nil && reason == "" {
		reason = PublicError(err)
	}
	c.snapshot = AccountSnapshot{Generation: c.snapshot.Generation + 1, Phase: "signed_out", ErrorCode: reason}
	return account
}

func releaseClient(client *Client, revoke bool) bool {
	if client == nil {
		return true
	}
	defer client.Close()
	if !revoke {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return client.Logout(ctx) == nil
}

func (c *Coordinator) setStatusLocked(status Status, started time.Time) {
	c.deadline = started.Add(time.Duration(status.SessionIdleTimeout) * time.Second)
	for _, limit := range []time.Time{started.Add(status.ExpiresAt.Sub(status.ServerTime)), status.ExpiresAt} {
		if limit.Before(c.deadline) {
			c.deadline = limit
		}
	}
	if c.profile != nil && (c.profile.Version != status.ProfileVersion || !status.CanConnect) {
		if err := c.discardConfigurationLocked(); err != nil {
			c.snapshot.Phase, c.snapshot.ErrorCode = "unavailable", PublicError(err)
			return
		}
	}
	c.snapshot.Session = copyStatus(status)
	c.snapshot.ErrorCode = ""
	c.snapshot.ProfileVersion = ""
	if status.CanConnect || status.Reason == "profile_required" {
		c.snapshot.Phase = "profile_required"
		if c.profile != nil {
			c.snapshot.Phase = "configuration_staged"
			c.snapshot.ProfileVersion = c.profile.Version
			c.configurationDeadline = authorizationDeadline(status, started)
			if c.snapshot.Configuration != nil {
				c.snapshot.Phase = "configuration_applied"
			}
		}
	} else {
		c.snapshot.Phase = "restricted"
		c.snapshot.ErrorCode = PublicError(&APIError{status.Reason})
		c.profile = nil
	}
	// Only an explicit runtime transition after Meter acknowledgement opens this gate.
	c.snapshot.CanConnect = false
}

func (c *Coordinator) failLocked(err error) *retiredAccount {
	code := PublicError(err)
	switch code {
	case "native_session_required", "native_session_expired", "subscription_changed":
		return c.clearLocked(code)
	default:
		c.snapshot.ErrorCode = code
		c.snapshot.Phase = "unavailable"
		c.snapshot.Busy = false
		if cleanupErr := c.discardConfigurationLocked(); cleanupErr != nil {
			c.snapshot.ErrorCode = PublicError(cleanupErr)
		}
		return nil
	}
}

func (c *Coordinator) Login(ctx context.Context, params LoginParams) (AccountSnapshot, error) {
	if !params.Valid() {
		return c.Snapshot(), &APIError{"invalid_client_login"}
	}
	select {
	case c.serial <- struct{}{}:
	default:
		return c.Snapshot(), &APIError{"operation_in_progress"}
	}
	defer c.unlock()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	if c.meter != nil {
		meter, client, generation := c.meter, c.client, c.snapshot.Generation
		c.stopLocked()
		c.snapshot.Busy = true
		c.requestCancel = cancel
		c.mu.Unlock()
		settle, finish := context.WithTimeout(work, 2*time.Second)
		var err error
		if engine, ok := c.configuration.(RuntimeEngine); ok {
			err = engine.Drain(settle)
		}
		if err == nil {
			err = meter.Settle(settle)
		}
		finish()
		c.mu.Lock()
		if c.client != client || c.snapshot.Generation != generation {
			c.mu.Unlock()
			return c.Snapshot(), &APIError{"operation_superseded"}
		}
		c.requestCancel = nil
		c.snapshot.Busy = false
		if err != nil {
			c.snapshot.ErrorCode = "traffic_unconfirmed"
			result := c.snapshotLocked()
			c.mu.Unlock()
			return result, &APIError{"traffic_unconfirmed"}
		}
	}
	old := c.clearLocked("")
	generation := c.snapshot.Generation
	if c.snapshot.ErrorCode != "" {
		result := c.snapshotLocked()
		c.mu.Unlock()
		_ = releaseAccount(old, true)
		return result, nil
	}
	c.snapshot.Phase, c.snapshot.Busy = "authenticating", true
	c.requestCancel = cancel
	c.mu.Unlock()
	_ = releaseAccount(old, true)
	started := c.clock()
	client, user, status, err := Login(work, c.base, c.transport(), params)
	c.mu.Lock()
	if c.snapshot.Generation != generation || work.Err() != nil {
		if c.snapshot.Generation == generation {
			c.clearLocked("heartbeat_unavailable")
		}
		c.mu.Unlock()
		releaseClient(client, true)
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	c.requestCancel = nil
	c.snapshot.Busy = false
	if err != nil {
		c.snapshot.Phase, c.snapshot.ErrorCode = "signed_out", PublicError(err)
		result := c.snapshotLocked()
		c.mu.Unlock()
		return result, nil
	}
	c.client, c.snapshot.User = client, &user
	c.setStatusLocked(status, started)
	life, stopLife := context.WithCancel(context.Background())
	c.lifeCancel = stopLife
	result := c.snapshotLocked()
	c.mu.Unlock()
	go c.keepAlive(life, generation)
	return result, nil
}

func (c *Coordinator) request(ctx context.Context, generation uint64, load bool, selections ...*nodeSelection) (AccountSnapshot, error) {
	if err := c.lock(ctx); err != nil {
		return c.Snapshot(), err
	}
	defer c.unlock()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.snapshotLocked()
	if c.snapshot.Generation != generation {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	client := c.client
	if client == nil {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"native_session_required"}
	}
	nodeID := ""
	if len(selections) != 0 {
		selection := selections[0]
		if len(selections) != 1 || !load || c.snapshot.Configuration == nil || c.profile == nil || c.snapshot.Configuration.ID != selection.configurationID || selection.group != "VPN" {
			c.mu.Unlock()
			return c.Snapshot(), &APIError{"invalid_managed_selection"}
		}
		for _, node := range c.profile.Nodes {
			if node.Name == selection.proxy {
				nodeID = node.ID
			}
		}
		if nodeID == "" {
			c.mu.Unlock()
			return c.Snapshot(), &APIError{"invalid_managed_selection"}
		}
	}
	c.requestCancel = cancel
	c.snapshot.Busy = true
	meter := c.meter
	if load {
		c.runtime.Stop()
		c.runRevision++
	}
	if load {
		if err := c.discardConfigurationLocked(); err != nil {
			c.failLocked(err)
			result := c.snapshotLocked()
			c.mu.Unlock()
			return result, nil
		}
		c.snapshot.Phase = "loading_configuration"
	}
	sessionID := c.snapshot.Session.SessionID
	c.mu.Unlock()
	started := c.clock()
	var status Status
	var profile Profile
	var err error
	if meter != nil && load {
		if engine, ok := c.configuration.(RuntimeEngine); ok {
			err = engine.Drain(work)
		}
	}
	if err != nil {
		err = &APIError{"traffic_unconfirmed"}
	} else if load {
		loader := func(ctx context.Context) (Profile, error) { return client.ConfigForNode(ctx, nodeID) }
		if meter == nil {
			profile, err = loader(work)
		} else {
			profile, err = meter.loadConfiguration(work, loader)
		}
		status = profile.Session
	} else if meter != nil {
		err = meter.Flush(work)
		_, status, started, _ = meter.View()
	} else {
		status, err = client.Status(work)
	}
	if err == nil && status.SessionID != sessionID {
		err = &APIError{"invalid_server_response"}
	}
	var prepared PreparedConfiguration
	if err == nil && work.Err() != nil {
		err = &APIError{"operation_superseded"}
	}
	if err == nil && load && c.configuration != nil {
		prepared, err = c.configuration.Prepare(work, profile)
	}
	c.mu.Lock()
	if c.snapshot.Generation != generation || c.client != client {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	if err == nil && work.Err() != nil {
		err = &APIError{"operation_superseded"}
	}
	if err == nil && prepared != nil {
		var applied ConfigurationView
		applied, err = prepared.Apply(work, c.configurationOwnerLocked())
		if err == nil {
			c.snapshot.Configuration = CopyConfiguration(&applied)
		}
	}
	c.requestCancel = nil
	c.snapshot.Busy = false
	var released *retiredAccount
	if err != nil {
		released = c.failLocked(err)
	} else {
		if load {
			profile.YAML = ""
			c.profile = &profile
		}
		c.setStatusLocked(status, started)
		if meter != nil && !load {
			c.meterSyncedAt = started
		}
	}
	result := c.snapshotLocked()
	c.mu.Unlock()
	_ = releaseAccount(released, false)
	return result, nil
}

func (c *Coordinator) Refresh(ctx context.Context, generation uint64) (AccountSnapshot, error) {
	return c.request(ctx, generation, false)
}

func (c *Coordinator) LoadConfig(ctx context.Context, generation uint64) (AccountSnapshot, error) {
	return c.request(ctx, generation, true)
}

func (c *Coordinator) Flush(ctx context.Context, generation uint64) (AccountSnapshot, error) {
	if err := c.lock(ctx); err != nil {
		return c.Snapshot(), err
	}
	defer c.unlock()
	c.mu.Lock()
	if c.snapshot.Generation != generation {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	meter, running := c.meter, c.runtime.Running()
	c.mu.Unlock()
	if meter == nil {
		return c.Snapshot(), &APIError{"meter_not_ready"}
	}
	var err error
	if !running {
		if engine, ok := c.configuration.(RuntimeEngine); ok {
			err = engine.Drain(ctx)
		}
	}
	if err == nil {
		err = meter.Settle(ctx)
	}
	c.mu.Lock()
	if c.snapshot.Generation == generation && err != nil {
		c.runtime.Stop()
		c.snapshot.ErrorCode = "traffic_unconfirmed"
	}
	result := c.snapshotLocked()
	c.mu.Unlock()
	return result, err
}

func (c *Coordinator) logout(ctx context.Context, expected *uint64) (AccountSnapshot, error) {
	c.mu.Lock()
	if expected != nil && c.snapshot.Generation != *expected {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	old := c.clearLocked("")
	generation := c.snapshot.Generation
	c.mu.Unlock()
	if err := c.lock(ctx); err != nil {
		_ = releaseAccount(old, false, ctx)
		return c.Snapshot(), &APIError{"logout_unconfirmed"}
	}
	defer c.unlock()
	confirmationErr := releaseAccount(old, true, ctx)
	c.mu.Lock()
	if confirmationErr != nil && c.snapshot.Generation == generation {
		c.snapshot.ErrorCode = PublicError(confirmationErr)
	}
	result := c.snapshotLocked()
	c.mu.Unlock()
	return result, nil
}

func (c *Coordinator) Logout(ctx context.Context, generation uint64) (AccountSnapshot, error) {
	return c.logout(ctx, &generation)
}

func (c *Coordinator) Reset(ctx context.Context) (AccountSnapshot, error) {
	return c.logout(ctx, nil)
}

func (c *Coordinator) keepAlive(ctx context.Context, generation uint64) {
	ticker := time.NewTicker(c.tickInterval)
	defer ticker.Stop()
	next := c.clock().Add(c.heartbeatInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := c.Snapshot()
			if state.Generation != generation || state.User == nil {
				return
			}
			if !c.clock().Before(next) {
				c.mu.Lock()
				hasMeter := c.meter != nil
				c.mu.Unlock()
				if !hasMeter {
					_, _ = c.Refresh(ctx, generation)
				}
				next = c.clock().Add(c.heartbeatInterval)
			}
		}
	}
}
