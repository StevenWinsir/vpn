package managed

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type configurationStub struct {
	mu                                   sync.Mutex
	home                                 string
	view                                 *ConfigurationView
	prepareError, applyError, clearError error
	entered                              chan struct{}
	release                              chan struct{}
	applied                              atomic.Int64
}

type preparedStub struct {
	engine  *configurationStub
	profile Profile
}

func (s *configurationStub) Prepare(ctx context.Context, profile Profile) (PreparedConfiguration, error) {
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prepareError != nil {
		return nil, s.prepareError
	}
	return &preparedStub{s, profile}, nil
}
func (p *preparedStub) Apply(_ context.Context, owner ConfigurationOwner) (ConfigurationView, error) {
	s := p.engine
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := StoreConfiguration(s.home, owner, p.profile)
	if err != nil {
		return ConfigurationView{}, err
	}
	if s.applyError != nil {
		return ConfigurationView{}, s.applyError
	}
	view := ConfigurationView{ID: id, Version: p.profile.Version, Owner: owner, Groups: []ConfigurationGroup{{Name: "VIP", Type: "select", Selected: "A", Proxies: []string{"A", "B"}}}}
	s.view = CopyConfiguration(&view)
	s.applied.Add(1)
	return view, nil
}
func (s *configurationStub) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = nil
	if s.clearError != nil {
		return s.clearError
	}
	return ClearStoredConfiguration(s.home)
}
func (s *configurationStub) Select(_ context.Context, owner ConfigurationOwner, id, group, proxy string) ([]ConfigurationGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.view == nil || s.view.Owner != owner || s.view.ID != id {
		return nil, &APIError{"operation_superseded"}
	}
	if group != "VIP" || (proxy != "A" && proxy != "B") {
		return nil, &APIError{"invalid_managed_selection"}
	}
	s.view.Groups[0].Selected = proxy
	return CopyConfiguration(s.view).Groups, nil
}
func attachConfigurationStub(t *testing.T, c *Coordinator) *configurationStub {
	t.Helper()
	s := &configurationStub{home: t.TempDir()}
	c.configuration = s
	return s
}

func TestAppliedConfigurationOwnsSessionSelectionAndLogoutCleanup(t *testing.T) {
	_, c, _ := newAccountFixture(t)
	engine := attachConfigurationStub(t, c)
	state := mustAccountLogin(t, c)
	state, err := c.LoadConfig(context.Background(), state.Generation)
	if err != nil || state.Phase != "configuration_applied" || state.CanConnect || state.Configuration == nil {
		t.Fatal("configuration not applied behind the meter gate")
	}
	view := state.Configuration
	if view.Owner.UserID != state.User.ID || view.Owner.SessionID != state.Session.SessionID || view.Owner.Generation != state.Generation || c.profile.YAML != "" {
		t.Fatal("wrong owner or duplicate private YAML retained")
	}
	view.Groups[0].Proxies[0] = "tampered"
	if c.Snapshot().Configuration.Groups[0].Proxies[0] != "A" {
		t.Fatal("snapshot escaped mutable kernel metadata")
	}
	if _, err = c.SelectProxy(context.Background(), state.Generation, view.ID, "VIP", "B"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SelectProxy(context.Background(), state.Generation, "stale", "VIP", "A"); PublicError(err) != "managed_configuration_required" {
		t.Fatal("stale configuration selection accepted")
	}
	if _, err = c.SelectProxy(context.Background(), state.Generation-1, view.ID, "VIP", "A"); PublicError(err) != "operation_superseded" {
		t.Fatal("stale generation accepted")
	}
	state, err = c.Logout(context.Background(), state.Generation)
	if err != nil || state.Configuration != nil || state.User != nil {
		t.Fatal("logout retained owned configuration")
	}
	assertStoredAbsent(t, engine.home)
	next := mustAccountLogin(t, c)
	next, err = c.LoadConfig(context.Background(), next.Generation)
	if err != nil || next.Configuration.Owner.UserID == view.Owner.UserID || next.Configuration.ID == view.ID {
		t.Fatal("new account reused previous configuration ownership")
	}
	if _, err = c.SelectProxy(context.Background(), next.Generation, view.ID, "VIP", "A"); PublicError(err) != "managed_configuration_required" {
		t.Fatal("cross-account configuration ID accepted")
	}
}

func TestConfigurationFailureClearsOldAndPartiallyCommittedBundles(t *testing.T) {
	for _, phase := range []string{"prepare", "apply"} {
		t.Run(phase, func(t *testing.T) {
			_, c, _ := newAccountFixture(t)
			engine := attachConfigurationStub(t, c)
			state := mustAccountLogin(t, c)
			state, _ = c.LoadConfig(context.Background(), state.Generation)
			engine.mu.Lock()
			if phase == "prepare" {
				engine.prepareError = &APIError{"invalid_client_config"}
			} else {
				engine.applyError = &APIError{"configuration_apply_failed"}
			}
			engine.mu.Unlock()
			state, err := c.LoadConfig(context.Background(), state.Generation)
			if err != nil || state.Phase != "unavailable" || state.Configuration != nil || state.ProfileVersion != "" || state.CanConnect {
				t.Fatal("configuration failure reused an old or partial profile")
			}
			assertStoredAbsent(t, engine.home)
		})
	}
}

func TestLogoutDuringValidationCannotApplyLateConfiguration(t *testing.T) {
	_, c, _ := newAccountFixture(t)
	engine := attachConfigurationStub(t, c)
	engine.entered = make(chan struct{})
	engine.release = make(chan struct{})
	state := mustAccountLogin(t, c)
	done := make(chan error, 1)
	go func() { _, err := c.LoadConfig(context.Background(), state.Generation); done <- err }()
	select {
	case <-engine.entered:
	case <-time.After(time.Second):
		t.Fatal("prepare did not start")
	}
	release := sync.OnceFunc(func() { close(engine.release) })
	defer release()
	logoutDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _, err := c.Logout(ctx, state.Generation); logoutDone <- err }()
	deadline := time.Now().Add(time.Second)
	for c.Snapshot().User != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := c.Snapshot(); snapshot.User != nil || snapshot.Configuration != nil {
		t.Fatal("logout did not immediately clear local authorization")
	}
	release()
	select {
	case err := <-done:
		if PublicError(err) != "operation_superseded" {
			t.Fatal("late configuration not superseded")
		}
	case <-time.After(time.Second):
		t.Fatal("late configuration did not finish")
	}
	if err := <-logoutDone; err != nil {
		t.Fatal(err)
	}
	if engine.applied.Load() != 0 || c.Snapshot().Configuration != nil {
		t.Fatal("late validation applied after logout")
	}
	assertStoredAbsent(t, engine.home)
}

