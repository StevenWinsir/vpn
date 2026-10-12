package model

import "time"

// NodeAgent is the identity of the metering/enforcement process that runs next to a
// real forwarder. It authenticates with a pinned mTLS client certificate; the node
// it controls is fixed here and never taken from a request.
type NodeAgent struct {
	ID              string `gorm:"type:uuid;primaryKey" json:"id"`
	NodeID          string `gorm:"type:uuid;not null;uniqueIndex" json:"node_id"`
	Node            Node   `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	Name            string `gorm:"size:80;not null" json:"name"`
	CertFingerprint string `gorm:"size:64;not null;uniqueIndex" json:"cert_fingerprint"`
	Enabled         bool   `gorm:"not null" json:"enabled"`
	// Protocols are the proxy types the agent reports it can enforce (per-user
	// authentication, trusted counting and kill of established flows).
	Protocols    string `gorm:"type:text;not null;default:'[]'" json:"-"`
	AgentVersion string `gorm:"size:64;not null;default:''" json:"agent_version"`
	// Epoch is the agent's process generation; usage counters restart in each one.
	Epoch      int64      `gorm:"not null;default:0" json:"epoch"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	CreatedAt  time.Time  `json:"-"`
	UpdatedAt  time.Time  `json:"-"`
}

// SubscriptionPeriod is the account of one subscription period (subscription id +
// start). The live Subscription row stays the balance of the current period; late
// usage for an older period lands here and never touches a newer period's balance.
type SubscriptionPeriod struct {
	ID             string    `gorm:"type:uuid;primaryKey"`
	UserID         string    `gorm:"type:uuid;not null;index"`
	SubscriptionID string    `gorm:"type:uuid;not null;uniqueIndex:idx_period_identity"`
	StartsAt       time.Time `gorm:"not null;uniqueIndex:idx_period_identity"`
	ExpiresAt      time.Time `gorm:"not null"`
	LimitBytes     int64     `gorm:"not null"`
	UsedUnits      int64     `gorm:"not null;default:0;check:used_units >= 0"`
	// ReservedUnits is budget handed to nodes and not yet consumed or released.
	ReservedUnits int64 `gorm:"not null;default:0;check:reserved_units >= 0"`
	UploadBytes   int64 `gorm:"not null;default:0"`
	DownloadBytes int64 `gorm:"not null;default:0"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ConnectionGrant is one per-user, per-node, per-rate credential lease. The
// credential is stored encrypted; the rate is a snapshot bound at creation.
type ConnectionGrant struct {
	ID             string `gorm:"type:uuid;primaryKey"`
	UserID         string `gorm:"type:uuid;not null;index"`
	SessionID      string `gorm:"type:uuid;not null;uniqueIndex:idx_grant_idempotency;index"`
	IdempotencyKey string `gorm:"size:64;not null;uniqueIndex:idx_grant_idempotency"`
	NodeID         string `gorm:"type:uuid;not null;index"`
	AgentID        string `gorm:"type:uuid;not null;index"`
	PeriodID       string `gorm:"type:uuid;not null;index"`
	State          string `gorm:"size:12;not null;index"`
	Protocol       string `gorm:"size:16;not null"`
	RatePermille   int64  `gorm:"not null;check:rate_permille > 0 AND rate_permille <= 10000"`
	// CredentialCipher is AES-GCM sealed JSON of the per-grant secret fields.
	CredentialCipher string    `gorm:"type:text;not null"`
	ExpiresAt        time.Time `gorm:"not null;index"`
	// BudgetBytes is the cumulative raw (up+down) allowance authorised for this grant;
	// ChargedBytes the cumulative raw bytes the node has reported against it.
	BudgetBytes  int64 `gorm:"not null;default:0"`
	ChargedBytes int64 `gorm:"not null;default:0"`
	ChargedUnits int64 `gorm:"not null;default:0"`
	// Closed epochs are folded into Base*; Epoch* are the open epoch's cumulative counters.
	BaseUpload    int64 `gorm:"not null;default:0"`
	BaseDownload  int64 `gorm:"not null;default:0"`
	Epoch         int64 `gorm:"not null;default:0"`
	LastSequence  int64 `gorm:"not null;default:0"`
	EpochUpload   int64 `gorm:"not null;default:0"`
	EpochDownload int64 `gorm:"not null;default:0"`
	// FinalEpoch is the epoch for which the node declared its last usage report.
	FinalEpoch int64 `gorm:"not null;default:0"`
	// NeedsReconcile: usage may be missing (an epoch ended without a final report, or
	// the reservation had to be released on timeout). Never silently zeroed.
	NeedsReconcile bool `gorm:"not null;default:false"`
	ReleasedAt     *time.Time
	InstalledAt    *time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NodeUsageReport is the idempotency/audit record of one node usage report.
type NodeUsageReport struct {
	ID            string          `gorm:"type:uuid;primaryKey"`
	GrantID       string          `gorm:"type:uuid;not null;uniqueIndex:idx_usage_cursor"`
	Grant         ConnectionGrant `gorm:"foreignKey:GrantID;constraint:OnDelete:RESTRICT"`
	Epoch         int64           `gorm:"not null;uniqueIndex:idx_usage_cursor"`
	Sequence      int64           `gorm:"not null;uniqueIndex:idx_usage_cursor"`
	UploadBytes   int64           `gorm:"not null"`
	DownloadBytes int64           `gorm:"not null"`
	UploadDelta   int64           `gorm:"not null"`
	DownloadDelta int64           `gorm:"not null"`
	ChargedUnits  int64           `gorm:"not null"`
	RatePermille  int64           `gorm:"not null"`
	Final         bool            `gorm:"not null;default:false"`
	CreatedAt     time.Time
}
