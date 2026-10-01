package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

type nativeP1Fixture struct {
	t       *testing.T
	db      *gorm.DB
	cfg     config.Config
	router  *gin.Engine
	profile string
}

func (f nativeP1Fixture) call(method, path, token string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/api/v1/client"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f nativeP1Fixture) want(w *httptest.ResponseRecorder, code int, reason string) {
	f.t.Helper()
	if w.Code != code || w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatalf("response status/cache: got %d want %d", w.Code, code)
	}
	if reason != "" {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || failure.Error.Code != reason {
			f.t.Fatalf("expected error code %s", reason)
		}
	}
}

func (f nativeP1Fixture) state(w *httptest.ResponseRecorder, reason string) nativeState {
	f.t.Helper()
	f.want(w, 200, "")
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		f.t.Fatal(err)
	}
	data := w.Body.Bytes()
	if wrapped, ok := fields["session"]; ok {
		data = wrapped
	}
	var state nativeState
	if err := json.Unmarshal(data, &state); err != nil {
		f.t.Fatal(err)
	}
	if state.Reason != reason || state.CanConnect != (reason == "") {
		f.t.Fatalf("authorization mismatch: %s, can_connect=%v", state.Reason, state.CanConnect)
	}
	if (state.AuthorizationExpiresAt != nil) != state.CanConnect {
		f.t.Fatal("authorization deadline/denial mismatch")
	}
	if state.ServerTime.IsZero() || state.SessionIdleTimeout != 180 || state.ReportInterval != 60 || state.LeaseSeconds != 90 {
		f.t.Fatal("lifetime contract mismatch")
	}
	return state
}

func (f nativeP1Fixture) user() model.User {
	f.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("Fixture-Only-Password!"), bcrypt.MinCost)
	if err != nil {
		f.t.Fatal(err)
	}
	u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "Fixture", Role: "user", Status: "active", PasswordHash: string(hash)}
	if err = f.db.Create(&u).Error; err != nil {
		f.t.Fatal(err)
	}
	return u
}

func (f nativeP1Fixture) paid() (model.User, model.Subscription) {
	f.t.Helper()
	u := f.user()
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 10000, MaxDevices: 2, AllowDedicated: true}
	if err := f.db.Create(&sub).Error; err != nil {
		f.t.Fatal(err)
	}
	return u, sub
}

