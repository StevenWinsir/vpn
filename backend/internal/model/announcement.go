package model

import "time"

// Announcement is a notice every signed-in user must acknowledge once
// (handoff_payment_account.md 9.6). Only a draft can be edited; once published its text is
// locked so the acknowledgement count means something.
type Announcement struct {
	ID          string     `gorm:"type:uuid;primaryKey" json:"id"`
	Title       string     `gorm:"size:400;not null" json:"title"`
	Body        string     `gorm:"type:text;not null" json:"body"`
	Status      string     `gorm:"size:12;not null;index:idx_announcement_feed,priority:1" json:"status"`
	PublishedAt *time.Time `gorm:"index:idx_announcement_feed,priority:2" json:"published_at"`
	CreatedBy   string     `gorm:"type:uuid;not null" json:"-"`
	Version     int64      `gorm:"not null;default:1" json:"version"`
	CreatedAt   time.Time  `gorm:"autoCreateTime:false" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime:false" json:"updated_at"`
}

// AnnouncementRead records that one user acknowledged one announcement. No foreign keys:
// the purge pipeline deletes a user's rows explicitly.
type AnnouncementRead struct {
	AnnouncementID string    `gorm:"type:uuid;primaryKey"`
	UserID         string    `gorm:"type:uuid;primaryKey;index"`
	AcknowledgedAt time.Time `gorm:"not null"`
}

// Announcement statuses.
const (
	AnnouncementDraft     = "draft"
	AnnouncementPublished = "published"
	AnnouncementArchived  = "archived"
)
