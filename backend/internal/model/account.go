package model

import "time"

// User.Status values besides "active". A deleting account is already unusable: every
// session is revoked and sign-in is refused; the purge pipeline removes its rows once
// the node side has settled (handoff_payment_account.md 7.2, 7.3).
const UserDeleting = "deleting"

// LoginEvent is one sign-in attempt of an existing account (handoff_payment_account.md
// 5.2, 8). The row is written after the session commits; GeoText is filled in later by
// the geo worker so a slow lookup service never slows a login down.
type LoginEvent struct {
	ID            string    `gorm:"type:uuid;primaryKey"`
	UserID        string    `gorm:"type:uuid;not null;index:idx_login_user_time,priority:1"`
	Channel       string    `gorm:"size:8;not null"`
	Platform      string    `gorm:"size:16;not null;default:''"`
	IP            string    `gorm:"size:45;not null;index"`
	UserAgent     string    `gorm:"size:200;not null;default:''"`
	Result        string    `gorm:"size:8;not null"`
	FailureReason string    `gorm:"size:24;not null;default:''"`
	OccurredAt    time.Time `gorm:"not null;index;index:idx_login_user_time,priority:2;index:idx_login_geo,priority:2"`
	GeoStatus     string    `gorm:"size:8;not null;default:'pending';index:idx_login_geo,priority:1"`
	GeoText       string    `gorm:"size:120;not null;default:''"`
	GeoResolvedAt *time.Time
	// GeoAttempts and GeoNextAt make a claim by the geo worker a lease: the row stays
	// pending, but nobody else picks it up before GeoNextAt.
	GeoAttempts int `gorm:"not null;default:0"`
	GeoNextAt   *time.Time
}

// IPGeoCache keeps a looked-up location by address. An empty GeoText is a remembered
// failure (cached for a shorter time than a success).
type IPGeoCache struct {
	IP         string    `gorm:"size:45;primaryKey"`
	GeoText    string    `gorm:"size:120;not null;default:''"`
	ResolvedAt time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
}

// Login event vocabulary.
const (
	LoginWeb     = "web"
	LoginNative  = "native"
	LoginSuccess = "success"
	LoginFailure = "failure"

	GeoPending = "pending"
	GeoDone    = "done"
	GeoFailed  = "failed"
	GeoSkipped = "skipped"

	// Retention and cache lifetimes are fixed by the plan (section 13).
	LoginEventRetention = 180 * 24 * time.Hour
	GeoCacheTTL         = 30 * 24 * time.Hour
	GeoFailureTTL       = time.Hour
	// GeoLocal is shown for private, loopback and link-local addresses, which are never
	// sent to an external service.
	GeoLocal = "内网/本机"
)
