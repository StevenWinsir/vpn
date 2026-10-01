package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/store"
)

func TestPostgresIntegration(t *testing.T) {
	var cfg config.Config
	var db *gorm.DB
	var err error
	if os.Getenv("NATIVE_TEST_DSN") != "" {
		db, cfg = newIsolatedNativeDatabase(t)
	} else {
		if os.Getenv("RUN_DB_TESTS") != "1" {
			t.Skip("run scripts/test-native.py; real database writes require RUN_DB_TESTS=1")
		}
		_ = godotenv.Load("../../.env")
		cfg, err = config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Env != "development" {
			t.Fatal("integration writes are restricted to development")
		}
		db, err = store.Open(cfg)
		if err != nil {
			t.Fatal("configured PostgreSQL connection failed")
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	cfg.AuthRate = 1000
	cfg.APIRate = 1000
	cfg.BcryptCost = 10
	cfg.TestPurchase = true
	if !db.Migrator().HasTable(&model.User{}) {
		if err = store.Migrate(db, cfg); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(db, cfg)
	var userIDs, nodeIDs []string
	t.Cleanup(func() {
		err := db.Transaction(func(tx *gorm.DB) error {
			if len(nodeIDs) > 0 {
				if e := tx.Where("id IN ?", nodeIDs).Delete(&model.Node{}).Error; e != nil {
					return e
				}
			}
			for _, id := range userIDs {
				for _, m := range []any{&model.Order{}, &model.Subscription{}, &model.Session{}} {
					if e := tx.Where("user_id = ?", id).Delete(m).Error; e != nil {
						return e
					}
				}
				if e := tx.Where("id = ?", id).Delete(&model.User{}).Error; e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})
	call := func(h http.Handler, method, path, body string, cookies []*http.Cookie, key, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	request := func(method, path, body string, cookies []*http.Cookie, key string) *httptest.ResponseRecorder {
		return call(handler, method, path, body, cookies, key, cfg.Origins[0])
	}
	want := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("expected status %d, got %d; body=%s", status, w.Code, w.Body.String())
		}
	}
	register := func() ([]*http.Cookie, model.User, string) {
		email := "qa-" + uuid.NewString() + "@example.invalid"
		body := fmt.Sprintf(`{"name":"集成测试","email":%q,"password":"Test-Only-Password-2026!","remember":true}`, email)
		w := request("POST", "/api/v1/auth/register", body, nil, "")
		want(w, 201)
		var result struct {
			User model.User `json:"user"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		userIDs = append(userIDs, result.User.ID)
		if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "refresh_hash") {
			t.Fatal("secret exposed in user JSON")
		}
		return w.Result().Cookies(), result.User, body
	}
	cookies, user, registration := register()
	t.Run("registration_and_jwt", func(t *testing.T) {
		want(request("POST", "/api/v1/auth/register", registration, nil, ""), 409)
		want(request("POST", "/api/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"wrong-password"}`, user.Email), nil, ""), 401)
		want(request("GET", "/api/v1/auth/me", "", nil, ""), 401)
		want(request("GET", "/api/v1/auth/me", "", cookies, ""), 200)
		want(request("GET", "/api/v1/auth/me", "", []*http.Cookie{{Name: "vpn_access", Value: "forged.jwt.value"}}, ""), 401)
		for _, c := range cookies {
			if !c.HttpOnly || c.Path != "/api/v1" || c.SameSite != http.SameSiteLaxMode || c.MaxAge <= 0 {
				t.Fatalf("unsafe remember cookie settings: %s", c.Name)
			}
		}
		want(request("POST", "/api/v1/auth/register", `{"name":"无效用户","email":"invalid@example.invalid","password":"Test-Only-Password!","role":"admin"}`, nil, ""), 400)
	})
	t.Run("csrf_and_cors", func(t *testing.T) {
		want(call(handler, "POST", "/api/v1/auth/logout", "{}", cookies, "", "https://evil.example"), 403)
		want(call(handler, "POST", "/api/v1/auth/logout", "{}", cookies, "", ""), 403)
		w := call(handler, "OPTIONS", "/api/v1/auth/login", "", nil, "", cfg.Origins[0])
		want(w, 204)
		if w.Header().Get("Access-Control-Allow-Origin") != cfg.Origins[0] {
			t.Fatal("CORS origin not exact")
		}
	})
	t.Run("plans_and_purchase_validation", func(t *testing.T) {
		want(request("GET", "/api/v1/plans", "", nil, ""), 200)
		want(request("GET", "/api/v1/client/bootstrap", "", cookies, ""), 403)
		want(request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter"}`, cookies, ""), 400)
		want(request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"missing"}`, cookies, uuid.NewString()), 404)
		want(request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter","price_cents":1}`, cookies, uuid.NewString()), 400)
	})
	var firstSub model.Subscription
	t.Run("idempotent_purchase", func(t *testing.T) {
		key := uuid.NewString()
		first := request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter"}`, cookies, key)
		want(first, 201)
		var one struct {
			Order model.Order `json:"order"`
		}
		_ = json.Unmarshal(first.Body.Bytes(), &one)
		second := request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter"}`, cookies, key)
		want(second, 200)
		var two struct {
			Order    model.Order `json:"order"`
			Replayed bool        `json:"replayed"`
		}
		_ = json.Unmarshal(second.Body.Bytes(), &two)
		if one.Order.ID != two.Order.ID || !two.Replayed || one.Order.PriceCents != 1990 {
			t.Fatal("idempotency or server price snapshot failed")
		}
		want(request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"pro"}`, cookies, key), 409)
		want(request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"pro"}`, cookies, uuid.NewString()), 409)
		if err := db.Where("user_id = ?", user.ID).First(&firstSub).Error; err != nil {
			t.Fatal(err)
		}
		if firstSub.TrafficLimitBytes != 100*(1<<30) {
			t.Fatal("replay changed traffic quota")
		}
	})
	t.Run("concurrent_same_key", func(t *testing.T) {
		key := uuid.NewString()
		var wg sync.WaitGroup
		results := make(chan int, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter"}`, cookies, key)
				results <- w.Code
			}()
		}
		wg.Wait()
		close(results)
		created := 0
		for code := range results {
			if code == 201 {
				created++
			} else if code != 200 {
				t.Fatalf("concurrent purchase returned %d", code)
			}
		}
		if created != 1 {
			t.Fatalf("expected one creation, got %d", created)
		}
		var sub model.Subscription
		if db.Where("user_id = ?", user.ID).First(&sub).Error != nil {
			t.Fatal("missing subscription")
		}
		if sub.TrafficLimitBytes != 2*firstSub.TrafficLimitBytes || !sub.ExpiresAt.Equal(firstSub.ExpiresAt.AddDate(0, 0, 30)) {
			t.Fatal("concurrent replay double charged")
		}
	})
	t.Run("concurrent_distinct_keys", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- request("POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter"}`, cookies, uuid.NewString()).Code
			}()
		}
		wg.Wait()
		close(results)
		for code := range results {
			if code != 201 {
				t.Fatalf("distinct purchase returned %d", code)
			}
		}
		var sub model.Subscription
		if db.Where("user_id = ?", user.ID).First(&sub).Error != nil {
			t.Fatal("missing subscription")
		}
		if sub.TrafficLimitBytes != 4*firstSub.TrafficLimitBytes {
			t.Fatal("concurrent renewal lost an update")
		}
	})
	t.Run("account_isolation", func(t *testing.T) {
		otherCookies, _, _ := register()
		w := request("GET", "/api/v1/orders", "", otherCookies, "")
		want(w, 200)
		var data struct {
			Total int64 `json:"total"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		if data.Total != 0 {
			t.Fatal("cross-account order leakage")
		}
		want(request("GET", "/api/v1/client/bootstrap", "", otherCookies, ""), 403)
	})
	t.Run("node_metadata_and_entitlement_gates", func(t *testing.T) {
		nodes := []model.Node{{ID: uuid.NewString(), Name: "QA direct", Region: "TEST", LineType: "direct", RatePermille: 500, Enabled: true}, {ID: uuid.NewString(), Name: "QA dedicated", Region: "TEST", LineType: "dedicated", RatePermille: 1000, Enabled: true}}
		for _, n := range nodes {
			nodeIDs = append(nodeIDs, n.ID)
		}
		if err := db.Create(&nodes).Error; err != nil {
			t.Fatal(err)
		}
		w := request("GET", "/api/v1/client/bootstrap", "", cookies, "")
		want(w, 200)
		var out struct {
			Nodes []model.Node `json:"nodes"`
			Ready bool         `json:"proxy_service_ready"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		for _, n := range out.Nodes {
			if n.LineType == "dedicated" {
				t.Fatal("starter received dedicated metadata")
			}
		}
		if out.Ready || strings.Contains(w.Body.String(), "password") {
			t.Fatal("proxy readiness or secrets incorrectly returned")
		}
		change := func(values map[string]any) {
			t.Helper()
			if err := db.Model(&model.Subscription{}).Where("user_id = ?", user.ID).Updates(values).Error; err != nil {
				t.Fatal(err)
			}
		}
		change(map[string]any{"expires_at": time.Now().Add(-time.Minute)})
		want(request("GET", "/api/v1/client/bootstrap", "", cookies, ""), 403)
		change(map[string]any{"expires_at": time.Now().Add(time.Hour), "used_units": 4 * firstSub.TrafficLimitBytes * 1000})
		want(request("GET", "/api/v1/client/bootstrap", "", cookies, ""), 403)
		change(map[string]any{"used_units": 0})
		disabled := cfg
		disabled.TestPurchase = false
		closed := New(db, disabled)
		want(call(closed, "POST", "/api/v1/orders/test-purchase", `{"plan_id":"starter"}`, cookies, uuid.NewString(), cfg.Origins[0]), 403)
		want(call(closed, "GET", "/api/v1/client/bootstrap", "", cookies, "", cfg.Origins[0]), 403)
	})
	t.Run("refresh_rotation_and_logout_revocation", func(t *testing.T) {
		w := request("POST", "/api/v1/auth/refresh", "{}", cookies, "")
		want(w, 200)
		updated := w.Result().Cookies()
		want(request("POST", "/api/v1/auth/refresh", "{}", cookies, ""), 401)
		want(request("GET", "/api/v1/auth/me", "", updated, ""), 200)
		want(request("POST", "/api/v1/auth/logout", "{}", updated, ""), 200)
		want(request("GET", "/api/v1/auth/me", "", updated, ""), 401)
		want(request("GET", "/api/v1/auth/me", "", cookies, ""), 401)
		want(request("POST", "/api/v1/auth/refresh", "{}", updated, ""), 401)
	})
	t.Run("rate_limit_ignores_untrusted_forwarded_header", func(t *testing.T) {
		limitedCfg := cfg
		limitedCfg.AuthRate = 1
		limited := New(db, limitedCfg)
		r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"none@example.invalid","password":"invalid"}`))
		r.RemoteAddr = "127.0.0.8:1234"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		limited.ServeHTTP(w, r)
		want(w, 401)
		r = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"none@example.invalid","password":"invalid"}`))
		r.RemoteAddr = "127.0.0.8:1234"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", "8.8.8.8")
		w = httptest.NewRecorder()
		limited.ServeHTTP(w, r)
		want(w, 429)
	})
}
