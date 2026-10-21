package api

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// Round 5 (presence plan 10.1): a node-reported Core needs the server it was placed on to
// retry elsewhere once (avoid_node_id), and must be told connection_reassigned, not the
// grant_expired that follows when the agent revokes the grant of a withdrawn server.
func TestGrantReadyNamesItsServerAndReassignsPostgres(t *testing.T) {
	e := newGrantEnv(t, nil)
	a, b := e.addNode("JP1", 1000, true), e.addNode("JP1", 1000, true)
	e.db.Model(&model.Node{}).Where("id = ?", b.ID).Update("group_id", a.ID)
	e.db.Delete(&model.NodeGroup{}, "id = ?", b.ID)
	agents := map[string]*testAgent{a.ID: e.addAgent(a), b.ID: e.addAgent(b)}
	for _, agent := range agents {
		e.sync(agent, nil, nil)
	}
	token := e.login(e.newUser(1<<30, 5))
	w := e.createGrant(token, a.ID, "reassign-0001")
	if w.Code != 202 {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	id := grantID(t, w)
	placed := e.grant(id).NodeID
	agent := agents[placed]
	e.sync(agent, nil, nil)          // install
	e.sync(agent, []string{id}, nil) // confirmed
	got := e.client("GET", "/grants/"+id, token, nil, nil)
	if got.Code != 200 {
		t.Fatalf("ready: %d %s", got.Code, got.Body.String())
	}
	contractShapeV3(t, "grant_ready", "response", got.Body.Bytes())
	var ready grantResponse
	if err := json.Unmarshal(got.Body.Bytes(), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.NodeID != a.ID || ready.ServerID != placed {
		t.Fatalf("node_id %s (group %s), server_id %s (placed %s)", ready.NodeID, a.ID, ready.ServerID, placed)
	}

	// The administrator withdraws that server (what POST /admin/nodes/:id does when it
	// disables one): the lease ends at once, the agent then revokes the grant.
	now := time.Now().UTC()
	s := &Server{cfg: e.cfg}
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Node{}).Where("id = ?", placed).Update("enabled", false).Error; err != nil {
			return err
		}
		return s.withdrawServer(tx, placed, now)
	}); err != nil {
		t.Fatal(err)
	}
	e.sync(agent, []string{id}, nil)
	if g := e.grant(id); g.State != grantRevoking && g.State != grantRevoked {
		t.Fatalf("test premise: the agent revokes the withdrawn server's grant, state %s", g.State)
	}
	r := e.client("GET", "/session", token, nil, nil)
	var session nativeState
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil || r.Code != 200 {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	if session.Reason != "connection_reassigned" || session.CanConnect || session.ConnectionState != model.LeaseEnded {
		t.Fatalf("withdrawn grant reported %q (state %s), want connection_reassigned", session.Reason, session.ConnectionState)
	}
	// Reconnecting, the device avoids the server it failed on and lands on the other.
	other := b.ID
	if placed == b.ID {
		other = a.ID
	}
	again := e.client("POST", "/grants", token, map[string]string{"Idempotency-Key": "reassign-0002"}, map[string]string{"node_id": a.ID, "avoid_node_id": placed})
	if again.Code != 202 || e.grant(grantID(t, again)).NodeID != other {
		t.Fatalf("reconnect: %d %s", again.Code, again.Body.String())
	}
}
