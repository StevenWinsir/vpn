package model

import "time"

type User struct {
	ID           string    `gorm:"type:uuid;primaryKey" json:"id"`
	Email        string    `gorm:"size:254;not null;uniqueIndex" json:"email"`
	Name         string    `gorm:"size:80;not null" json:"name"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"size:16;not null;default:user" json:"role"`
	Status       string    `gorm:"size:16;not null;default:active" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"-"`
}

type Session struct {
	ID          string    `gorm:"type:uuid;primaryKey"`
	UserID      string    `gorm:"type:uuid;not null;index"`
	User        User      `gorm:"constraint:OnDelete:CASCADE"`
	RefreshHash string    `gorm:"size:64;not null;uniqueIndex"`
	Remember    bool      `gorm:"not null"`
	ExpiresAt   time.Time `gorm:"not null;index"`
	RevokedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Plan struct {
	ID             string    `gorm:"size:32;primaryKey" json:"id"`
	Name           string    `gorm:"size:80;not null" json:"name"`
	Description    string    `gorm:"size:300" json:"description"`
	PriceCents     int64     `gorm:"not null;check:price_cents >= 0" json:"price_cents"`
	DurationDays   int       `gorm:"not null;check:duration_days > 0" json:"duration_days"`
	TrafficBytes   int64     `gorm:"not null;check:traffic_bytes > 0" json:"traffic_bytes"`
	MaxDevices     int       `gorm:"not null;check:max_devices > 0" json:"max_devices"`
	AllowDedicated bool      `gorm:"not null" json:"allow_dedicated"`
	Active         bool      `gorm:"not null" json:"-"`
	Sort           int       `json:"-"`
	CreatedAt      time.Time `json:"-"`
	UpdatedAt      time.Time `json:"-"`
}

type Subscription struct {
	ID                string    `gorm:"type:uuid;primaryKey" json:"id"`
	UserID            string    `gorm:"type:uuid;not null;uniqueIndex" json:"-"`
	User              User      `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	PlanID            string    `gorm:"size:32;not null" json:"plan_id"`
	Plan              Plan      `gorm:"constraint:OnDelete:RESTRICT" json:"-"`
	PlanName          string    `gorm:"size:80;not null" json:"plan_name"`
	StartsAt          time.Time `gorm:"not null" json:"starts_at"`
	ExpiresAt         time.Time `gorm:"not null;index" json:"expires_at"`
	TrafficLimitBytes int64     `gorm:"not null;check:traffic_limit_bytes > 0" json:"traffic_limit_bytes"`
	UsedUnits         int64     `gorm:"not null;default:0;check:used_units >= 0" json:"used_units"`
	UploadBytes       int64     `gorm:"not null;default:0" json:"upload_bytes"`
	DownloadBytes     int64     `gorm:"not null;default:0" json:"download_bytes"`
	MaxDevices        int       `gorm:"not null" json:"max_devices"`
	AllowDedicated    bool      `gorm:"not null" json:"allow_dedicated"`
	IsTest            bool      `gorm:"not null" json:"is_test"`
	CreatedAt         time.Time `json:"-"`
	UpdatedAt         time.Time `json:"-"`
}

type Order struct {
	ID             string    `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         string    `gorm:"type:uuid;not null;uniqueIndex:idx_order_idempotency;index" json:"-"`
	User           User      `gorm:"constraint:OnDelete:RESTRICT" json:"-"`
	IdempotencyKey string    `gorm:"size:64;not null;uniqueIndex:idx_order_idempotency" json:"-"`
	PlanID         string    `gorm:"size:32;not null" json:"plan_id"`
	PlanName       string    `gorm:"size:80;not null" json:"plan_name"`
	PriceCents     int64     `gorm:"not null" json:"price_cents"`
	DurationDays   int       `gorm:"not null" json:"duration_days"`
	TrafficBytes   int64     `gorm:"not null" json:"traffic_bytes"`
	Status         string    `gorm:"size:24;not null" json:"status"`
	Provider       string    `gorm:"size:24;not null" json:"provider"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
	PaidAt         time.Time `json:"paid_at"`
}

// Public metadata only. Never put shared proxy credentials in public-facing models.
type Node struct {
	ID           string    `gorm:"type:uuid;primaryKey" json:"id"`
	Name         string    `gorm:"size:120;not null" json:"name"`
	Region       string    `gorm:"size:16;not null" json:"region"`
	LineType     string    `gorm:"size:16;not null;check:line_type IN ('direct','dedicated')" json:"line_type"`
	RatePermille int64     `gorm:"not null;check:rate_permille > 0 AND rate_permille <= 10000" json:"rate_permille"`
	Enabled      bool      `gorm:"not null" json:"-"`
	Version      int64     `gorm:"not null;default:1" json:"version"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
}
