package managed

import (
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/meter"
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
	// MeteringSource is "node_reported" for nodes billed from the node's own counters.
	MeteringSource string `json:"metering_source,omitempty"`
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

// MeteringView separates what the proxy physically carried from what was charged:
// upload/download are real proxy bytes (confirmed + unreported); the balance is
// confirmed (server) and estimated (confirmed minus unreported at the bound rate).
type MeteringView struct {
	Source        string `json:"source"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	LastSequence  int64  `json:"last_sequence"`
	UploadRate    int64  `json:"upload_bytes_per_second"`
	DownloadRate  int64  `json:"download_bytes_per_second"`

	ConfirmedUploadBytes    int64          `json:"confirmed_upload_bytes"`
	ConfirmedDownloadBytes  int64          `json:"confirmed_download_bytes"`
	ConfirmedRemainingBytes int64          `json:"confirmed_remaining_bytes"`
	EstimatedRemainingBytes int64          `json:"estimated_remaining_bytes"`
	RatePermille            int64          `json:"rate_permille"`
	Pending                 bool           `json:"pending"`
	History                 []meter.Sample `json:"history"`
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
	// Gate is present only while a connected segment's authorization gate is closed
	// (tunnel_health "gated": the tunnel is held and forwards nothing); LastGate only
	// once a gate has closed in this session. Both exist only under ResumeGate (Android).
	Gate     *GateView   `json:"gate,omitempty"`
	LastGate *GateRecord `json:"last_gate,omitempty"`
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
	switch m.state {
	case StateConnected:
		out.TunnelHealth = "up"
		if g := m.gate; g != nil {
			out.TunnelHealth = "gated"
			out.Gate = &GateView{Reason: g.reason, WaitingNetwork: g.waiting, Attempts: int64(g.attempts),
				ClosedSeconds: max(0, int64(m.opts.Now().Round(0).Sub(g.since.Round(0))/time.Second))}
		}
	case StateConnecting, StatePreparing:
		out.TunnelHealth = "starting"
	case StateStopping:
		out.TunnelHealth = "stopping"
	}
	if m.lastGate != nil {
		record := *m.lastGate
		out.LastGate = &record
	}
	if m.user != nil {
		out.User = &UserView{ID: m.user.ID, Email: m.user.Email, Name: m.user.Name, Role: m.user.Role}
	}
	for _, node := range m.nodes {
		out.Nodes = append(out.Nodes, NodeView{ID: node.ID, Name: node.Name, Region: node.Region, LineType: node.LineType, RatePermille: node.RatePermille, MeteringSource: node.MeteringSource})
	}
	if m.profile != nil {
		out.ConfigReady, out.ProfileVersion = true, m.profile.Version
	} else if m.selected != "" {
		// Nothing to fetch ahead of time: the server, its config and the online lease are
		// assigned when connecting (a grant in node-reported mode).
		out.ConfigReady = true
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
		view := &MeteringView{
			Source: s.MeteringSource, UploadBytes: s.UploadBytes, DownloadBytes: s.DownloadBytes, LastSequence: s.LastSequence,
			ConfirmedUploadBytes: s.UploadBytes, ConfirmedDownloadBytes: s.DownloadBytes,
			ConfirmedRemainingBytes: s.RemainingBytes, EstimatedRemainingBytes: s.RemainingBytes, RatePermille: s.RatePermille,
			History: []meter.Sample{},
		}
		if m.local != nil && s.MeteringSource == apiclient.MeteringNode {
			// The node is the ledger. Show what this machine measured (speed, session
			// bytes) next to the server's own balance, never a client-side "confirmation".
			v := m.local.View()
			view.UploadBytes, view.DownloadBytes = v.UploadBytes, v.DownloadBytes
			view.ConfirmedUploadBytes, view.ConfirmedDownloadBytes = 0, 0
			view.ConfirmedRemainingBytes, view.EstimatedRemainingBytes = s.RemainingBytes, min(s.RemainingBytes, v.EstimatedRemaining)
			view.RatePermille, view.Pending, view.LastSequence = s.RatePermille, false, 0
			view.UploadRate, view.DownloadRate = v.UploadRate, v.DownloadRate
			if v.History != nil {
				view.History = v.History
			}
		} else if m.meter != nil {
			v := m.meter.View()
			view.UploadBytes, view.DownloadBytes = v.UploadBytes, v.DownloadBytes
			view.LastSequence = v.LastSequence
			view.ConfirmedUploadBytes, view.ConfirmedDownloadBytes = v.ConfirmedUpload, v.ConfirmedDownload
			view.ConfirmedRemainingBytes, view.EstimatedRemainingBytes = v.ConfirmedRemaining, v.EstimatedRemaining
			view.RatePermille, view.Pending = v.RatePermille, v.Pending
			view.UploadRate, view.DownloadRate = v.UploadRate, v.DownloadRate
			if v.History != nil {
				view.History = v.History
			}
		}
		out.Metering = view
	}
	return out
}
