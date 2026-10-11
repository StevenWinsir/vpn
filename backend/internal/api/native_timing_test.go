package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
)

// The protocol timings are operator-editable (CLIENT_*_SECONDS). The server must report
// the effective values so clients never hard-code them, and must enforce the configured
// idle window rather than the old fixed three minutes.
func TestNativeConfiguredTimings(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientReportInterval, cfg.ClientLease, cfg.ClientIdleTimeout, cfg.ClientNodeWatch = 20*time.Second, 40*time.Second, 70*time.Second, 2*time.Second
	router := New(db, cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Native-Test-Password!"), 10)
	user := model.User{ID: uuid.NewString(), Email: "timing@example.invalid", Name: "timing", PasswordHash: string(hash), Role: "user", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"email": user.Email, "password": "Native-Test-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "test"})
	request := httptest.NewRequest("POST", "/api/v1/client/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("login %d: %s", recorder.Code, recorder.Body.String())
	}
	var login struct {
		Token   string      `json:"token"`
		Session nativeState `json:"session"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.Session.ReportInterval != 20 || login.Session.LeaseSeconds != 40 || login.Session.SessionIdleTimeout != 70 {
		t.Fatalf("effective timings not reported: %+v", login.Session)
	}
	status := func() int {
		r := httptest.NewRequest("GET", "/api/v1/client/session", nil)
		r.Header.Set("Authorization", "Bearer "+login.Token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	if code := status(); code != 200 {
		t.Fatalf("fresh session rejected: %d", code)
	}
	// 80s idle is beyond the configured 70s but inside the legacy 180s: it must now expire.
	if err := db.Model(&model.NativeSession{}).Where("id = ?", login.Session.SessionID).Update("last_seen_at", time.Now().UTC().Add(-80*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if code := status(); code != 401 {
		t.Fatalf("configured idle timeout not enforced: %d", code)
	}
}
