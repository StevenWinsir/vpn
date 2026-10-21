package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

const sharedVlessUUID = "99999999-8888-4777-8666-555555555555"

type grantEnv struct {
	t      *testing.T
	db     *gorm.DB
	cfg    config.Config
	router *gin.Engine
	agent  *gin.Engine
	// Raw bodies of the latest agent responses, for contract-shape checks.
	lastSync, lastUsage []byte
}

func newGrantEnv(t *testing.T, mutate func(*config.Config)) *grantEnv {
	t.Helper()
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.ClientProfileDir = ""
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg.NodeAgentAddr = "127.0.0.1:0"
	cfg.GrantBudgetBytes = 64 << 20
	if mutate != nil {
		mutate(&cfg)
	}
	return &grantEnv{t: t, db: db, cfg: cfg, router: New(db, cfg), agent: NewAgent(db, cfg)}
}

type user struct {
	model.User
	subID string
}

func (e *grantEnv) newUser(quota int64, devices int) user {
	e.t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Native-Test-Password!"), 10)
	u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "grant", PasswordHash: string(hash), Role: "user", Status: "active"}
	if err := e.db.Create(&u).Error; err != nil {
		e.t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: quota, MaxDevices: devices, AllowDedicated: true}
	if err := e.db.Create(&sub).Error; err != nil {
		e.t.Fatal(err)
	}
	return user{User: u, subID: sub.ID}
}

func (e *grantEnv) client(method, path, token string, headers map[string]string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/api/v1/client"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *grantEnv) login(u user) string {
	e.t.Helper()
	w := e.client("POST", "/login", "", nil, map[string]any{"email": u.Email, "password": "Native-Test-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": "1.0.0"})
	if w.Code != 200 {
		e.t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Token
}

func (e *grantEnv) addNode(name string, rate int64, withShared bool) model.Node {
	e.t.Helper()
	node := model.Node{ID: uuid.NewString(), Name: name, Region: "HK", LineType: "direct", RatePermille: rate, Enabled: true, Version: 1}
	if err := e.db.Create(&node).Error; err != nil {
		e.t.Fatal(err)
	}
	yaml := fmt.Sprintf("proxies:\n  - {name: %s, type: vless, server: 127.0.0.1, port: 9, uuid: %s, udp: true}\n", name, sharedVlessUUID)
	sealed, err := nodes.Encrypt(e.cfg.NodeEncryptionKey, node.ID, []byte(yaml))
	if err != nil {
		e.t.Fatal(err)
	}
	if err = e.db.Create(&model.NodeConfig{NodeID: node.ID, Ciphertext: sealed, PlanIDs: "[]"}).Error; err != nil {
		e.t.Fatal(err)
	}
	adoptServer(e.t, e.db, node)
	return node
}

type testAgent struct {
	cert  *x509.Certificate
	id    string
	epoch int64
}

func newCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "test-agent"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert
}

func (e *grantEnv) addAgent(node model.Node) *testAgent {
	e.t.Helper()
	cert := newCertificate(e.t)
	sum := sha256.Sum256(cert.Raw)
	row := model.NodeAgent{ID: uuid.NewString(), NodeID: node.ID, Name: "agent-" + node.Name, CertFingerprint: hex.EncodeToString(sum[:]), Enabled: true, Protocols: "[]"}
	if err := e.db.Create(&row).Error; err != nil {
		e.t.Fatal(err)
	}
	return &testAgent{cert: cert, id: row.ID, epoch: 1}
}

func (e *grantEnv) agentCall(a *testAgent, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/internal/v1/node-agents"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if a != nil {
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{a.cert}}
	}
	w := httptest.NewRecorder()
	e.agent.ServeHTTP(w, req)
	return w
}

type order struct {
	GrantID          string            `json:"grant_id"`
	Action           string            `json:"action"`
	Protocol         string            `json:"protocol"`
	Credential       map[string]string `json:"credential"`
	RatePermille     int64             `json:"rate_permille"`
	ExpiresAt        *time.Time        `json:"expires_at"`
	EpochBudgetBytes int64             `json:"epoch_budget_bytes"`
}

