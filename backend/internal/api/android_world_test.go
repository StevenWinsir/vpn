package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// TestAndroidAccountWorld serves a seeded native world for the Android account acceptance
// (scripts/test-native-android.py --suite account, handoff_app_android.md round 3: A01-A04).
// It is skipped unless the suite names an output file in ASTERLINK_ANDROID_WORLD, and it only
// ever listens on 127.0.0.1 (the emulator reaches it through `adb reverse`).
//
// Besides the real /api/v1 router it answers a few loopback-only control calls under
// /__world/ that do what an administrator does in the web console (rename, re-rate, disable,
// delete a line; expire a plan; occupy the only device slot of a plan), so the app's
// reaction to each can be timed. The world and its private database vanish with the test.
func TestAndroidAccountWorld(t *testing.T) {
	out := os.Getenv("ASTERLINK_ANDROID_WORLD")
	if out == "" {
		t.Skip("set by scripts/test-native-android.py --suite account")
	}
	port := os.Getenv("ASTERLINK_ANDROID_WORLD_PORT")
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg.Origins = []string{"http://127.0.0.1:3000"}
	router := New(db, cfg)
	world := seedNativeWorld(t, db, cfg, router)
	const privateHost, privateSecret, vlessUUID = e2ePrivateHost, e2ePrivateSecret, e2eVlessUUID

	var mu sync.Mutex
	nodes := map[string]adminNode{"dedicated": world.dedicated, "ordinary": world.ordinary}
	users := map[string]model.User{"admin": world.admin, "vip": world.vip, "basic": world.basic, "expiring": world.expiring, "broke": world.broke}
	yamlFor := func(which, name string) string {
		if which == "dedicated" {
			return fmt.Sprintf("proxies: [{name: %s, type: ss, server: %s, port: 31000, cipher: aes-256-gcm, password: %s, udp: true}]", name, privateHost, privateSecret)
		}
		return fmt.Sprintf("proxies: [{name: %s, type: vless, server: %s, port: 443, uuid: %s, tls: true, servername: %s, network: tcp, udp: true}]", name, privateHost, vlessUUID, privateHost)
	}
	// The administrator's web session, as in the console.
	adminCall := func(method, path string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		login := httptest.NewRecorder()
		loginBody, _ := json.Marshal(loginInput{Email: world.admin.Email, Password: world.password})
		r := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(loginBody))
		r.Header.Set("Origin", cfg.Origins[0])
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(login, r)
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		req.Header.Set("Origin", cfg.Origins[0])
		req.Header.Set("Content-Type", "application/json")
		for _, c := range login.Result().Cookies() {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	stop := make(chan struct{})
	var once sync.Once
	control := func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := net.SplitHostPort(r.RemoteAddr); host != "127.0.0.1" || r.Method != http.MethodPost {
			http.Error(w, "loopback POST only", http.StatusForbidden)
			return
		}
		var in struct {
			Node    string `json:"node"`
			Name    string `json:"name"`
			Rate    int64  `json:"rate_permille"`
			Enabled *bool  `json:"enabled"`
			User    string `json:"user"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		defer mu.Unlock()
		switch strings.TrimPrefix(r.URL.Path, "/__world/") {
		case "edit": // rename and/or re-rate and/or enable/disable a line
			node, ok := nodes[in.Node]
			if !ok {
				http.Error(w, "unknown node", 400)
				return
			}
			enabled := node.Enabled
			if in.Enabled != nil {
				enabled = *in.Enabled
			}
			rate := node.RatePermille
			if in.Rate > 0 {
				rate = in.Rate
			}
			name := in.Name
			if name == "" {
				name = node.Name
			}
			nodes[in.Node] = world.save(node.ID, nodeInput{YAML: yamlFor(in.Node, name), Region: node.Region, LineType: node.LineType, RatePermille: rate, Enabled: enabled, PlanIDs: []string{}, Version: node.Version})
		case "delete":
			node, ok := nodes[in.Node]
			if !ok {
				http.Error(w, "unknown node", 400)
				return
			}
			if res := adminCall("DELETE", "/admin/nodes/"+node.ID, map[string]int64{"version": node.Version}); res.Code != 200 {
				http.Error(w, "delete failed", 500)
				return
			}
			delete(nodes, in.Node)
		case "expire": // the plan ends now
			user, ok := users[in.User]
			if !ok {
				http.Error(w, "unknown user", 400)
				return
			}
			db.Model(&model.Subscription{}).Where("user_id = ?", user.ID).Update("expires_at", time.Now().Add(-time.Second))
		case "occupy": // another device signs in and binds a line: the plan's only slot is taken
			user, ok := users[in.User]
			node, known := nodes[in.Node]
			if !ok || !known {
				http.Error(w, "unknown user or node", 400)
				return
			}
			body, _ := json.Marshal(map[string]string{"email": user.Email, "password": world.password, "device_id": uuid.NewString(), "platform": "macos", "app_version": "android-world"})
			login := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/api/v1/client/login", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(login, request)
			var session struct {
				Token string `json:"token"`
			}
			_ = json.Unmarshal(login.Body.Bytes(), &session)
			config := httptest.NewRequest("GET", "/api/v1/client/config?node_id="+node.ID, nil)
			config.Header.Set("Authorization", "Bearer "+session.Token)
			bound := httptest.NewRecorder()
			router.ServeHTTP(bound, config)
			if login.Code != 200 || bound.Code != 200 {
				http.Error(w, fmt.Sprintf("occupy: login %d config %d %s", login.Code, bound.Code, strings.TrimSpace(bound.Body.String()+login.Body.String())), 500)
				return
			}
		case "stop":
			once.Do(func() { close(stop) })
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__world/") {
			control(w, r)
			return
		}
		router.ServeHTTP(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	address := listener.Addr().String()
	facts, _ := json.Marshal(map[string]any{
		"api_base": "http://" + address + "/api/v1/client",
		"password": world.password,
		"emails": map[string]string{
			"admin": world.admin.Email, "vip": world.vip.Email, "basic": world.basic.Email, "expiring": world.expiring.Email, "broke": world.broke.Email,
		},
		"nodes": map[string]string{"dedicated": world.dedicated.ID, "ordinary": world.ordinary.ID},
		"names": map[string]string{"dedicated": world.dedicated.Name, "ordinary": world.ordinary.Name},
	})
	if err := os.WriteFile(out, facts, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("android world on %s", address)
	select {
	case <-stop:
	case <-time.After(45 * time.Minute):
		t.Fatal("the account suite never stopped the world")
	}
}
