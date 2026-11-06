package model

import "time"

// ClientDownload is one installer the /download page offers (handoff_download.md 4.1):
// one row per platform, architecture and package format. Publishing a new release edits
// the row; the previous one survives only in the audit trail.
type ClientDownload struct {
	ID       string `gorm:"type:uuid;primaryKey" json:"id"`
	Platform string `gorm:"size:16;not null;uniqueIndex:idx_client_download_variant,priority:1" json:"platform"`
	Arch     string `gorm:"size:16;not null;uniqueIndex:idx_client_download_variant,priority:2" json:"arch"`
	Format   string `gorm:"size:16;not null;uniqueIndex:idx_client_download_variant,priority:3" json:"format"`
	// AppVersion is the client's own version; Version below is the row's optimistic lock.
	AppVersion string `gorm:"size:32;not null" json:"app_version"`
	// URL is where the file really lives. It never leaves the server except as the
	// Location of the public redirect and in the administrator's own list (D4).
	URL               string  `gorm:"size:1024;not null" json:"url"`
	SHA256            *string `gorm:"size:64" json:"sha256"`
	SizeBytes         *int64  `json:"size_bytes"`
	SigningCertSHA256 *string `gorm:"size:64" json:"signing_cert_sha256"`
	BundleID          *string `gorm:"size:155" json:"bundle_id"`
	Published         bool    `gorm:"not null;default:false" json:"published"`
	// ReleasedAt moves only when AppVersion or URL changes, not when publishing.
	ReleasedAt time.Time `gorm:"not null" json:"released_at"`
	Version    int64     `gorm:"not null;default:1" json:"version"`
	UpdatedBy  *string   `gorm:"type:uuid" json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime:false" json:"updated_at"`
}