func (e *grantEnv) sync(a *testAgent, installed, removed []string) map[string]order {
	e.t.Helper()
	if installed == nil {
		installed = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	w := e.agentCall(a, "/sync", map[string]any{"epoch": a.epoch, "agent_version": "test", "protocols": []string{"vless"}, "installed": installed, "removed": removed})
	if w.Code != 200 {
		e.t.Fatalf("sync %d %s", w.Code, w.Body.String())
	}
	e.lastSync = w.Body.Bytes()
	var out struct {
		Grants []order `json:"grants"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	byID := map[string]order{}
	for _, o := range out.Grants {
		byID[o.GrantID] = o
	}
	return byID
}

type usage struct {
	GrantID                                     string
	Epoch, Sequence, UploadBytes, DownloadBytes int64
	Final                                       bool
}

func (u usage) body() map[string]any {
	return map[string]any{"grant_id": u.GrantID, "epoch": u.Epoch, "sequence": u.Sequence, "upload_bytes": u.UploadBytes, "download_bytes": u.DownloadBytes, "final": u.Final}
}

func (e *grantEnv) report(a *testAgent, u usage, status int) map[string]any {
	e.t.Helper()
	w := e.agentCall(a, "/usage", u.body())
	if w.Code != status {
		e.t.Fatalf("usage %+v: got %d want %d: %s", u, w.Code, status, w.Body.String())
	}
	e.lastUsage = w.Body.Bytes()
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func errorCode(w *httptest.ResponseRecorder) string {
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Error.Code
}

func (e *grantEnv) createGrant(token, node, key string) *httptest.ResponseRecorder {
	return e.client("POST", "/grants", token, map[string]string{"Idempotency-Key": key}, map[string]string{"node_id": node})
}

func grantID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		GrantID string `json:"grant_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.GrantID == "" {
		t.Fatalf("no grant id in %s", w.Body.String())
	}
	return out.GrantID
}

func (e *grantEnv) subscription(id string) model.Subscription {
	var sub model.Subscription
	if err := e.db.First(&sub, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return sub
}

func (e *grantEnv) period(subID string) model.SubscriptionPeriod {
	var p model.SubscriptionPeriod
	if err := e.db.First(&p, "subscription_id = ?", subID).Error; err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *grantEnv) grant(id string) model.ConnectionGrant {
	var g model.ConnectionGrant
	if err := e.db.First(&g, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return g
}

const mib = int64(1 << 20)

func TestGrantLifecyclePostgres(t *testing.T) {
	e := newGrantEnv(t, func(c *config.Config) { c.GrantBudgetBytes = 256 * mib })
	u := e.newUser(1<<30, 2)
	node := e.addNode("Authority-1", 500, true)
	// Any catalog edit changes every profile version (handoff_app.md 2.2 item 5), so
	// the second node exists before the first grant.
	other := e.addNode("Authority-2", 1000, true)
	token := e.login(u)

	// No agent bound: the node cannot issue authoritative credentials.
	if w := e.createGrant(token, node.ID, "key-no-agent-1"); w.Code != 503 || errorCode(w) != "agent_unavailable" {
		t.Fatalf("no agent: %d %s", w.Code, w.Body.String())
	}
	agent := e.addAgent(node)
	// Bound but never seen / not claiming vless: still unavailable.
	if w := e.createGrant(token, node.ID, "key-no-agent-2"); w.Code != 503 {
		t.Fatalf("silent agent accepted: %d", w.Code)
	}
	e.sync(agent, nil, nil)

	if w := e.client("POST", "/grants", token, nil, map[string]string{"node_id": node.ID}); w.Code != 400 {
		t.Fatalf("missing idempotency key: %d", w.Code)
	}
	w := e.createGrant(token, node.ID, "key-first-0001")
	if w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := grantID(t, w)
	contractShape(t, "grant_create", "response", w.Body.Bytes())
	if again := e.createGrant(token, node.ID, "key-first-0001"); again.Code != 202 || grantID(t, again) != id {
		t.Fatalf("idempotent replay changed the grant: %d %s", again.Code, again.Body.String())
	}
	if conflict := e.createGrant(token, other.ID, "key-first-0001"); conflict.Code != 409 || errorCode(conflict) != "idempotency_conflict" {
		t.Fatalf("key reuse for another node: %d %s", conflict.Code, conflict.Body.String())
	}
	if got := e.client("GET", "/grants/"+id, token, nil, nil); got.Code != 202 || strings.Contains(got.Body.String(), "yaml") {
		t.Fatalf("pending grant must not expose a credential: %d %s", got.Code, got.Body.String())
	} else {
		contractShape(t, "grant_pending", "response", got.Body.Bytes())
	}
	// A node-reported session never mixes with the client-reported ledger.
	if w = e.client("GET", "/config?node_id="+node.ID+"&last_sequence=0", token, nil, nil); w.Code != 409 || errorCode(w) != "metering_source_mismatch" {
		t.Fatalf("/config on node session: %d %s", w.Code, w.Body.String())
	}
	if w = e.client("POST", "/traffic", token, nil, map[string]int64{"sequence": 1, "upload_bytes": 1, "download_bytes": 1}); w.Code != 409 || errorCode(w) != "metering_source_mismatch" {
		t.Fatalf("/traffic on node session: %d %s", w.Code, w.Body.String())
	}

	orders := e.sync(agent, nil, nil)
	contractOrders(t, e.lastSync)
	install := orders[id]
	if install.Action != "install" || install.Protocol != "vless" || install.Credential["uuid"] == "" || install.Credential["uuid"] == sharedVlessUUID || install.RatePermille != 500 {
		t.Fatalf("install order: %+v", install)
	}
	if install.EpochBudgetBytes != 256*mib {
		t.Fatalf("budget %d", install.EpochBudgetBytes)
	}
	// Not ready until the node confirms installation.
	if got := e.client("GET", "/grants/"+id, token, nil, nil); got.Code != 202 {
		t.Fatalf("still pending expected: %d", got.Code)
	}
	if orders = e.sync(agent, []string{id}, nil); orders[id].Action != "keep" || len(orders[id].Credential) != 0 {
		t.Fatalf("keep order: %+v", orders[id])
	}
	got := e.client("GET", "/grants/"+id, token, nil, nil)
	if got.Code != 200 {
		t.Fatalf("ready: %d %s", got.Code, got.Body.String())
	}
	// v3: the session now also carries its online lease.
	contractShapeV3(t, "grant_ready", "response", got.Body.Bytes())
	var ready struct {
		State   string      `json:"state"`
		YAML    string      `json:"yaml"`
		Version string      `json:"version"`
		NodeID  string      `json:"node_id"`
		Session nativeState `json:"session"`
	}
	_ = json.Unmarshal(got.Body.Bytes(), &ready)
	if ready.State != "ready" || !strings.Contains(ready.YAML, install.Credential["uuid"]) || strings.Contains(ready.YAML, sharedVlessUUID) ||
		ready.Version != nativeProfileVersion([]byte(ready.YAML)) || ready.NodeID != node.ID || ready.Session.MeteringSource != "node_reported" || ready.Session.GrantID != id {
		t.Fatalf("ready grant: %+v", ready)
	}
	if !ready.Session.CanConnect || ready.Session.Reason != "" || ready.Session.GrantState != "ready" {
		t.Fatalf("session after ready: %+v", ready.Session)
	}

	// 20 MiB up + 80 MiB down at 0.5x = 52,428,800,000 units charged from node counters
	// alone: the client reported nothing.
	e.report(agent, usage{id, 1, 1, 20 * mib, 80 * mib, false}, 200)
	contractShape(t, "agent_usage_response", "response", e.lastUsage)
	sub := e.subscription(u.subID)
	if sub.UsedUnits != 52428800000 || sub.UploadBytes != 20*mib || sub.DownloadBytes != 80*mib {
		t.Fatalf("subscription after usage: %+v", sub)
	}
	if rep := e.report(agent, usage{id, 1, 1, 20 * mib, 80 * mib, false}, 200); rep["replayed"] != true {
		t.Fatalf("replay not detected: %v", rep)
	}
	if e.subscription(u.subID).UsedUnits != 52428800000 {
		t.Fatal("replayed report was charged twice")
	}
	e.report(agent, usage{id, 1, 1, 21 * mib, 80 * mib, false}, 409) // same sequence, different counters
	e.report(agent, usage{id, 1, 3, 30 * mib, 90 * mib, false}, 409) // gap
	e.report(agent, usage{id, 1, 2, 19 * mib, 80 * mib, false}, 409) // regression
	e.report(agent, usage{id, 2, 1, 1, 1, false}, 409)               // epoch the agent never announced
	e.report(agent, usage{id, 1, 2, 30 * mib, 90 * mib, false}, 200)
	// delta = 10 MiB up + 10 MiB down at 500 = 10,485,760,000 more.
	if used := e.subscription(u.subID).UsedUnits; used != 52428800000+20*mib*500 {
		t.Fatalf("used %d", used)
	}
	period := e.period(u.subID)
	g := e.grant(id)
	if period.UsedUnits != e.subscription(u.subID).UsedUnits || period.ReservedUnits != (g.BudgetBytes-g.ChargedBytes)*500 || g.ChargedBytes != 120*mib {
		t.Fatalf("period %+v grant %+v", period, g)
	}

	// The session reflects the server balance and the grant.
	status := e.client("GET", "/session", token, nil, nil)
	contractShapeV3(t, "session_node_reported", "response", status.Body.Bytes())
	var state nativeState
	_ = json.Unmarshal(status.Body.Bytes(), &state)
	if state.MeteringSource != "node_reported" || state.UsedUnits != e.subscription(u.subID).UsedUnits || state.GrantState != "ready" {
		t.Fatalf("session: %+v", state)
	}

	// Revocation: client asks, node removes, final usage, then confirmation.
	rev := e.client("POST", "/grants/"+id+"/revoke", token, nil, struct{}{})
	if rev.Code != 200 || !strings.Contains(rev.Body.String(), "revoking") {
		t.Fatalf("revoke: %d %s", rev.Code, rev.Body.String())
	}
	contractShape(t, "grant_revoke", "response", rev.Body.Bytes())
	if again := e.client("POST", "/grants/"+id+"/revoke", token, nil, struct{}{}); again.Code != 200 {
		t.Fatalf("revoke not idempotent: %d", again.Code)
	}
	if orders = e.sync(agent, []string{id}, nil); orders[id].Action != "revoke" {
		t.Fatalf("revoke order: %+v", orders[id])
	}
	contractOrders(t, e.lastSync)
	// Removal confirmed before the final cursor arrives is not accepted.
	e.sync(agent, nil, []string{id})
	if s := e.grant(id).State; s != "revoking" || e.grant(id).ReleasedAt != nil {
		t.Fatalf("released without final usage: %s", s)
	}
	e.report(agent, usage{id, 1, 3, 31 * mib, 91 * mib, true}, 200)
	e.report(agent, usage{id, 1, 4, 32 * mib, 92 * mib, false}, 409) // closed
	e.sync(agent, nil, []string{id})
	g = e.grant(id)
	if g.State != "revoked" || g.ReleasedAt == nil || g.NeedsReconcile {
		t.Fatalf("after confirmation: %+v", g)
	}
	if p := e.period(u.subID); p.ReservedUnits != 0 {
		t.Fatalf("reservation not released: %d", p.ReservedUnits)
	}
	end := e.client("GET", "/grants/"+id, token, nil, nil)
	if end.Code != 200 || !strings.Contains(end.Body.String(), `"revoked"`) || strings.Contains(end.Body.String(), "yaml") {
		t.Fatalf("revoked grant view: %d %s", end.Code, end.Body.String())
	}
	contractShape(t, "grant_revoked", "response", end.Body.Bytes())
	if used := e.subscription(u.subID).UsedUnits; used != (31+91)*mib*500 {
		t.Fatalf("final used %d", used)
	}
}

func TestAgentIdentityPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	u := e.newUser(1<<30, 2)
	nodeA, nodeB := e.addNode("A", 1000, true), e.addNode("B", 1000, true)
	agentA, agentB := e.addAgent(nodeA), e.addAgent(nodeB)
	e.sync(agentA, nil, nil)
	e.sync(agentB, nil, nil)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, nodeA.ID, "key-identity-1"))

	if w := e.agentCall(nil, "/sync", map[string]any{"epoch": 1}); w.Code != 401 {
		t.Fatalf("no certificate: %d", w.Code)
	}
	stranger := &testAgent{cert: newCertificate(t), epoch: 1}
	if w := e.agentCall(stranger, "/sync", map[string]any{"epoch": 1, "installed": []string{}, "removed": []string{}, "protocols": []string{}}); w.Code != 401 {
		t.Fatalf("unknown certificate: %d", w.Code)
	}
	// Agent B cannot see or bill node A's grant.
	if orders := e.sync(agentB, nil, nil); len(orders) != 0 {
		t.Fatalf("agent B received foreign orders: %+v", orders)
	}
	e.report(agentB, usage{id, 1, 1, 1, 1, false}, 404)
	if w := e.agentCall(agentB, "/sync", map[string]any{"epoch": 1, "installed": []string{id}, "removed": []string{}, "protocols": []string{"vless"}}); w.Code != 200 || !strings.Contains(w.Body.String(), `"revoke"`) {
		t.Fatalf("foreign grant held by B must be revoked: %s", w.Body.String())
	}
	// Invalid shapes.
	for name, body := range map[string]map[string]any{
		"bad id":    {"epoch": 1, "installed": []string{"nope"}, "removed": []string{}, "protocols": []string{}},
		"bad proto": {"epoch": 1, "installed": []string{}, "removed": []string{}, "protocols": []string{"wireguard"}},
		"no epoch":  {"installed": []string{}, "removed": []string{}, "protocols": []string{}},
	} {
		if w := e.agentCall(agentA, "/sync", body); w.Code != 400 {
			t.Fatalf("%s accepted: %d", name, w.Code)
		}
	}
	// Disabled agents lose access immediately.
	if err := e.db.Model(&model.NodeAgent{}).Where("id = ?", agentA.id).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if w := e.agentCall(agentA, "/sync", map[string]any{"epoch": 1, "installed": []string{}, "removed": []string{}, "protocols": []string{}}); w.Code != 401 {
		t.Fatalf("disabled agent served: %d", w.Code)
	}
	// An older process generation is refused once a newer one has been seen.
	agentB.epoch = 5
	e.sync(agentB, nil, nil)
	agentB.epoch = 4
	if w := e.agentCall(agentB, "/sync", map[string]any{"epoch": 4, "installed": []string{}, "removed": []string{}, "protocols": []string{}}); w.Code != 409 {
		t.Fatalf("stale epoch accepted: %d", w.Code)
	}
}

