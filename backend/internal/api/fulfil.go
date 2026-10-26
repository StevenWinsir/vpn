package api

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/model"
)

// The one place a purchase becomes an entitlement (handoff_payment_account.md 6.5),
// shared by gateway payments, test purchases and (round 4) administrator edits.
var (
	errPlanChange     = &apiError{409, "plan_change_not_supported", "当前套餐有效期内仅支持续费同一套餐，到期后可更换"}
	errTestPlanChange = &apiError{409, "plan_change_not_supported", "有效套餐期间仅支持续购相同的测试套餐"}
	errRenewalLimit   = &apiError{409, "renewal_limit", "续购超过允许的套餐上限"}
	errPlanInvalid    = &apiError{409, "invalid_plan", "套餐配置不可用"}
)

// maxPlanBytes bounds a subscription's total allowance (as before: 1 PiB).
const maxPlanBytes = int64(1) << 50

// usablePlan reports whether a plan's terms are inside the supported bounds.
func usablePlan(plan model.Plan) bool {
	return plan.DurationDays > 0 && plan.DurationDays <= 365 && plan.TrafficBytes > 0 && plan.TrafficBytes <= maxPlanBytes && plan.PriceCents >= 0
}

// planDecision decides how buying plan applies to the current subscription at now:
// renew (same plan, still valid: days and traffic are added, usage and the period
// start are kept) or a new period (usage resets). Test purchases keep the original
// rule; a paid purchase replaces a valid test subscription with a new period and may
// not switch plans while a paid one is valid.
func planDecision(sub *model.Subscription, plan model.Plan, now time.Time, test bool) (renew bool, err error) {
	if !usablePlan(plan) {
		return false, errPlanInvalid
	}
	active := sub != nil && now.Before(sub.ExpiresAt)
	if test {
		if active && (!sub.IsTest || sub.PlanID != plan.ID) {
			return false, errTestPlanChange
		}
		renew = active
	} else {
		if active && !sub.IsTest && sub.PlanID != plan.ID {
			return false, errPlanChange
		}
		renew = active && !sub.IsTest
	}
	limit, expires := int64(0), now
	if renew {
		limit, expires = sub.TrafficLimitBytes, sub.ExpiresAt
	}
	if limit > math.MaxInt64/1000-plan.TrafficBytes || limit+plan.TrafficBytes > maxPlanBytes || expires.After(now.AddDate(10, 0, 0)) {
		return false, errRenewalLimit
	}
	return renew, nil
}

// readSubscription returns the user's subscription as it stands at now (a refreshing
// allowance moved to its current cycle, in memory only), or nil. A caller that writes
// the row back (holding the user lock) persists the refresh with it.
func readSubscription(tx *gorm.DB, userID string, now time.Time) (*model.Subscription, error) {
	var sub model.Subscription
	err := tx.Where("user_id = ?", userID).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	billing.RollQuotaCycle(&sub, now)
	return &sub, nil
}

// applyPlan writes one purchase of plan into the user's subscription. The caller holds
// the user's row lock (lock order User -> Order -> Coupon -> Subscription -> Period).
// When the current period already has a grant ledger row it is kept in step, so a
// node's budget sees the new expiry and allowance at once.
//
// cycleDays > 0 is a yearly purchase: plan.TrafficBytes is then the allowance of one
// cycle, refreshed every cycleDays (handoff_shop.md D2 revised):
//   - a new period, or a renewal of a one-shot (monthly) subscription, starts the
//     cycles now; what is left of the one-shot allowance carries into the first cycle;
//   - a renewal of a subscription that already refreshes extends it and keeps its reset
//     dates and the current cycle's balance; the new allowance applies from the next
//     reset on.
//
// A monthly purchase onto a refreshing subscription extends it the same way: the days
// it adds are covered by the refreshes, so the current cycle gains nothing.
func applyPlan(tx *gorm.DB, userID string, plan model.Plan, cycleDays int, now time.Time, test bool) (*model.Subscription, error) {
	current, err := readSubscription(tx, userID, now)
	if err != nil {
		return nil, err
	}
	renew, err := planDecision(current, plan, now, test)
	if err != nil {
		return nil, err
	}
	var sub model.Subscription
	if renew {
		sub = *current
	} else {
		sub = model.Subscription{UserID: userID, PlanID: plan.ID, PlanName: plan.Name, StartsAt: now, ExpiresAt: now, MaxDevices: plan.MaxDevices, AllowDedicated: plan.AllowDedicated, IsTest: test}
		if current != nil {
			sub.ID, sub.CreatedAt, sub.Version = current.ID, current.CreatedAt, current.Version
		} else {
			sub.ID = uuid.NewString()
		}
	}
	refreshing := billing.QuotaCycleActive(&sub)
	switch {
	case cycleDays > 0 && refreshing:
		sub.QuotaCycleBytes = plan.TrafficBytes
	case cycleDays > 0:
		start := now
		sub.QuotaCycleDays, sub.QuotaCycleBytes, sub.QuotaCycleStartsAt = cycleDays, plan.TrafficBytes, &start
		sub.TrafficLimitBytes += plan.TrafficBytes
	case refreshing:
	default:
		sub.TrafficLimitBytes += plan.TrafficBytes
	}
	sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, plan.DurationDays)
	sub.Version++
	return saveSubscription(tx, &sub)
}

// saveSubscription writes sub and mirrors it into its current period when that period
// already has a ledger row (created on the first grant).
func saveSubscription(tx *gorm.DB, sub *model.Subscription) (*model.Subscription, error) {
	if err := tx.Omit("User", "Plan").Save(sub).Error; err != nil {
		return nil, err
	}
	var period model.SubscriptionPeriod
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("subscription_id = ? AND starts_at = ?", sub.ID, sub.StartsAt).First(&period).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return sub, nil
	}
	if err != nil {
		return nil, err
	}
	syncPeriod(&period, sub)
	return sub, tx.Save(&period).Error
}
