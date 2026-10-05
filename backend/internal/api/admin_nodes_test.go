package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
)

func TestAdminNodeCatalogPostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32))
	router := New(db, cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Catalog-Test-Password!"), 10)
	makeUser := func(role string) model.User {
		u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "Catalog tester", Role: role, Status: "active", PasswordHash: string(hash)}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin, customer := makeUser("admin"), makeUser("user")
	request := func(method, path, credential string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", cfg.Origins[0])
		if strings.HasPrefix(credential, "Bearer ") {
			r.Header.Set("Authorization", credential)
		} else if credential != "" {
			r.Header.Set("Cookie", credential)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	want := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("got %d want %d: %s", w.Code, code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("sensitive API response was cacheable")
		}
	}
	webLogin := func(u model.User) string {
		w := request("POST", "/auth/login", "", loginInput{Email: u.Email, Password: "Catalog-Test-Password!"})
		want(w, 200)
		cookies := []string{}
		for _, cookie := range w.Result().Cookies() {
			cookies = append(cookies, cookie.Name+"="+cookie.Value)
		}
		return strings.Join(cookies, "; ")
	}
	adminCookie, userCookie := webLogin(admin), webLogin(customer)
	want(request("GET", "/admin/nodes", "", nil), 401)
	want(request("GET", "/admin/nodes", userCookie, nil), 403)
	want(request("POST", "/admin/nodes", userCookie, nodeInput{}), 403)
	nodeBody := func(name, line string, rate int64) nodeInput {
		return nodeInput{YAML: fmt.Sprintf("proxies: [{name: %s, type: ss, server: 127.0.0.1, port: 18443, cipher: aes-256-gcm, password: catalog-private-secret, udp: true}]", name), Region: "HK", LineType: line, RatePermille: rate, Enabled: true, PlanIDs: []string{}}
	}
	save := func(id string, input nodeInput, code int) []adminNode {
		path := "/admin/nodes"
		if id != "" {
			path += "/" + id
		}
		w := request("POST", path, adminCookie, input)
		want(w, code)
		if code >= 400 {
			return nil
		}
		if strings.Contains(w.Body.String(), "catalog-private-secret") {
			t.Fatal("mutation response leaked proxy password")
		}
		var result struct {
			Nodes []adminNode `json:"nodes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Nodes
	}
	directInput := nodeBody("ordinary", "direct", 500)
	direct := save("", directInput, 201)[0]
	dedicated := save("", nodeBody("premium", "dedicated", 1000), 201)[0]
	maxInput := nodeBody("max-only", "direct", 1000)
	maxInput.PlanIDs = []string{"max"}
	maxNode := save("", maxInput, 201)[0]
	want(request("GET", "/admin/nodes/"+direct.ID, userCookie, nil), 403)
	list := request("GET", "/admin/nodes", adminCookie, nil)
	want(list, 200)
	if strings.Contains(list.Body.String(), "catalog-private-secret") || strings.Contains(list.Body.String(), "127.0.0.1") {
		t.Fatal("catalog list leaked a secret or endpoint")
	}
	var secret model.NodeConfig
	if err := db.First(&secret, "node_id = ?", direct.ID).Error; err != nil || strings.Contains(secret.Ciphertext, "catalog-private-secret") {
		t.Fatal("node config was not encrypted at rest")
	}
	detail := request("GET", "/admin/nodes/"+direct.ID, adminCookie, nil)
	want(detail, 200)
	if !strings.Contains(detail.Body.String(), "catalog-private-secret") {
		t.Fatal("authorized administrator cannot edit YAML")
	}
	save("", directInput, 409)
	invalid := directInput
	invalid.YAML += "\nexternal-controller: 0.0.0.0:9090"
	save("", invalid, 400)
	invalid = nodeBody("unknown-plan", "direct", 500)
	invalid.PlanIDs = []string{"does-not-exist"}
	save("", invalid, 400)

	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := model.Subscription{ID: uuid.NewString(), UserID: customer.ID, PlanID: "pro", PlanName: "Pro", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 1000, MaxDevices: 2, AllowDedicated: true}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	login := request("POST", "/client/login", "", map[string]any{"email": customer.Email, "password": "Catalog-Test-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "catalog-test"})
	want(login, 200)
	var logged struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &logged); err != nil {
		t.Fatal(err)
	}
	native := "Bearer " + logged.Token
	want(request("GET", "/client/config?node_id="+maxNode.ID, native, nil), 503)
	config := request("GET", "/client/config?node_id="+direct.ID, native, nil)
	want(config, 200)
	var profile struct {
		YAML    string       `json:"yaml"`
		Nodes   []nodeChoice `json:"nodes"`
		NodeID  string       `json:"node_id"`
		Session nativeState  `json:"session"`
	}
	if err := json.Unmarshal(config.Body.Bytes(), &profile); err != nil || len(profile.Nodes) != 2 || profile.NodeID != direct.ID || profile.Session.RatePermille != 500 || strings.Contains(profile.YAML, "premium") || strings.Contains(profile.YAML, "max-only") {
		t.Fatal("catalog did not enforce single-node credentials and plan filtering")
	}
	report := func(sequence, up, down int64) *httptest.ResponseRecorder {
		return request("POST", "/client/traffic", native, map[string]int64{"sequence": sequence, "upload_bytes": up, "download_bytes": down})
	}
	want(report(1, 101, 100), 200)
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- report(1, 101, 100).Code }()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal("idempotent concurrent report failed")
		}
	}
	want(report(1, 102, 100), 409)
	want(report(2, 201, 100), 200)
	directInput.Version, directInput.RatePermille = direct.Version, 1000
	save(direct.ID, directInput, 200)
	save(direct.ID, directInput, 409)
	state := request("GET", "/client/session", native, nil)
	want(state, 200)
	if !strings.Contains(state.Body.String(), "profile_changed") {
		t.Fatal("administrator change did not invalidate cached profile")
	}
	want(report(3, 203, 100), 200)
	want(request("GET", "/client/config?node_id="+direct.ID+"&last_sequence=2", native, nil), 409)
	want(request("GET", "/client/config?node_id="+direct.ID+"&last_sequence=3", native, nil), 200)
	want(report(4, 207, 100), 200)
	if err := db.First(&sub, "id = ?", sub.ID).Error; err != nil || sub.UsedUnits != 155500 || sub.UploadBytes != 207 || sub.DownloadBytes != 100 {
		t.Fatalf("rate snapshot, fractional units, or retry accounting incorrect: used=%d up=%d down=%d", sub.UsedUnits, sub.UploadBytes, sub.DownloadBytes)
	}
	var reports []model.ClientTrafficReport
	if err := db.Where("subscription_id = ?", sub.ID).Order("sequence").Find(&reports).Error; err != nil || len(reports) != 4 || reports[2].RatePermille != 500 || reports[3].RatePermille != 1000 || reports[3].NodeID != direct.ID {
		t.Fatal("historical rate/node ledger not preserved")
	}
	directInput.Version, directInput.Enabled = 2, false
	save(direct.ID, directInput, 200)
	want(request("GET", "/client/config?node_id="+direct.ID+"&last_sequence=4", native, nil), 503)
	reloaded := request("GET", "/client/config?last_sequence=4", native, nil)
	want(reloaded, 200)
	if err := json.Unmarshal(reloaded.Body.Bytes(), &profile); err != nil || profile.NodeID != dedicated.ID || len(profile.Nodes) != 1 {
		t.Fatal("disabled node was not removed on synchronization")
	}
	if err := db.Model(&sub).Update("expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	want(request("GET", "/client/config?last_sequence=4", native, nil), 403)
	tail := report(5, 209, 100)
	want(tail, 200)
	if !strings.Contains(tail.Body.String(), "subscription_expired") {
		t.Fatal("expired subscription was authorized")
	}

	starter := makeUser("user")
	starterSub := model.Subscription{ID: uuid.NewString(), UserID: starter.ID, PlanID: "starter", PlanName: "Starter", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 10, MaxDevices: 1}
	if err := db.Create(&starterSub).Error; err != nil {
		t.Fatal(err)
	}
	starterLogin := request("POST", "/client/login", "", map[string]any{"email": starter.Email, "password": "Catalog-Test-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "catalog-test"})
	want(starterLogin, 200)
	if err := json.Unmarshal(starterLogin.Body.Bytes(), &logged); err != nil {
		t.Fatal(err)
	}
	want(request("GET", "/client/config?node_id="+dedicated.ID, "Bearer "+logged.Token, nil), 503)
	var auditCount int64
	if err := db.Model(&model.AdminAudit{}).Count(&auditCount).Error; err != nil || auditCount != 5 {
		t.Fatal("successful node mutations were not atomically audited")
	}

	t.Run("csrf", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/nodes", strings.NewReader(`{}`))
		r.Header.Set("Cookie", adminCookie)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		want(w, 403)
	})
}
