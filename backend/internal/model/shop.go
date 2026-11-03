package model

import "time"

// Billing cycles of a plan (handoff_shop.md D1). A yearly purchase is another cycle of
// the same plan id, not another plan.
const (
	CycleMonthly = "monthly"
	CycleYearly  = "yearly"
)

// QuotaCycleDays is the traffic refresh period of a yearly purchase: every 30 days from
// the purchase the subscription's allowance is reset to one month's traffic, whatever
// was left (handoff_shop.md D2, revised).
const QuotaCycleDays = 30

// DefaultYearlyDiscountPercent is the yearly price as a percentage of twelve months
// (90 = 9折) when no setting row can be read.
const DefaultYearlyDiscountPercent = 90

// DefaultUpgradeMinChargeCents is the least an upgrade costs when no setting row can be
// read (1 yuan, handoff_plans.md D5); MaxUpgradeMinChargeCents bounds the setting.
const (
	DefaultUpgradeMinChargeCents = int64(100)
	MaxUpgradeMinChargeCents     = int64(10000)
)

// ShopSettings is the single settings row (ID is always 1).
type ShopSettings struct {
	ID                    int `gorm:"primaryKey;autoIncrement:false;check:id = 1"`
	YearlyDiscountPercent int `gorm:"not null;default:90;check:yearly_discount_percent >= 50 AND yearly_discount_percent <= 100"`
	// Mid-period upgrades (handoff_plans.md 9.2): the switch and the least an upgrade
	// costs. GORM replaces a false bool by the column default on Create, so a row created
	// with the switch off must be written again (or created with Select("*")).
	UpgradeEnabled        bool      `gorm:"not null;default:true"`
	UpgradeMinChargeCents int64     `gorm:"not null;default:100;check:upgrade_min_charge_cents >= 1 AND upgrade_min_charge_cents <= 10000"`
	Version               int64     `gorm:"not null;default:1"`
	UpdatedBy             *string   `gorm:"type:uuid"`
	UpdatedAt             time.Time `gorm:"autoUpdateTime:false"`
}
