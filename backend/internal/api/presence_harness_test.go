package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// presenceEnv drives the presence handlers with an injected clock, so lease expiry
// and the 24-hour window are tested without waiting (plan 12: no real 90 s sleeps).
type presenceEnv struct {
	t      *testing.T
	db     *gorm.DB
	cfg    config.Config
	router *gin.Engine
	mu     sync.Mutex
	at     time.Time
}

const presencePassword = "Presence-Test-Password!"

// presenceHash is computed once: allocation tests create hundreds of users.
var presenceHash = sync.OnceValue(func() string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(presencePassword), 10)
	return string(hash)
})

func newPresenceEnv(t *testing.T, mutate func(*config.Config)) *presenceEnv {
	t.Helper()
	return newPresenceEnvWith(t, mutate, nil)
}

// newPresenceEnvWith also injects the allocator's tie-break source.
func newPresenceEnvWith(t *testing.T, mutate func(*config.Config), random func(int) int) *presenceEnv {
	t.Helper()
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.ClientProfileDir = ""
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32))
	if mutate != nil {
		mutate(&cfg)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(40)
	e := &presenceEnv{t: t, db: db, cfg: cfg, at: time.Date(2030, 1, 2, 10, 7, 30, 0, time.UTC)}
	e.router = newServerWith(db, cfg, authmail.NewService(db, cfg), e.now, random)
	return e
}

func (e *presenceEnv) now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.at
}

func (e *presenceEnv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.at = e.at.Add(d)
}

// server returns a group holding one enabled server, as the admin API would create it.
func (e *presenceEnv) server(name string) model.Node {
	e.t.Helper()
	group := model.NodeGroup{ID: uuid.NewString(), Name: name, Region: "JP", LineType: "direct", RatePermille: 1000, PlanIDs: "[]", Enabled: true, Version: 1}
	if err := e.db.Create(&group).Error; err != nil {
		e.t.Fatal(err)
	}
	node := model.Node{ID: group.ID, Name: name, Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, Version: 1, GroupID: &group.ID}
	if err := e.db.Omit("Group").Create(&node).Error; err != nil {
		e.t.Fatal(err)
	}
	yaml := fmt.Sprintf("proxies:\n  - {name: %s, type: ss, server: 127.0.0.1, port: 18443, cipher: aes-256-gcm, password: presence-secret}\n", name)
	sealed, err := nodes.Encrypt(e.cfg.NodeEncryptionKey, node.ID, []byte(yaml))
	if err != nil {
		e.t.Fatal(err)
	}
	if err = e.db.Omit("Node").Create(&model.NodeConfig{NodeID: node.ID, Ciphertext: sealed, PlanIDs: "[]"}).Error; err != nil {
		e.t.Fatal(err)
	}
	return node
}

// adoptServer gives a fixture server written without a group the one-server group of
// its own id, as the migration's backfill does (D3). Clients only see groups.
func adoptServer(t *testing.T, db *gorm.DB, node model.Node) {
	t.Helper()
	group := model.NodeGroup{ID: node.ID, Name: node.Name, Region: node.Region, LineType: node.LineType, RatePermille: node.RatePermille, PlanIDs: "[]", Enabled: true, Version: node.Version}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Node{}).Where("id = ?", node.ID).Update("group_id", node.ID).Error; err != nil {
		t.Fatal(err)
	}
}

type presenceUser struct {
	model.User
	sub model.Subscription
}

func (e *presenceEnv) user(maxDevices int) presenceUser {
	e.t.Helper()
	u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "presence", PasswordHash: presenceHash(), Role: "user", Status: "active"}
	if err := e.db.Create(&u).Error; err != nil {
		e.t.Fatal(err)
	}
	now := e.now()
	sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), TrafficLimitBytes: 100 << 30, MaxDevices: maxDevices, AllowDedicated: true}
	if err := e.db.Omit("User", "Plan").Create(&sub).Error; err != nil {
		e.t.Fatal(err)
	}
	return presenceUser{User: u, sub: sub}
}

