package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// TestTrafficTimingBaseline measures POST /traffic latency through the full handler
// (presence plan 12, round 2 acceptance: within 20% of round 1). Opt-in with
// TRAFFIC_TIMING_REPORTS=<n>; it uses only APIs that already existed in round 1, so the
// same file runs against the previous tree for the baseline.
func TestTrafficTimingBaseline(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("TRAFFIC_TIMING_REPORTS"))
	if n <= 0 {
		t.Skip("set TRAFFIC_TIMING_REPORTS to measure /traffic latency")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog, cfg.ClientProfileDir = true, ""
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	router := New(db, cfg)
	node := model.Node{ID: uuid.NewString(), Name: "Timing", Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, Version: 1}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	sealed, _ := nodes.Encrypt(cfg.NodeEncryptionKey, node.ID, []byte("proxies:\n  - {name: Timing, type: ss, server: 127.0.0.1, port: 18443, cipher: aes-256-gcm, password: timing}\n"))
	if err := db.Create(&model.NodeConfig{NodeID: node.ID, Ciphertext: sealed, PlanIDs: "[]"}).Error; err != nil {
		t.Fatal(err)
	}
	adoptServer(t, db, node)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Timing-Test-Password!"), 10)
	user := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "timing", PasswordHash: string(hash), Role: "user", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&model.Subscription{ID: uuid.NewString(), UserID: user.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), TrafficLimitBytes: 1 << 40, MaxDevices: 2}).Error; err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1/client"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	var login struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(call("POST", "/login", "", map[string]string{"email": user.Email, "password": "Timing-Test-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "1.0.0"}).Body.Bytes(), &login)
	call("GET", "/config?node_id="+node.ID, login.Token, nil)
	durations := make([]time.Duration, 0, n)
	for i := 1; i <= n+20; i++ {
		start := time.Now()
		call("POST", "/traffic", login.Token, map[string]int64{"sequence": int64(i), "upload_bytes": int64(i) * 1000, "download_bytes": int64(i) * 3000})
		if i > 20 { // warm-up excluded
			durations = append(durations, time.Since(start))
		}
	}
	slices.Sort(durations)
	p50, p95 := durations[len(durations)/2], durations[len(durations)*95/100]
	t.Logf("POST /traffic over %d reports: p50 %v p95 %v", n, p50, p95)
	if out := os.Getenv("TRAFFIC_TIMING_OUT"); out != "" {
		data, _ := json.Marshal(map[string]any{"reports": n, "p50_us": p50.Microseconds(), "p95_us": p95.Microseconds()})
		if err := os.WriteFile(out, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