func (f nativeP1Fixture) login(u model.User, reason string) string {
	f.t.Helper()
	w := f.call("POST", "/login", "", map[string]any{"email": u.Email, "password": "Fixture-Only-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "contract-test"})
	f.state(w, reason)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		f.t.Fatal(err)
	}
	if len(payload) != 3 || payload["user"] == nil || payload["session"] == nil || len(w.Result().Cookies()) != 0 {
		f.t.Fatal("login envelope changed")
	}
	var token string
	if err := json.Unmarshal(payload["token"], &token); err != nil || len(token) != 43 {
		f.t.Fatal("invalid native token contract")
	}
	return token
}

func (f nativeP1Fixture) change(sub model.Subscription, values map[string]any) {
	f.t.Helper()
	if err := f.db.Model(&sub).Updates(values).Error; err != nil {
		f.t.Fatal(err)
	}
}

func TestNativeP1Postgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	var profile struct {
		YAML string `json:"yaml"`
	}
	if err := json.Unmarshal(readNativeContract(t)["vip_ready"].Response, &profile); err != nil {
		t.Fatal(err)
	}
	controlled := startNativeLoopbackFixture(t, profile.YAML)
	profile.YAML = controlled.profile
	for _, plan := range []string{"pro", "starter"} {
		if err := os.WriteFile(filepath.Join(cfg.ClientProfileDir, plan+".yaml"), []byte(profile.YAML), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := nativeP1Fixture{db: db, cfg: cfg, router: New(db, cfg), profile: profile.YAML}
	t.Run("same_period_permission_changes_settle_but_never_authorize", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			values     map[string]any
			reason     string
			configCode int
		}{
			{"plan", map[string]any{"plan_id": "starter"}, "subscription_changed", 409},
			{"dedicated_downgrade", map[string]any{"allow_dedicated": false}, "subscription_changed", 409},
			{"device_limit", map[string]any{"max_devices": 1}, "subscription_changed", 409},
			{"test_source", map[string]any{"is_test": true}, "paid_vip_required", 403},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := base
				f.t = t
				u, sub := f.paid()
				token := f.login(u, "profile_required")
				f.state(f.call("GET", "/config", token, nil), "")
				f.change(sub, tc.values)
				f.state(f.call("GET", "/session", token, nil), tc.reason)
				body := map[string]int{"sequence": 1, "upload_bytes": 100, "download_bytes": 200}
				f.state(f.call("POST", "/traffic", token, body), tc.reason)
				f.state(f.call("POST", "/traffic", token, body), tc.reason)
				f.want(f.call("GET", "/config", token, nil), tc.configCode, tc.reason)
				var saved model.Subscription
				if err := db.First(&saved, "id = ?", sub.ID).Error; err != nil {
					t.Fatal(err)
				}
				if saved.UsedUnits != 300000 || !saved.StartsAt.Equal(sub.StartsAt) {
					t.Fatal("tail settlement lost, double charged, or changed period")
				}
				if tc.name != "test_source" {
					fresh := f.login(u, "profile_required")
					f.state(f.call("GET", "/config", fresh, nil), "")
				}
			})
		}
	})
	t.Run("reduced_device_limit_serializes_new_bindings", func(t *testing.T) {
		f := base
		f.t = t
		u, sub := f.paid()
		old := []string{f.login(u, "profile_required"), f.login(u, "profile_required")}
		for _, token := range old {
			f.state(f.call("GET", "/config", token, nil), "")
		}
		f.change(sub, map[string]any{"max_devices": 1})
		for _, token := range old {
			f.state(f.call("GET", "/session", token, nil), "subscription_changed")
		}
		fresh := []string{f.login(u, "profile_required"), f.login(u, "profile_required")}
		var wg sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, len(fresh))
		for _, token := range fresh {
			wg.Add(1)
			go func(token string) { defer wg.Done(); results <- f.call("GET", "/config", token, nil) }(token)
		}
		wg.Wait()
		close(results)
		allowed := 0
		for response := range results {
			if response.Code == 200 {
				allowed++
				f.state(response, "")
			} else {
				f.want(response, 403, "device_limit")
			}
		}
		if allowed != 1 {
			t.Fatalf("new device allocation count: %d", allowed)
		}
	})
	t.Run("profile_rotation_withdrawal_and_manual_rebind_keep_counters", func(t *testing.T) {
		f := base
		f.t = t
		u, _ := f.paid()
		token := f.login(u, "profile_required")
		first := f.state(f.call("GET", "/config", token, nil), "")
		f.state(f.call("POST", "/traffic", token, map[string]int{"sequence": 1, "upload_bytes": 100, "download_bytes": 200}), "")
		path := filepath.Join(cfg.ClientProfileDir, "pro.yaml")
		rotated := []byte(f.profile + "# local fixture revision 2\n")
		if err := os.WriteFile(path, rotated, 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.WriteFile(path, []byte(f.profile), 0600); err != nil {
				t.Error(err)
			}
		})
		f.state(f.call("GET", "/session", token, nil), "profile_changed")
		f.state(f.call("POST", "/traffic", token, map[string]int{"sequence": 2, "upload_bytes": 150, "download_bytes": 250}), "profile_changed")
		next := f.state(f.call("GET", "/config", token, nil), "")
		if next.ProfileVersion == first.ProfileVersion || next.LastSequence != 2 || next.UploadBytes != 150 || next.DownloadBytes != 250 {
			t.Fatal("sync did not change version or reset cumulative accounting")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		f.state(f.call("GET", "/session", token, nil), "client_config_unavailable")
		f.state(f.call("POST", "/traffic", token, map[string]int{"sequence": 3, "upload_bytes": 150, "download_bytes": 250}), "client_config_unavailable")
		f.want(f.call("GET", "/config", token, nil), 503, "client_config_unavailable")
	})
	t.Run("same_plan_renewal_preserves_binding_and_expiry_is_clamped", func(t *testing.T) {
		f := base
		f.t = t
		u, sub := f.paid()
		token := f.login(u, "profile_required")
		f.state(f.call("GET", "/config", token, nil), "")
		f.change(sub, map[string]any{"traffic_limit_bytes": 20000, "expires_at": time.Now().Add(2 * time.Hour)})
		state := f.state(f.call("GET", "/session", token, nil), "")
		if state.TotalBytes != 20000 {
			t.Fatal("renewal not reflected")
		}
		soon := time.Now().UTC().Add(20 * time.Second).Truncate(time.Microsecond)
		f.change(sub, map[string]any{"expires_at": soon})
		state = f.state(f.call("POST", "/traffic", token, map[string]int{"sequence": 1, "upload_bytes": 1, "download_bytes": 2}), "")
		if !state.AuthorizationExpiresAt.Equal(soon) {
			t.Fatal("lease exceeded subscription expiry")
		}
		earlier := time.Now().UTC().Add(10 * time.Second).Truncate(time.Microsecond)
		if err := db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Update("expires_at", earlier).Error; err != nil {
			t.Fatal(err)
		}
		state = f.state(f.call("GET", "/session", token, nil), "")
		if !state.AuthorizationExpiresAt.Equal(earlier) {
			t.Fatal("lease exceeded session expiry")
		}
	})
	t.Run("unbound_keepalive_and_legacy_binding_fail_closed", func(t *testing.T) {
		f := base
		f.t = t
		for _, paid := range []bool{false, true} {
			u := f.user()
			reason := "upgrade_required"
			if paid {
				u, _ = f.paid()
				reason = "profile_required"
			}
			token := f.login(u, reason)
			for i := 0; i < 2; i++ {
				if err := db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Update("last_seen_at", time.Now().Add(-120*time.Second)).Error; err != nil {
					t.Fatal(err)
				}
				state := f.state(f.call("GET", "/session", token, nil), reason)
				if state.LastSequence != 0 {
					t.Fatal("idle page wrote a traffic report")
				}
				var saved model.NativeSession
				if err := db.First(&saved, "id = ?", state.SessionID).Error; err != nil {
					t.Fatal(err)
				}
				if time.Since(saved.LastSeenAt) > 5*time.Second {
					t.Fatal("session keepalive did not advance last seen")
				}
			}
			if err := db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Update("last_seen_at", time.Now().Add(-180*time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			f.want(f.call("GET", "/session", token, nil), 401, "native_session_expired")
		}
		u, _ := f.paid()
		token := f.login(u, "profile_required")
		f.state(f.call("GET", "/config", token, nil), "")
		if err := db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Updates(map[string]any{"entitlement_fingerprint": "", "profile_version": ""}).Error; err != nil {
			t.Fatal(err)
		}
		f.state(f.call("GET", "/session", token, nil), "subscription_changed")
		f.want(f.call("GET", "/config", token, nil), 409, "subscription_changed")
	})
}
