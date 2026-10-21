package model

import "time"

// NodeGroup is the line a client sees (for example "JP1"). It owns the public
// metadata; its servers (nodes rows) own the encrypted YAML and the capacity.
// Version increments only when public metadata changes.
type NodeGroup struct {
	ID           string `gorm:"type:uuid;primaryKey" json:"id"`
	Name         string `gorm:"size:120;not null" json:"name"`
	Region       string `gorm:"size:16;not null" json:"region"`
	LineType     string `gorm:"size:16;not null;check:line_type IN ('direct','dedicated')" json:"line_type"`
	RatePermille int64  `gorm:"not null;check:rate_permille > 0 AND rate_permille <= 10000" json:"rate_permille"`
	// PlanIDs is a JSON array; empty means every plan (same meaning as node_configs.plan_ids).
	PlanIDs   string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	Enabled   bool      `gorm:"not null" json:"enabled"`
	Sort      int       `gorm:"not null;default:0" json:"sort"`
	Version   int64     `gorm:"not null;default:1" json:"version"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

const (
	LeaseActive = "active"
	LeaseEnded  = "ended"

	LeaseByConnections = "connections"
	LeaseByLegacy      = "legacy_config"
	LeaseByGrant       = "grant"

	// End reasons (allow-list; EndReason is "" while active).
	LeaseReleased        = "released"
	LeaseExpired         = "expired"
	LeaseSessionRevoked  = "session_revoked"
	LeaseNodeUnavailable = "node_unavailable"
	// LeaseGroupUnavailable: the whole group was disabled or deleted; the line is gone,
	// so the client is not asked to reconnect to it.
	LeaseGroupUnavailable = "group_unavailable"
	LeaseReplaced         = "replaced"
	LeaseEntitlementEnded = "entitlement_ended"
	LeaseTunnelUnhealthy  = "tunnel_unhealthy"
	LeaseTunnelFailed     = "tunnel_failed"
)

// ConnectionLease is the single source of truth for "this device is connected now":
// presence on the dashboard, the per-user online cap, per-server load and slot
// release. A lease counts only while active, unexpired and its session is live;
// those conditions are always checked in the query, never by trusting a sweep.
// NodeID deliberately has no foreign key: a deleted server keeps its history.
type ConnectionLease struct {
	ID         string        `gorm:"type:uuid;primaryKey"`
	UserID     string        `gorm:"type:uuid;not null"`
	User       User          `gorm:"constraint:OnDelete:CASCADE"`
	SessionID  string        `gorm:"type:uuid;not null;index:idx_lease_session"`
	Session    NativeSession `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE"`
	DeviceID   string        `gorm:"type:uuid;not null"`
	Platform   string        `gorm:"size:16;not null"`
	AppVersion string        `gorm:"size:64;not null"`
	GroupID    string        `gorm:"type:uuid;not null;index:idx_lease_group"`
	NodeID     string        `gorm:"type:uuid;not null"`
	Method     string        `gorm:"size:16;not null;check:method IN ('connections','legacy_config','grant')"`
	GrantID    string        `gorm:"size:36;not null;default:''"`
	// IdempotencyKey is the client's Idempotency-Key for POST /client/connections; ""
	// for leases created by /config or /grants. Unique per session when set.
	IdempotencyKey string    `gorm:"size:64;not null;default:''"`
	State          string    `gorm:"size:8;not null;check:state IN ('active','ended')"`
	AcquiredAt     time.Time `gorm:"not null"`
	RenewedAt      time.Time `gorm:"not null"`
	ExpiresAt      time.Time `gorm:"not null"`
	EndedAt        *time.Time
	EndReason      string `gorm:"size:24;not null;default:''"`
	// Session counters when the lease began, so "this connection" traffic is a difference.
	StartUploadBytes   int64 `gorm:"not null;default:0"`
	StartDownloadBytes int64 `gorm:"not null;default:0"`
}

// NodeAssignment is a user's preferred server inside one group (sticky assignment).
type NodeAssignment struct {
	UserID     string    `gorm:"type:uuid;primaryKey"`
	User       User      `gorm:"constraint:OnDelete:CASCADE"`
	GroupID    string    `gorm:"type:uuid;primaryKey"`
	Group      NodeGroup `gorm:"constraint:OnDelete:CASCADE"`
	NodeID     string    `gorm:"type:uuid;not null;index:idx_assignment_node"`
	Node       Node      `gorm:"constraint:OnDelete:CASCADE"`
	AssignedAt time.Time `gorm:"not null"`
	LastUsedAt time.Time `gorm:"not null;index:idx_assignment_last_used"`
}

// UsageBucket is a user's traffic in one 5-minute slot of server UTC time. Rows
// older than the 24-hour window are deleted; billing ledgers are not touched.
type UsageBucket struct {
	UserID        string    `gorm:"type:uuid;primaryKey"`
	User          User      `gorm:"constraint:OnDelete:CASCADE"`
	BucketStart   time.Time `gorm:"primaryKey;index:idx_usage_bucket_start"`
	UploadBytes   int64     `gorm:"not null;default:0;check:upload_bytes >= 0"`
	DownloadBytes int64     `gorm:"not null;default:0;check:download_bytes >= 0"`
	ChargedUnits  int64     `gorm:"not null;default:0;check:charged_units >= 0"`
	// Written from the injected clock, never by GORM's own time.Now.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime:false"`
}

// NodeHealthEvent is one user's failure report against a server. Kept for one
// hour and used only for the soft cooldown decision.
type NodeHealthEvent struct {
	ID         string    `gorm:"type:uuid;primaryKey"`
	NodeID     string    `gorm:"type:uuid;not null;index:idx_node_health_recent,priority:1"`
	Node       Node      `gorm:"constraint:OnDelete:CASCADE"`
	UserID     string    `gorm:"type:uuid;not null"`
	User       User      `gorm:"constraint:OnDelete:CASCADE"`
	Reason     string    `gorm:"size:24;not null"`
	OccurredAt time.Time `gorm:"not null;index:idx_node_health_occurred;index:idx_node_health_recent,priority:2"`
}

// Fixed by the requirement itself, not configuration.
const (
	UsageGranularity = 5 * time.Minute
	UsageBucketSpan  = 3 * time.Hour
	UsageWindow      = 24 * time.Hour
	// HealthEventRetention, EndedLeaseRetention and AssignmentRetention bound the
	// tables the maintenance loop prunes.
	HealthEventRetention = time.Hour
	EndedLeaseRetention  = 24 * time.Hour
	AssignmentRetention  = 30 * 24 * time.Hour
)

// UsageBaseline is server time rounded down to the 5-minute grid (T in the plan).
func UsageBaseline(now time.Time) time.Time { return now.UTC().Truncate(UsageGranularity) }

// UsageCutoff is T-24h: rows with bucket_start strictly before it are deleted.
func UsageCutoff(now time.Time) time.Time { return UsageBaseline(now).Add(-UsageWindow) }
