// Package apiclient is the Core's only HTTP client for the existing v1 native API
// (handoff_app.md section 5.1). It owns no state beyond the opaque bearer token.
package apiclient

import "time"

// Session mirrors the server's nativeState. Fields are copied verbatim; the Core
// never infers authorization from anything but can_connect/reason and the dates.
type Session struct {
	ServerTime             time.Time  `json:"server_time"`
	SubscriptionExpiresAt  *time.Time `json:"subscription_expires_at"`
	AuthorizationExpiresAt *time.Time `json:"authorization_expires_at"`
	SessionIdleTimeout     int        `json:"session_idle_timeout_seconds"`
	ProfileVersion         string     `json:"profile_version"`
	SessionID              string     `json:"session_id"`
	ExpiresAt              time.Time  `json:"expires_at"`
	CanConnect             bool       `json:"can_connect"`
	Reason                 string     `json:"reason"`
	RemainingBytes         int64      `json:"remaining_bytes"`
	TotalBytes             int64      `json:"total_bytes"`
	UsedUnits              int64      `json:"used_units"`
	PlanName               string     `json:"plan_name"`
	LastSequence           int64      `json:"last_sequence"`
	UploadBytes            int64      `json:"upload_bytes"`
	DownloadBytes          int64      `json:"download_bytes"`
	ReportInterval         int        `json:"report_interval_seconds"`
	LeaseSeconds           int        `json:"lease_seconds"`
	MeteringSource         string     `json:"metering_source"`
	RatePermille           int64      `json:"rate_permille"`
	// Set only for node_reported sessions holding a grant (contract native-v2).
	GrantID        string     `json:"grant_id,omitempty"`
	GrantState     string     `json:"grant_state,omitempty"`
	GrantExpiresAt *time.Time `json:"grant_expires_at,omitempty"`
	// The session's latest connection lease (contract native-v3), present once the session
	// has held one. ConnectionState is active, expired or ended.
	ConnectionID        string     `json:"connection_id,omitempty"`
	ConnectionState     string     `json:"connection_state,omitempty"`
	ConnectionExpiresAt *time.Time `json:"connection_expires_at,omitempty"`
}

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type LoginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
}

type LoginResponse struct {
	Token   string  `json:"token"`
	User    User    `json:"user"`
	Session Session `json:"session"`
}

// NodeChoice is pure metadata: no server, port, password or UUID ever appears here.
type NodeChoice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	LineType     string `json:"line_type"`
	RatePermille int64  `json:"rate_permille"`
	Version      int64  `json:"version"`
	// MeteringSource is "node_reported" when a node-side agent governs this node; such
	// a node is only usable through a grant. Empty means the development ledger.
	MeteringSource string `json:"metering_source,omitempty"`
}

// MeteringNode marks nodes (and sessions) billed from the forwarder's own counters.
const MeteringNode = "node_reported"

type Catalog struct {
	SessionID string       `json:"session_id"`
	Revision  string       `json:"revision"`
	Reason    string       `json:"reason"`
	Nodes     []NodeChoice `json:"nodes"`
	// Connection summarises the session's latest lease taken by a v3 call; it is part of
	// the revision, so a withdrawn server wakes the long-poll (contract native-v3).
	Connection *CatalogConnection `json:"connection,omitempty"`
}

// CatalogConnection is the lease summary carried by GET /nodes (no expiry: renewals never
// wake the long-poll).
type CatalogConnection struct {
	ID      string `json:"id"`
	GroupID string `json:"group_id"`
	State   string `json:"state"`
}

// Connection is one online lease (contract native-v3): taken when a device actually
// connects, renewed by its traffic reports, released when it disconnects.
type Connection struct {
	ID        string    `json:"id"`
	GroupID   string    `json:"group_id"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Connection lease states.
const (
	ConnectionActive  = "active"
	ConnectionExpired = "expired"
	ConnectionEnded   = "ended"
)

// ConnectionRequest is the body of POST /connections. GroupID is the catalog entry;
// AvoidNodeID is a server this device just failed on (one automatic retry only).
type ConnectionRequest struct {
	GroupID      string `json:"group_id"`
	LastSequence int64  `json:"last_sequence"`
	AvoidNodeID  string `json:"avoid_node_id"`
}

// ConnectionResponse carries the admitted lease and the one server's connection material.
// NodeID is the opaque server id, usable only as AvoidNodeID; the profile names its proxy
// after the group. Like ConfigResponse it must be consumed and dropped, never kept.
type ConnectionResponse struct {
	Connection Connection   `json:"connection"`
	NodeID     string       `json:"node_id"`
	YAML       string       `json:"yaml"`
	Version    string       `json:"version"`
	Nodes      []NodeChoice `json:"nodes"`
	Session    Session      `json:"session"`
}

// ReleaseResponse is the answer to POST /connections/:id/release.
type ReleaseResponse struct {
	Connection Connection `json:"connection"`
	Session    Session    `json:"session"`
}

// Release reasons accepted by POST /connections/:id/release and POST /grants/:id/revoke.
// tunnel_unhealthy and tunnel_failed also count as failure reports against the server.
const (
	ReleaseUser            = "user"
	ReleaseStopped         = "stopped"
	ReleaseNetworkChanged  = "network_changed"
	ReleaseLifecycle       = "lifecycle"
	ReleaseTunnelUnhealthy = "tunnel_unhealthy"
	ReleaseTunnelFailed    = "tunnel_failed"
)

// ConfigResponse carries the one selected node's connection material. It must be
// handed to the ConfigStore and dropped; it is never part of a snapshot.
type ConfigResponse struct {
	YAML    string       `json:"yaml"`
	Version string       `json:"version"`
	Session Session      `json:"session"`
	Nodes   []NodeChoice `json:"nodes"`
	NodeID  string       `json:"node_id"`
}

// GrantStatus is the server's view of one grant. YAML and Version are present only when
// State is "ready"; like ConfigResponse they must be consumed and dropped, never kept.
type GrantStatus struct {
	GrantID string `json:"grant_id"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
	YAML    string `json:"yaml"`
	Version string `json:"version"`
	NodeID  string `json:"node_id"`
	// ServerID is the opaque server a ready grant was minted on (contract native-v3),
	// usable only as avoid_node_id.
	ServerID string   `json:"server_id,omitempty"`
	Session  *Session `json:"session"`
}

// Grant lifecycle states (contract native-v2).
const (
	GrantPending  = "pending"
	GrantReady    = "ready"
	GrantRevoking = "revoking"
	GrantRevoked  = "revoked"
	GrantExpired  = "expired"
)

// Capabilities are the server's public, non-secret feature flags.
type Capabilities struct {
	ContractVersions   []int    `json:"contract_versions"`
	MeteringModes      []string `json:"metering_modes"`
	MinAppVersion      string   `json:"min_app_version"`
	CorePolicyRevision string   `json:"core_policy_revision"`
}
