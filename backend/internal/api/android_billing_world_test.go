package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// TestAndroidBillingWorld serves a seeded world for the Android connection and billing
// acceptance (scripts/test-native-android.py --suite billing, handoff_app_android.md round 4:
// B01, B02, A04, B04; round 5 reuses it for --suite lifecycle). It is skipped unless the suite names an output file in
// ASTERLINK_ANDROID_BILLING_WORLD, and it only ever listens on 127.0.0.1 (the emulator reaches
// it through `adb reverse`).
//
// The lines point at the suite's controlled test node (cmd/asterlink-testnode: pinned Mihomo
// as Shadowsocks and VLESS servers on the host loopback, reached from the emulator at
// ASTERLINK_ANDROID_NODE_HOST, normally 10.0.2.2). With ASTERLINK_NODEAGENT_BIN it also runs
// the real node agent over real mTLS (the e2eNode of agent_e2e_test.go) behind one
// node-reported line, for B04.
//
// Besides the real /api/v1 router it answers loopback-only control calls under /__world/:
// what an administrator does (re-rate, disable a line, end a plan, lower a quota, and since node
// groups round 6 the presence calls of presence_world_test.go), the server's
// own ledger, the sink's independent byte counts, and a one-shot "lost acknowledgement": the
// next /traffic report is committed by the real handler and then its reply is thrown away by
// closing the connection, which is exactly what a lost ACK looks like to the client. That
// injection lives here, in a test, so no build of the app carries a switch for it.
func TestAndroidBillingWorld(t *testing.T) {
	out := os.Getenv("ASTERLINK_ANDROID_BILLING_WORLD")
	if out == "" {
		t.Skip("set by scripts/test-native-android.py --suite billing")
	}
	// Bound before the node agent and the presence world open listeners of their own.
	listener := listenWorld(t, os.Getenv("ASTERLINK_ANDROID_WORLD_PORT"))
	var testnode struct {
		SSPort     int    `json:"ss_port"`
		SSCipher   string `json:"ss_cipher"`
		SSPassword string `json:"ss_password"`
		VLESSPort  int    `json:"vless_port"`
		VLESSUUID  string `json:"vless_uuid"`
	}
	raw, err := os.ReadFile(os.Getenv("ASTERLINK_ANDROID_TESTNODE"))
	if err != nil || json.Unmarshal(raw, &testnode) != nil || testnode.SSPort == 0 || testnode.VLESSPort == 0 {
		t.Fatal("ASTERLINK_ANDROID_TESTNODE must name the test node's ready file")
	}
	nodeHost := os.Getenv("ASTERLINK_ANDROID_NODE_HOST")
	if nodeHost == "" {
		nodeHost = "10.0.2.2"
	}
	sinkBase, sinkToken := os.Getenv("ASTERLINK_ANDROID_SINK"), os.Getenv("ASTERLINK_ANDROID_SINK_TOKEN")

	// Short client timings so the stop cases take seconds, not minutes (the client only ever
	// uses what the server says). The relations config.Load enforces still hold.
	timings := func(c *config.Config) {
		c.ClientReportInterval = 2 * time.Second
		c.ClientLease = 6 * time.Second
		c.ClientIdleTimeout = 30 * time.Second
		c.ClientNodeWatch = 2 * time.Second
	}
	var env *grantEnv
	var agentNode *e2eNode
	if os.Getenv("ASTERLINK_NODEAGENT_BIN") != "" {
		agentNode = newE2E(t, "1", timings)
		env = agentNode.env
	} else {
		env = newGrantEnv(t, timings)
	}
	db, cfg, router := env.db, env.cfg, env.router

	const password = "Android-Billing-Only!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	users := map[string]model.User{}
	subs := map[string]string{}
	makeUser := func(label, role string, quota int64, devices int) {
		u := model.User{ID: uuid.NewString(), Email: label + "-" + uuid.NewString()[:8] + "@example.invalid", Name: label, PasswordHash: string(hash), Role: role, Status: "active"}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		users[label] = u
		if quota == 0 {
			return
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(2 * time.Hour), TrafficLimitBytes: quota, MaxDevices: devices, AllowDedicated: true}
		if err := db.Create(&sub).Error; err != nil {
			t.Fatal(err)
		}
		subs[label] = sub.ID
	}
	makeUser("admin", "admin", 0, 0)
	makeUser("meter", "user", 4<<30, 4)  // B01, B02, notifications
	makeUser("change", "user", 4<<30, 4) // A04: re-rate, disable
	makeUser("expire", "user", 4<<30, 4) // A04: plan ends while connected
	makeUser("quota", "user", 64<<20, 4) // A04: the data runs out while connected
	makeUser("grant", "user", 4<<30, 4)  // B04: node-reported
	makeUser("idle", "user", 4<<30, 4)   // spare
	makeUser("life", "user", 4<<30, 4)   // round 5: lifecycle (scripts/test-native-android.py --suite lifecycle)
	// Node groups round 6: device limit, reassignment, release (presence_world_test.go). The plan
	// allows 4 devices; the effective limit is CLIENT_MAX_ONLINE_DEVICES (2, decision D1).
	makeUser("presence", "user", 4<<30, 4)

	// The administrator's web session, as in the console.
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
	login := webCall("POST", "/auth/login", nil, loginInput{Email: users["admin"].Email, Password: password})
	if login.Code != 200 {
		t.Fatalf("admin web login %d", login.Code)
	}
	cookies := login.Result().Cookies()
	save := func(id string, in nodeInput) (adminNode, error) {
		path, status := "/admin/nodes", 201
		if id != "" {
			path, status = path+"/"+id, 200
		}
		w := webCall("POST", path, cookies, in)
		if w.Code != status {
			return adminNode{}, fmt.Errorf("save node %d: %s", w.Code, w.Body.String())
		}
		var res struct {
			Nodes []adminNode `json:"nodes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return res.Nodes[0], nil
	}
	ss := func(name string) string {
		return fmt.Sprintf("proxies: [{name: %s, type: ss, server: %s, port: %d, cipher: %s, password: %s, udp: true}]", name, nodeHost, testnode.SSPort, testnode.SSCipher, testnode.SSPassword)
	}
	vless := func(name string) string {
		return fmt.Sprintf("proxies: [{name: %s, type: vless, server: %s, port: %d, uuid: %s, udp: true}]", name, nodeHost, testnode.VLESSPort, testnode.VLESSUUID)
	}
	type line struct {
		node adminNode
		yaml func(string) string
	}
	var mu sync.Mutex
	lines := map[string]*line{}
	for _, l := range []struct {
		key, name, region, kind string
		rate                    int64
		yaml                    func(string) string
	}{
		{"dedicated", "HK-IEPL-01", "HK", "dedicated", 1000, ss},
		{"ordinary", "JP-Direct-01", "JP", "direct", 500, vless},
		{"changing", "SG-Edit-01", "SG", "dedicated", 1000, ss},
	} {
		n, err := save("", nodeInput{YAML: l.yaml(l.name), Region: l.region, LineType: l.kind, RatePermille: l.rate, Enabled: true, PlanIDs: []string{}})
		if err != nil {
			t.Fatal(err)
		}
		lines[l.key] = &line{node: n, yaml: l.yaml}
	}
	ids := map[string]string{"dedicated": lines["dedicated"].node.ID, "ordinary": lines["ordinary"].node.ID, "changing": lines["changing"].node.ID}
	names := map[string]string{"dedicated": "HK-IEPL-01", "ordinary": "JP-Direct-01", "changing": "SG-Edit-01"}
	if agentNode != nil {
		// What an administrator enters for a managed line: the forwarder's address as the
		// emulator sees it; the agent hands each grant its own UUID.
		_, port, _ := net.SplitHostPort(agentNode.vless)
		number, _ := strconv.Atoi(port)
		yaml := fmt.Sprintf("proxies:\n  - {name: %s, type: vless, server: %s, port: %d, uuid: %s, udp: true}\n", agentNode.node.Name, nodeHost, number, sharedVlessUUID)
		sealed, err := nodes.Encrypt(cfg.NodeEncryptionKey, agentNode.node.ID, []byte(yaml))
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Model(&model.NodeConfig{}).Where("node_id = ?", agentNode.node.ID).Update("ciphertext", sealed).Error; err != nil {
			t.Fatal(err)
		}
		ids["agent"], names["agent"] = agentNode.node.ID, agentNode.node.Name
	}
	emails := map[string]string{}
	for k, u := range users {
		emails[k] = u.Email
	}
	presence := newPresenceWorld(t, db, cfg, router, func(method, path string, body any) *httptest.ResponseRecorder {
		return webCall(method, path, cookies, body)
	}, ss, password, emails)
	defer presence.stopAll()
	ids["pair"], names["pair"] = presence.groupID, pairLine
	// B04's target: a name only the node resolves, to the host's loopback (RFC 6761 *.localhost).
	// Whether this machine's resolver does so decides whether B04 can run here at all.
	const agentTarget = "sink.asterlink.localhost"
	agentTargetOK := false
	if addrs, err := net.LookupHost(agentTarget); err == nil {
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && ip.IsLoopback() {
				agentTargetOK = true
			}
		}
	}

	// /traffic as the server saw it, and the one-shot lost acknowledgement.
	type report struct {
		Index   int    `json:"index"`
		Token   string `json:"token_sha256_prefix"`
		Body    string `json:"body"`
		Status  int    `json:"status"`
		Dropped bool   `json:"dropped"`
		At      int64  `json:"at_ms"`
	}
	var reports []report
	dropNext := false
	// Round 5 (lifecycle): an unreachable client API, and request counts per endpoint.
	apiOffline := false
	hits := map[string]int{}
	grantTokens := map[string]string{} // email-less: the last bearer token that asked for a grant
	tokenHash := func(header string) string {
		sum := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
		return hex.EncodeToString(sum[:4])
	}
	started := time.Now()

	ledger := func(who string) (map[string]int64, error) {
		var sub model.Subscription
		if err := db.First(&sub, "id = ?", subs[who]).Error; err != nil {
			return nil, err
		}
		return map[string]int64{
			"used_units": sub.UsedUnits, "total_bytes": sub.TrafficLimitBytes, "upload_bytes": sub.UploadBytes, "download_bytes": sub.DownloadBytes,
			"remaining_bytes": max(0, (sub.TrafficLimitBytes*1000-sub.UsedUnits)/1000),
		}, nil
	}

	stop := make(chan struct{})
	var once sync.Once
	control := func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := net.SplitHostPort(r.RemoteAddr); host != "127.0.0.1" || r.Method != http.MethodPost {
			http.Error(w, "loopback POST only", http.StatusForbidden)
			return
		}
		var in struct {
			Node      string `json:"node"`
			Rate      int64  `json:"rate_permille"`
			Enabled   *bool  `json:"enabled"`
			User      string `json:"user"`
			Remaining int64  `json:"remaining_bytes"`
			Offline   bool   `json:"offline"`
			Count     int    `json:"count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		answer := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		mu.Lock()
		defer mu.Unlock()
		switch strings.TrimPrefix(r.URL.Path, "/__world/") {
		case "edit": // re-rate and/or enable/disable a line, through the admin API
			l, ok := lines[in.Node]
			if !ok {
				http.Error(w, "unknown node", 400)
				return
			}
			enabled := l.node.Enabled
			if in.Enabled != nil {
				enabled = *in.Enabled
			}
			rate := l.node.RatePermille
			if in.Rate > 0 {
				rate = in.Rate
			}
			n, err := save(l.node.ID, nodeInput{YAML: l.yaml(l.node.Name), Region: l.node.Region, LineType: l.node.LineType, RatePermille: rate, Enabled: enabled, PlanIDs: []string{}, Version: l.node.Version})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			l.node = n
			w.WriteHeader(http.StatusNoContent)
		case "expire": // the plan ends now
			db.Model(&model.Subscription{}).Where("id = ?", subs[in.User]).Update("expires_at", time.Now().Add(-time.Second).UTC())
			w.WriteHeader(http.StatusNoContent)
		case "quota": // only this much of the plan is left
			var sub model.Subscription
			if db.First(&sub, "id = ?", subs[in.User]).Error != nil {
				http.Error(w, "unknown user", 400)
				return
			}
			db.Model(&sub).Update("used_units", max(0, sub.TrafficLimitBytes-in.Remaining)*1000)
			w.WriteHeader(http.StatusNoContent)
		case "ledger":
			l, err := ledger(in.User)
			if err != nil {
				http.Error(w, "unknown user", 400)
				return
			}
			answer(l)
		case "drop_ack":
			dropNext = true
			w.WriteHeader(http.StatusNoContent)
		case "traffic":
			answer(map[string]any{"reports": reports})
		case "sink":
			response, err := http.Get(sinkBase + "/stats?t=" + sinkToken)
			if err != nil {
				http.Error(w, "sink unreachable", 502)
				return
			}
			defer response.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.Copy(w, response.Body)
		case "reject_probe": // B04: the node-reported session must refuse a client report
			token := grantTokens["last"]
			if token == "" {
				http.Error(w, "no grant seen yet", 409)
				return
			}
			type sessionState struct {
				LastSequence int64 `json:"last_sequence"`
				UsedUnits    int64 `json:"used_units"`
				RatePermille int64 `json:"rate_permille"`
			}
			var session, later sessionState
			_ = json.Unmarshal(env.client("GET", "/session", token, nil, nil).Body.Bytes(), &session)
			const up, down = 1 << 20, 1 << 20
			refused := env.client("POST", "/traffic", token, nil, map[string]int64{"sequence": session.LastSequence + 1, "upload_bytes": up, "download_bytes": down})
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(refused.Body.Bytes(), &body)
			_ = json.Unmarshal(env.client("GET", "/session", token, nil, nil).Body.Bytes(), &later)
			// The session stays connected through the node, whose agent reports usage every
			// second, so the ledger may legitimately grow between the two reads. What must not
			// happen is the refused report being applied: its sequence accepted, or its own
			// units (both directions at the session's rate) charged.
			rate := session.RatePermille
			if rate <= 0 {
				rate = 1000
			}
			refusedUnits := (up + down) * rate
			delta := later.UsedUnits - session.UsedUnits
			billed := later.LastSequence != session.LastSequence || delta >= refusedUnits
			answer(map[string]any{"status": refused.Code, "code": body.Error.Code, "report_billed": billed,
				"sequence_moved": later.LastSequence != session.LastSequence, "delta_units": delta, "refused_units": refusedUnits})
		case "api": // round 5: the client API becomes unreachable (connections closed) or comes back
			apiOffline = in.Offline
			w.WriteHeader(http.StatusNoContent)
		case "hits": // round 5: client API requests per endpoint since the last call (7.6 measurement)
			answer(hits)
			hits = map[string]int{}
		case "stop":
			once.Do(func() { close(stop) })
			w.WriteHeader(http.StatusNoContent)
		default:
			if !presence.handle(strings.TrimPrefix(r.URL.Path, "/__world/"), in.User, in.Count, answer, func(code int, message string) { http.Error(w, message, code) }) {
				http.NotFound(w, r)
			}
		}
	}

	api := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		mu.Lock()
		offline := apiOffline
		if strings.HasPrefix(path, "/api/v1/client/") {
			hits[strings.SplitN(strings.TrimPrefix(path, "/api/v1/client/"), "/", 2)[0]]++
		}
		mu.Unlock()
		if offline && strings.HasPrefix(path, "/api/v1/client/") {
			// What an unreachable server looks like to the client: the connection just ends.
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		if r.Method == http.MethodPost && path == "/api/v1/client/grants" {
			mu.Lock()
			grantTokens["last"] = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			mu.Unlock()
		}
		if r.Method != http.MethodPost || path != "/api/v1/client/traffic" {
			router.ServeHTTP(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(bytes.NewReader(body))
		mu.Lock()
		drop := dropNext
		dropNext = false
		mu.Unlock()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, r)
		mu.Lock()
		reports = append(reports, report{Index: len(reports), Token: tokenHash(r.Header.Get("Authorization")), Body: string(body), Status: recorder.Code, Dropped: drop, At: time.Since(started).Milliseconds()})
		mu.Unlock()
		if drop {
			// Committed by the real handler; the reply never arrives.
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}

	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__world/") {
			control(w, r)
			return
		}
		api(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	address := listener.Addr().String()
	facts, _ := json.Marshal(map[string]any{
		"api_base": "http://" + address + "/api/v1/client", "password": password, "emails": emails, "nodes": ids, "names": names,
		"report_interval_seconds": int(cfg.ClientReportInterval / time.Second), "lease_seconds": int(cfg.ClientLease / time.Second), "node_reported": agentNode != nil,
		"agent_target": agentTarget, "agent_target_resolves": agentTargetOK,
	})
	if err := os.WriteFile(out, facts, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("android billing world on %s (node-reported line: %v, B04 target resolves: %v)", address, agentNode != nil, agentTargetOK)
	select {
	case <-stop:
	case <-time.After(60 * time.Minute):
		t.Fatal("the billing suite never stopped the world")
	}
	if agentNode != nil {
		t.Logf("node agent log:\n%s", agentNode.agentLog())
	}
}