func TestEpochRestartAndReconcilePostgres(t *testing.T) {
	e := newGrantEnv(t, func(c *config.Config) { c.GrantSettleGrace = time.Millisecond })
	u := e.newUser(1<<30, 2)
	node := e.addNode("N", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, node.ID, "key-epoch-0001"))
	e.sync(agent, nil, nil)
	e.sync(agent, []string{id}, nil)
	e.report(agent, usage{id, 1, 1, 10 * mib, 10 * mib, false}, 200)

	// The agent restarts without sending a final report for epoch 1.
	agent.epoch = 2
	e.sync(agent, nil, nil)
	e.report(agent, usage{id, 2, 2, 1, 1, false}, 409) // a new epoch starts at sequence 1
	e.report(agent, usage{id, 2, 1, 4 * mib, 6 * mib, false}, 200)
	g := e.grant(id)
	if g.Epoch != 2 || g.BaseUpload != 10*mib || g.BaseDownload != 10*mib || !g.NeedsReconcile || g.ChargedBytes != 30*mib {
		t.Fatalf("epoch rollover not recorded as possible loss: %+v", g)
	}
	if used := e.subscription(u.subID).UsedUnits; used != 30*mib*1000 {
		t.Fatalf("used %d", used)
	}
	// Lower epochs can no longer report.
	e.report(agent, usage{id, 1, 2, 11 * mib, 11 * mib, false}, 409)
	// The new epoch's cap excludes bytes already charged to the old one.
	orders := e.sync(agent, []string{id}, nil)
	if want := 64*mib - 20*mib; orders[id].EpochBudgetBytes != want {
		t.Fatalf("epoch budget %d want %d", orders[id].EpochBudgetBytes, want)
	}

	// The node vanishes: nothing confirms. After lease + grace the budget returns.
	if err := e.db.Model(&model.ConnectionGrant{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	released, err := ReconcileGrants(t.Context(), e.db, e.cfg)
	if err != nil || released != 1 {
		t.Fatalf("reconcile: %d %v", released, err)
	}
	g = e.grant(id)
	if g.State != "expired" || g.ReleasedAt == nil || !g.NeedsReconcile {
		t.Fatalf("lapsed grant: %+v", g)
	}
	if p := e.period(u.subID); p.ReservedUnits != 0 {
		t.Fatalf("reserved %d", p.ReservedUnits)
	}
	// Late tail usage after release is still charged, never dropped.
	e.report(agent, usage{id, 2, 2, 5 * mib, 6 * mib, true}, 200)
	if used := e.subscription(u.subID).UsedUnits; used != 31*mib*1000 {
		t.Fatalf("tail usage lost: %d", used)
	}
	if again, _ := ReconcileGrants(t.Context(), e.db, e.cfg); again != 0 {
		t.Fatal("released grant reconciled twice")
	}
}

func TestGrantAuthorizationEndsPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	node := e.addNode("N", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)

	cases := map[string]func(u user){
		"subscription expired": func(u user) {
			e.db.Model(&model.Subscription{}).Where("id = ?", u.subID).Update("expires_at", time.Now().Add(-time.Second))
		},
		"account disabled": func(u user) { e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("status", "disabled") },
		"quota exhausted": func(u user) {
			e.db.Model(&model.Subscription{}).Where("id = ?", u.subID).Update("used_units", gorm.Expr("traffic_limit_bytes * 1000"))
		},
		"node disabled": func(user) { e.db.Model(&model.Node{}).Where("id = ?", node.ID).Update("enabled", false) },
		"session revoked": func(u user) {
			e.db.Model(&model.NativeSession{}).Where("user_id = ?", u.ID).Update("revoked_at", time.Now())
		},
		"session idle": func(u user) {
			e.db.Model(&model.NativeSession{}).Where("user_id = ?", u.ID).Update("last_seen_at", time.Now().Add(-time.Hour))
		},
	}
	for name, cut := range cases {
		t.Run(name, func(t *testing.T) {
			e.db.Model(&model.Node{}).Where("id = ?", node.ID).Update("enabled", true)
			u := e.newUser(1<<30, 2)
			token := e.login(u)
			id := grantID(t, e.createGrant(token, node.ID, "key-"+strings.ReplaceAll(name, " ", "-")+"-1"))
			e.sync(agent, nil, nil)
			if o := e.sync(agent, []string{id}, nil)[id]; o.Action != "keep" {
				t.Fatalf("healthy grant not kept: %+v", o)
			}
			cut(u)
			if o := e.sync(agent, []string{id}, nil)[id]; o.Action != "revoke" {
				t.Fatalf("%s: order %+v", name, o)
			}
			if st := e.grant(id).State; st != "revoking" {
				t.Fatalf("%s: state %s", name, st)
			}
			// Even then the node's tail usage is accepted and charged.
			e.report(agent, usage{id, 1, 1, mib, mib, true}, 200)
			e.sync(agent, nil, []string{id})
			if st := e.grant(id).State; st != "revoked" {
				t.Fatalf("%s: final state %s", name, st)
			}
		})
	}
}

