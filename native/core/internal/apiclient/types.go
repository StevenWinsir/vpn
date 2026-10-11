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
}

type Catalog struct {
	SessionID string       `json:"session_id"`
	Revision  string       `json:"revision"`
	Reason    string       `json:"reason"`
	Nodes     []NodeChoice `json:"nodes"`
}

// ConfigResponse carries the one selected node's connection material. It must be
// handed to the ConfigStore and dropped; it is never part of a snapshot.
type ConfigResponse struct {
	YAML    string       `json:"yaml"`
	Version string       `json:"version"`
	Session Session      `json:"session"`
	Nodes   []NodeChoice `json:"nodes"`
	NodeID  string       `json:"node_id"`
}
