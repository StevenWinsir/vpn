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
	m.mu.Unlock()
	if err = m.refreshSession(ctx, gen, client, token); err != nil {
		return m.Snapshot(), err
	}
	return m.Snapshot(), nil
}

// SelectNode records the user's choice and fetches, verifies and holds that single
// node's config. catalogRevision must equal the revision the UI was looking at.
func (m *Manager) SelectNode(ctx context.Context, nodeID, catalogRevision string) (Snapshot, error) {
	gen, client, token, err := m.handles()
	if err != nil {
		return m.Snapshot(), err
	}
	m.mu.Lock()
	if catalogRevision != m.revision {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_catalog")
	}
	m.mu.Unlock()
	if err = m.bindNode(ctx, gen, client, token, nodeID); err != nil {
		return m.Snapshot(), err
	}
	return m.Snapshot(), nil
}

// bindNode downloads, verifies and stores the config for nodeID. The raw document
// lives only in this function's frame.
func (m *Manager) bindNode(ctx context.Context, gen uint64, client *apiclient.Client, token, nodeID string) error {
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return codeError("stale_generation")
	}
	var node *apiclient.NodeChoice
	for i := range m.nodes {
		if m.nodes[i].ID == nodeID {
			choice := m.nodes[i]
			node = &choice
		}
	}
	if node == nil {
		m.mu.Unlock()
		return codeError("node_unavailable")
	}
	var lastSequence int64
	if m.session != nil {
		lastSequence = m.session.LastSequence
	}
	// A previously held config is dropped before the request: if the fetch fails,
	// nothing stale can be used.
	m.profile = nil
	previous := m.state
	m.state = StatePreparing
	m.mu.Unlock()

	start := m.opts.Now()
	response, err := client.Config(ctx, token, nodeID, lastSequence)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		return codeError("stale_generation")
	}
	m.state = previous
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiclient.TerminatesSession(apiErr.Code) {
			m.dropSessionLocked(apiErr.Code)
			return err
		}
		m.failLocked(err)
		m.selected = nodeID
		return err
	}
	profile, parseErr := ParseProfile(nodeID, node.Name, response.Version, response.YAML)
	if parseErr != nil || response.NodeID != nodeID {
		m.failLocked(codeError("config_rejected"))
		return codeError("config_rejected")
	}
	m.applySessionLocked(response.Session, start)
	if response.Session.Reason != "" && response.Session.Reason != "profile_required" {
		// The bind went through but the account is not currently authorized.
		m.selected = nodeID
		m.failLocked(codeError(response.Session.Reason))
		return codeError(response.Session.Reason)
	}
	m.selected, m.profile, m.profileNode = nodeID, &profile, *node
	m.lastError = nil
	return nil
}

// Connect is the only path to Engine.Start, and it re-checks every gate itself:
// signed in, selected node, a fresh server authorization with an active lease, and
// a config that was validated against the same version. Stage 2 ships an engine that
// refuses, so a passing gate ends in tun_unavailable rather than a tunnel.
func (m *Manager) Connect(ctx context.Context) (Snapshot, error) {
	gen, client, token, err := m.handles()
	if err != nil {
		return m.Snapshot(), err
	}
	m.mu.Lock()
	selected := m.selected
	m.mu.Unlock()
	if selected == "" {
		return m.Snapshot(), codeError("profile_required")
	}

	// Fresh authorization first: connecting never relies on a cached verdict.
	if err = m.refreshSession(ctx, gen, client, token); err != nil {
		return m.Snapshot(), err
	}
	// If the server says our binding is stale (or we hold nothing), bind once.
	m.mu.Lock()
	need := m.profile == nil
	m.mu.Unlock()
	if need {
		if err = m.bindNode(ctx, gen, client, token, selected); err != nil {
			return m.Snapshot(), err
		}
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		return m.Snapshot(), codeError("stale_generation")
	}
	code := m.gateLocked()
	if code != "" {
		m.failLocked(codeError(code))
		m.mu.Unlock()
		return m.Snapshot(), codeError(code)
	}
	profile := *m.profile
	m.state = StateConnecting
	m.mu.Unlock()

	startErr := m.opts.Engine.Start(ctx, profile)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != gen {
		_ = m.opts.Engine.Stop()
		return m.snapshotLocked(), codeError("stale_generation")
	}
	if startErr != nil {
		code := "tun_unavailable"
		if !errors.Is(startErr, ErrTunUnavailable) {
			code = "network_unreachable"
		}
		m.state = StateReady
		m.failLocked(codeError(code))
		return m.snapshotLocked(), codeError(code)
	}
	m.state = StateConnected
	m.lastError = nil
	return m.snapshotLocked(), nil
}

// gateLocked is the authorization verdict as of now ("" means allowed). It is also
// what Snapshot reports, so the UI button and the real gate can never disagree.
func (m *Manager) gateLocked() string {
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

// Disconnect stops the data path. It is idempotent.
func (m *Manager) Disconnect() Snapshot {
	m.mu.Lock()
	if m.state == StateConnected || m.state == StateConnecting {
		m.state = StateStopping
	}
	m.mu.Unlock()
	_ = m.opts.Engine.Stop()
	m.mu.Lock()
	if m.state == StateStopping {
		m.state = StateReady
	}
	m.mu.Unlock()
	return m.Snapshot()
}