func TestGrantBudgetPoolNeverOversubscribesPostgres(t *testing.T) {
	// The pool, not the online cap (D1 defaults to 2 devices), is under test here.
	e := newGrantEnv(t, func(c *config.Config) { c.GrantBudgetBytes, c.ClientMaxOnlineDevices = 2*mib, 8 })
	// 5 MiB at 1x with 2 MiB chunks: two grants fit fully, the third gets the rest.
	u := e.newUser(5*mib, 8)
	node := e.addNode("N", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		token := e.login(u)
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.createGrant(token, node.ID, fmt.Sprintf("key-pool-%04d", i)).Code
		}()
	}
	wg.Wait()
	accepted := 0
	for _, c := range codes {
		if c == 202 {
			accepted++
		} else if c != 403 && c != 500 && c != 503 {
			t.Fatalf("unexpected status %d", c)
		}
	}
	p := e.period(u.subID)
	var budgets, grants int64
	var all []model.ConnectionGrant
	e.db.Find(&all)
	for _, g := range all {
		budgets += g.BudgetBytes
		grants++
	}
	if accepted != 3 || grants != 3 || budgets != 5*mib || p.ReservedUnits != 5*mib*1000 {
		t.Fatalf("accepted=%d grants=%d budgets=%d reserved=%d (codes %v)", accepted, grants, budgets, p.ReservedUnits, codes)
	}
	if p.UsedUnits+p.ReservedUnits > p.LimitBytes*1000 {
		t.Fatalf("pool oversubscribed: %+v", p)
	}
	// Spending frees nothing until reported, and reported usage converts reservation to use.
	var g model.ConnectionGrant
	e.db.Order("created_at").First(&g)
	e.sync(agent, nil, nil)
	e.sync(agent, []string{g.ID}, nil)
	e.report(agent, usage{g.ID, 1, 1, mib / 2, mib / 2, false}, 200)
	p = e.period(u.subID)
	if p.UsedUnits+p.ReservedUnits != 5*mib*1000 {
		t.Fatalf("used+reserved must stay constant while spending reserved budget: %+v", p)
	}
}

func TestPeriodIsolationPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	u := e.newUser(1<<30, 2)
	node := e.addNode("N", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	token := e.login(u)
	id := grantID(t, e.createGrant(token, node.ID, "key-period-0001"))
	e.sync(agent, nil, nil)
	e.sync(agent, []string{id}, nil)
	e.report(agent, usage{id, 1, 1, mib, mib, false}, 200)

	// The subscription is renewed into a new period (same row, new start, fresh balance).
	if err := e.db.Model(&model.Subscription{}).Where("id = ?", u.subID).Updates(map[string]any{"starts_at": time.Now().Add(-time.Minute), "used_units": 0, "upload_bytes": 0, "download_bytes": 0}).Error; err != nil {
		t.Fatal(err)
	}
	if o := e.sync(agent, []string{id}, nil)[id]; o.Action != "revoke" {
		t.Fatalf("a grant from the old period must be revoked: %+v", o)
	}
	// Tail usage from the old period lands on the old period only.
	e.report(agent, usage{id, 1, 2, 3 * mib, 3 * mib, true}, 200)
	if sub := e.subscription(u.subID); sub.UsedUnits != 0 {
		t.Fatalf("old-period usage polluted the new period: %d", sub.UsedUnits)
	}
	var old model.SubscriptionPeriod
	if err := e.db.First(&old, "id = ?", e.grant(id).PeriodID).Error; err != nil || old.UsedUnits != 6*mib*1000 {
		t.Fatalf("old period: %+v %v", old, err)
	}
	e.sync(agent, nil, []string{id})
	if err := e.db.First(&old, "id = ?", old.ID).Error; err != nil || old.ReservedUnits != 0 {
		t.Fatalf("old period reservation: %+v", old)
	}
}

