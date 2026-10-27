package model

import "time"

type User struct {
	ID           string    `gorm:"type:uuid;primaryKey" json:"id"`
	Email        string    `gorm:"size:254;not null;uniqueIndex" json:"email"`
	Name         string    `gorm:"size:80;not null" json:"name"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"size:16;not null;default:user" json:"role"`
	Status       string    `gorm:"size:16;not null;default:active" json:"-"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
	UpdatedAt    time.Time `json:"-"`
	// Sign-in facts (handoff_payment_account.md 5.1). Accounts that predate them keep
	// NULL: nothing back-fills them. LastLogin* is written inside the login transaction.
	RegisterIP          *string    `gorm:"size:45" json:"-"`
	LastLoginAt         *time.Time `gorm:"index" json:"-"`
	LastLoginIP         *string    `gorm:"size:45" json:"-"`
	DeletionRequestedAt *time.Time `json:"-"`
	// Backend-only authentication state (handoff_auth.md 4.1). Existing accounts keep a
	// NULL EmailVerifiedAt: nothing back-fills it except a completed mailbox proof.
	EmailVerifiedAt   *time.Time `json:"-"`
	PasswordChangedAt *time.Time `json:"-"`
	// AuthVersion increments only when the password actually changes.
	AuthVersion int64 `gorm:"not null;default:1" json:"-"`
	// ReferralCode is the account's own referral code (handoff_coupon.md 5.1), created the
	// first time the account opens its referral page; NULL until then. Never serialized
	// with the user: only the referral endpoints return it.
	ReferralCode *string `gorm:"size:8;uniqueIndex" json:"-"`
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
	// Impersonation (handoff_payment_account.md 9.7): non-nil ImpersonatorID marks a session
	// an administrator opened as this user. HardExpiresAt is its fixed end; refreshing never
	// extends it. Ordinary sessions leave all three NULL.
	ImpersonatorID        *string `gorm:"type:uuid"`
	ImpersonatorSessionID *string `gorm:"type:uuid"`
	HardExpiresAt         *time.Time
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
	CreatedAt      time.Time `gorm:"autoCreateTime:false" json:"-"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime:false" json:"-"`
	// Shop fields (handoff_shop.md 4.1). None of them is ever serialized: the public
	// catalogue uses its own view struct. A plan is only ever soft-deleted (D7), because
	// the seed step would bring a hard-deleted seed plan back.
	YearlyEnabled         bool       `gorm:"not null;default:true" json:"-"`
	YearlyDurationDays    int        `gorm:"not null;default:365;check:yearly_duration_days >= 1 AND yearly_duration_days <= 365" json:"-"`
	YearlyTrafficBytes    *int64     `gorm:"check:yearly_traffic_bytes > 0" json:"-"`
	YearlyDiscountPercent *int       `gorm:"check:yearly_discount_percent >= 50 AND yearly_discount_percent <= 100" json:"-"`
	Featured              bool       `gorm:"not null;default:false" json:"-"`
	Note                  string     `gorm:"size:200;not null;default:''" json:"-"`
	Version               int64      `gorm:"not null;default:1" json:"-"`
	DeletedAt             *time.Time `json:"-"`
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
	// Version moves on every entitlement write (purchase or administrator edit), so an
	// administrator's stale form cannot overwrite a renewal that landed meanwhile. Traffic
	// accounting does not touch it.
	Version   int64     `gorm:"not null;default:1" json:"-"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
	// Refreshing traffic (yearly purchases, handoff_shop.md D2 revised). With
	// QuotaCycleDays > 0 the allowance is a per-cycle one: every QuotaCycleDays from
	// QuotaCycleStartsAt the balance (TrafficLimitBytes, UsedUnits and the byte counters)
	// is reset to QuotaCycleBytes, used up or not. 0 is the one-shot allowance of a
	// monthly purchase. An anchor before StartsAt belongs to an earlier period and is
	// ignored (an older binary may have started a new period without clearing it).
	QuotaCycleDays     int        `gorm:"not null;default:0;check:quota_cycle_days >= 0 AND quota_cycle_days <= 365" json:"quota_cycle_days"`
	QuotaCycleBytes    int64      `gorm:"not null;default:0;check:quota_cycle_bytes >= 0" json:"quota_cycle_bytes"`
	QuotaCycleStartsAt *time.Time `json:"quota_cycle_starts_at"`
	// Referral rewards (handoff_coupon.md 5.2, 9). BonusBytes is what is left of the
	// rewards granted in the period that starts at BonusStartsAt; it is already part of
	// TrafficLimitBytes and only survives a traffic refresh. An anchor other than
	// StartsAt belongs to another period and counts as no bonus. Not serialized: the
	// client contracts keep their exact shape.
	BonusBytes    int64      `gorm:"not null;default:0;check:bonus_bytes >= 0" json:"-"`
	BonusStartsAt *time.Time `json:"-"`
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
	// PaidAt is nil while a gateway order is unpaid. Legacy rows always have it.
	PaidAt *time.Time `json:"paid_at"`
	// Gateway orders (handoff_payment_account.md 5.1). Legacy test orders keep the zero
	// values; PriceCents of a gateway order is its payable amount (AmountCents).
	TradeNo        *string    `gorm:"size:20;uniqueIndex" json:"trade_no,omitempty"`
	PayType        string     `gorm:"size:12;not null;default:''" json:"pay_type,omitempty"`
	ListPriceCents int64      `gorm:"not null;default:0" json:"list_price_cents"`
	DiscountCents  int64      `gorm:"not null;default:0" json:"discount_cents"`
	AmountCents    int64      `gorm:"not null;default:0" json:"amount_cents"`
	PaidCents      *int64     `json:"paid_cents"`
	CouponID       *string    `gorm:"type:uuid;index" json:"-"`
	CouponCode     string     `gorm:"size:32;not null;default:''" json:"coupon_code,omitempty"`
	PriceSource    string     `gorm:"size:16;not null;default:''" json:"price_source,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	ReviewReason   string     `gorm:"size:32;not null;default:''" json:"-"`
	// GatewayCheckedAt throttles status queries (reconciler and result page).
	GatewayCheckedAt *time.Time `json:"-"`
	// Written from the injected clock, never by GORM's own time.Now.
	UpdatedAt *time.Time `gorm:"autoUpdateTime:false" json:"-"`
	// Shop snapshots (handoff_shop.md 4.3). Rows written before them (or by an older
	// binary) read as monthly with NULL entitlement snapshots. ListPriceCents is the
	// price of this billing cycle; OriginalCents is twelve months at the monthly price
	// for a yearly order, 0 on legacy rows.
	BillingCycle         string `gorm:"size:8;not null;default:monthly;check:billing_cycle IN ('monthly','yearly')" json:"billing_cycle"`
	CycleDiscountPercent int    `gorm:"not null;default:100" json:"cycle_discount_percent"`
	OriginalCents        int64  `gorm:"not null;default:0" json:"original_cents"`
	EntMaxDevices        *int   `json:"-"`
	EntAllowDedicated    *bool  `json:"-"`
	// QuotaCycleDays > 0 (yearly orders): the purchase grants QuotaCycleBytes again every
	// QuotaCycleDays instead of TrafficBytes at once. TrafficBytes keeps the nominal
	// twelve months so an older binary that fulfils the order still grants the year.
	QuotaCycleDays  int   `gorm:"not null;default:0" json:"quota_cycle_days"`
	QuotaCycleBytes int64 `gorm:"not null;default:0" json:"quota_cycle_bytes"`
}

