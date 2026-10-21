package managed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"asterlink/core/internal/apiclient"
)

// connFake is the online-lease half of the scriptable API (contract native-v3): a miniature
// of POST /connections and POST /connections/:id/release with servers inside each group.
type connFake struct {
	leases map[string]*fakeLease
	byKey  map[string]string
	seq    int
	// servers lists each group's servers (default: two per group); down ones are skipped.
	servers map[string][]string
	down    map[string]bool

	releaseFailures int // releases answered 503 before one succeeds
	releaseGate     chan struct{}
	// connectGate, when set, holds every admission until closed (connectEntered is told).
	connectGate, connectEntered chan struct{}
	// atRelease records the ledger sequence the server had confirmed when each release came.
	atRelease []int64
	// failFirst admissions are applied and then answered 503 (a lost reply).
	failFirst int
	keys      []string
	lastAvoid string
	avoids    []string
	releases  []fakeRelease

	connects, releaseHits atomic.Int32
}

type fakeLease struct {
	id, group, server, state, key string
}

type fakeRelease struct{ id, reason string }

func serverOf(group string, i int) string {
	return fmt.Sprintf("%s-%04d-4000-8000-%012d", group[:8], i, i)
}

func (c *connFake) serversOf(group string) []string {
	if list, ok := c.servers[group]; ok {
		return list
	}
	return []string{serverOf(group, 1), serverOf(group, 2)}
}

func (f *fakeAPI) connectionCreate(w http.ResponseWriter, r *http.Request) {
	if !f.auth(w, r) {
		return
	}
	f.conns.connects.Add(1)
	if f.conns.connectEntered != nil {
		select {
		case f.conns.connectEntered <- struct{}{}:
		default:
		}
	}
	if gate := f.conns.connectGate; gate != nil {
		<-gate
	}
	var in apiclient.ConnectionRequest
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		writeError(w, apiError{400, "invalid_request"})
		return
	}
	key := r.Header.Get("Idempotency-Key")
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &f.conns
	c.keys = append(c.keys, key)
	if c.leases == nil {
		c.leases, c.byKey, c.down = map[string]*fakeLease{}, map[string]string{}, map[string]bool{}
	}
	c.lastAvoid = in.AvoidNodeID
	c.avoids = append(c.avoids, in.AvoidNodeID)
	f.lastSeqSeen = fmt.Sprint(in.LastSequence)
	if f.configErr != nil {
		writeError(w, *f.configErr)
		return
	}
	switch f.session.Reason {
	case "", "profile_required", "profile_changed", "connection_expired", "connection_reassigned", "client_config_unavailable":
	default: // like the real endpoint: an entitlement problem is a 403 carrying the reason
		writeError(w, apiError{403, f.session.Reason})
		return
	}
	if in.LastSequence != f.session.LastSequence {
		writeError(w, apiError{409, "traffic_sequence"})
		return
	}
	var group *apiclient.NodeChoice
	for i := range f.nodes {
		if f.nodes[i].ID == in.GroupID {
			group = &f.nodes[i]
		}
	}
	if group == nil || key == "" {
		writeError(w, apiError{503, "group_unavailable"})
		return
	}
	if group.MeteringSource == apiclient.MeteringNode {
		writeError(w, apiError{409, "grant_required"})
		return
	}
	status := 201
	var lease *fakeLease
	if id, ok := c.byKey[key]; ok {
		lease, status = c.leases[id], 200
	} else {
		server := ""
		for _, s := range c.serversOf(group.ID) {
			if s != in.AvoidNodeID && !c.down[s] {
				server = s
				break
			}
		}
		if server == "" {
			writeError(w, apiError{503, "group_unavailable"})
			return
		}
		for _, old := range c.leases {
			if old.state == apiclient.ConnectionActive {
				old.state = apiclient.ConnectionEnded // this device's older lease is replaced
			}
		}
		c.seq++
		lease = &fakeLease{id: fmt.Sprintf("77777777-7777-4777-8777-%012d", c.seq), group: group.ID, server: server, state: apiclient.ConnectionActive, key: key}
		c.leases[lease.id], c.byKey[key] = lease, lease.id
	}
	if c.failFirst > 0 {
		c.failFirst--
		writeError(w, apiError{503, "client_service_unavailable"})
		return
	}
	document := f.configYAML(group.Name)
	s := &f.session
	s.ProfileVersion, s.Reason, s.CanConnect = sha(document), "", true
	s.MeteringSource = "client_reported" // the rate stays the session's (tests pick it)
	s.ConnectionID, s.ConnectionState = lease.id, lease.state
	expires := time.Now().UTC().Add(90 * time.Second)
	s.ConnectionExpiresAt = &expires
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiclient.ConnectionResponse{
		Connection: apiclient.Connection{ID: lease.id, GroupID: group.ID, State: lease.state, ExpiresAt: expires},
		NodeID:     lease.server, YAML: document, Version: sha(document), Nodes: f.nodes, Session: f.snapshotSession(),
	})
}

func (f *fakeAPI) connectionRelease(w http.ResponseWriter, r *http.Request) {
	if !f.auth(w, r) {
		return
	}
	f.conns.releaseHits.Add(1)
	if gate := f.conns.releaseGate; gate != nil {
		<-gate
	}
	id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/client/connections/"), "/release")
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &f.conns
	if c.releaseFailures > 0 {
		c.releaseFailures--
		writeError(w, apiError{503, "client_service_unavailable"})
		return
	}
	lease := c.leases[id]
	if !ok || lease == nil {
		writeError(w, apiError{404, "connection_not_found"})
		return
	}
	switch in.Reason {
	case "user", "stopped", "network_changed", "lifecycle", "tunnel_unhealthy", "tunnel_failed":
	default:
		writeError(w, apiError{400, "invalid_release"})
		return
	}
	c.releases = append(c.releases, fakeRelease{id, in.Reason})
	c.atRelease = append(c.atRelease, f.session.LastSequence)
	lease.state = apiclient.ConnectionEnded
	if f.session.ConnectionID == id {
		f.session.ConnectionState = apiclient.ConnectionEnded
	}
	json.NewEncoder(w).Encode(apiclient.ReleaseResponse{Connection: apiclient.Connection{ID: id, GroupID: lease.group, State: lease.state}, Session: f.snapshotSession()})
}

// reassign is the administrator withdrawing the server the session's lease is on: the
// lease ends, and the session reports connection_reassigned until the device reconnects.
func (f *fakeAPI) reassign() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	lease := f.conns.leases[f.session.ConnectionID]
	if lease == nil {
		f.t.Fatal("no lease to reassign")
	}
	lease.state = apiclient.ConnectionEnded
	if f.conns.down == nil {
		f.conns.down = map[string]bool{}
	}
	f.conns.down[lease.server] = true
	f.session.ConnectionState, f.session.Reason, f.session.CanConnect = apiclient.ConnectionEnded, "connection_reassigned", false
	return lease.server
}

// activeLeases counts the leases the fake server holds open.
func (f *fakeAPI) activeLeases() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, l := range f.conns.leases {
		if l.state == apiclient.ConnectionActive {
			n++
		}
	}
	return n
}

func (f *fakeAPI) releasesSeen() []fakeRelease {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRelease(nil), f.conns.releases...)
}
