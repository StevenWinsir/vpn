package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

type nativeContractCase struct {
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Status   int             `json:"status"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

func readNativeContract(t *testing.T) map[string]nativeContractCase {
	t.Helper()
	data, err := os.ReadFile("../../../FlClash/test/fixtures/native_client_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version int                           `json:"contract_version"`
		Cases   map[string]nativeContractCase `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != 1 || len(fixture.Cases) != 8 {
		t.Fatal("unexpected native contract version/cases")
	}
	return fixture.Cases
}

func contractState(t *testing.T, fixture nativeContractCase) nativeState {
	t.Helper()
	data := fixture.Response
	if fixture.Path != "/session" {
		var response struct {
			Session json.RawMessage `json:"session"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		data = response.Session
	}
	var state nativeState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestNativeContractSnapshots(t *testing.T) {
	cases := readNativeContract(t)
	var profile struct {
		YAML    string `json:"yaml"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(cases["vip_ready"].Response, &profile); err != nil {
		t.Fatal(err)
	}
	if nativeProfileVersion([]byte(profile.YAML)) != profile.Version {
		t.Fatal("fixture YAML digest mismatch")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pro.yaml"), []byte(profile.YAML), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{ClientProfileDir: dir}}
	for _, name := range []string{"login", "free", "vip_unbound", "vip_ready", "traffic_ready", "quota_exhausted"} {
		t.Run(name, func(t *testing.T) {
			want := contractState(t, cases[name])
			sess := model.NativeSession{ID: want.SessionID, ExpiresAt: want.ExpiresAt, LastSeenAt: want.ServerTime, LastSequence: want.LastSequence, UploadBytes: want.UploadBytes, DownloadBytes: want.DownloadBytes, ProfileVersion: want.ProfileVersion}
			var sub *model.Subscription
			if want.SubscriptionExpiresAt != nil {
				sub = &model.Subscription{ID: "contract-subscription", PlanID: "pro", PlanName: want.PlanName, StartsAt: want.ServerTime.Add(-time.Hour), ExpiresAt: *want.SubscriptionExpiresAt, TrafficLimitBytes: want.TotalBytes, UsedUnits: want.UsedUnits, MaxDevices: 2}
				if want.ProfileVersion != "" {
					sess.SubscriptionID, sess.SubscriptionStartsAt = &sub.ID, &sub.StartsAt
					sess.EntitlementFingerprint = nativeEntitlementFingerprint(sub)
				}
			}
			got := s.nativeSnapshot(sess, sub, want.ServerTime)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot differs from shared contract: got %+v want %+v", got, want)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected map[string]any
			if err = json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(cases[name].Response, &expected); err != nil {
				t.Fatal(err)
			}
			if cases[name].Path != "/session" {
				expected = expected["session"].(map[string]any)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("state fields drifted from the shared JSON fixture")
			}
		})
	}
}

func TestNativeAuthorizationDeadlineBoundaries(t *testing.T) {
	want := contractState(t, readNativeContract(t)["vip_ready"])
	dir := t.TempDir()
	data := []byte("proxies: []\n")
	if err := os.WriteFile(filepath.Join(dir, "pro.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{ClientProfileDir: dir}}
	for _, tc := range []struct {
		name                  string
		session, subscription time.Duration
		used                  int64
		reason                string
		allowed               time.Duration
	}{
		{"lease", time.Hour, time.Hour, 0, "", 90 * time.Second},
		{"subscription_first", time.Hour, 15 * time.Second, 0, "", 15 * time.Second},
		{"session_first", 10 * time.Second, time.Hour, 0, "", 10 * time.Second},
		{"subscription_exact_expiry", time.Hour, 0, 0, "subscription_expired", 0},
		{"session_exact_expiry", 0, time.Hour, 0, "native_session_expired", 0},
		{"fractional_byte_remaining", time.Hour, time.Hour, 999999, "quota_exhausted", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := want.ServerTime
			sub := &model.Subscription{ID: "sub", PlanID: "pro", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(tc.subscription), TrafficLimitBytes: 1000, UsedUnits: tc.used, MaxDevices: 2}
			sess := model.NativeSession{ID: want.SessionID, ExpiresAt: now.Add(tc.session), SubscriptionID: &sub.ID, SubscriptionStartsAt: &sub.StartsAt, EntitlementFingerprint: nativeEntitlementFingerprint(sub), ProfileVersion: nativeProfileVersion(data)}
			state := s.nativeSnapshot(sess, sub, now)
			if state.Reason != tc.reason || state.CanConnect != (tc.reason == "") {
				t.Fatalf("unexpected authorization: %+v", state)
			}
			if tc.reason == "" {
				if state.AuthorizationExpiresAt == nil || !state.AuthorizationExpiresAt.Equal(now.Add(tc.allowed)) {
					t.Fatal("authorization exceeded its effective lifetime")
				}
			} else if state.AuthorizationExpiresAt != nil {
				t.Fatal("denied state retained a lease")
			}
		})
	}
}
