package model

import "time"

// EmailAuthChallenge is one mailbox proof (a registration code or a reset link).
// Only keyed digests are stored, never the code or token itself. Purpose, email,
// user and digest are all bound into the check. There are deliberately no foreign
// keys: cleaning challenges can never cascade into accounts or financial records.
type EmailAuthChallenge struct {
	ID      string  `gorm:"type:uuid;primaryKey"`
	Purpose string  `gorm:"size:16;not null;index:idx_auth_challenge_lookup,priority:1;uniqueIndex:idx_auth_challenge_digest,priority:1"`
	Email   string  `gorm:"size:254;not null;index:idx_auth_challenge_lookup,priority:2"`
	UserID  *string `gorm:"type:uuid;index"`
	Digest  string  `gorm:"size:64;not null;uniqueIndex:idx_auth_challenge_digest,priority:2"`
	// Generation increases per (purpose, email); the newest active one is the only usable one.
	Generation int64 `gorm:"not null"`
	// BoundAuthVersion is the user's AuthVersion when a reset was issued (0 for registration).
	BoundAuthVersion int64 `gorm:"not null;default:0"`
	// Delivered is false for equal-shape requests that intentionally queue no mail.
	Delivered  bool      `gorm:"not null"`
	Status     string    `gorm:"size:16;not null;index:idx_auth_challenge_lookup,priority:3"`
	Failures   int       `gorm:"not null;default:0"`
	IssuedAt   time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
	ConsumedAt *time.Time
	UpdatedAt  time.Time
}

// AuthMailJob is one outbound authentication email. The recipient and any secret
// (code or link) live only inside the AEAD-sealed Payload, which is erased as soon as
// the job reaches a final state. Retries reuse the same payload and MessageID.
type AuthMailJob struct {
	ID          string  `gorm:"type:uuid;primaryKey"`
	ChallengeID *string `gorm:"type:uuid;index"`
	Template    string  `gorm:"size:24;not null"`
	// RecipientHint is a masked address for operators (for example a***@qq.com).
	RecipientHint string `gorm:"size:64;not null"`
	Payload       []byte
	Status        string    `gorm:"size:16;not null;index:idx_auth_mail_job_due,priority:1;index:idx_auth_mail_job_lease,priority:1"`
	Attempts      int       `gorm:"not null;default:0"`
	NextAttemptAt time.Time `gorm:"not null;index:idx_auth_mail_job_due,priority:2"`
	// DeliverBy mirrors the challenge expiry so the worker can skip stale mail without
	// locking the challenge (lock order: challenge before job, never the reverse).
	DeliverBy    *time.Time
	LeaseUntil   *time.Time `gorm:"index:idx_auth_mail_job_lease,priority:2"`
	LeaseOwner   string     `gorm:"size:64;not null;default:''"`
	LeaseVersion int64      `gorm:"not null;default:0"`
	MessageID    string     `gorm:"size:160;not null"`
	FailureClass string     `gorm:"size:32;not null;default:''"`
	CreatedAt    time.Time
	FinishedAt   *time.Time `gorm:"index"`
}

// AuthRateBucket is a database-shared counter, so limits hold across API instances
// and restarts. Key is a keyed digest of the email or source address, never raw.
// Scope "email_coord" rows are stable per-email coordination rows (WindowStart is the
// zero time); they serialize issuance and carry the shared resend cooldown.
type AuthRateBucket struct {
	Scope         string    `gorm:"size:24;primaryKey"`
	Key           string    `gorm:"size:64;primaryKey"`
	WindowStart   time.Time `gorm:"primaryKey"`
	Count         int       `gorm:"not null;default:0"`
	NextAllowedAt *time.Time
	ExpiresAt     time.Time `gorm:"not null;index"`
	UpdatedAt     time.Time
}
