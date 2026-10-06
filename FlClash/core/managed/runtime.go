package managed

import (
	"context"
	"sync"
	"time"
)

type RuntimeEngine interface {
	Start(context.Context, ConfigurationOwner, string, int) error
	Drain(context.Context) error
}

type runtimeLease struct {
	mu      sync.Mutex
	stop    func()
	retired bool
	running bool
}

func (r *runtimeLease) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.retired {
		r.running = false
		r.stop()
	}
}

func (r *runtimeLease) retire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.retired {
		r.running = false
		r.stop()
		r.retired = true
	}
}

func (r *runtimeLease) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running && !r.retired
}

func (r *runtimeLease) start(run func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retired {
		return &APIError{"operation_superseded"}
	}
	if err := run(); err != nil {
		r.running = false
		r.stop()
		return err
	}
	r.running = true
	return nil
}

type retiredAccount struct {
	client *Client
	meter  *Meter
	drain  func(context.Context) error
	once   sync.Once
	done   chan struct{}
	err    error
}

func releaseAccount(account *retiredAccount, revoke bool, parents ...context.Context) error {
	if account == nil {
		return nil
	}
	parent := context.Background()
	if len(parents) == 1 {
		parent = parents[0]
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	owner := false
	account.once.Do(func() {
		if account.done == nil {
			account.done = make(chan struct{})
		}
		owner = true
	})
	if !owner {
		select {
		case <-account.done:
			return account.err
		case <-ctx.Done():
			return &APIError{"traffic_unconfirmed"}
		}
	}
	defer close(account.done)
	account.err = releaseAccountOwned(ctx, account, revoke)
	return account.err
}

func releaseAccountOwned(ctx context.Context, account *retiredAccount, revoke bool) error {
	if account.meter == nil {
		if account.client == nil {
			return nil
		}
		defer account.client.Close()
		if revoke && account.client.Logout(ctx) != nil {
			return &APIError{"logout_unconfirmed"}
		}
		return nil
	}
	var drainErr error
	if account.drain != nil {
		drainErr = account.drain(ctx)
	}
	err := account.meter.Close(ctx, revoke && drainErr == nil)
	if drainErr == nil && errorCode(err) == "logout_unconfirmed" {
		return err
	}
	if drainErr != nil || err != nil {
		return &APIError{"final_traffic_unconfirmed"}
	}
	return nil
}

func (c *Coordinator) Connect(ctx context.Context, generation uint64, port int, expectedRevision ...uint64) (AccountSnapshot, error) {
	if port < 1024 || port > 65535 {
		return c.Snapshot(), &APIError{"invalid_runtime_options"}
	}
	if err := c.lock(ctx); err != nil {
		return c.Snapshot(), err
	}
	defer c.unlock()
	c.mu.Lock()
	retirement := c.retirement
	c.mu.Unlock()
	if retirement != nil {
		select {
		case <-retirement:
		case <-ctx.Done():
			return c.Snapshot(), &APIError{"traffic_unconfirmed"}
		}
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.snapshotLocked()
	engine, supported := c.configuration.(RuntimeEngine)
	if c.snapshot.Generation != generation || (len(expectedRevision) != 0 && (len(expectedRevision) != 1 || expectedRevision[0] != c.runRevision)) {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	if c.client == nil || c.snapshot.Configuration == nil || c.sampler == nil || !supported {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"managed_configuration_required"}
	}
	c.runRevision++
	revision, lease, client, meter := c.runRevision, c.runtime, c.client, c.meter
	c.snapshot.Busy = true
	c.requestCancel = cancel
	lease.Stop()
	c.mu.Unlock()
	err := engine.Drain(work)
	if err == nil {
		if meter == nil {
			meter, err = StartWithSampler(work, client, c.sampler, lease.Stop)
		} else {
			err = meter.Settle(work)
		}
	}
	c.mu.Lock()
	if c.snapshot.Generation != generation || c.client != client {
		c.mu.Unlock()
		if meter != nil {
			_ = releaseAccount(&retiredAccount{client: client, meter: meter, drain: engine.Drain}, false)
		}
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	if meter != nil {
		c.meter = meter
	}
	c.requestCancel = nil
	c.snapshot.Busy = false
	if c.runRevision != revision || work.Err() != nil {
		err = &APIError{"operation_superseded"}
	}
	if err == nil {
		_, status, started, _ := meter.View()
		if status.SessionID != c.snapshot.Session.SessionID {
			err = &APIError{"invalid_server_response"}
		} else {
			c.setStatusLocked(status, started)
		}
		c.meterSyncedAt = started
		if err != nil {
			_ = c.discardConfigurationLocked()
		} else if c.snapshot.Configuration == nil {
			err = &APIError{"managed_configuration_required"}
		} else {
			owner, id := c.configurationOwnerLocked(), c.snapshot.Configuration.ID
			err = meter.authorize(func() error { return lease.start(func() error { return engine.Start(work, owner, id, port) }) })
		}
	}
	if err != nil {
		lease.Stop()
		c.snapshot.ErrorCode = PublicError(err)
	} else {
		c.snapshot.ErrorCode = ""
	}
	result := c.snapshotLocked()
	c.mu.Unlock()
	return result, nil
}

func (c *Coordinator) stopLocked() {
	c.runRevision++
	if c.requestCancel != nil {
		c.requestCancel()
	}
	c.snapshot.CanConnect = false
	c.runtime.Stop()
}

func (c *Coordinator) StopNow() {
	c.mu.Lock()
	c.stopLocked()
	c.mu.Unlock()
}

func (c *Coordinator) Disconnect(ctx context.Context, generation uint64) (AccountSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c.mu.Lock()
	if c.snapshot.Generation != generation {
		c.mu.Unlock()
		return c.Snapshot(), &APIError{"operation_superseded"}
	}
	c.stopLocked()
	hasMeter := c.meter != nil
	c.mu.Unlock()
	if !hasMeter {
		return c.Snapshot(), nil
	}
	return c.Flush(ctx, generation)
}

func (c *Coordinator) WithRunning(run func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshotLocked()
	if c.meter == nil || c.snapshot.Configuration == nil || !c.runtime.Running() {
		return &APIError{"managed_connection_required"}
	}
	return c.meter.authorize(func() error {
		c.runtime.mu.Lock()
		defer c.runtime.mu.Unlock()
		if !c.runtime.running || c.runtime.retired {
			return &APIError{"managed_connection_required"}
		}
		return run()
	})
}
