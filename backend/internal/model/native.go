package model

import "time"

// Native tokens are opaque, memory-only on the client, and stored only as hashes here.
type NativeSession struct {
	NodeID                 string  `gorm:"size:36;not null;default:''"`
	RatePermille           int64   `gorm:"not null;default:1000"`
	ID                     string  `gorm:"type:uuid;primaryKey"`
	UserID                 string  `gorm:"type:uuid;not null;index"`
	User                   User    `gorm:"constraint:OnDelete:CASCADE"`
	TokenHash              string  `gorm:"size:64;not null;uniqueIndex"`
	DeviceID               string  `gorm:"type:uuid;not null;index"`
	Platform               string  `gorm:"size:16;not null"`
	AppVersion             string  `gorm:"size:64;not null"`
	SubscriptionID         *string `gorm:"type:uuid;index"`
	SubscriptionStartsAt   *time.Time
	EntitlementFingerprint string    `gorm:"size:64;not null;default:''"`
	ProfileVersion         string    `gorm:"size:64;not null;default:''"`
	LastSequence           int64     `gorm:"not null;default:0"`
	UploadBytes            int64     `gorm:"not null;default:0"`
	DownloadBytes          int64     `gorm:"not null;default:0"`
	LastSeenAt             time.Time `gorm:"not null;index"`
	ExpiresAt              time.Time `gorm:"not null;index"`
	RevokedAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// The ledger records client-reported usage, not independently verified node usage.
type ClientTrafficReport struct {
	NodeID         string        `gorm:"size:36;not null;default:''"`
	ID             string        `gorm:"type:uuid;primaryKey"`
	SessionID      string        `gorm:"type:uuid;not null;uniqueIndex:idx_client_report_sequence"`
	Session        NativeSession `gorm:"foreignKey:SessionID;constraint:OnDelete:RESTRICT"`
	Sequence       int64         `gorm:"not null;uniqueIndex:idx_client_report_sequence"`
	UserID         string        `gorm:"type:uuid;not null;index"`
	SubscriptionID string        `gorm:"type:uuid;not null;index"`
	UploadBytes    int64         `gorm:"not null"`
	DownloadBytes  int64         `gorm:"not null"`
	UploadDelta    int64         `gorm:"not null"`
	DownloadDelta  int64         `gorm:"not null"`
	ChargedUnits   int64         `gorm:"not null"`
	RatePermille   int64         `gorm:"not null"`
	Source         string        `gorm:"size:24;not null"`
	CreatedAt      time.Time
}
