package model

import "time"

// PaymentNotification is every gateway callback exactly as received, written before
// it is judged (handoff_payment_account.md 6.3). OrderID is cleared when an account
// is purged, leaving an anonymous ledger of trade_no, amount and time.
type PaymentNotification struct {
	ID          string    `gorm:"type:uuid;primaryKey"`
	TradeNo     string    `gorm:"size:32;not null;index"`
	OrderID     *string   `gorm:"type:uuid;index"`
	MoneyText   string    `gorm:"size:32;not null"`
	TradeStatus string    `gorm:"size:24;not null"`
	Name        string    `gorm:"size:80;not null"`
	SourceIP    string    `gorm:"size:45;not null"`
	Result      string    `gorm:"size:24;not null"`
	ReceivedAt  time.Time `gorm:"not null;index"`
}

// Callback results (PaymentNotification.Result).
const (
	NotifyReceived     = "received"
	NotifyAccepted     = "accepted"
	NotifyDuplicate    = "duplicate"
	NotifyUnknownOrder = "unknown_order"
	NotifyIPRejected   = "ip_rejected"
	NotifyInvalid      = "invalid"
	NotifyReview       = "review"
	NotifyIgnored      = "ignored"
)

// Order review reasons (Order.ReviewReason).
const (
	ReviewAmountMismatch     = "amount_mismatch"
	ReviewUnverifiedSource   = "unverified_source"
	ReviewStateConflict      = "state_conflict"
	ReviewCallbackMissing    = "callback_missing"
	ReviewGatewayUnreachable = "gateway_unreachable"
	// ReviewClosed marks a failed order an administrator closed by hand; it is final.
	ReviewClosed = "closed_by_admin"
)

// Coupon is an administrator-defined discount code. UsedCount only ever grows (paid
// orders); live pending orders hold a reservation that is counted when checking the
// limits. A coupon an order has referenced is only ever soft-deleted.
type Coupon struct {
	ID             string     `gorm:"type:uuid;primaryKey" json:"id"`
	Code           string     `gorm:"size:32;not null;uniqueIndex" json:"code"`
	Note           string     `gorm:"size:120;not null;default:''" json:"note"`
	DiscountType   string     `gorm:"size:8;not null;check:discount_type IN ('percent','amount')" json:"discount_type"`
	PercentOff     int        `gorm:"not null;default:0;check:percent_off >= 0 AND percent_off <= 99" json:"percent_off"`
	AmountOffCents int64      `gorm:"not null;default:0;check:amount_off_cents >= 0" json:"amount_off_cents"`
	PlanIDs        string     `gorm:"type:text;not null;default:'[]'" json:"-"`
	StartsAt       *time.Time `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
	MaxUses        int        `gorm:"not null;default:0;check:max_uses >= 0" json:"max_uses"`
	PerUserLimit   int        `gorm:"not null;default:1;check:per_user_limit >= 1" json:"per_user_limit"`
	UsedCount      int        `gorm:"not null;default:0;check:used_count >= 0" json:"used_count"`
	Enabled        bool       `gorm:"not null" json:"enabled"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	CreatedBy      string     `gorm:"type:uuid;not null" json:"-"`
	Version        int64      `gorm:"not null;default:1" json:"version"`
	CreatedAt      time.Time  `gorm:"autoCreateTime:false" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime:false" json:"updated_at"`
}

const (
	DiscountPercent = "percent"
	DiscountAmount  = "amount"
)
