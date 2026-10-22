package managed

import (
	"context"
	"errors"

	"asterlink/core/internal/apiclient"
)

func codeError(code string) error { return &apiclient.Error{Code: code} }

// session returns the live session handles, or not_signed_in.
func (m *Manager) handles() (uint64, *apiclient.Client, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil || m.token == "" {
		return 0, nil, "", codeError("not_signed_in")
	}
	return m.generation, m.client, m.token, nil
}

// RefreshCatalog forces one immediate catalog and session sync (used on app launch,
// foreground and wake). The background watcher keeps running independently.
func (m *Manager) RefreshCatalog(ctx context.Context) (Snapshot, error) {
	gen, client, token, err := m.handles()
	if err != nil {
		return m.Snapshot(), err
	}
	catalog, err := client.Nodes(ctx, token, "")
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_generation")
	}
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.dropSessionLocked(apiErr.Code)
		} else {
			m.failLocked(err)
		}
		m.mu.Unlock()
		return m.Snapshot(), err
	}
	m.applyCatalogLocked(catalog)
	m.catalogStale = m.background
	m.mu.Unlock()
	if err = m.refreshSession(ctx, gen, client, token); err != nil {
		return m.Snapshot(), err
	}
	return m.Snapshot(), nil
}

// syncCatalog reads the catalog once (no long-poll) and applies it.
func (m *Manager) syncCatalog(ctx context.Context, gen uint64, client *apiclient.Client, token string) error {
	m.mu.Lock()
	pauses := m.pauses
	m.mu.Unlock()
	catalog, err := client.Nodes(ctx, token, "")
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return codeError("stale_generation")
	}
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.dropSessionLocked(apiErr.Code)
		} else {
			m.failLocked(err)
		}
		return err
	}
	m.applyCatalogLocked(catalog)
	if m.pauses == pauses {
		m.catalogStale = m.background
	}
	return nil
}

// SelectNode records the user's choice of line (a node group). It sends nothing and takes
// no slot: the online lease and the one server's config are obtained when the user connects
// (contract native-v3), in either metering mode. catalogRevision must be the revision the UI
// was looking at, or an earlier one whose list of lines is unchanged (rememberSameListLocked).
func (m *Manager) SelectNode(ctx context.Context, nodeID, catalogRevision string) (Snapshot, error) {
	gen, client, token, err := m.handles()
	if err != nil {
		return m.Snapshot(), err
	}
	m.mu.Lock()
	if catalogRevision != m.revision && !m.sameList[catalogRevision] {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_catalog")
	}
	m.bumpIntentLocked()
	m.mu.Unlock()
	// Switching lines never overlaps with forwarding: close the data path, have the old tail
	// acknowledged and the old lease released before the new line is recorded.
	if err = m.stopTunnel(ctx, ""); err != nil {
		return m.Snapshot(), err
	}
	m.mu.Lock()
	stale := m.catalogStale
	m.mu.Unlock()
	if stale {
		// The long-poll was paused (Android background): the line's metadata may be old, and
		// a selection made against it would be invalidated as "changed" by the next catalog.
		if err = m.syncCatalog(ctx, gen, client, token); err != nil {
			return m.Snapshot(), err
		}
	}
	return m.selectLine(gen, nodeID)
}

// selectLine records nodeID as the selection if the catalog offers it.
func (m *Manager) selectLine(gen uint64, nodeID string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return m.snapshotLocked(), codeError("stale_generation")
	}
	for i := range m.nodes {
		if m.nodes[i].ID == nodeID {
			m.selected, m.profile, m.profileNode = nodeID, nil, m.nodes[i]
			m.lastError = nil
			return m.snapshotLocked(), nil
		}
	}
	return m.snapshotLocked(), codeError("node_unavailable")
}

// gateLocked is the authorization verdict as of now ("" means allowed). It is also
// what Snapshot reports, so the UI button and the real gate can never disagree. Before a
// segment holds its lease (and, node-reported, before its tunnel runs) the verdict is
// whether pressing Connect may proceed; the admission itself is then the server's check.
func (m *Manager) gateLocked() string {
	if m.client != nil && m.session != nil && m.conn == nil && (m.admission == nil || m.selectedNodeReportedLocked()) {
		return m.admitGateLocked()
	}
	switch {
	case m.client == nil || m.session == nil:
		return "not_signed_in"
	case m.catalogReason != "":
		return m.catalogReason
	case m.session.Reason != "":
		return m.session.Reason
	case !m.session.CanConnect:
		return "upgrade_required"
	case !m.lease.active(m.opts.Now()):
		return "lease_expired"
	case m.selected == "":
		return "profile_required"
	case m.profile == nil:
		return "profile_changed"
	}
	return ""
}
