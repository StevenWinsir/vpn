package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"time"
)

type Catalog struct {
	SessionID string       `json:"session_id"`
	Revision  string       `json:"revision"`
	Reason    string       `json:"reason"`
	Nodes     []NodeChoice `json:"nodes"`
}

func (c *Client) Catalog(ctx context.Context, revision string) (Catalog, error) {
	var catalog Catalog
	path := "/nodes?" + url.Values{"revision": []string{revision}}.Encode()
	if err := c.do(ctx, "GET", path, nil, &catalog); err != nil {
		return catalog, err
	}
	valid := catalog.SessionID != "" && catalog.Nodes != nil && len(catalog.Nodes) <= 128
	if len(catalog.Nodes) > 0 {
		profile := Profile{Nodes: catalog.Nodes, NodeID: catalog.Nodes[0].ID, Session: Status{RatePermille: catalog.Nodes[0].RatePermille}}
		valid = valid && profile.ValidCatalog() && catalog.Reason == ""
	}
	if catalog.Reason != "" && PublicError(&APIError{catalog.Reason}) != catalog.Reason {
		valid = false
	}
	data, _ := json.Marshal(struct {
		Reason string       `json:"reason"`
		Nodes  []NodeChoice `json:"nodes"`
	}{catalog.Reason, catalog.Nodes})
	digest := sha256.Sum256(data)
	if !valid || catalog.Revision != hex.EncodeToString(digest[:]) {
		return Catalog{}, &APIError{"invalid_server_response"}
	}
	return catalog, nil
}

type catalogObservation struct {
	client          *Client
	generation      uint64
	runtimeRevision uint64
	configurationID string
	sessionID       string
}

func (c *Coordinator) observeCatalog(generation uint64) (catalogObservation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil || c.snapshot.Generation != generation || c.snapshot.Session == nil {
		return catalogObservation{}, false
	}
	observation := catalogObservation{client: c.client, generation: generation, runtimeRevision: c.runRevision, sessionID: c.snapshot.Session.SessionID}
	if c.snapshot.Configuration != nil {
		observation.configurationID = c.snapshot.Configuration.ID
	}
	return observation, true
}

func (c *Coordinator) synchronizeCatalog(ctx context.Context, observation catalogObservation, catalog Catalog) {
	if catalog.SessionID != observation.sessionID {
		return
	}
	if c.lock(ctx) != nil {
		return
	}
	defer c.unlock()
	c.mu.Lock()
	id := ""
	if c.snapshot.Configuration != nil {
		id = c.snapshot.Configuration.ID
	}
	current := c.client == observation.client && c.snapshot.Generation == observation.generation && c.runRevision == observation.runtimeRevision && id == observation.configurationID && !c.snapshot.Busy
	changed := false
	if current {
		if c.profile == nil {
			changed = len(catalog.Nodes) > 0 && catalog.Reason == ""
		} else if len(c.profile.Nodes) > 0 {
			changed = catalog.Reason != "" || !slices.Equal(c.profile.Nodes, catalog.Nodes)
		}
	}
	c.mu.Unlock()
	if changed {
		_, _ = c.requestLocked(ctx, observation.generation, true)
	}
}

func (c *Coordinator) watchCatalog(ctx context.Context, generation uint64) {
	revision := ""
	for {
		observation, ok := c.observeCatalog(generation)
		if !ok || ctx.Err() != nil {
			return
		}
		catalog, err := observation.client.Catalog(ctx, revision)
		delay := time.Second
		if err == nil {
			revision = catalog.Revision
			c.synchronizeCatalog(ctx, observation, catalog)
		} else {
			delay = 5 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
