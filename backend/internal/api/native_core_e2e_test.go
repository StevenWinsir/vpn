package api

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// coreProc drives the real asterlink-core binary over its framed stdin/stdout protocol.
type coreProc struct {
	t      *testing.T
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    io.Reader
	seq    int
	apiURL string
	device string
	all    bytes.Buffer // every byte the Core ever sent, scanned for secrets
	gen    uint64
}

func startCore(t *testing.T, bin, apiURL string) *coreProc {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = []string{}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &coreProc{t: t, cmd: cmd, in: in, out: out, apiURL: apiURL, device: uuid.NewString()}
	t.Cleanup(func() { c.close() })
	return c
}

func (c *coreProc) close() {
	if c.cmd.ProcessState != nil {
		return
	}
	_ = c.in.Close()
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}

type coreReply struct {
	OK     bool                            `json:"ok"`
	Error  *struct{ Code, Message string } `json:"error"`
	Result json.RawMessage                 `json:"result"`
}

type snapshotView struct {
	Generation  uint64                        `json:"generation"`
	State       string                        `json:"state"`
	User        *struct{ Email, Role string } `json:"user"`
	Entitlement *struct {
		AccountAuthorized bool   `json:"account_authorized"`
		CanConnect        bool   `json:"can_connect"`
		Reason            string `json:"reason"`
		PlanName          string `json:"plan_name"`
	} `json:"entitlement"`
	Nodes []struct {
		ID, Name, Region string
		LineType         string `json:"line_type"`
		RatePermille     int64  `json:"rate_permille"`
	} `json:"nodes"`
	SelectedNodeID  string                          `json:"selected_node_id"`
	CatalogRevision string                          `json:"catalog_revision"`
	ConfigReady     bool                            `json:"config_ready"`
	LastError       *struct{ Code, Message string } `json:"last_error"`
	ActionError     *struct{ Code, Message string } `json:"action_error"`
}

func (c *coreProc) call(command string, generation uint64, payload any) (coreReply, snapshotView) {
	c.t.Helper()
	c.seq++
	body := map[string]any{"request_id": fmt.Sprintf("e2e-%d", c.seq), "protocol_version": 1, "generation": generation, "command": command}
	if payload != nil {
		body["payload"] = payload
	}
	data, _ := json.Marshal(body)
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	if _, err := c.in.Write(frame); err != nil {
		c.t.Fatal(err)
	}
	var header [4]byte
	if _, err := io.ReadFull(c.out, header[:]); err != nil {
		c.t.Fatal(err)
	}
	raw := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(c.out, raw); err != nil {
		c.t.Fatal(err)
	}
	c.all.Write(raw)
	var reply coreReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		c.t.Fatal(err)
	}
	var snap snapshotView
	if reply.OK {
		_ = json.Unmarshal(reply.Result, &snap)
		c.gen = snap.Generation
	}
	return reply, snap
}

func (c *coreProc) login(email, password string) snapshotView {
	c.t.Helper()
	reply, snap := c.call("login", 0, map[string]string{"email": email, "password": password, "api_base": c.apiURL + "/api/v1/client", "channel": "development", "device_id": c.device, "app_version": "e2e"})
	if !reply.OK {
		c.t.Fatalf("login refused: %+v", reply.Error)
	}
	return snap
}

func (c *coreProc) snapshot() snapshotView {
	_, snap := c.call("read_snapshot", 0, nil)
	return snap
}

