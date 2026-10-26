package model

import "time"

// Billing cycles of a plan (handoff_shop.md D1). A yearly purchase is another cycle of
// the same plan id, not another plan.
const (
	CycleMonthly = "monthly"
	CycleYearly  = "yearly"
)

// DefaultYearlyDiscountPercent is the yearly price as a percentage of twelve months
// (90 = 9折) when no setting row can be read.
const DefaultYearlyDiscountPercent = 90

// ShopSettings is the single settings row (ID is always 1).
type ShopSettings struct {
	ID                    int       `gorm:"primaryKey;autoIncrement:false;check:id = 1"`
	YearlyDiscountPercent int       `gorm:"not null;default:90;check:yearly_discount_percent >= 50 AND yearly_discount_percent <= 100"`
	Version               int64     `gorm:"not null;default:1"`
	UpdatedBy             *string   `gorm:"type:uuid"`
	UpdatedAt             time.Time `gorm:"autoUpdateTime:false"`
}