func TestManagedConfigurationExpiresWithoutRenewalFromCachedSnapshots(t *testing.T) {
	_, c, _ := newAccountFixture(t)
	engine := attachConfigurationStub(t, c)
	var offset atomic.Int64
	c.clock = func() time.Time { return time.Now().Add(time.Duration(offset.Load()) * time.Second) }
	state := mustAccountLogin(t, c)
	state, _ = c.LoadConfig(context.Background(), state.Generation)
	if state.Configuration == nil {
		t.Fatal("missing applied configuration")
	}
	offset.Store(91)
	state = c.Snapshot()
	if state.ErrorCode != "configuration_expired" || state.Configuration != nil || state.CanConnect {
		t.Fatal("cached status extended a managed configuration lease")
	}
	assertStoredAbsent(t, engine.home)
}

func TestConfigurationCleanupFailureBlocksLoginBeforeNetwork(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	engine := attachConfigurationStub(t, c)
	engine.clearError = &APIError{"configuration_cleanup_failed"}
	state, err := c.Login(context.Background(), accountParams())
	if err != nil || state.ErrorCode != "configuration_cleanup_failed" || state.User != nil || state.CanConnect || fixture.logins.Load() != 0 {
		t.Fatal("login ignored configuration cleanup failure")
	}
	engine.clearError = nil
}

func TestConfigurationRefreshRestrictionClearsDiskAndMetadata(t *testing.T) {
	for _, reason := range []string{"quota_exhausted", "subscription_expired", "profile_changed"} {
		t.Run(reason, func(t *testing.T) {
			fixture, c, _ := newAccountFixture(t)
			engine := attachConfigurationStub(t, c)
			state := mustAccountLogin(t, c)
			state, _ = c.LoadConfig(context.Background(), state.Generation)
			fixture.mu.Lock()
			fixture.reason = reason
			fixture.mu.Unlock()
			state, err := c.Refresh(context.Background(), state.Generation)
			if err != nil || state.Configuration != nil || state.CanConnect {
				t.Fatal("server restriction retained configuration")
			}
			assertStoredAbsent(t, engine.home)
		})
	}
}

func TestConfigurationColdResetRemovesCrashResidueWithoutLoadingIt(t *testing.T) {
	_, c, _ := newAccountFixture(t)
	engine := attachConfigurationStub(t, c)
	if _, err := StoreConfiguration(engine.home, ConfigurationOwner{7, "previous", "old"}, storedFixture("old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.home, "config.yaml"), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := c.Reset(context.Background())
	if err != nil || state.Configuration != nil || state.User != nil || engine.applied.Load() != 0 {
		t.Fatal("cold reset loaded a previous session")
	}
	assertStoredAbsent(t, engine.home)
	if data, _ := os.ReadFile(filepath.Join(engine.home, "config.yaml")); string(data) != "legacy" {
		t.Fatal("cold reset removed unrelated configuration")
	}
}
