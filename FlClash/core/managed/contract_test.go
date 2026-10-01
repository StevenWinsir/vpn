package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type contractCase struct {
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Status   int             `json:"status"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

func readContract(t *testing.T) map[string]contractCase {
	t.Helper()
	data, err := os.ReadFile("../../test/fixtures/native_client_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version int                     `json:"contract_version"`
		Cases   map[string]contractCase `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != 1 || len(fixture.Cases) != 8 {
		t.Fatal("unexpected contract version/cases")
	}
	return fixture.Cases
}

func fixtureStatus(t *testing.T, fixture contractCase) Status {
	t.Helper()
	data := fixture.Response
	if fixture.Path != "/session" {
		var envelope struct {
			Session json.RawMessage `json:"session"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatal(err)
		}
		data = envelope.Session
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected map[string]any
	if err = json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("Go status fields differ from shared JSON contract")
	}
	return status
}

func TestSharedNativeContract(t *testing.T) {
	cases := readContract(t)
	for _, name := range []string{"login", "free", "vip_unbound", "vip_ready", "traffic_ready", "quota_exhausted"} {
		t.Run(name, func(t *testing.T) {
			if !validStatus(fixtureStatus(t, cases[name])) {
				t.Fatal("shared fixture rejected")
			}
		})
	}
	var config struct {
		YAML    string `json:"yaml"`
		Version string `json:"version"`
		Session Status `json:"session"`
	}
	if err := json.Unmarshal(cases["vip_ready"].Response, &config); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(config.YAML))
	if hex.EncodeToString(digest[:]) != config.Version || config.Version != config.Session.ProfileVersion {
		t.Fatal("configuration version does not identify its exact bytes")
	}
	for _, name := range []string{"free", "vip_unbound", "traffic_ready", "quota_exhausted", "session_expired"} {
		t.Run("http_"+name, func(t *testing.T) {
			fixture := cases[name]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != fixture.Method || r.URL.Path != fixture.Path {
					t.Error("wrong native method or path")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(fixture.Status)
				_, _ = w.Write(fixture.Response)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, strings.Repeat("A", 43), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var got Status
			if fixture.Path == "/traffic" {
				var input Counters
				if err = json.Unmarshal(fixture.Request, &input); err != nil {
					t.Fatal(err)
				}
				got, err = client.Report(context.Background(), input)
			} else {
				got, err = client.Status(context.Background())
			}
			if fixture.Status != 200 {
				if errorCode(err) != "native_session_expired" {
					t.Fatal("expired session error not preserved")
				}
			} else if err != nil || !reflect.DeepEqual(got, fixtureStatus(t, fixture)) {
				t.Fatalf("HTTP contract mismatch: %v", err)
			}
		})
	}
}

func TestStatusRejectsMissingOrOverlongAuthorization(t *testing.T) {
	base := fixtureStatus(t, readContract(t)["vip_ready"])
	for _, tc := range []struct {
		name   string
		mutate func(*Status)
	}{
		{"missing_server_time", func(s *Status) { s.ServerTime = time.Time{} }},
		{"missing_idle_timeout", func(s *Status) { s.SessionIdleTimeout = 0 }},
		{"missing_subscription_expiry", func(s *Status) { s.SubscriptionExpiresAt = nil }},
		{"missing_authorization_expiry", func(s *Status) { s.AuthorizationExpiresAt = nil }},
		{"invalid_profile_version", func(s *Status) { s.ProfileVersion = "old" }},
		{"overlong_lease", func(s *Status) { v := s.ServerTime.Add(91 * time.Second); s.AuthorizationExpiresAt = &v }},
		{"expired_lease", func(s *Status) { s.AuthorizationExpiresAt = &s.ServerTime }},
		{"past_subscription", func(s *Status) { v := s.ServerTime.Add(time.Second); s.SubscriptionExpiresAt = &v }},
		{"past_session", func(s *Status) { s.ExpiresAt = s.ServerTime.Add(time.Second) }},
		{"denial_retains_lease", func(s *Status) { s.CanConnect = false; s.Reason = "profile_changed" }},
		{"contradictory_reason", func(s *Status) { s.Reason = "quota_exhausted" }},
		{"quota_over_total", func(s *Status) { s.RemainingBytes = s.TotalBytes + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := base
			tc.mutate(&status)
			if validStatus(status) {
				t.Fatal("malformed authorization accepted")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"session": base})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, strings.Repeat("A", 43), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Status(context.Background()); errorCode(err) != "invalid_server_response" {
		t.Fatal("wrapped /session response accepted")
	}
}

func TestAuthorizationDeadlineConsumesDelayAndClockSkew(t *testing.T) {
	status := fixtureStatus(t, readContract(t)["vip_ready"])
	limit := status.ServerTime.Add(15 * time.Second)
	status.SubscriptionExpiresAt, status.AuthorizationExpiresAt = &limit, &limit
	for _, offset := range []time.Duration{0, -time.Hour, time.Hour} {
		started := status.ServerTime.Add(offset)
		deadline := authorizationDeadline(status, started)
		if deadline.After(started.Add(15*time.Second)) || deadline.After(limit) {
			t.Fatal("clock skew extended authorization")
		}
		if started.Add(20 * time.Second).Before(deadline) {
			t.Fatal("slow response renewed authorization from its arrival")
		}
	}
}

func TestMeterStopsAtSubscriptionDeadline(t *testing.T) {
	f := newFixture(t)
	expiry := time.Now().Add(250 * time.Millisecond)
	f.mu.Lock()
	f.status.SubscriptionExpiresAt = &expiry
	f.mu.Unlock()
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.meter.mu.Lock()
	deadline := f.meter.deadline
	f.meter.mu.Unlock()
	if deadline.After(expiry) {
		t.Fatal("meter retained the fixed 90-second authorization")
	}
	before := f.stops.Load()
	time.Sleep(time.Until(expiry) + 20*time.Millisecond)
	if f.meter.sample() || f.meter.Status().CanConnect || f.stops.Load() <= before {
		t.Fatal("subscription expiry did not trigger the stop callback")
	}
}

func TestMeterPreservesSuccessfulSettlementDenial(t *testing.T) {
	f := newFixture(t)
	f.up.Store(100)
	f.mu.Lock()
	f.status.CanConnect = false
	f.status.Reason = "profile_changed"
	f.mu.Unlock()
	before := f.stops.Load()
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := f.meter.Status()
	if status.CanConnect || status.Reason != "profile_changed" || status.UploadBytes != 100 || f.stops.Load() <= before {
		t.Fatal("successful settlement hid its denial or failed to stop")
	}
}