func TestProductionGuardsPostgres(t *testing.T) {
	e := newGrantEnv(t, func(c *config.Config) { c.Env = "production" })
	u := e.newUser(1<<30, 2)
	governed := e.addNode("Governed", 1000, true)
	e.addNode("Shared-only", 1000, true) // no agent: must never be delivered in production
	agent := e.addAgent(governed)
	e.sync(agent, nil, nil)
	token := e.login(u)

	if w := e.client("GET", "/config?node_id="+governed.ID+"&last_sequence=0", token, nil, nil); w.Code != 426 || errorCode(w) != "client_upgrade_required" {
		t.Fatalf("/config in production: %d %s", w.Code, w.Body.String())
	}
	if w := e.client("POST", "/traffic", token, nil, map[string]int64{"sequence": 1, "upload_bytes": 1, "download_bytes": 1}); w.Code != 426 {
		t.Fatalf("/traffic in production: %d", w.Code)
	}
	w := e.client("GET", "/nodes", token, nil, nil)
	var catalog struct {
		Nodes []nodeChoice `json:"nodes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &catalog)
	if len(catalog.Nodes) != 1 || catalog.Nodes[0].ID != governed.ID || catalog.Nodes[0].MeteringSource != "node_reported" {
		t.Fatalf("production catalog: %s", w.Body.String())
	}
	caps := e.client("GET", "/capabilities", "", nil, nil)
	contractShape(t, "capabilities", "response", caps.Body.Bytes())
	if caps.Code != 200 || !strings.Contains(caps.Body.String(), `"metering_modes":["node_reported"]`) {
		t.Fatalf("capabilities: %s", caps.Body.String())
	}
	if w = e.createGrant(token, governed.ID, "key-production-1"); w.Code != 202 {
		t.Fatalf("grant in production: %d %s", w.Code, w.Body.String())
	}
	// Development exposes both modes and does not require an agent for shared nodes.
	dev := newGrantEnv(t, nil)
	if c := dev.client("GET", "/capabilities", "", nil, nil); !strings.Contains(c.Body.String(), `"metering_modes":["client_reported","node_reported"]`) {
		t.Fatalf("dev capabilities: %s", c.Body.String())
	}
}

func TestMinAppVersionPostgres(t *testing.T) {
	e := newGrantEnv(t, func(c *config.Config) { c.ClientMinAppVersion = "1.2.0" })
	u := e.newUser(1<<30, 2)
	node := e.addNode("N", 1000, true)
	agent := e.addAgent(node)
	e.sync(agent, nil, nil)
	login := func(version string) string {
		w := e.client("POST", "/login", "", nil, map[string]any{"email": u.Email, "password": "Native-Test-Password!", "device_id": uuid.NewString(), "platform": "macos", "app_version": version})
		var out struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Token
	}
	if w := e.createGrant(login("1.1.9"), node.ID, "key-version-001"); w.Code != 426 || errorCode(w) != "client_upgrade_required" {
		t.Fatalf("old client: %d %s", w.Code, w.Body.String())
	}
	if w := e.createGrant(login("1.2.0"), node.ID, "key-version-002"); w.Code != 202 {
		t.Fatalf("current client: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminAgentManagementPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	hash, _ := bcrypt.GenerateFromPassword([]byte("Admin-Test-Password!"), bcrypt.MinCost)
	login := func(role string) []*http.Cookie {
		u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "admin", Role: role, Status: "active", PasswordHash: string(hash)}
		if err := e.db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(loginInput{Email: u.Email, Password: "Admin-Test-Password!"})
		r := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(data))
		r.Header.Set("Origin", e.cfg.Origins[0])
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, r)
		return w.Result().Cookies()
	}
	call := func(cookies []*http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(data))
		r.Header.Set("Origin", e.cfg.Origins[0])
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, r)
		return w
	}
	admin, customer := login("admin"), login("user")
	node := e.addNode("N", 1000, true)
	cert := newCertificate(t)
	sum := sha256.Sum256(cert.Raw)
	fingerprint := hex.EncodeToString(sum[:])
	body := map[string]any{"node_id": node.ID, "name": "hk-agent", "cert_fingerprint": strings.ToUpper(fingerprint[:2]) + ":" + fingerprint[2:], "enabled": true}

	if w := call(customer, "POST", "/node-agents", body); w.Code != 403 {
		t.Fatalf("customer registered an agent: %d", w.Code)
	}
	w := call(admin, "POST", "/node-agents", body)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Agent model.NodeAgent `json:"agent"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Agent.CertFingerprint != fingerprint {
		t.Fatalf("fingerprint not normalised: %s", created.Agent.CertFingerprint)
	}
	if dup := call(admin, "POST", "/node-agents", body); dup.Code != 409 {
		t.Fatalf("second agent for one node/cert: %d %s", dup.Code, dup.Body.String())
	}
	if bad := call(admin, "POST", "/node-agents", map[string]any{"node_id": node.ID, "name": "x", "cert_fingerprint": "zz", "enabled": true}); bad.Code != 400 {
		t.Fatalf("bad fingerprint: %d", bad.Code)
	}
	agent := &testAgent{cert: cert, id: created.Agent.ID, epoch: 1}
	e.sync(agent, nil, nil)
	list := call(admin, "GET", "/node-agents", nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"online":true`) || !strings.Contains(list.Body.String(), `"vless"`) || strings.Contains(list.Body.String(), "ciphertext") {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	if upd := call(admin, "POST", "/node-agents/"+created.Agent.ID, map[string]any{"name": "hk-agent", "cert_fingerprint": fingerprint, "enabled": false}); upd.Code != 200 {
		t.Fatalf("update: %d %s", upd.Code, upd.Body.String())
	}
	if w := e.agentCall(agent, "/sync", map[string]any{"epoch": 1, "installed": []string{}, "removed": []string{}, "protocols": []string{}}); w.Code != 401 {
		t.Fatalf("disabled agent still authenticates: %d", w.Code)
	}
	if del := call(admin, "DELETE", "/node-agents/"+created.Agent.ID, nil); del.Code != 200 {
		t.Fatalf("delete: %d", del.Code)
	}
	if del := call(admin, "DELETE", "/node-agents/"+created.Agent.ID, nil); del.Code != 404 {
		t.Fatalf("second delete: %d", del.Code)
	}
}
