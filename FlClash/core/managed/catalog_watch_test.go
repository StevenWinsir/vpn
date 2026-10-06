package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func signCatalog(catalog Catalog) Catalog {
	data, _ := json.Marshal(struct {
		Reason string       `json:"reason"`
		Nodes  []NodeChoice `json:"nodes"`
	}{catalog.Reason, catalog.Nodes})
	hash := sha256.Sum256(data)
	catalog.Revision = hex.EncodeToString(hash[:])
	return catalog
}

func TestCatalogWatchAppliesDeletionWithoutHeartbeatAndClearsLastNode(t *testing.T) {
	fixture, c, stops := newAccountFixture(t)
	engine := attachConfigurationStub(t, c)
	var stage atomic.Int32
	var watches atomic.Int64
	nodes := []NodeChoice{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "A", LineType: "direct", RatePermille: 500, Version: 1},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "B", LineType: "dedicated", RatePermille: 1000, Version: 1},
	}
	fixture.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/nodes" && r.URL.Path != "/config" {
			return false
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		status, ok := fixture.sessions[token]
		if !ok {
			t.Error("catalog request omitted session authentication")
			w.WriteHeader(401)
			return true
		}
		selected := append([]NodeChoice{}, nodes[int(stage.Load()):]...)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/nodes" {
			watches.Add(1)
			_ = json.NewEncoder(w).Encode(signCatalog(Catalog{SessionID: status.SessionID, Nodes: selected}))
			return true
		}
		if len(selected) == 0 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"client_config_unavailable"}}`))
			return true
		}
		yaml := "proxies: []\n# " + selected[0].Name
		hash := sha256.Sum256([]byte(yaml))
		status.ProfileVersion = hex.EncodeToString(hash[:])
		status.RatePermille = selected[0].RatePermille
		status = fixture.fresh(status)
		fixture.sessions[token] = status
		_ = json.NewEncoder(w).Encode(Profile{YAML: yaml, Version: status.ProfileVersion, Session: status, NodeID: selected[0].ID, Nodes: selected})
		return true
	}
	state := mustAccountLogin(t, c)
	await := func(want int, empty bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			ready := c.profile != nil && len(c.profile.Nodes) == want && c.snapshot.Configuration != nil
			if empty {
				ready = c.profile == nil && c.snapshot.Configuration == nil && c.snapshot.ErrorCode == "client_config_unavailable"
			}
			c.mu.Unlock()
			if ready {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("catalog did not synchronize stage %d", stage.Load())
	}
	await(2, false)
	c.mu.Lock()
	lease, applied := c.configurationDeadline, engine.applied.Load()
	c.mu.Unlock()
	observation, _ := c.observeCatalog(state.Generation)
	c.synchronizeCatalog(context.Background(), observation, Catalog{SessionID: observation.sessionID, Nodes: append([]NodeChoice(nil), nodes...)})
	c.mu.Lock()
	unchanged := c.configurationDeadline.Equal(lease) && engine.applied.Load() == applied
	c.mu.Unlock()
	if !unchanged {
		t.Fatal("unchanged metadata renewed a lease or reapplied configuration")
	}
	for _, mutate := range []func(*catalogObservation){
		func(o *catalogObservation) { o.generation++ },
		func(o *catalogObservation) { o.runtimeRevision++ },
		func(o *catalogObservation) { o.configurationID = "stale" },
		func(o *catalogObservation) { o.sessionID = "other-session" },
	} {
		stale := observation
		mutate(&stale)
		c.synchronizeCatalog(context.Background(), stale, Catalog{SessionID: observation.sessionID, Nodes: nodes[1:]})
		if engine.applied.Load() != applied {
			t.Fatal("stale watch response replaced configuration")
		}
	}
	beforeStops := stops.Load()
	stage.Store(1)
	await(1, false)
	if c.Snapshot().CanConnect || stops.Load() <= beforeStops {
		t.Fatal("deletion did not stop the old runtime before replacement")
	}
	stage.Store(2)
	await(0, true)
	assertStoredAbsent(t, engine.home)
	if fixture.reports.Load() != 0 || fixture.refreshes.Load() != 0 || watches.Load() < 3 {
		t.Fatal("watch used traffic heartbeats or did not run independently")
	}
	if _, err := c.Logout(context.Background(), state.Generation); err != nil {
		t.Fatal(err)
	}
	c.synchronizeCatalog(context.Background(), observation, Catalog{SessionID: observation.sessionID, Nodes: nodes})
	if c.Snapshot().User != nil || c.Snapshot().Configuration != nil {
		t.Fatal("late watch revived logged-out session")
	}
}

func TestCatalogRejectsMalformedOrTamperedMetadata(t *testing.T) {
	fixture, c, _ := newAccountFixture(t)
	var mode atomic.Int32
	fixture.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/nodes" {
			return false
		}
		catalog := signCatalog(Catalog{SessionID: "session-1", Nodes: []NodeChoice{}})
		switch mode.Load() {
		case 1:
			catalog.Nodes = nil
		case 2:
			catalog.Revision = strings.Repeat("0", 64)
		case 3:
			catalog = signCatalog(Catalog{SessionID: "session-1", Reason: "private-stack-trace", Nodes: []NodeChoice{}})
		case 4:
			catalog = signCatalog(Catalog{SessionID: "session-1", Nodes: []NodeChoice{{ID: "invalid", Name: "A", Version: 1, LineType: "direct", RatePermille: 500}}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalog)
		return true
	}
	state := mustAccountLogin(t, c)
	observation, _ := c.observeCatalog(state.Generation)
	for index := int32(1); index <= 4; index++ {
		mode.Store(index)
		if _, err := observation.client.Catalog(context.Background(), ""); PublicError(err) != "invalid_server_response" {
			t.Fatalf("invalid catalog %d accepted: %v", index, err)
		}
	}
	mode.Store(0)
	if _, err := observation.client.Catalog(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}
