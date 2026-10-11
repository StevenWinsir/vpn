package managed

import (
	"time"

	"asterlink/core/internal/apiclient"
)

type UserView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// NodeView is metadata only. There is intentionally no field that could carry a
// server address, port, credential or configuration text.
type NodeView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	LineType     string `json:"line_type"`
	RatePermille int64  `json:"rate_permille"`
}

type EntitlementView struct {
	PlanName               string     `json:"plan_name"`
	SubscriptionExpiresAt  *time.Time `json:"subscription_expires_at"`
	AuthorizationExpiresAt *time.Time `json:"authorization_expires_at"`
	LeaseRemainingSeconds  int64      `json:"lease_remaining_seconds"`
	// AccountAuthorized: the account may use the proxy (ignores whether a node is chosen).
	AccountAuthorized bool `json:"account_authorized"`
	// CanConnect: every gate for pressing Connect right now is satisfied.
	CanConnect     bool   `json:"can_connect"`
	Reason         string `json:"reason"`
	ReasonMessage  string `json:"reason_message"`
	TotalBytes     int64  `json:"total_bytes"`
	RemainingBytes int64  `json:"remaining_bytes"`
	UsedUnits      int64  `json:"used_units"`
}

type MeteringView struct {
	Source        string `json:"source"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	LastSequence  int64  `json:"last_sequence"`
	// Stage 2 has no tunnel and therefore no live rate.
	UploadRate   int64 `json:"upload_bytes_per_second"`
	DownloadRate int64 `json:"download_bytes_per_second"`
}

type ErrorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Snapshot is the complete, redacted UI state (section 5.3). It contains no token,
// password, YAML or connection material, and nothing here can be used to obtain them.
type Snapshot struct {
	Generation      uint64           `json:"generation"`
	State           State            `json:"state"`
	User            *UserView        `json:"user"`
	Entitlement     *EntitlementView `json:"entitlement"`
	Nodes           []NodeView       `json:"nodes"`
	SelectedNodeID  string           `json:"selected_node_id"`
	CatalogRevision string           `json:"catalog_revision"`
	ProfileVersion  string           `json:"profile_version"`
	ConfigReady     bool             `json:"config_ready"`
	TunnelHealth    string           `json:"tunnel_health"`
	Metering        *MeteringView    `json:"metering"`
	LastError       *ErrorView       `json:"last_error"`
	CoreVersion     string           `json:"core_version"`
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() Snapshot {
	out := Snapshot{
		Generation: m.generation, State: m.state, Nodes: []NodeView{}, TunnelHealth: "idle",
		CatalogRevision: m.revision, SelectedNodeID: m.selected, CoreVersion: m.opts.CoreVersion,
	}
	if m.lastError != nil {
		out.LastError = &ErrorView{Code: m.lastError.Code, Message: m.lastError.Message}
	}
	if m.state == StateConnected {
		out.TunnelHealth = "up"
	}
	if m.user != nil {
		out.User = &UserView{ID: m.user.ID, Email: m.user.Email, Name: m.user.Name, Role: m.user.Role}
	}
	for _, node := range m.nodes {
		out.Nodes = append(out.Nodes, NodeView{ID: node.ID, Name: node.Name, Region: node.Region, LineType: node.LineType, RatePermille: node.RatePermille})
	}
	if m.profile != nil {
		out.ConfigReady, out.ProfileVersion = true, m.profile.Version
	}
	if s := m.session; s != nil {
		now := m.opts.Now()
		reason := m.gateLocked()
		// An unselected node is not an entitlement problem; report the account verdict.
		accountReason := reason
		if reason == "profile_required" || reason == "profile_changed" {
			accountReason = ""
		}
		out.Entitlement = &EntitlementView{
			PlanName: s.PlanName, SubscriptionExpiresAt: s.SubscriptionExpiresAt, AuthorizationExpiresAt: s.AuthorizationExpiresAt,
			LeaseRemainingSeconds: int64(m.lease.remaining(now) / time.Second),
			AccountAuthorized:     accountReason == "",
			CanConnect:            reason == "",
			Reason:                accountReason,
			TotalBytes:            s.TotalBytes, RemainingBytes: s.RemainingBytes, UsedUnits: s.UsedUnits,
		}
		if accountReason != "" {
			out.Entitlement.ReasonMessage = apiclient.Message(accountReason)
		}
		out.Metering = &MeteringView{Source: s.MeteringSource, UploadBytes: s.UploadBytes, DownloadBytes: s.DownloadBytes, LastSequence: s.LastSequence}
	}
	return out
}
