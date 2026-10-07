package managed

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type recoverableNetworkStub struct {
	*configurationStub
	denied atomic.Bool
	code   string
}

func (s *recoverableNetworkStub) Start(context.Context, ConfigurationOwner, string, int) error {
	if s.denied.Load() {
		return &APIError{s.code}
	}
	return nil
}
func (s *recoverableNetworkStub) Drain(context.Context) error { return nil }

func TestTUNFailureSurvivesSettledDisconnectAndClearsAfterSuccessfulRetry(t *testing.T) {
	// Non-TUN start failures were previously erased by the settling refresh, so
	// the app flashed "connected" and then stopped without any visible reason.
	for _, code := range []string{"managed_tun_permission_required", "managed_tun_route_conflict", "managed_tun_route_check_failed", "runtime_start_failed", "request_failed", "managed_connection_required"} {
		t.Run(code, func(t *testing.T) { testRecoverableTUNFailure(t, code) })
	}
}

func testRecoverableTUNFailure(t *testing.T, code string) {
	t.Helper()
	fixture, c, _ := newAccountFixture(t)
	fixture.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/traffic" {
			return false
		}
		var counters Counters
		if json.NewDecoder(r.Body).Decode(&counters) != nil {
			t.Error("invalid traffic report")
			w.WriteHeader(400)
			return true
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		status, ok := fixture.sessions[token]
		if !ok {
			t.Error("unauthenticated report")
			w.WriteHeader(401)
			return true
		}
		status = fixture.fresh(status)
		status.LastSequence = counters.Sequence
		status.UploadBytes, status.DownloadBytes = counters.UploadBytes, counters.DownloadBytes
		fixture.sessions[token] = status
		fixture.reports.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]Status{"session": status})
		return true
	}
	engine := &recoverableNetworkStub{configurationStub: attachConfigurationStub(t, c), code: code}
	engine.denied.Store(true)
	c.configuration = engine
	c.sampler = func(context.Context) (int64, int64, error) { return 0, 0, nil }
	state := mustAccountLogin(t, c)
	state, err := c.LoadConfig(context.Background(), state.Generation)
	if err != nil || state.Configuration == nil {
		t.Fatal("fixture configuration not applied")
	}
	state, err = c.Connect(context.Background(), state.Generation, 17890)
	if err != nil || state.CanConnect || state.ErrorCode != code {
		t.Fatalf("TUN refusal not exposed: %v / %s", err, state.ErrorCode)
	}
	state, err = c.Disconnect(context.Background(), state.Generation)
	if err != nil || state.CanConnect || state.ErrorCode != code {
		t.Fatalf("settling cleanup hid the TUN error: %v / %s", err, state.ErrorCode)
	}
	state, err = c.Refresh(context.Background(), state.Generation)
	if err != nil || state.ErrorCode != code {
		t.Fatal("meter heartbeat hid the TUN error")
	}
	engine.denied.Store(false)
	state, err = c.Connect(context.Background(), state.Generation, 17890)
	if err != nil || !state.CanConnect || state.ErrorCode != "" || fixture.reports.Load() < 3 {
		t.Fatalf("successful explicit retry did not clear TUN error behind billing gate: %v / %s", err, state.ErrorCode)
	}
}

func TestTUNFailureSurvivesAccountRefreshUntilAuthorizationChanges(t *testing.T) {
	for _, code := range []string{"managed_tun_permission_required", "managed_tun_start_failed", "managed_tun_cleanup_failed", "managed_tun_route_conflict", "managed_tun_route_check_failed"} {
		t.Run(code, func(t *testing.T) {
			fixture, c, _ := newAccountFixture(t)
			attachConfigurationStub(t, c)
			state := mustAccountLogin(t, c)
			state, err := c.LoadConfig(context.Background(), state.Generation)
			if err != nil || state.Configuration == nil {
				t.Fatal("fixture configuration not applied")
			}
			c.mu.Lock()
			c.snapshot.ErrorCode = code
			c.mu.Unlock()
			state, err = c.Refresh(context.Background(), state.Generation)
			if err != nil || state.ErrorCode != code || state.CanConnect || state.Configuration == nil {
				t.Fatalf("healthy account response concealed TUN failure: %v / %s", err, state.ErrorCode)
			}
			state, err = c.Disconnect(context.Background(), state.Generation)
			if err != nil || state.ErrorCode != code {
				t.Fatal("disconnect concealed TUN failure")
			}
			fixture.mu.Lock()
			fixture.reason = "quota_exhausted"
			fixture.mu.Unlock()
			state, err = c.Refresh(context.Background(), state.Generation)
			if err != nil || state.ErrorCode != "quota_exhausted" || state.Configuration != nil {
				t.Fatal("TUN diagnostic overrode a revoked entitlement")
			}
		})
	}
}