type presenceCall struct {
	method, path, token string
	headers             map[string]string
	body                any
	ctx                 context.Context
}

func (e *presenceEnv) do(c presenceCall) *httptest.ResponseRecorder {
	var data []byte
	if c.body != nil {
		data, _ = json.Marshal(c.body)
	}
	req := httptest.NewRequest(c.method, "/api/v1"+c.path, bytes.NewReader(data))
	if c.ctx != nil {
		req = req.WithContext(c.ctx)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(c.token, "vpn_") {
		req.Header.Set("Cookie", c.token)
		req.Header.Set("Origin", e.cfg.Origins[0])
	} else if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *presenceEnv) native(method, path, token string, body any) *httptest.ResponseRecorder {
	return e.do(presenceCall{method: method, path: "/client" + path, token: token, body: body})
}

func (e *presenceEnv) want(w *httptest.ResponseRecorder, status int, code string) {
	e.t.Helper()
	if w.Code != status || code != "" && errorCode(w) != code {
		e.t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), status, code)
	}
}

func (e *presenceEnv) login(u presenceUser, device string) string {
	e.t.Helper()
	w := e.native("POST", "/login", "", map[string]any{"email": u.Email, "password": presencePassword, "device_id": device, "platform": "android", "app_version": "2.0.0"})
	e.want(w, 200, "")
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Token
}

func (e *presenceEnv) connect(token, group, key string) *httptest.ResponseRecorder {
	return e.do(presenceCall{method: "POST", path: "/client/connections", token: token, headers: map[string]string{"Idempotency-Key": key}, body: map[string]any{"group_id": group, "last_sequence": e.sequence(token)}})
}

// sequence is the session's confirmed batch, as a Core would send it.
func (e *presenceEnv) sequence(token string) int64 {
	var sess model.NativeSession
	e.db.Select("last_sequence").Take(&sess, "token_hash = ?", refreshHash(token))
	return sess.LastSequence
}

func (e *presenceEnv) connection(w *httptest.ResponseRecorder) connectionResponse {
	e.t.Helper()
	var out connectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Connection.ID == "" {
		e.t.Fatalf("no connection in %d %s", w.Code, w.Body.String())
	}
	return out
}

func (e *presenceEnv) traffic(token string, sequence, up, down int64) *httptest.ResponseRecorder {
	return e.native("POST", "/traffic", token, map[string]int64{"sequence": sequence, "upload_bytes": up, "download_bytes": down})
}

func snapshotOf(t *testing.T, w *httptest.ResponseRecorder) nativeState {
	t.Helper()
	var out struct {
		Session *nativeState `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err == nil && out.Session != nil {
		return *out.Session
	}
	var state nativeState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("no snapshot in %s", w.Body.String())
	}
	return state
}

func (e *presenceEnv) lease(id string) model.ConnectionLease {
	e.t.Helper()
	var l model.ConnectionLease
	if err := e.db.Take(&l, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return l
}

// live counts the user's leases exactly as admission does.
func (e *presenceEnv) live(userID string) int64 {
	e.t.Helper()
	var n int64
	s := &Server{cfg: e.cfg}
	if err := s.liveLeases(e.db, e.now()).Where("l.user_id = ?", userID).Count(&n).Error; err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *presenceEnv) web(u presenceUser) string {
	e.t.Helper()
	w := e.do(presenceCall{method: "POST", path: "/auth/login", body: loginInput{Email: u.Email, Password: presencePassword}, headers: map[string]string{"Origin": e.cfg.Origins[0]}})
	e.want(w, 200, "")
	cookies := []string{}
	for _, cookie := range w.Result().Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(cookies, "; ")
}

func (e *presenceEnv) webGet(cookie, path string) map[string]any {
	e.t.Helper()
	w := e.do(presenceCall{method: "GET", path: path, token: cookie})
	e.want(w, 200, "")
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}
