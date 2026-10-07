package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
)

func TestWebNodeListPostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	router := New(db, cfg)
	password := "Web-Nodes-Test-Only!"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, cookies []*http.Cookie, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Origin", cfg.Origins[0])
		r.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	want := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got %d, want %d: %s", w.Code, status, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("node metadata must not be cached")
		}
	}
	makeUser := func(role string) (model.User, []*http.Cookie) {
		t.Helper()
		u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "Node list test", Role: role, Status: "active", PasswordHash: string(hash)}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		w := call("POST", "/auth/login", nil, loginInput{Email: u.Email, Password: password})
		want(w, 200)
		return u, w.Result().Cookies()
	}
	_, admin := makeUser("admin")
	user, customer := makeUser("user")
	_, unsubscribed := makeUser("user")
	want(call("GET", "/client/bootstrap", nil, nil), 401)
	want(call("GET", "/client/bootstrap", unsubscribed, nil), 403)
	want(call("GET", "/client/bootstrap", admin, nil), 403) // Admin role does not bypass subscription checks.
	want(call("GET", "/admin/nodes", customer, nil), 403)
	want(call("GET", "/client/config", customer, nil), 401) // Web cookies cannot fetch native credentials.

	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := model.Subscription{ID: uuid.NewString(), UserID: user.ID, PlanID: "pro", PlanName: "Pro", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 1000, MaxDevices: 2}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	input := func(name, line string) nodeInput {
		return nodeInput{YAML: fmt.Sprintf("proxies: [{name: %s, type: ss, server: metadata-private.example.invalid, port: 18443, cipher: aes-256-gcm, password: never-in-web-list, udp: true}]", name), Region: "HK", LineType: line, RatePermille: 500, Enabled: true, PlanIDs: []string{}}
	}
	save := func(id string, in nodeInput) adminNode {
		t.Helper()
		path, status := "/admin/nodes", 201
		if id != "" {
			path, status = path+"/"+id, 200
		}
		w := call("POST", path, admin, in)
		want(w, status)
		var out struct {
			Nodes []adminNode `json:"nodes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Nodes) != 1 {
			t.Fatal("invalid save response")
		}
		return out.Nodes[0]
	}
	directInput := input("B-ordinary", "direct")
	direct := save("", directInput)
	dedicatedInput := input("A-dedicated", "dedicated")
	dedicatedInput.RatePermille = 1000
	dedicated := save("", dedicatedInput)
	restricted := input("C-max-only", "direct")
	restricted.PlanIDs = []string{"max"}
	save("", restricted)
	disabled := input("D-disabled", "direct")
	disabled.Enabled = false
	save("", disabled)

	read := func(names ...string) []model.Node {
		t.Helper()
		w := call("GET", "/client/bootstrap", customer, nil)
		want(w, 200)
		for _, secret := range []string{"metadata-private.example.invalid", "never-in-web-list", "ciphertext", "proxies:"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("metadata leaked %q", secret)
			}
		}
		var raw struct {
			Nodes []map[string]json.RawMessage `json:"nodes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || raw.Nodes == nil {
			t.Fatal("nodes must always be an array")
		}
		keys := []string{"id", "name", "region", "line_type", "rate_permille", "version"}
		for _, node := range raw.Nodes {
			if len(node) != len(keys) {
				t.Fatal("public node schema changed")
			}
			for key := range node {
				if !slices.Contains(keys, key) {
					t.Fatalf("unexpected public field: %s", key)
				}
			}
		}
		var out struct {
			Nodes []model.Node `json:"nodes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, node := range out.Nodes {
			got = append(got, node.Name)
		}
		if !slices.Equal(got, names) {
			t.Fatalf("got nodes %v, want %v", got, names)
		}
		return out.Nodes
	}
	read("B-ordinary")
	if err := db.Model(&sub).Update("allow_dedicated", true).Error; err != nil {
		t.Fatal(err)
	}
	read("A-dedicated", "B-ordinary")

	directInput.Version = direct.Version
	directInput.YAML = strings.ReplaceAll(directInput.YAML, "B-ordinary", "B-renamed")
	directInput.Region, directInput.RatePermille = "JP", 1250
	direct = save(direct.ID, directInput)
	updated := read("A-dedicated", "B-renamed")[1]
	if updated.Region != "JP" || updated.RatePermille != 1250 || updated.Version != direct.Version {
		t.Fatal("admin metadata update did not synchronize")
	}
	directInput.Version, directInput.PlanIDs = direct.Version, []string{"max"}
	direct = save(direct.ID, directInput)
	read("A-dedicated")
	directInput.Version, directInput.PlanIDs = direct.Version, []string{}
	direct = save(direct.ID, directInput)
	read("A-dedicated", "B-renamed")
	dedicatedInput.Version, dedicatedInput.Enabled = dedicated.Version, false
	save(dedicated.ID, dedicatedInput)
	read("B-renamed")
	want(call("DELETE", "/admin/nodes/"+direct.ID, admin, map[string]int64{"version": direct.Version}), 200)
	read()

	for _, tc := range []struct {
		name    string
		changes map[string]any
	}{
		{"expired", map[string]any{"expires_at": now.Add(-time.Second)}},
		{"not started", map[string]any{"starts_at": now.Add(time.Hour)}},
		{"quota exhausted", map[string]any{"used_units": int64(1000 * 1000)}},
		{"test entitlement disabled", map[string]any{"is_test": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset := map[string]any{"starts_at": now.Add(-time.Hour), "expires_at": now.Add(time.Hour), "used_units": 0, "is_test": false}
			for key, value := range tc.changes {
				reset[key] = value
			}
			if err := db.Model(&sub).Updates(reset).Error; err != nil {
				t.Fatal(err)
			}
			w := call("GET", "/client/bootstrap", customer, nil)
			want(w, 403)
			if !strings.Contains(w.Body.String(), "entitlement_inactive") || strings.Contains(w.Body.String(), `"nodes"`) {
				t.Fatal("inactive entitlement received nodes")
			}
		})
	}
	var sessions int64
	if err := db.Model(&model.NativeSession{}).Where("user_id = ?", user.ID).Count(&sessions).Error; err != nil || sessions != 0 {
		t.Fatal("web browsing created a native session")
	}
	want(call("POST", "/auth/logout", customer, map[string]any{}), 200)
	want(call("GET", "/client/bootstrap", customer, nil), 401)
}
