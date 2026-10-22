package api

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// readSubscription returns the user's subscription or nil. The caller holds the user lock.
func readSubscription(tx *gorm.DB, userID string) (*model.Subscription, error) {
	var sub model.Subscription
	err := tx.Where("user_id = ?", userID).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// applyPlan writes one purchase of plan into the user's subscription. The caller holds
// the user's row lock (lock order User -> Order -> Coupon -> Subscription -> Period).
// When the current period already has a grant ledger row it is kept in step, so a
// node's budget sees the new expiry and allowance at once.
func applyPlan(tx *gorm.DB, userID string, plan model.Plan, now time.Time, test bool) (*model.Subscription, error) {
	current, err := readSubscription(tx, userID)
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
			sub.ID, sub.CreatedAt = current.ID, current.CreatedAt
		} else {
			sub.ID = uuid.NewString()
		}
	}
	sub.TrafficLimitBytes += plan.TrafficBytes
	sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, plan.DurationDays)
	if err = tx.Omit("User", "Plan").Save(&sub).Error; err != nil {
		return nil, err
	}
	var period model.SubscriptionPeriod
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("subscription_id = ? AND starts_at = ?", sub.ID, sub.StartsAt).First(&period).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &sub, nil
	}
	if err != nil {
		return nil, err
	}
	syncPeriod(&period, &sub)
	return &sub, tx.Save(&period).Error
}
