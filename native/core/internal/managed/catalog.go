package managed

import (
	"context"
	"errors"
	"time"

	"asterlink/core/internal/apiclient"
)

// minPollGap keeps a misbehaving server (one that answers the long-poll instantly)
// from turning the watcher into a hot loop.
const minPollGap = 500 * time.Millisecond

// watchCatalog runs the single /nodes long-poll for one session generation. It only
// observes the catalog: it never counts as authorization (section 5.2).
func (m *Manager) watchCatalog(ctx context.Context, gen uint64, client *apiclient.Client, token string) {
	revision := ""
	backoff := m.opts.BackoffMin
	for {
		began := time.Now()
		catalog, err := client.Nodes(ctx, token, revision)
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		if m.generation != gen {
			m.mu.Unlock()
			return
		}
		if err != nil {
			var apiErr *apiclient.Error
			wait := backoff
			if errors.As(err, &apiErr) {
				if apiclient.TerminatesSession(apiErr.Code) {
					m.dropSessionLocked(apiErr.Code)
					m.mu.Unlock()
					return
				}
				if apiErr.Code == "node_catalog_disabled" {
					wait = 30 * time.Second
				}
			}
			m.failLocked(err)
			m.mu.Unlock()
			if !sleepCtx(ctx, jitter(wait)) {
				return
			}
			backoff = min(backoff*2, m.opts.BackoffMax)
			continue
		}
		backoff = m.opts.BackoffMin
		changed := catalog.Revision != revision
		m.applyCatalogLocked(catalog)
		m.mu.Unlock()
		revision = catalog.Revision
		if changed {
			// An entitlement or node edit: pull the matching session promptly so the
			// plan, quota and authorization shown are the server's current view.
			m.refreshSession(ctx, gen, client, token)
		}
		if gap := minPollGap - time.Since(began); gap > 0 && !sleepCtx(ctx, gap) {
			return
		}
	}
}

// refreshSession performs one GET /session and applies it (generation-checked).
func (m *Manager) refreshSession(ctx context.Context, gen uint64, client *apiclient.Client, token string) error {
	start := m.opts.Now()
	s, err := client.Session(ctx, token)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return &apiclient.Error{Code: "stale_generation"}
	}
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.dropSessionLocked(apiErr.Code)
			return err
		}
		m.failLocked(err)
		return err
	}
	if apiclient.TerminatesSession(s.Reason) {
		m.dropSessionLocked(s.Reason)
		return &apiclient.Error{Code: s.Reason}
	}
	m.applySessionLocked(s, start)
	return nil
}

// applyCatalogLocked installs a new catalog and invalidates whatever depended on a
// node that has changed or vanished.
func (m *Manager) applyCatalogLocked(c apiclient.Catalog) {
	m.nodes, m.revision, m.catalogReady, m.catalogReason = c.Nodes, c.Revision, true, c.Reason
	if c.Reason != "" {
		m.profile = nil
	}
	if m.selected == "" {
		return
	}
	var current *apiclient.NodeChoice
	for i := range m.nodes {
		if m.nodes[i].ID == m.selected {
			current = &m.nodes[i]
		}
	}
	switch {
	case current == nil && c.Reason == "":
		// Disabled, deleted, or no longer in the plan.
		m.selected, m.profile = "", nil
		m.lastError = &errorInfo{Code: "node_unavailable", Message: apiclient.Message("node_unavailable")}
	case current != nil && m.profile != nil && *current != m.profileNode:
		// Name, line type, rate or version moved: the held config is no longer current.
		m.profile = nil
		m.lastError = &errorInfo{Code: "profile_changed", Message: apiclient.Message("profile_changed")}
	}
}
