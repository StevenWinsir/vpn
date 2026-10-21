package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// Round 3 harness: groups with several servers, fast sessions (no bcrypt per user),
// an administrator and direct lease fixtures, all on the presence clock.

var serverPort atomic.Int64

func init() { serverPort.Store(20000) }

// group creates an enabled client_reported group visible to plans ("[]": every plan).
func (e *presenceEnv) group(name, plans string) model.NodeGroup {
	e.t.Helper()
	g := model.NodeGroup{ID: uuid.NewString(), Name: name, Region: "JP", LineType: "direct", RatePermille: 1000, PlanIDs: plans, Enabled: true, Version: 1}
	if err := e.db.Create(&g).Error; err != nil {
		e.t.Fatal(err)
	}
	return g
}

// serverIn adds an enabled server to g whose YAML proxy is named proxyName (servers of
// one group may share it) and whose port is unique, so a profile identifies it.
func (e *presenceEnv) serverIn(g model.NodeGroup, proxyName string, capacity int) model.Node {
	e.t.Helper()
	node := model.Node{ID: uuid.NewString(), Name: proxyName, Region: g.Region, LineType: g.LineType, RatePermille: g.RatePermille, Enabled: true, Version: 1, GroupID: &g.ID, MaxConnections: capacity}
	if err := e.db.Omit("Group").Create(&node).Error; err != nil {
		e.t.Fatal(err)
	}
	yaml := fmt.Sprintf("proxies:\n  - {name: %s, type: ss, server: 127.0.0.1, port: %d, cipher: aes-256-gcm, password: group-secret}\n", proxyName, serverPort.Add(1))
	sealed, err := nodes.Encrypt(e.cfg.NodeEncryptionKey, node.ID, []byte(yaml))
	if err != nil {
		e.t.Fatal(err)
	}
	if err = e.db.Omit("Node").Create(&model.NodeConfig{NodeID: node.ID, Ciphertext: sealed, PlanIDs: g.PlanIDs}).Error; err != nil {
		e.t.Fatal(err)
	}
	return node
}

// servers returns n servers of one group, sorted by id (the allocator's order).
func (e *presenceEnv) servers(g model.NodeGroup, capacities ...int) []model.Node {
	out := []model.Node{}
	for _, c := range capacities {
		out = append(out, e.serverIn(g, g.Name, c))
	}
	slices.SortFunc(out, func(a, b model.Node) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return out
}

// session signs a device in without a password round trip (bcrypt would dominate the
// concurrency tests); the row is what POST /client/login writes.
func (e *presenceEnv) session(u presenceUser, device string) string {
	e.t.Helper()
	token, _ := randomToken()
	now := e.now()
	sess := model.NativeSession{ID: uuid.NewString(), UserID: u.ID, TokenHash: refreshHash(token), DeviceID: device, Platform: "android", AppVersion: "2.0.0", LastSeenAt: now, ExpiresAt: now.Add(12 * time.Hour)}
	if err := e.db.Omit("User").Create(&sess).Error; err != nil {
		e.t.Fatal(err)
	}
	return token
}

// device is a fresh user with one signed-in device.
func (e *presenceEnv) device() (presenceUser, string) {
	u := e.user(5)
	return u, e.session(u, uuid.NewString())
}

// admit connects token to group (a new idempotency key each time) and returns the
// response.
func (e *presenceEnv) admit(token, group string) *httptest.ResponseRecorder {
	return e.connect(token, group, "k-"+uuid.NewString())
}

// admitted connects and returns the chosen server, failing on a refusal.
func (e *presenceEnv) admitted(token, group string) connectionResponse {
	e.t.Helper()
	w := e.admit(token, group)
	e.want(w, 201, "")
	return e.connection(w)
}

func (e *presenceEnv) release(token, id, reason string) *httptest.ResponseRecorder {
	return e.native("POST", "/connections/"+id+"/release", token, map[string]string{"reason": reason})
}

// loads counts live leases per server exactly as admission does.
func (e *presenceEnv) loads() map[string]int64 {
	e.t.Helper()
	s := &Server{cfg: e.cfg}
	out, err := s.liveLoad(e.db, e.now())
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// fill puts n other users' live leases on a server.
func (e *presenceEnv) fill(server model.Node, n int) {
	e.t.Helper()
	for range n {
		u := e.user(5)
		device := uuid.NewString()
		token := e.session(u, device)
		var sess model.NativeSession
		e.db.Take(&sess, "token_hash = ?", refreshHash(token))
		now := e.now()
		lease := model.ConnectionLease{ID: uuid.NewString(), UserID: u.ID, SessionID: sess.ID, DeviceID: device, Platform: "android", AppVersion: "2.0.0", GroupID: *server.GroupID, NodeID: server.ID,
			Method: model.LeaseByConnections, State: model.LeaseActive, AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(e.cfg.Lease())}
		if err := e.db.Omit("User", "Session").Create(&lease).Error; err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *presenceEnv) assignment(userID, groupID string) string {
	var a model.NodeAssignment
	if e.db.Take(&a, "user_id = ? AND group_id = ?", userID, groupID).Error != nil {
		return ""
	}
	return a.NodeID
}

func (e *presenceEnv) nodeRow(id string) model.Node {
	e.t.Helper()
	var n model.Node
	if err := e.db.Take(&n, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return n
}

// admin returns the cookie of a signed-in administrator.
func (e *presenceEnv) admin() (model.User, string) {
	e.t.Helper()
	u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "admin", PasswordHash: presenceHash(), Role: "admin", Status: "active"}
	if err := e.db.Create(&u).Error; err != nil {
		e.t.Fatal(err)
	}
	return u, e.web(presenceUser{User: u})
}

func (e *presenceEnv) adminCall(cookie, method, path string, body any) *httptest.ResponseRecorder {
	return e.do(presenceCall{method: method, path: path, token: cookie, body: body})
}

// editServer saves a server through the grouped admin shape with its current YAML
// unless yaml is given.
func (e *presenceEnv) editServer(cookie string, n model.Node, yaml string, capacity int, label string, enabled bool) *httptest.ResponseRecorder {
	e.t.Helper()
	current := e.nodeRow(n.ID)
	if yaml == "" {
		var secret model.NodeConfig
		e.db.Take(&secret, "node_id = ?", n.ID)
		data, err := nodes.Decrypt(e.cfg.NodeEncryptionKey, n.ID, secret.Ciphertext)
		if err != nil {
			e.t.Fatal(err)
		}
		yaml = string(data)
	}
	return e.adminCall(cookie, "POST", "/admin/nodes/"+n.ID, map[string]any{"yaml": yaml, "group_id": *current.GroupID, "max_connections": capacity, "label": label, "enabled": enabled, "version": current.Version})
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %d %s: %v", w.Code, w.Body.String(), err)
	}
	return out
}

// catalog reads /client/nodes once (no long poll).
func (e *presenceEnv) catalog(token string) nativeCatalog {
	e.t.Helper()
	w := e.native("GET", "/nodes", token, nil)
	e.want(w, 200, "")
	return decodeBody[nativeCatalog](e.t, w)
}

func (e *presenceEnv) state(token string) nativeState {
	e.t.Helper()
	w := e.native("GET", "/session", token, nil)
	e.want(w, 200, "")
	return snapshotOf(e.t, w)
}
