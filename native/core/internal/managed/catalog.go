package managed

import (
	"context"
	"errors"
	"slices"
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
		// In the background the long-poll pauses (SetForeground); it resumes at once.
		if !m.awaitForeground(ctx) {
			return
		}
		began := time.Now()
		m.mu.Lock()
		pauses := m.pauses
		m.mu.Unlock()
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
		refresh := catalog.Revision != revision && m.catalogNewsLocked(catalog)
		m.applyCatalogLocked(catalog)
		if m.pauses == pauses {
			m.catalogStale = false
		}
		m.mu.Unlock()
		revision = catalog.Revision
		if refresh {
			// An entitlement or node edit, or the running segment's lease ended at the
			// server: pull the matching session promptly so the plan, quota, authorization
			// and a reassignment are the server's current view.
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

// catalogNewsLocked reports whether a catalog with a new revision says something the session
// must be re-read for. The revision also moves with this session's own lease summary on every
// connect and disconnect; that alone is not news (re-reading /session then would make it a
// second renewal writer while connected), except when the summary says the lease of the
// running segment has ended (an administrator withdrew its server: connection_reassigned).
func (m *Manager) catalogNewsLocked(c apiclient.Catalog) bool {
	if m.revision == "" || c.Reason != m.catalogReason || !slices.Equal(c.Nodes, m.nodes) {
		return true
	}
	return m.conn != nil && c.Connection != nil && c.Connection.State != apiclient.ConnectionActive
}

// sameListRevisions bounds how many earlier revisions of an unchanged list are remembered
// (each connect and disconnect adds one).
const sameListRevisions = 16

// rememberSameListLocked keeps the outgoing revision acceptable for SelectNode when the new
// catalog lists exactly the same lines: the revision also covers this session's lease
// summary, which moves on every connect and disconnect, and a choice made against the list
// the user is looking at must not be refused for that (stale_catalog). Any change to the
// lines themselves forgets every earlier revision.
func (m *Manager) rememberSameListLocked(c apiclient.Catalog) {
	if m.revision == "" || c.Revision == m.revision {
		return
	}
	if c.Reason != m.catalogReason || !slices.Equal(c.Nodes, m.nodes) {
		m.sameList = nil
		return
	}
	if m.sameList == nil || len(m.sameList) >= sameListRevisions {
		m.sameList = map[string]bool{}
	}
	m.sameList[m.revision] = true
}

// applyCatalogLocked installs a new catalog and invalidates whatever depended on a
// node that has changed or vanished.
func (m *Manager) applyCatalogLocked(c apiclient.Catalog) {
	m.rememberSameListLocked(c)
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