func (c *coreProc) waitSnapshot(what string, within time.Duration, ok func(snapshotView) bool) (snapshotView, time.Duration) {
	c.t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if snap := c.snapshot(); ok(snap) {
			return snap, time.Since(start)
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.t.Fatalf("timed out after %v waiting for %s: %+v", within, what, c.snapshot())
	return snapshotView{}, 0
}

const (
	e2ePrivateHost   = "metadata-private.example.invalid"
	e2ePrivateSecret = "never-reaches-the-ui"
	e2eVlessUUID     = "9b17fe2c-6070-41ca-908b-02d69ab01efc"
)

// nativeWorld is a seeded database: an administrator without a plan (like test@test.com),
// users on different plans, and two real nodes created through the admin API.
type nativeWorld struct {
	password                           string
	admin, vip, basic, expiring, broke model.User
	dedicated, ordinary                adminNode
	save                               func(id string, in nodeInput) adminNode
}

func seedNativeWorld(t *testing.T, db *gorm.DB, cfg config.Config, router http.Handler) *nativeWorld {
	password := "E2E-Only-Password!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	makeUser := func(role, label string) model.User {
		u := model.User{ID: uuid.NewString(), Email: label + "-" + uuid.NewString()[:8] + "@example.invalid", Name: label, PasswordHash: string(hash), Role: role, Status: "active"}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}
	subscribe := func(u model.User, quota int64, devices int, dedicated bool) model.Subscription {
		now := time.Now().UTC().Truncate(time.Microsecond)
		sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: quota, MaxDevices: devices, AllowDedicated: dedicated}
		if err := db.Create(&sub).Error; err != nil {
			t.Fatal(err)
		}
		return sub
	}
	admin := makeUser("admin", "admin") // like test@test.com: administrator, no plan
	vip := makeUser("user", "vip")
	subscribe(vip, 10<<30, 1, true)
	basic := makeUser("user", "basic")
	subscribe(basic, 10<<30, 2, false)
	expiring := makeUser("user", "expiring")
	subscribe(expiring, 10<<30, 2, true)
	broke := makeUser("user", "broke")
	subscribe(broke, 1, 2, true)
	db.Model(&model.Subscription{}).Where("user_id = ?", broke.ID).Update("used_units", 1000)

	// Administrator HTTP session (cookie) for node management, as in the web console.
	webCall := func(method, path string, cookies []*http.Cookie, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Origin", cfg.Origins[0])
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := webCall("POST", "/auth/login", nil, loginInput{Email: admin.Email, Password: password})
	if w.Code != 200 {
		t.Fatalf("admin web login %d", w.Code)
	}
	adminCookies := w.Result().Cookies()
	const privateHost, privateSecret, vlessUUID = e2ePrivateHost, e2ePrivateSecret, e2eVlessUUID
	save := func(id string, in nodeInput) adminNode {
		t.Helper()
		path, status := "/admin/nodes", 201
		if id != "" {
			path, status = path+"/"+id, 200
		}
		w := webCall("POST", path, adminCookies, in)
		if w.Code != status {
			t.Fatalf("save node %d: %s", w.Code, w.Body.String())
		}
		var out struct {
			Nodes []adminNode `json:"nodes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Nodes[0]
	}
	ssYAML := func(name string) string {
		return fmt.Sprintf("proxies: [{name: %s, type: ss, server: %s, port: 31000, cipher: aes-256-gcm, password: %s, udp: true}]", name, privateHost, privateSecret)
	}
	dedicated := save("", nodeInput{YAML: ssYAML("HK-IEPL-01"), Region: "HK", LineType: "dedicated", RatePermille: 1000, Enabled: true, PlanIDs: []string{}})
	ordinary := save("", nodeInput{YAML: fmt.Sprintf("proxies: [{name: JP-Direct-01, type: vless, server: %s, port: 443, uuid: %s, tls: true, servername: %s, network: tcp, udp: true}]", privateHost, vlessUUID, privateHost), Region: "JP", LineType: "direct", RatePermille: 500, Enabled: true, PlanIDs: []string{}})

	return &nativeWorld{password: password, admin: admin, vip: vip, basic: basic, expiring: expiring, broke: broke, dedicated: dedicated, ordinary: ordinary, save: save}
}

// TestNativeCoreEndToEnd runs the real Core binary against the real API and PostgreSQL:
// stage 2 acceptance (login, plan-filtered nodes, admin edits, entitlement loss, device limit).
// scripts/test-native.py builds the Core and sets ASTERLINK_CORE_BIN.
func TestNativeCoreEndToEnd(t *testing.T) {
	bin := os.Getenv("ASTERLINK_CORE_BIN")
	if bin == "" {
		t.Skip("set ASTERLINK_CORE_BIN (built by scripts/test-native.py)")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	cfg.Origins = []string{"http://127.0.0.1:3000"}
	router := New(db, cfg)
	server := httptest.NewServer(router)
	defer server.Close()
	world := seedNativeWorld(t, db, cfg, router)
	password, admin, vip, basic, expiring, broke := world.password, world.admin, world.vip, world.basic, world.expiring, world.broke
	dedicated, ordinary, save := world.dedicated, world.ordinary, world.save
	_ = broke
	const privateHost, privateSecret, vlessUUID = e2ePrivateHost, e2ePrivateSecret, e2eVlessUUID

	t.Run("vip_login_lists_plan_nodes_and_never_leaks", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		snap := core.login(vip.Email, password)
		if snap.State != "ready" || snap.User == nil || snap.User.Email != vip.Email {
			t.Fatalf("login: %+v", snap)
		}
		snap, _ = core.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 2 })
		byName := map[string]int64{}
		for _, n := range snap.Nodes {
			byName[n.Name] = n.RatePermille
		}
		if byName["HK-IEPL-01"] != 1000 || byName["JP-Direct-01"] != 500 {
			t.Fatalf("catalog metadata wrong: %+v", snap.Nodes)
		}
		reply, selected := core.call("select_node", core.gen, map[string]string{"node_id": dedicated.ID, "catalog_revision": snap.CatalogRevision})
		if !reply.OK || selected.ActionError != nil || !selected.ConfigReady || selected.SelectedNodeID != dedicated.ID {
			t.Fatalf("select: %+v %+v", reply.Error, selected)
		}
		_, connected := core.call("connect", core.gen, nil)
		if connected.ActionError == nil || connected.ActionError.Code != "tun_unavailable" || connected.State == "connected" {
			t.Fatalf("stage 2 must stop at the engine boundary: %+v", connected)
		}
		// VLESS too: select the ordinary line.
		_, vless := core.call("select_node", core.gen, map[string]string{"node_id": ordinary.ID, "catalog_revision": connected.CatalogRevision})
		if vless.ActionError != nil || !vless.ConfigReady {
			t.Fatalf("vless node not accepted: %+v", vless.ActionError)
		}
		for _, secret := range []string{privateHost, privateSecret, vlessUUID, "31000", "aes-256-gcm", password} {
			if bytes.Contains(core.all.Bytes(), []byte(secret)) {
				t.Fatalf("Core output leaked %q", secret)
			}
		}
	})

	t.Run("plan_without_dedicated_sees_only_ordinary", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		core.login(basic.Email, password)
		snap, _ := core.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) > 0 })
		if len(snap.Nodes) != 1 || snap.Nodes[0].Name != "JP-Direct-01" {
			t.Fatalf("dedicated line visible to a plan without it: %+v", snap.Nodes)
		}
	})

	t.Run("admin_without_plan_cannot_proxy", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		snap := core.login(admin.Email, password)
		if snap.User.Role != "admin" {
			t.Fatal("role not surfaced")
		}
		snap, _ = core.waitSnapshot("blocked", 6*time.Second, func(s snapshotView) bool { return s.State == "blocked" })
		if len(snap.Nodes) != 0 || snap.Entitlement.AccountAuthorized || snap.Entitlement.Reason != "upgrade_required" {
			t.Fatalf("admin role bypassed the plan: %+v", snap)
		}
		reply, after := core.call("connect", core.gen, nil)
		if !reply.OK || after.ActionError == nil || after.ActionError.Code == "tun_unavailable" {
			t.Fatalf("connect for planless admin: %+v", after.ActionError)
		}
	})

	t.Run("wrong_password_and_zero_quota", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		reply, snap := core.call("login", 0, map[string]string{"email": vip.Email, "password": "nope", "api_base": server.URL + "/api/v1/client", "channel": "development", "device_id": core.device, "app_version": "e2e"})
		if !reply.OK || snap.ActionError == nil || snap.ActionError.Code != "invalid_credentials" || snap.State != "signed_out" {
			t.Fatalf("wrong password: %+v", snap)
		}
		core.login(broke.Email, password)
		snap, _ = core.waitSnapshot("quota", 6*time.Second, func(s snapshotView) bool { return s.Entitlement != nil && s.Entitlement.Reason == "quota_exhausted" })
		if snap.Entitlement.AccountAuthorized || len(snap.Nodes) != 0 {
			t.Fatalf("zero quota still offered nodes: %+v", snap)
		}
	})

	t.Run("device_limit", func(t *testing.T) {
		first := startCore(t, bin, server.URL)
		first.login(vip.Email, password)
		snap, _ := first.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 2 })
		if _, s := first.call("select_node", first.gen, map[string]string{"node_id": dedicated.ID, "catalog_revision": snap.CatalogRevision}); s.ActionError != nil {
			t.Fatalf("first device: %+v", s.ActionError)
		}
		second := startCore(t, bin, server.URL)
		second.login(vip.Email, password)
		snap, _ = second.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 2 })
		_, s := second.call("select_node", second.gen, map[string]string{"node_id": dedicated.ID, "catalog_revision": snap.CatalogRevision})
		if s.ActionError == nil || s.ActionError.Code != "device_limit" || !strings.Contains(s.ActionError.Message, "设备上限") {
			t.Fatalf("second device (plan allows 1): %+v", s.ActionError)
		}
	})

	t.Run("admin_edits_reach_the_client_within_five_seconds", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		core.login(basic.Email, password)
		snap, _ := core.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 1 })
		if _, s := core.call("select_node", core.gen, map[string]string{"node_id": ordinary.ID, "catalog_revision": snap.CatalogRevision}); s.ActionError != nil || !s.ConfigReady {
			t.Fatalf("select: %+v", s.ActionError)
		}
		// Rename + change the multiplier from 0.5x to 1.5x.
		edited := nodeInput{YAML: fmt.Sprintf("proxies: [{name: JP-Direct-Renamed, type: vless, server: %s, port: 443, uuid: %s, tls: true, servername: %s, network: tcp, udp: true}]", privateHost, vlessUUID, privateHost), Region: "JP", LineType: "direct", RatePermille: 1500, Enabled: true, PlanIDs: []string{}, Version: ordinary.Version}
		changedAt := time.Now()
		ordinary = save(ordinary.ID, edited)
		snap, _ = core.waitSnapshot("rename + rate", 5*time.Second, func(s snapshotView) bool {
			return len(s.Nodes) == 1 && s.Nodes[0].Name == "JP-Direct-Renamed" && s.Nodes[0].RatePermille == 1500
		})
		t.Logf("admin rename+rate observed by the client after %v", time.Since(changedAt))
		if snap.ConfigReady {
			t.Fatal("stale config kept after the node changed")
		}
		// The very next connect re-validates against the new version before any engine call.
		_, again := core.call("connect", core.gen, nil)
		if again.ActionError == nil || again.ActionError.Code != "tun_unavailable" {
			t.Fatalf("connect after edit should re-bind and reach the engine boundary: %+v", again.ActionError)
		}
		// Disable the node: it disappears and the selection is cleared.
		edited.Enabled, edited.Version = false, ordinary.Version
		changedAt = time.Now()
		save(ordinary.ID, edited)
		snap, _ = core.waitSnapshot("disable", 5*time.Second, func(s snapshotView) bool { return len(s.Nodes) == 0 })
		t.Logf("admin disable observed by the client after %v", time.Since(changedAt))
		if snap.SelectedNodeID != "" || snap.ConfigReady {
			t.Fatalf("disabled node still selected: %+v", snap)
		}
	})

	t.Run("expired_subscription_stops_authorization", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		core.login(expiring.Email, password)
		snap, _ := core.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) > 0 })
		if _, s := core.call("select_node", core.gen, map[string]string{"node_id": dedicated.ID, "catalog_revision": snap.CatalogRevision}); s.ActionError != nil {
			t.Fatalf("%+v", s.ActionError)
		}
		db.Model(&model.Subscription{}).Where("user_id = ?", expiring.ID).Update("expires_at", time.Now().Add(-time.Second))
		started := time.Now()
		snap, _ = core.waitSnapshot("entitlement loss", 8*time.Second, func(s snapshotView) bool {
			return len(s.Nodes) == 0 && s.Entitlement != nil && !s.Entitlement.AccountAuthorized
		})
		t.Logf("expiry observed by the client after %v (reason %s)", time.Since(started), snap.Entitlement.Reason)
		_, after := core.call("connect", core.gen, nil)
		if after.ActionError == nil || after.ActionError.Code == "tun_unavailable" {
			t.Fatalf("connect allowed after expiry: %+v", after.ActionError)
		}
	})

	t.Run("revoked_session_ends_locally_and_logout_revokes", func(t *testing.T) {
		core := startCore(t, bin, server.URL)
		core.login(vip.Email, password)
		core.waitSnapshot("catalog", 6*time.Second, func(s snapshotView) bool { return len(s.Nodes) > 0 })
		db.Model(&model.NativeSession{}).Where("user_id = ?", vip.ID).Update("revoked_at", time.Now())
		snap, _ := core.waitSnapshot("sign-out after revocation", 8*time.Second, func(s snapshotView) bool { return s.State == "signed_out" })
		if snap.User != nil || len(snap.Nodes) != 0 || snap.LastError == nil || !strings.HasPrefix(snap.LastError.Code, "native_session_") {
			t.Fatalf("identity survived revocation: %+v", snap)
		}
		// A fresh login, then explicit logout, must revoke server-side.
		core.login(vip.Email, password)
		core.call("logout", 0, nil)
		var live int64
		db.Model(&model.NativeSession{}).Where("user_id = ? AND revoked_at IS NULL", vip.ID).Count(&live)
		if live != 0 {
			t.Fatalf("%d sessions still live after logout", live)
		}
	})
}

// TestNativeDevFixture keeps a seeded API running on a loopback port so the macOS app can
// be driven by hand or by UI automation. Only runs under ASTERLINK_DEV_FIXTURE_DIR.
func TestNativeDevFixture(t *testing.T) {
	dir := os.Getenv("ASTERLINK_DEV_FIXTURE_DIR")
	if dir == "" || !strings.HasPrefix(dir, "/tmp/") {
		t.Skip("manual fixture: set ASTERLINK_DEV_FIXTURE_DIR=/tmp/...")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	cfg.Origins = []string{"http://127.0.0.1:3000"}
	router := New(db, cfg)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(router)
	server.Listener = listener
	server.Start()
	defer server.Close()
	world := seedNativeWorld(t, db, cfg, router)
	info, _ := json.Marshal(map[string]any{"api": server.URL + "/api/v1/client", "password": world.password, "admin": world.admin.Email, "vip": world.vip.Email, "basic": world.basic.Email, "broke": world.broke.Email, "dedicated_id": world.dedicated.ID, "ordinary_id": world.ordinary.ID})
	if err := os.WriteFile(dir+"/fixture.json", info, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(30 * time.Minute)
	for {
		select {
		case <-deadline:
			return
		case <-time.After(200 * time.Millisecond):
			if _, err := os.Stat(dir + "/stop"); err == nil {
				return
			}
		}
	}
}