// Order statuses. paid means paid and fulfilled, in one transaction.
const (
	OrderPending  = "pending"
	OrderPaid     = "paid"
	OrderExpired  = "expired"
	OrderFailed   = "failed"
	OrderReview   = "review"
	OrderPaidTest = "paid_test"
)

// Public metadata only. Never put shared proxy credentials in public-facing models.
//
// A Node is one real upstream server. Region, LineType, RatePermille (and
// node_configs.plan_ids) are the legacy copies of its NodeGroup's public metadata,
// kept in step for one release so an older binary still reads a consistent catalog.
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
	// GroupID stays nullable so an older binary can still insert servers; the
	// migration back-fills every NULL with a one-server group of the same id.
	GroupID *string    `gorm:"type:uuid;index:idx_nodes_group_id" json:"-"`
	Group   *NodeGroup `gorm:"constraint:OnDelete:RESTRICT" json:"-"`
	// The column default keeps rows written by an older binary valid and lets the
	// migration land without locking anyone out ("no limit" until an admin sets it).
	MaxConnections int        `gorm:"not null;default:1000;check:max_connections >= 1 AND max_connections <= 10000" json:"-"`
	Label          string     `gorm:"size:40;not null;default:''" json:"-"`
	UnhealthyUntil *time.Time `json:"-"`
}
