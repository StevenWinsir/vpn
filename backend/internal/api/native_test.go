package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/store"
)

func TestNativeProfileFiles(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: config.Config{ClientProfileDir: dir}}
	if _, err := s.readNativeProfile("../secret"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := s.readNativeProfile("missing"); err == nil {
		t.Fatal("missing file accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readNativeProfile("escape"); err == nil {
		t.Fatal("symlink escape accepted")
	}
	for name, value := range map[string][]byte{"empty": {}, "nul": {0}, "huge": bytes.Repeat([]byte("x"), maxNativeProfileBytes+1)} {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), value, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.readNativeProfile(name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "pro.yaml"), []byte("proxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := s.readNativeProfile("pro"); err != nil || string(data) != "proxies: []\n" {
		t.Fatal("valid file rejected")
	}
}

func newIsolatedNativeDatabase(t *testing.T) (*gorm.DB, config.Config) {
	t.Helper()
	dsn := os.Getenv("NATIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("run scripts/test-native.py for an isolated PostgreSQL instance")
	}
	if !strings.HasPrefix(dsn, "host=/") {
		t.Fatal("native integration tests require an explicitly supplied private Unix socket")
	}
	cfg := config.Config{Env: "test", Schema: "native_" + strings.ReplaceAll(uuid.NewString(), "-", ""), BcryptCost: 10, APIRate: 100000, AuthRate: 10000, RequestTimeout: time.Minute, MigrationTimeout: time.Minute, SessionTTL: 12 * time.Hour, AccessTTL: 15 * time.Minute, RememberTTL: 30 * 24 * time.Hour, Origins: []string{"https://test.hyshentou.cn"}, JWTSecret: strings.Repeat("t", 64), Issuer: "native-test", Audience: "native-test", ClientProfileDir: t.TempDir()}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: cfg.Schema + "."}})
	if err != nil {
		t.Fatal("isolated database connection failed")
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = store.Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(db, cfg); err != nil {
		t.Fatalf("migration is not repeatable: %v", err)
	}
	return db, cfg
}

func TestNativePostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	var err error
	profile := "proxies:\n  - name: Fixture\n    type: ss\n    server: 127.0.0.1\n    port: 18443\n    cipher: aes-128-gcm\n    password: test-only\nproxy-groups:\n  - name: VIP\n    type: select\n    proxies: [Fixture]\nrules: ['MATCH,VIP']\n"
	if err = os.WriteFile(filepath.Join(cfg.ClientProfileDir, "pro.yaml"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	router := New(db, cfg)
	call := func(r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/v1/client"+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	want := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("native response was cacheable")
		}
	}
	newUser := func(name string) model.User {
		hash, _ := bcrypt.GenerateFromPassword([]byte("Native-Test-Password!"), 10)
		u := model.User{ID: uuid.NewString(), Email: name + "@example.invalid", Name: name, PasswordHash: string(hash), Role: "user", Status: "active"}
		if e := db.Create(&u).Error; e != nil {
			t.Fatal(e)
		}
		return u
	}
	login := func(u model.User, device string) string {
		w := call(router, "POST", "/login", "", map[string]any{"email": u.Email, "password": "Native-Test-Password!", "device_id": device, "platform": "macos", "app_version": "test"})
		want(w, 200)
		var data struct {
			Token string `json:"token"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		if len(data.Token) != 43 || len(w.Result().Cookies()) != 0 {
			t.Fatal("native login token/cookie contract broken")
		}
		return data.Token
	}
	subscribe := func(u model.User, quota int64, test bool) model.Subscription {
		now := time.Now().UTC().Truncate(time.Microsecond)
		sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: quota, MaxDevices: 2, IsTest: test}
		if e := db.Create(&sub).Error; e != nil {
			t.Fatal(e)
		}
		return sub
	}
	t.Run("authentication_free_and_test_users", func(t *testing.T) {
		want(call(router, "GET", "/session", "", nil), 401)
		u := newUser("free")
		token := login(u, uuid.NewString())
		want(call(router, "GET", "/config", token, nil), 403)
		subscribe(u, 1000, true)
		want(call(router, "GET", "/config", token, nil), 403)
		devCfg := cfg
		devCfg.TestPurchase = true
		devCfg.ClientAllowTestEntitlements = true
		want(call(New(db, devCfg), "GET", "/config", token, nil), 200)
		want(call(router, "POST", "/traffic", token, map[string]int{"sequence": 1, "upload_bytes": 1, "download_bytes": 1}), 200)
		w := call(router, "GET", "/session", token, nil)
		want(w, 200)
		if !strings.Contains(w.Body.String(), "paid_vip_required") {
			t.Fatal("test entitlement silently authorized")
		}
		want(call(router, "POST", "/login", "", map[string]any{"email": u.Email, "password": "wrong", "device_id": uuid.NewString(), "platform": "macos"}), 401)
	})
	t.Run("paid_profile_sessions_and_accounting", func(t *testing.T) {
		u := newUser("vip")
		sub := subscribe(u, 1000, false)
		dev := uuid.NewString()
		token := login(u, dev)
		w := call(router, "GET", "/config", token, nil)
		want(w, 200)
		if !strings.Contains(w.Body.String(), "test-only") {
			t.Fatal("profile not returned")
		}
		var stored model.NativeSession
		if e := db.Where("token_hash = ?", refreshHash(token)).First(&stored).Error; e != nil {
			t.Fatal(e)
		}
		if stored.TokenHash == token || stored.SubscriptionID == nil {
			t.Fatal("session storage not hashed/bound")
		}
		token2 := login(u, uuid.NewString())
		want(call(router, "GET", "/config", token2, nil), 200)
		token3 := login(u, uuid.NewString())
		want(call(router, "GET", "/config", token3, nil), 403)
		body := map[string]int64{"sequence": 1, "upload_bytes": 100, "download_bytes": 200}
		var wg sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); results <- call(router, "POST", "/traffic", token, body) }()
		}
		wg.Wait()
		close(results)
		for result := range results {
			want(result, 200)
		}
		if e := db.First(&sub, "id = ?", sub.ID).Error; e != nil {
			t.Fatal(e)
		}
		if sub.UsedUnits != 300000 || sub.UploadBytes != 100 || sub.DownloadBytes != 200 {
			t.Fatalf("duplicate charged: %+v", sub)
		}
		var ledgerCount int64
		db.Model(&model.ClientTrafficReport{}).Where("session_id = ?", stored.ID).Count(&ledgerCount)
		if ledgerCount != 1 {
			t.Fatalf("ledger rows %d", ledgerCount)
		}
		want(call(router, "POST", "/traffic", token, map[string]int{"sequence": 1, "upload_bytes": 101, "download_bytes": 200}), 409)
		want(call(router, "POST", "/traffic", token, map[string]int{"sequence": 3, "upload_bytes": 200, "download_bytes": 200}), 409)
		want(call(router, "POST", "/traffic", token, map[string]int{"sequence": 2, "upload_bytes": 99, "download_bytes": 200}), 409)
		want(call(router, "POST", "/traffic", token, map[string]int{"sequence": 2, "upload_bytes": -1, "download_bytes": 200}), 400)
		want(call(router, "POST", "/traffic", token, map[string]any{"sequence": 2, "upload_bytes": 100, "download_bytes": 200, "rate_permille": 0}), 400)
		want(call(router, "POST", "/traffic", token2, map[string]int{"sequence": 1, "upload_bytes": 200, "download_bytes": 600}), 200)
		w = call(router, "POST", "/traffic", token, body)
		want(w, 200)
		if !strings.Contains(w.Body.String(), `"remaining_bytes":0`) || !strings.Contains(w.Body.String(), `"can_connect":false`) {
			t.Fatalf("replay returned stale allowance: %s", w.Body.String())
		}
		want(call(router, "GET", "/config", token, nil), 403)
		want(call(router, "POST", "/logout", token2, map[string]any{}), 200)
		want(call(router, "GET", "/session", token2, nil), 401)
		replacement := login(u, dev)
		want(call(router, "GET", "/session", token, nil), 401)
		want(call(router, "GET", "/session", replacement, nil), 200)
	})
	t.Run("renewal_epoch_missing_config_and_expiry", func(t *testing.T) {
		u := newUser("renew")
		sub := subscribe(u, 10000, false)
		token := login(u, uuid.NewString())
		want(call(router, "GET", "/config", token, nil), 200)
		if e := db.Model(&sub).Update("starts_at", time.Now().Add(-time.Minute)).Error; e != nil {
			t.Fatal(e)
		}
		want(call(router, "POST", "/traffic", token, map[string]int{"sequence": 1, "upload_bytes": 100, "download_bytes": 0}), 409)
		want(call(router, "GET", "/config", token, nil), 409)
		if e := db.Model(&sub).Update("max_devices", 1).Error; e != nil {
			t.Fatal(e)
		}
		replacement := login(u, uuid.NewString())
		want(call(router, "GET", "/config", replacement, nil), 200)
		if e := db.Model(&sub).Update("plan_id", "starter").Error; e != nil {
			t.Fatal(e)
		}
		token = login(u, uuid.NewString())
		want(call(router, "GET", "/config", token, nil), 503)
		if e := db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Update("last_seen_at", time.Now().Add(-4*time.Minute)).Error; e != nil {
			t.Fatal(e)
		}
		want(call(router, "GET", "/session", token, nil), 401)
		token = login(u, uuid.NewString())
		if e := db.Model(&model.NativeSession{}).Where("token_hash = ?", refreshHash(token)).Update("expires_at", time.Now().Add(-time.Second)).Error; e != nil {
			t.Fatal(e)
		}
		want(call(router, "GET", "/session", token, nil), 401)
	})
	t.Run("cross_user_scope_and_revocation", func(t *testing.T) {
		u := newUser("scope")
		subscribe(u, 1000, false)
		token := login(u, uuid.NewString())
		want(call(router, "GET", "/config", token, nil), 200)
		want(call(router, "POST", "/traffic", token, map[string]any{"sequence": 1, "upload_bytes": 1, "download_bytes": 1, "user_id": uuid.NewString()}), 400)
		if e := db.Model(&u).Update("status", "disabled").Error; e != nil {
			t.Fatal(e)
		}
		want(call(router, "GET", "/config", token, nil), 401)
	})
	t.Log(fmt.Sprintf("isolated schema %s; no production credentials loaded", cfg.Schema))
}
