package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// Every shipped Core checks that the node_id of a /config or ready-grant response is the
// line it selected (legacy-core ConfigForNode, native/core actions.go and grant.go). With
// groups the selection is a group id that need not equal any server id, so these
// responses must echo the group and keep the server opaque. Round 4 found this through
// the website-built group whose id is not a server id.

func TestSelectedLineEchoedForConfigPostgres(t *testing.T) {
	e := newPresenceEnv(t, nil)
	g := e.group("Echo-JP", "[]")
	servers := e.servers(g, 5, 5)
	u := e.user(5)
	token := e.session(u, uuid.NewString())
	w := e.native("GET", "/config?node_id="+g.ID+"&last_sequence=0", token, nil)
	e.want(w, 200, "")
	var out struct {
		NodeID string       `json:"node_id"`
		YAML   string       `json:"yaml"`
		Nodes  []nodeChoice `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, n := range out.Nodes {
		listed = listed || n.ID == g.ID
	}
	if out.NodeID != g.ID || !listed || !strings.Contains(out.YAML, "name: Echo-JP") {
		t.Fatalf("config must echo the selected group: %+v", out)
	}
	// The server it was assigned is recorded, never returned.
	var sess model.NativeSession
	e.db.Take(&sess, "token_hash = ?", refreshHash(token))
	if sess.NodeID != servers[0].ID && sess.NodeID != servers[1].ID || strings.Contains(w.Body.String(), sess.NodeID) {
		t.Fatalf("server leaked or not bound: session node %s in %s", sess.NodeID, w.Body.String())
	}
	// Selecting again with no node_id stays on the same line and still echoes the group.
	w = e.native("GET", "/config?last_sequence=0", token, nil)
	e.want(w, 200, "")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.NodeID != g.ID {
		t.Fatalf("reselect echoed %s, want group %s", out.NodeID, g.ID)
	}
}

func TestSelectedLineEchoedForGrantPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	a, b := e.addNode("Echo-Grant", 1000, true), e.addNode("Echo-Grant", 1000, true)
	// One group of a's id holding both; only b's agent is online, so the grant lands on
	// b, whose id is not the group id.
	e.db.Model(&model.Node{}).Where("id = ?", b.ID).Update("group_id", a.ID)
	e.db.Delete(&model.NodeGroup{}, "id = ?", b.ID)
	agentA, agentB := e.addAgent(a), e.addAgent(b)
	e.sync(agentA, nil, nil)
	e.sync(agentB, nil, nil)
	e.db.Model(&model.NodeAgent{}).Where("id = ?", agentA.id).Update("last_seen_at", time.Now().UTC().Add(-time.Hour))
	token := e.login(e.newUser(1<<30, 5))
	id := grantID(t, e.createGrant(token, a.ID, "echo-grant-0001"))
	if g := e.grant(id); g.NodeID != b.ID {
		t.Fatalf("grant placed on %s, want b", g.NodeID)
	}
	e.sync(agentB, []string{id}, nil)
	w := e.client("GET", "/grants/"+id, token, nil, nil)
	var out grantResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || out.YAML == "" {
		t.Fatalf("ready grant: %d %s", w.Code, w.Body.String())
	}
	// node_id echoes the group; the server appears only as the opaque server_id (contract v3,
	// round 5), which a client sends back solely as avoid_node_id after a failed probe.
	if out.NodeID != a.ID || out.ServerID != b.ID || strings.Count(w.Body.String(), b.ID) != 1 {
		t.Fatalf("grant must echo the selected group %s and name the server only as server_id: %s", a.ID, w.Body.String())
	}
}
