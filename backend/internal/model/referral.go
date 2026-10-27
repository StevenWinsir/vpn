package model

import "time"

// Referral is one referred account (handoff_coupon.md 5.3): bound at registration, made
// qualified by the referee's first real paid order and settled once by the reward worker.
// A purged referee leaves the row with RefereeID NULL and the masked e-mail snapshot; a
// purged referrer takes all of its rows with it (the foreign keys also cover an older
// binary's purge, which does not know this table).
type Referral struct {
	ID                 string    `gorm:"type:uuid;primaryKey"`
	ReferrerID         string    `gorm:"type:uuid;not null;index:idx_referral_referrer_time,priority:1"`
	Referrer           User      `gorm:"foreignKey:ReferrerID;constraint:OnDelete:CASCADE"`
	RefereeID          *string   `gorm:"type:uuid;uniqueIndex"`
	Referee            *User     `gorm:"foreignKey:RefereeID;constraint:OnDelete:SET NULL"`
	RefereeEmailMasked string    `gorm:"size:80;not null"`
	CodeSnapshot       string    `gorm:"size:8;not null"`
	Status             string    `gorm:"size:16;not null;check:status IN ('registered','qualified','rewarded','forfeited')"`
	CreatedAt          time.Time `gorm:"not null;autoCreateTime:false;index:idx_referral_referrer_time,priority:2,sort:desc"`
	QualifiedAt        *time.Time
	FirstOrderID       *string `gorm:"type:uuid"`
	FirstOrderCents    int64   `gorm:"not null;default:0"`
	RewardBytes        int64   `gorm:"not null;default:0;check:reward_bytes >= 0"`
	GrantedBytes       int64   `gorm:"not null;default:0;check:granted_bytes >= 0"`
	GrantedAt          *time.Time
	// GrantedSubscriptionID and GrantedPlanName snapshot what the reward was added to.
	GrantedSubscriptionID *string `gorm:"type:uuid"`
	GrantedPlanName       string  `gorm:"size:80;not null;default:''"`
	ForfeitReason         string  `gorm:"size:32;not null;default:''"`
	// Attempts and NextAttemptAt make the worker's claim a lease and its retries a
	// back-off; LastErrorKind is a category, never a message with personal data.
	Attempts      int `gorm:"not null;default:0"`
	NextAttemptAt *time.Time
	LastErrorKind string    `gorm:"size:32;not null;default:''"`
	UpdatedAt     time.Time `gorm:"not null;autoUpdateTime:false"`
}

// Referral statuses: registered -> qualified -> rewarded | forfeited. A row may also go
// straight from registered to forfeited (zero_amount).
const (
	ReferralRegistered = "registered"
	ReferralQualified  = "qualified"
	ReferralRewarded   = "rewarded"
	ReferralForfeited  = "forfeited"
)

// Forfeit reasons (Referral.ForfeitReason).
const (
	ForfeitNoPlan      = "referrer_no_plan"
	ForfeitUnavailable = "referrer_unavailable"
	ForfeitZeroAmount  = "zero_amount"
)
