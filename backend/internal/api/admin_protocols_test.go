package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
	"vpn/nodepolicy"
)

// Exercise the real administrator/native routes and PostgreSQL; no network
// connection to fixture proxies is made and no deployed database is used.
func TestAdminMultiProtocolCatalogPostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32))
	router := New(db, cfg)
	hash, err := bcrypt.GenerateFromPassword([]byte("Protocol-Fixture-Password!"), 10)
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "Protocol fixture", Role: "admin", Status: "active", PasswordHash: string(hash)}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	request := func(method, path, credential string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", cfg.Origins[0])
		if strings.HasPrefix(credential, "Bearer ") {
			r.Header.Set("Authorization", credential)
		} else {
			r.Header.Set("Cookie", credential)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential-bearing response is cacheable")
		}
		return w
	}
	login := request("POST", "/auth/login", "", loginInput{Email: user.Email, Password: "Protocol-Fixture-Password!"}, 200)
	cookies := []string{}
	for _, cookie := range login.Result().Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	cookie := strings.Join(cookies, "; ")
	data, err := os.ReadFile("../../../FlClash/core/nodepolicy/testdata/proxies.yaml")
	if err != nil {
		t.Fatal(err)
	}
	input := nodeInput{YAML: string(data), Region: "TEST", LineType: "direct", RatePermille: 500, Enabled: true, PlanIDs: []string{"pro"}}
	w := request("POST", "/admin/nodes", cookie, input, 201)
	var saved struct {
		Nodes []adminNode `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || len(saved.Nodes) != 30 {
		t.Fatal("batch import failed")
	}
	if strings.Contains(w.Body.String(), "fixture-secret") || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("import response leaked credentials")
	}
	list := request("GET", "/admin/nodes", cookie, nil, 200)
	var catalog struct {
		Supported []string `json:"supported_protocols"`
		Revision  string   `json:"core_revision"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &catalog); err != nil || !reflect.DeepEqual(catalog.Supported, nodepolicy.Supported()) || catalog.Revision != nodepolicy.CoreRevision {
		t.Fatal("advertised capability mismatch")
	}
	now := time.Now().UTC()
	sub := model.Subscription{ID: uuid.NewString(), UserID: user.ID, PlanID: "pro", PlanName: "Pro", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 100000, MaxDevices: 1}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	nativeLogin := request("POST", "/client/login", "", map[string]any{"email": user.Email, "password": "Protocol-Fixture-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "protocol-test"}, 200)
	var auth struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(nativeLogin.Body.Bytes(), &auth); err != nil || auth.Token == "" {
		t.Fatal("native login failed")
	}
	bearer := "Bearer " + auth.Token
	for _, node := range saved.Nodes {
		t.Run(node.Name, func(t *testing.T) {
			var secret model.NodeConfig
			if err := db.First(&secret, "node_id = ?", node.ID).Error; err != nil {
				t.Fatal(err)
			}
			if strings.Contains(secret.Ciphertext, "fixture-secret") || strings.Contains(secret.Ciphertext, "private-key") {
				t.Fatal("plaintext node at rest")
			}
			plaintext, err := nodes.Decrypt(cfg.NodeEncryptionKey, node.ID, secret.Ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := nodes.Parse(string(plaintext))
			if err != nil || len(parsed) != 1 || parsed[0]["name"] != node.Name {
				t.Fatal("nested credentials changed at rest")
			}
			profile := request("GET", "/client/config?node_id="+node.ID+"&last_sequence=0", bearer, nil, 200)
			var result struct {
				YAML    string      `json:"yaml"`
				Session nativeState `json:"session"`
			}
			if err := json.Unmarshal(profile.Body.Bytes(), &result); err != nil || result.Session.RatePermille != 500 {
				t.Fatal("server rate binding lost")
			}
			if strings.Count(result.YAML, "type: ") != 2 || !strings.Contains(result.YAML, node.Name) {
				t.Fatal("selected-node-only delivery violated")
			}
		})
	}
	// Client-controlled pricing/identity fields must never reach the ledger.
	for _, key := range []string{"rate_permille", "node_id", "user_id", "plan_id"} {
		request("POST", "/client/traffic", bearer, map[string]any{"sequence": 1, "upload_bytes": 20, "download_bytes": 40, key: 0}, 400)
	}
	request("POST", "/client/traffic", bearer, map[string]int64{"sequence": 1, "upload_bytes": 20, "download_bytes": 40}, 200)
	request("POST", "/client/traffic", bearer, map[string]int64{"sequence": 1, "upload_bytes": 20, "download_bytes": 40}, 200)
	request("POST", "/client/traffic", bearer, map[string]int64{"sequence": 1, "upload_bytes": 21, "download_bytes": 40}, 409)
	if err := db.First(&sub, "id = ?", sub.ID).Error; err != nil || sub.UsedUnits != 30000 {
		t.Fatal("protocol-independent replay/rate accounting failed")
	}
	if err := db.Model(&sub).Update("expires_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	request("GET", "/client/config?last_sequence=1", bearer, nil, 403)
	// One invalid member aborts the whole batch, before any DB writes.
	input.YAML = "proxies: [{name: would-be-valid, type: socks5, server: localhost, port: 9}, {name: unsafe, type: vless, server: localhost, port: 9, uuid: invalid-secret}]"
	bad := request("POST", "/admin/nodes", cookie, input, 400)
	if strings.Contains(bad.Body.String(), "invalid-secret") {
		t.Fatal("validation response reflected secret")
	}
	var count int64
	if err := db.Model(&model.Node{}).Count(&count).Error; err != nil || count != 30 {
		t.Fatal("failed import partially committed")
	}
}
