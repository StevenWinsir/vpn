package api

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"vpn/backend/internal/model"
)

// Round 3 (handoff_node_use_history_and_load_balancing_c.md 12): node group
// allocation, load balancing, failover and the admin group API.

func first(int) int { return 0 }

// spawn is sync.WaitGroup.Go for the module's Go 1.24.
func spawn(wg *sync.WaitGroup, f func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		f()
	}()
}

func profileProxies(t *testing.T, doc string) []map[string]any {
	t.Helper()
	var out struct {
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal([]byte(doc), &out); err != nil || len(out.ProxyGroups) != 1 {
		t.Fatalf("profile: %v %s", err, doc)
	}
	if len(out.Proxies) == 1 && (len(out.ProxyGroups[0].Proxies) != 1 || out.ProxyGroups[0].Proxies[0] != out.Proxies[0]["name"]) {
		t.Fatalf("select group does not name the proxy: %s", doc)
	}
	return out.Proxies
}

// pickServer is the pure decision; its distribution is checked with the production
// random source.
func TestPickServerRules(t *testing.T) {
	c := func(id string, max int, load int64) candidate { return candidate{ID: id, Max: max, Load: load} }
	cases := []struct {
		name      string
		cands     []candidate
		preferred string
		want      int
	}{
		{"own server first even when not lightest", []candidate{c("a", 5, 0), {ID: "b", Max: 5, Load: 4, Mine: true}}, "a", 1},
		{"own server skipped when full", []candidate{c("a", 5, 0), {ID: "b", Max: 5, Load: 5, Mine: true}}, "", 0},
		{"sticky beats load", []candidate{c("a", 5, 0), c("b", 5, 4)}, "b", 1},
		{"sticky skipped when full", []candidate{c("a", 5, 3), c("b", 5, 5)}, "b", 0},
		{"sticky skipped when cooling", []candidate{c("a", 5, 3), {ID: "b", Max: 5, Load: 0, Cooling: true}}, "b", 0},
		{"ratio, not count", []candidate{c("a", 2, 1), c("b", 6, 2)}, "", 1},
		{"cooling only as fallback", []candidate{{ID: "a", Max: 5, Load: 0, Cooling: true}, c("b", 5, 5), c("c", 2, 1)}, "", 2},
		{"cooling used when nothing healthy has room", []candidate{{ID: "a", Max: 5, Load: 3, Cooling: true}, c("b", 5, 5)}, "", 0},
		{"all full", []candidate{c("a", 1, 1), c("b", 2, 2)}, "", -1},
		{"empty", nil, "", -1},
	}
	for _, tc := range cases {
		if got := pickServer(tc.cands, tc.preferred, first); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
	// Ties: every tied server is reachable through the injected source...
	tied := []candidate{c("a", 5, 1), c("b", 10, 2), c("c", 5, 2), c("d", 20, 4)}
	for k, want := range []int{0, 1, 3} {
		if got := pickServer(tied, "", func(n int) int { return k % n }); got != want {
			t.Errorf("tie %d: got %d want %d", k, got, want)
		}
	}
	// ...and the production source spreads 1000 allocations evenly over five servers.
	counts := make([]int, 5)
	even := []candidate{c("a", 5, 0), c("b", 5, 0), c("c", 5, 0), c("d", 5, 0), c("e", 5, 0)}
	for range 1000 {
		counts[pickServer(even, "", rand.IntN)]++
	}
	for i, n := range counts {
		if n < 140 || n > 260 { // expected 200, sd about 12.6
			t.Errorf("server %d chosen %d/1000 times: %v", i, n, counts)
		}
	}
}

func TestAllocatorBalancesPostgres(t *testing.T) {
	e := newPresenceEnvWith(t, nil, first)
	jp := e.group("JP1", "[]")
	servers := e.servers(jp, 5, 5, 5, 5, 5)
	token := ""
	for i := range 25 {
		_, token = e.device()
		got := e.admitted(token, jp.ID)
		if !slices.ContainsFunc(servers, func(n model.Node) bool { return n.ID == got.NodeID }) || got.Connection.GroupID != jp.ID {
			t.Fatalf("admission %d: %+v", i, got.Connection)
		}
		// Every server renders under the group name; nothing names the server.
		if proxies := profileProxies(t, got.YAML); len(proxies) != 1 || proxies[0]["name"] != "JP1" {
			t.Fatalf("profile not renamed to its group: %s", got.YAML)
		}
	}
	for _, n := range servers {
		if load := e.loads()[n.ID]; load != 5 {
			t.Fatalf("server %s has %d, want exactly 5: %v", n.ID, load, e.loads())
		}
	}
	_, extra := e.device()
	e.want(e.admit(extra, jp.ID), 409, "group_full")
	// The catalog shows the group once, whatever its server count.
	if cat := e.catalog(token); len(cat.Nodes) != 1 || cat.Nodes[0].ID != jp.ID || cat.Nodes[0].Name != "JP1" {
		t.Fatalf("catalog: %+v", cat.Nodes)
	}

	// Capacities 2/4/6 are filled in proportion: after 6 admissions loads are 1/2/3.
	mixed := e.group("Mixed", "[]")
	byCap := map[int]model.Node{}
	for _, n := range e.servers(mixed, 2, 4, 6) {
		byCap[n.MaxConnections] = n
	}
	for i := range 12 {
		_, token = e.device()
		e.admitted(token, mixed.ID)
		if i == 5 {
			l := e.loads()
			if l[byCap[2].ID] != 1 || l[byCap[4].ID] != 2 || l[byCap[6].ID] != 3 {
				t.Fatalf("not proportional after 6: %v", l)
			}
		}
	}
	l := e.loads()
	if l[byCap[2].ID] != 2 || l[byCap[4].ID] != 4 || l[byCap[6].ID] != 6 {
		t.Fatalf("final loads %v", l)
	}

	// Equal load: the injected source decides, and each server can be the one.
	for k := range 3 {
		pick := k
		e2 := newPresenceEnvWith(t, nil, func(n int) int { return pick % n })
		g := e2.group("Tie", "[]")
		tied := e2.servers(g, 5, 5, 5)
		_, tok := e2.device()
		if got := e2.admitted(tok, g.ID); got.NodeID != tied[k].ID {
			t.Fatalf("tie-break %d chose %s, want %s", k, got.NodeID, tied[k].ID)
		}
	}
}

func TestStickyAssignmentPostgres(t *testing.T) {
	e := newPresenceEnvWith(t, nil, first)
	_, cookie := e.admin()
	g := e.group("JP1", "[]")
	servers := e.servers(g, 5, 5, 5)
	u := e.user(5)
	device := uuid.NewString()
	token := e.login(u, device)
	got := e.admitted(token, g.ID)
	preferred := got.NodeID
	if e.assignment(u.ID, g.ID) != preferred {
		t.Fatal("first allocation did not record the sticky server")
	}
	e.want(e.release(token, got.Connection.ID, "user"), 200, "")
	// The preferred server becomes the busiest; signing in again, refreshing the
	// catalog and reconnecting still lands on it.
	var pref model.Node
	for _, n := range servers {
		if n.ID == preferred {
			pref = n
		}
	}
	e.fill(pref, 3)
	e.want(e.native("POST", "/logout", token, nil), 200, "")
	token = e.login(u, device)
	e.catalog(token)
	if again := e.admitted(token, g.ID); again.NodeID != preferred {
		t.Fatalf("sticky server not reused: got %s want %s (loads %v)", again.NodeID, preferred, e.loads())
	}
	// Full: the user's next device goes elsewhere, the preference stays.
	e.fill(pref, 1)
	second := e.login(u, uuid.NewString())
	other := e.admitted(second, g.ID)
	if other.NodeID == preferred || e.assignment(u.ID, g.ID) != preferred {
		t.Fatalf("full sticky server: placed on %s, preference %s", other.NodeID, e.assignment(u.ID, g.ID))
	}
	// Disabled: its devices are told to reconnect, land elsewhere, and the
	// preference moves.
	e.want(e.editServer(cookie, pref, "", 5, "", false), 200, "")
	if s := e.state(token); s.Reason != "connection_reassigned" || s.ConnectionState != model.LeaseEnded {
		t.Fatalf("withdrawn device: %+v", s)
	}
	moved := e.admitted(token, g.ID)
	if moved.NodeID == preferred || e.assignment(u.ID, g.ID) != moved.NodeID {
		t.Fatalf("after withdrawal: on %s, preference %s", moved.NodeID, e.assignment(u.ID, g.ID))
	}
}

// Same-group admissions are serialised by the group row lock: capacity is never
// exceeded, other groups are not blocked, and admin saves running alongside never
// deadlock. CAPACITY_RUNS repeats the whole test (the round record uses 20).
func TestGroupCapacityConcurrencyPostgres(t *testing.T) {
	runs, _ := strconv.Atoi(os.Getenv("CAPACITY_RUNS"))
	for run := range max(runs, 1) {
		t.Run(fmt.Sprintf("run%d", run), groupCapacityConcurrency)
	}
}

func groupCapacityConcurrency(t *testing.T) {
	e := newPresenceEnv(t, nil)
	_, cookie := e.admin()
	full, other := e.group("JP1", "[]"), e.group("HK1", "[]")
	servers := e.servers(full, 5, 5, 5, 5, 5)
	e.servers(other, 100)
	tokens, others := make([]string, 200), make([]string, 30)
	for i := range tokens {
		_, tokens[i] = e.device()
	}
	for i := range others {
		_, others[i] = e.device()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	codes := make([]string, len(tokens))
	var wg sync.WaitGroup
	for i, token := range tokens {
		spawn(&wg, func() {
			w := e.do(presenceCall{ctx: ctx, method: "POST", path: "/client/connections", token: token, headers: map[string]string{"Idempotency-Key": "c-" + uuid.NewString()}, body: map[string]any{"group_id": full.ID, "last_sequence": 0}})
			codes[i] = strconv.Itoa(w.Code) + errorCode(w)
		})
	}
	otherCodes := make([]int, len(others))
	for i, token := range others {
		spawn(&wg, func() {
			otherCodes[i] = e.do(presenceCall{ctx: ctx, method: "POST", path: "/client/connections", token: token, headers: map[string]string{"Idempotency-Key": "o-" + uuid.NewString()}, body: map[string]any{"group_id": other.ID, "last_sequence": 0}}).Code
		})
	}
	// Label edits keep the version and capacity, so they can run throughout.
	adminCodes := make([]int, 20)
	for i := range adminCodes {
		spawn(&wg, func() {
			adminCodes[i] = e.editServer(cookie, servers[i%len(servers)], "", 5, "edit-"+strconv.Itoa(i), true).Code
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("admissions did not finish: lock wait or deadlock")
	}
	tally := map[string]int{}
	for _, c := range codes {
		tally[c]++
	}
	if tally["201"] != 25 || tally["409group_full"] != 175 {
		t.Fatalf("200 admissions to a 25-slot group: %v", tally)
	}
	for _, c := range otherCodes {
		if c != 201 {
			t.Fatalf("other group refused during contention: %v", otherCodes)
		}
	}
	for _, c := range adminCodes {
		if c != 200 {
			t.Fatalf("admin saves during contention: %v", adminCodes)
		}
	}
	var over int64
	e.db.Raw(`SELECT COUNT(*) FROM (SELECT l.node_id FROM ` + e.cfg.Schema + `.connection_leases l JOIN ` + e.cfg.Schema + `.nodes n ON n.id = l.node_id
		WHERE l.state = 'active' GROUP BY l.node_id, n.max_connections HAVING COUNT(*) > n.max_connections) x`).Scan(&over)
	if over != 0 {
		t.Fatalf("%d servers over capacity", over)
	}

	// Withdrawing a server while its group admits: no deadlock, and once it is disabled
	// nothing lives on it.
	e2 := newPresenceEnv(t, nil)
	_, cookie2 := e2.admin()
	g := e2.group("SG1", "[]")
	pair := e2.servers(g, 50, 50)
	churn := make([]string, 40)
	for i := range churn {
		_, churn[i] = e2.device()
	}
	failures := make(chan string, 200)
	for _, token := range churn {
		spawn(&wg, func() {
			for range 3 {
				w := e2.do(presenceCall{ctx: ctx, method: "POST", path: "/client/connections", token: token, headers: map[string]string{"Idempotency-Key": "w-" + uuid.NewString()}, body: map[string]any{"group_id": g.ID, "last_sequence": 0}})
				if w.Code != 201 {
					failures <- w.Body.String()
					continue
				}
				if r := e2.release(token, e2.connection(w).Connection.ID, "stopped"); r.Code != 200 {
					failures <- r.Body.String()
				}
			}
		})
	}
	spawn(&wg, func() {
		for i := range 6 {
			if w := e2.editServer(cookie2, pair[0], "", 50, "", i%2 == 0); w.Code != 200 {
				failures <- w.Body.String()
			}
		}
	})
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Fatalf("churn with withdrawal: %s", f)
	}
	if ctx.Err() != nil {
		t.Fatal("withdrawal churn did not finish")
	}
	if e2.loads()[pair[0].ID] != 0 {
		t.Fatal("a lease lives on the disabled server")
	}
}

func TestServerWithdrawalPostgres(t *testing.T) {
	e := newPresenceEnvWith(t, nil, first)
	_, cookie := e.admin()
	g := e.group("JP1", "[]")
	servers := e.servers(g, 5, 5, 5)
	// A new Core (v3) and an older Core (/config) on the same first server.
	_, v3 := e.device()
	conn := e.admitted(v3, g.ID)
	u2 := e.user(5)
	legacy := e.login(u2, uuid.NewString())
	w := e.native("GET", "/config?node_id="+g.ID+"&last_sequence=0", legacy, nil)
	e.want(w, 200, "")
	legacyNode := decodeBody[struct {
		NodeID string `json:"node_id"`
	}](t, w).NodeID
	if conn.NodeID != servers[0].ID || legacyNode != servers[1].ID {
		t.Fatalf("setup: v3 on %s, legacy on %s", conn.NodeID, legacyNode)
	}
	e.want(e.traffic(legacy, 1, 10, 10), 200, "")
	revision := e.catalog(v3).Revision
	if c := e.catalog(v3).Connection; c == nil || c.ID != conn.Connection.ID || c.State != "active" || c.GroupID != g.ID {
		t.Fatalf("catalog connection summary: %+v", c)
	}

	// Disable the v3 device's server: its lease ends at once and it is told to
	// reconnect; the long poll's revision moves with it.
	e.want(e.editServer(cookie, servers[0], "", 5, "", false), 200, "")
	if l := e.lease(conn.Connection.ID); l.State != model.LeaseEnded || l.EndReason != model.LeaseNodeUnavailable {
		t.Fatalf("withdrawn lease: %+v", l)
	}
	if s := e.state(v3); s.Reason != "connection_reassigned" || s.CanConnect {
		t.Fatalf("v3 after withdrawal: %+v", s)
	}
	contractShapeV3(t, "catalog_connection", "response", e.native("GET", "/nodes", v3, nil).Body.Bytes())
	cat := e.catalog(v3)
	if cat.Revision == revision || cat.Connection == nil || cat.Connection.State != model.LeaseEnded || len(cat.Nodes) != 1 {
		t.Fatalf("catalog after withdrawal: %+v", cat)
	}
	e.advance(time.Second)
	again := e.admitted(v3, g.ID)
	if again.NodeID != servers[2].ID {
		t.Fatalf("reconnected to %s, want the least loaded %s (loads %v)", again.NodeID, servers[2].ID, e.loads())
	}
	// Delete the older Core's server: it is told profile_changed (a code it knows),
	// and its next /config lands on the remaining server.
	current := e.nodeRow(servers[1].ID)
	e.want(e.adminCall(cookie, "DELETE", "/admin/nodes/"+current.ID, map[string]int64{"version": current.Version}), 200, "")
	w = e.traffic(legacy, 2, 20, 20)
	e.want(w, 200, "")
	if s := snapshotOf(t, w); s.Reason != "profile_changed" {
		t.Fatalf("older Core after deletion: %+v", s)
	}
	if e.assignment(u2.ID, g.ID) != "" {
		t.Fatal("assignment to a deleted server survived")
	}
	w = e.native("GET", "/config?node_id="+g.ID+"&last_sequence=2", legacy, nil)
	e.want(w, 200, "")
	if n := decodeBody[struct {
		NodeID string `json:"node_id"`
	}](t, w).NodeID; n != servers[2].ID {
		t.Fatalf("older Core re-bound to %s", n)
	}
	// Disable the whole group: every lease ends, the line leaves the catalog and the
	// devices are not asked to reconnect to it.
	group := e.webGroup(cookie, g.ID)
	group["enabled"] = false
	e.want(e.adminCall(cookie, "POST", "/admin/node-groups/"+g.ID, group), 200, "")
	if e.loads()[servers[2].ID] != 0 {
		t.Fatal("leases survived disabling the group")
	}
	if s := e.state(v3); s.Reason != "client_config_unavailable" {
		t.Fatalf("v3 after group disabled: %+v", s)
	}
	if cat := e.catalog(v3); len(cat.Nodes) != 0 {
		t.Fatalf("disabled group still listed: %+v", cat.Nodes)
	}
	e.want(e.admit(v3, g.ID), 503, "group_unavailable")
}

// webGroup reads one group from the admin list in its save shape.
func (e *presenceEnv) webGroup(cookie, id string) map[string]any {
	e.t.Helper()
	list := decodeBody[struct {
		Groups []adminGroupView `json:"groups"`
	}](e.t, e.adminCall(cookie, "GET", "/admin/node-groups", nil))
	for _, g := range list.Groups {
		if g.ID == id {
			return map[string]any{"name": g.Name, "region": g.Region, "line_type": g.LineType, "rate_permille": g.RatePermille, "plan_ids": g.PlanIDs, "enabled": g.Enabled, "sort": g.Sort, "version": g.Version}
		}
	}
	e.t.Fatalf("group %s not listed", id)
	return nil
}

func TestSoftCooldownPostgres(t *testing.T) {
	e := newPresenceEnvWith(t, nil, first)
	admin, cookie := e.admin()
	g := e.group("JP1", "[]")
	servers := e.servers(g, 5, 5)
	bad := servers[0]
	failOn := func(token string) {
		t.Helper()
		got := e.admitted(token, g.ID)
		if got.NodeID != bad.ID {
			t.Fatalf("setup: admitted to %s", got.NodeID)
		}
		e.want(e.release(token, got.Connection.ID, "tunnel_failed"), 200, "")
	}
	// One user reporting again and again never triggers a cooldown.
	u1 := e.user(5)
	t1 := e.session(u1, uuid.NewString())
	for range 4 {
		failOn(t1)
	}
	if e.nodeRow(bad.ID).UnhealthyUntil != nil {
		t.Fatal("one user's reports cooled the server down")
	}
	// A third distinct user does (NODE_HEALTH_FAILURE_USERS = 3).
	_, t2 := e.device()
	failOn(t2)
	_, t3 := e.device()
	got := e.admitted(t3, g.ID)
	e.want(e.release(t3, got.Connection.ID, "tunnel_unhealthy"), 200, "")
	until := e.nodeRow(bad.ID).UnhealthyUntil
	if until == nil || !until.Equal(e.now().Add(300*time.Second)) {
		t.Fatalf("cooldown not set: %v", until)
	}
	// Cooling servers are left alone while a healthy one has room, and used only
	// when none does. Online devices are never kicked.
	_, t4 := e.device()
	if got := e.admitted(t4, g.ID); got.NodeID != servers[1].ID {
		t.Fatal("cooling server chosen while a healthy one had room")
	}
	e.fill(servers[1], 4)
	_, t5 := e.device()
	if got := e.admitted(t5, g.ID); got.NodeID != bad.ID {
		t.Fatal("cooling server not used as the fallback")
	}
	// The cooldown lapses by itself.
	e.advance(301 * time.Second)
	if list := e.adminGroups(cookie); list[0].Servers[0].Health != "ok" {
		t.Fatalf("health after cooldown: %+v", list[0].Servers[0])
	}
	// An administrator can clear it at once, together with the reports behind it.
	e.db.Model(&model.Node{}).Where("id = ?", bad.ID).Update("unhealthy_until", e.now().Add(time.Hour))
	e.want(e.adminCall(cookie, "POST", "/admin/nodes/"+bad.ID+"/health", map[string]any{}), 200, "")
	var events, audits int64
	e.db.Model(&model.NodeHealthEvent{}).Where("node_id = ?", bad.ID).Count(&events)
	e.db.Model(&model.AdminAudit{}).Where("actor_id = ? AND action = ? AND target_id = ?", admin.ID, "node.health_cleared", bad.ID).Count(&audits)
	if e.nodeRow(bad.ID).UnhealthyUntil != nil || events != 0 || audits != 1 {
		t.Fatalf("clear: until %v events %d audits %d", e.nodeRow(bad.ID).UnhealthyUntil, events, audits)
	}
}

func listedGroup(t *testing.T, groups []adminGroupView, id string) adminGroupView {
	t.Helper()
	for _, g := range groups {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("group %s not listed", id)
	return adminGroupView{}
}

func (e *presenceEnv) adminGroups(cookie string) []adminGroupView {
	e.t.Helper()
	w := e.adminCall(cookie, "GET", "/admin/node-groups", nil)
	e.want(w, 200, "")
	return decodeBody[struct {
		Groups []adminGroupView `json:"groups"`
	}](e.t, w).Groups
}

// Plan 6.5: YAML edits reach only that server's sessions; capacity, label and
// cooldown reach none; group metadata reaches the whole group.
func TestProfileVersionRulesPostgres(t *testing.T) {
	e := newPresenceEnvWith(t, nil, first)
	_, cookie := e.admin()
	g := e.group("JP1", "[]")
	servers := e.servers(g, 5, 5)
	_, d1 := e.device()
	_, d2 := e.device()
	if e.admitted(d1, g.ID).NodeID != servers[0].ID || e.admitted(d2, g.ID).NodeID != servers[1].ID {
		t.Fatal("setup placement")
	}
	revision := e.catalog(d1).Revision
	quiet := func(step string) {
		t.Helper()
		if a, b := e.state(d1), e.state(d2); a.Reason != "" || b.Reason != "" {
			t.Fatalf("%s disturbed a session: %q %q", step, a.Reason, b.Reason)
		}
		if e.catalog(d1).Revision != revision {
			t.Fatalf("%s changed the catalog revision", step)
		}
	}
	e.want(e.editServer(cookie, servers[0], "", 3, "tokyo-a", true), 200, "")
	if n := e.nodeRow(servers[0].ID); n.Version != 1 || n.MaxConnections != 3 || n.Label != "tokyo-a" {
		t.Fatalf("capacity/label edit: %+v", n)
	}
	quiet("capacity and label")
	e.db.Model(&model.Node{}).Where("id = ?", servers[1].ID).Update("unhealthy_until", e.now().Add(time.Minute))
	quiet("cooldown")
	e.want(e.editServer(cookie, servers[0], "proxies: [{name: JP1, type: ss, server: 127.0.0.2, port: 30001, cipher: aes-256-gcm, password: rotated}]", 3, "tokyo-a", true), 200, "")
	if e.nodeRow(servers[0].ID).Version != 2 {
		t.Fatal("YAML edit did not move the server version")
	}
	if a, b := e.state(d1), e.state(d2); a.Reason != "profile_changed" || b.Reason != "" {
		t.Fatalf("YAML edit: %q %q", a.Reason, b.Reason)
	}
	if e.catalog(d1).Revision != revision {
		t.Fatal("a server YAML edit changed the catalog revision")
	}
	group := e.webGroup(cookie, g.ID)
	group["rate_permille"] = 1500
	e.want(e.adminCall(cookie, "POST", "/admin/node-groups/"+g.ID, group), 200, "")
	if a, b := e.state(d1), e.state(d2); a.Reason != "profile_changed" || b.Reason != "profile_changed" {
		t.Fatalf("group rate edit: %q %q", a.Reason, b.Reason)
	}
	if cat := e.catalog(d2); cat.Revision == revision || cat.Nodes[0].RatePermille != 1500 || cat.Nodes[0].Version != 2 {
		t.Fatalf("catalog after group edit: %+v", cat)
	}
	// Legacy copies follow the group (dual write), server versions do not.
	if n := e.nodeRow(servers[1].ID); n.RatePermille != 1500 || n.Version != 1 {
		t.Fatalf("dual write: %+v", n)
	}
	// A reconnect is billed at the group's rate.
	again := e.admitted(d2, g.ID)
	if again.Session.RatePermille != 1500 {
		t.Fatalf("session rate %d", again.Session.RatePermille)
	}
}

func TestAdminGroupAPIPostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	admin, cookie := e.admin()
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		return e.adminCall(cookie, method, path, body)
	}
	groupBody := func(name string, plans []string, version int64) map[string]any {
		return map[string]any{"name": name, "region": "JP", "line_type": "direct", "rate_permille": 1000, "plan_ids": plans, "enabled": true, "sort": 0, "version": version}
	}
	saved := func(w *httptest.ResponseRecorder) adminGroupView {
		t.Helper()
		return decodeBody[struct {
			Group adminGroupView `json:"group"`
		}](t, w).Group
	}
	w := call("POST", "/admin/node-groups", groupBody("JP1", []string{"starter"}, 0))
	e.want(w, 201, "")
	jp := saved(w)
	// Names are unique within an overlapping plan scope ("every plan" overlaps all).
	e.want(call("POST", "/admin/node-groups", groupBody("JP1", []string{}, 0)), 409, "node_group_name_exists")
	e.want(call("POST", "/admin/node-groups", groupBody("JP1", []string{"starter", "pro"}, 0)), 409, "node_group_name_exists")
	w = call("POST", "/admin/node-groups", groupBody("JP1", []string{"pro"}, 0))
	e.want(w, 201, "")
	jpPro := saved(w)
	e.want(call("POST", "/admin/node-groups/"+jpPro.ID, groupBody("JP1", []string{"starter"}, jpPro.Version)), 409, "node_group_name_exists")
	e.want(call("POST", "/admin/node-groups", groupBody("VPN", nil, 0)), 400, "invalid_node_group")
	e.want(call("POST", "/admin/node-groups", groupBody("JP2", []string{"no-such-plan"}, 0)), 400, "invalid_plan")
	// Optimistic version.
	w = call("POST", "/admin/node-groups/"+jp.ID, groupBody("JP1 Tokyo", []string{"starter"}, jp.Version))
	e.want(w, 200, "")
	if jp = saved(w); jp.Version != 2 {
		t.Fatalf("rename did not move the version: %+v", jp)
	}
	e.want(call("POST", "/admin/node-groups/"+jp.ID, groupBody("JP1", []string{"starter"}, 1)), 409, "node_group_version_conflict")
	// Servers: same proxy names are allowed within a group, each import joins it.
	proxy := func(port int) string {
		return fmt.Sprintf("{name: JP1, type: ss, server: 127.0.0.1, port: %d, cipher: aes-256-gcm, password: api-secret}", port)
	}
	server := func(port int) map[string]any {
		return map[string]any{"yaml": "proxies: [" + proxy(port) + "]", "group_id": jp.ID, "max_connections": 5, "label": "s" + strconv.Itoa(port), "enabled": true, "version": 0}
	}
	ids := []string{}
	for _, port := range []int{31001, 31002, 31003} {
		w = call("POST", "/admin/nodes", server(port))
		e.want(w, 201, "")
		out := decodeBody[struct {
			Nodes []adminServerSaved `json:"nodes"`
		}](t, w).Nodes
		if len(out) != 1 || out[0].GroupID != jp.ID || out[0].Name != "JP1" || out[0].MaxConnections != 5 {
			t.Fatalf("grouped import: %+v", out)
		}
		ids = append(ids, out[0].ID)
	}
	bad := server(31004)
	bad["region"] = "JP"
	e.want(call("POST", "/admin/nodes", bad), 400, "invalid_node_config")
	bad = server(31004)
	bad["max_connections"] = 0
	e.want(call("POST", "/admin/nodes", bad), 400, "invalid_node_config")
	bad = server(31004)
	bad["group_id"] = uuid.NewString()
	e.want(call("POST", "/admin/nodes", bad), 404, "node_group_not_found")
	// The legacy per-server shape cannot edit a server of a managed group.
	e.want(call("POST", "/admin/nodes/"+ids[0], map[string]any{"yaml": "proxies: [" + proxy(31001) + "]", "region": "JP", "line_type": "direct", "rate_permille": 1000, "enabled": true, "plan_ids": []string{}, "version": 1}), 409, "node_group_managed")
	// Online counts come from one aggregate query; compare with per-server counts.
	for range 4 {
		u := e.user(5)
		var plan model.Subscription
		e.db.Model(&plan).Where("user_id = ?", u.ID).Update("plan_id", "starter")
		e.admitted(e.session(u, uuid.NewString()), jp.ID)
	}
	listed := listedGroup(t, e.adminGroups(cookie), jp.ID)
	if listed.Online != 4 || listed.Capacity != 15 || len(listed.Servers) != 3 || listed.MeteringSource != meteringClient || listed.MixedMetering {
		t.Fatalf("group summary: %+v", listed)
	}
	s := &Server{cfg: e.cfg}
	for _, srv := range listed.Servers {
		var n int64
		s.liveLeases(e.db, e.now()).Where("l.node_id = ?", srv.ID).Count(&n)
		if srv.Online != n || srv.Protocol != "ss" || srv.Health != "ok" || srv.Name != "JP1" || !strings.HasPrefix(srv.Label, "s") {
			t.Fatalf("server row %+v (counted %d)", srv, n)
		}
	}
	// Lowering a capacity kicks nobody and is shown as over capacity.
	busiest := listed.Servers[0]
	for _, srv := range listed.Servers {
		if srv.Online > busiest.Online {
			busiest = srv
		}
	}
	e.want(e.editServer(cookie, model.Node{ID: busiest.ID}, "", 1, busiest.Label, true), 200, "")
	for _, g := range e.adminGroups(cookie) {
		for _, srv := range g.Servers {
			if srv.ID == busiest.ID && g.ID != jp.ID {
				t.Fatal("server listed under another group")
			}
		}
	}
	for _, srv := range listedGroup(t, e.adminGroups(cookie), jp.ID).Servers {
		if srv.ID == busiest.ID && (!srv.OverCapacity || srv.Online != busiest.Online) {
			t.Fatalf("lowered capacity: %+v", srv)
		}
	}
	// Only an empty group can be deleted, with its current version.
	e.want(call("DELETE", "/admin/node-groups/"+jp.ID, map[string]int64{"version": jp.Version}), 409, "node_group_not_empty")
	for _, id := range ids {
		n := e.nodeRow(id)
		e.want(call("DELETE", "/admin/nodes/"+id, map[string]int64{"version": n.Version}), 200, "")
	}
	e.want(call("DELETE", "/admin/node-groups/"+jp.ID, map[string]int64{"version": 1}), 409, "node_group_version_conflict")
	e.want(call("DELETE", "/admin/node-groups/"+jp.ID, map[string]int64{"version": jp.Version}), 200, "")
	var audits []model.AdminAudit
	e.db.Where("actor_id = ?", admin.ID).Order("created_at").Find(&audits)
	actions := map[string]int{}
	for _, a := range audits {
		actions[a.Action]++
	}
	if actions["node_group.saved"] != 3 || actions["node.saved"] != 4 || actions["node.deleted"] != 3 || actions["node_group.deleted"] != 1 {
		t.Fatalf("audit trail: %v", actions)
	}
}

// node_reported groups: only agent-governed servers whose agent is online are
// allocated; an offline agent is a hard exclusion.
func TestGrantAllocationSkipsOfflineAgentsPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	a, b := e.addNode("JP1", 1000, true), e.addNode("JP1", 1000, true)
	// Put both servers in a's group.
	e.db.Model(&model.Node{}).Where("id = ?", b.ID).Update("group_id", a.ID)
	e.db.Delete(&model.NodeGroup{}, "id = ?", b.ID)
	agentA, agentB := e.addAgent(a), e.addAgent(b)
	e.sync(agentA, nil, nil)
	e.sync(agentB, nil, nil)
	stale := time.Now().UTC().Add(-time.Hour)
	e.db.Model(&model.NodeAgent{}).Where("id = ?", agentB.id).Update("last_seen_at", stale)
	for i := range 4 {
		token := e.login(e.newUser(1<<30, 5))
		w := e.createGrant(token, a.ID, "offline-"+strconv.Itoa(i))
		if w.Code != 202 {
			t.Fatalf("grant: %d %s", w.Code, w.Body.String())
		}
		id := grantID(t, w)
		if g := e.grant(id); g.NodeID != a.ID {
			t.Fatal("grant placed behind an offline agent")
		}
		if i > 0 {
			continue
		}
		// Revoking with a failure reason is a health report (contract v3 grant_revoke).
		if r := e.client("POST", "/grants/"+id+"/revoke", token, nil, map[string]string{"reason": "bogus"}); r.Code != 400 || errorCode(r) != "invalid_release" {
			t.Fatalf("revoke reason allow-list: %d %s", r.Code, r.Body.String())
		}
		r := e.client("POST", "/grants/"+id+"/revoke", token, nil, map[string]string{"reason": "tunnel_failed"})
		contractShapeV3(t, "grant_revoke", "response", r.Body.Bytes())
		var events int64
		e.db.Model(&model.NodeHealthEvent{}).Where("node_id = ? AND reason = ?", a.ID, model.LeaseTunnelFailed).Count(&events)
		var lease model.ConnectionLease
		e.db.Take(&lease, "grant_id = ?", id)
		if r.Code != 200 || events != 1 || lease.EndReason != model.LeaseTunnelFailed {
			t.Fatalf("revoke with failure: %d events %d lease %+v", r.Code, events, lease)
		}
	}
	// Agent lost: the grant is no longer extended, so its lease stops renewing and ends
	// with the grant when the control plane reconciles it.
	lost := e.login(e.newUser(1<<30, 5))
	w := e.createGrant(lost, a.ID, "offline-lost")
	lostID := grantID(t, w)
	e.db.Model(&model.ConnectionGrant{}).Where("id = ?", lostID).Update("expires_at", stale)
	var before, after model.ConnectionLease
	e.db.Take(&before, "grant_id = ?", lostID)
	if r := e.client("GET", "/session", lost, nil, nil); r.Code != 200 {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	e.db.Take(&after, "grant_id = ?", lostID)
	if !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatal("a lapsed grant still renewed its lease")
	}
	if _, err := ReconcileGrants(t.Context(), e.db, e.cfg); err != nil {
		t.Fatal(err)
	}
	if e.db.Take(&after, "grant_id = ?", lostID); after.State != model.LeaseEnded || after.EndReason != model.LeaseExpired {
		t.Fatalf("lease after the lost agent's grant was reconciled: %+v", after)
	}
	e.db.Model(&model.NodeAgent{}).Where("id = ?", agentA.id).Update("last_seen_at", stale)
	token := e.login(e.newUser(1<<30, 5))
	if w := e.createGrant(token, a.ID, "offline-last"); w.Code != 503 || errorCode(w) != "agent_unavailable" {
		t.Fatalf("all agents offline: %d %s", w.Code, w.Body.String())
	}
}

// Opt-in (ADMISSION_TIMING=500): concurrent admissions to one group, latency per
// request including the wait for the group lock.
func TestAdmissionTimingPostgres(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("ADMISSION_TIMING"))
	if n == 0 {
		t.Skip("set ADMISSION_TIMING=500 to measure")
	}
	e := newPresenceEnv(t, nil)
	sqlDB, _ := e.db.DB()
	sqlDB.SetMaxOpenConns(80)
	g := e.group("JP1", "[]")
	e.servers(g, 200, 200, 200, 200, 200)
	tokens := make([]string, n)
	for i := range tokens {
		_, tokens[i] = e.device()
	}
	latencies := make([]time.Duration, n)
	var wg sync.WaitGroup
	start := time.Now()
	serial := os.Getenv("ADMISSION_SERIAL") == "1" // the uncontended cost of one admission
	for i, token := range tokens {
		if serial {
			began := time.Now()
			e.want(e.admit(token, g.ID), 201, "")
			latencies[i] = time.Since(began)
			continue
		}
		spawn(&wg, func() {
			began := time.Now()
			w := e.admit(token, g.ID)
			latencies[i] = time.Since(began)
			if w.Code != 201 {
				t.Errorf("admission %d: %d %s", i, w.Code, w.Body.String())
			}
		})
	}
	wg.Wait()
	total := time.Since(start)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("%d concurrent admissions to one group: total %v, p50 %v, p95 %v, max %v, %.0f/s", n, total, latencies[n/2], latencies[n*95/100], latencies[n-1], float64(n)/total.Seconds())
}
