package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Price sources (Order.PriceSource).
const (
	priceList         = "list"
	priceTest         = "test_price"
	priceTestPurchase = "test_purchase"
)

var couponCodePattern = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

// normalizeCoupon trims and upper-cases a code; "" means no code was entered.
func normalizeCoupon(raw string) string { return strings.ToUpper(strings.TrimSpace(raw)) }

// couponProblem is the user-facing reason a code does not apply. The four reasons
// deliberately do not distinguish a missing code from a disabled or expired one.
type couponProblem struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

var (
	couponInvalid   = couponProblem{"coupon_invalid", "优惠码无效或已失效"}
	couponPlan      = couponProblem{"coupon_not_applicable", "此优惠码不适用于所选套餐或付费周期"}
	couponExhausted = couponProblem{"coupon_exhausted", "此优惠码已达使用上限"}
	couponUsed      = couponProblem{"coupon_used", "你已使用过此优惠码"}
)

// pricing is the server's price for one purchase: list price, then the test price
// (listed accounts only), then the coupon, never below 0.01 yuan.
type pricing struct {
	ListCents, BaseCents, DiscountCents, AmountCents int64
	Source                                           string
	Coupon                                           *model.Coupon
	// Terms is the billing cycle the price belongs to.
	Terms cycleTerms
}

func couponPlans(c model.Coupon) []string {
	var ids []string
	if json.Unmarshal([]byte(c.PlanIDs), &ids) != nil {
		return nil
	}
	return ids
}

// couponDiscount is the discount for base cents: percent rounds down to the cent, a
// fixed amount is capped so at least one cent is always paid.
func couponDiscount(c model.Coupon, base int64) int64 {
	var discount int64
	switch c.DiscountType {
	case model.DiscountPercent:
		discount = base * int64(c.PercentOff) / 100
	case model.DiscountAmount:
		discount = c.AmountOffCents
	}
	return max(0, min(discount, base-1))
}

// livePending restricts an order query to gateway orders still inside their window.
func livePending(db *gorm.DB, now time.Time) *gorm.DB {
	return db.Where("status = ? AND expires_at > ?", model.OrderPending, now)
}

// couponCoversCycle: a code is scoped to all cycles, only monthly or only yearly.
func couponCoversCycle(c model.Coupon, cycle string) bool {
	return c.BillingCycles == "" || c.BillingCycles == "all" || c.BillingCycles == cycle
}

// price computes the payable amount for one billing cycle of a plan: the cycle price,
// then the test price, then the coupon (handoff_shop.md 5.1). With lock it holds the coupon row (after the user
// lock), so concurrent orders cannot both take the last use. A coupon problem is not an
// error: the caller decides whether to refuse or just report it.
func (s *Server) price(tx *gorm.DB, user model.User, plan model.Plan, terms cycleTerms, code string, lock bool, now time.Time) (pricing, *couponProblem, error) {
	p := pricing{ListCents: terms.PriceCents, BaseCents: terms.PriceCents, Source: priceList, Terms: terms}
	if s.cfg.Payment.TestPriceFor(user.Email) {
		p.BaseCents, p.Source = s.cfg.Payment.TestPriceCents, priceTest
	}
	p.AmountCents = p.BaseCents
	if code == "" {
		return p, nil, nil
	}
	if !couponCodePattern.MatchString(code) {
		return p, &couponInvalid, nil
	}
	var coupon model.Coupon
	query := tx
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Where("code = ?", code).First(&coupon).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return p, &couponInvalid, nil
	}
	if err != nil {
		return p, nil, err
	}
	if !coupon.Enabled || coupon.DeletedAt != nil || coupon.StartsAt != nil && now.Before(*coupon.StartsAt) || coupon.EndsAt != nil && !now.Before(*coupon.EndsAt) {
		return p, &couponInvalid, nil
	}
	if plans := couponPlans(coupon); len(plans) > 0 && !slices.Contains(plans, plan.ID) || !couponCoversCycle(coupon, terms.Cycle) {
		return p, &couponPlan, nil
	}
	if coupon.MaxUses > 0 {
		var reserved int64
		if err = livePending(tx.Model(&model.Order{}), now).Where("coupon_id = ?", coupon.ID).Count(&reserved).Error; err != nil {
			return p, nil, err
		}
		if int64(coupon.UsedCount)+reserved >= int64(coupon.MaxUses) {
			return p, &couponExhausted, nil
		}
	}
	var mine int64
	if err = tx.Model(&model.Order{}).Where("coupon_id = ? AND user_id = ? AND (status = ? OR (status = ? AND expires_at > ?))", coupon.ID, user.ID, model.OrderPaid, model.OrderPending, now).Count(&mine).Error; err != nil {
		return p, nil, err
	}
	if mine >= int64(coupon.PerUserLimit) {
		return p, &couponUsed, nil
	}
	p.Coupon = &coupon
	p.DiscountCents = couponDiscount(coupon, p.BaseCents)
	p.AmountCents = p.BaseCents - p.DiscountCents
	return p, nil, nil
}

// Database-shared limits for payment actions, stored in auth_rate_buckets with their
// own scopes. Keys are digests, never raw identifiers.
const (
	scopePayOrder  = "pay_order"
	scopePayCoupon = "pay_coupon"

	payOrderWindow  = 10 * time.Minute
	payOrderLimit   = 20
	payCouponWindow = time.Hour
	payCouponLimit  = 20
)

func limitKey(scope, id string) string {
	sum := sha256.Sum256([]byte(scope + ":" + id))
	return hex.EncodeToString(sum[:])
}

// bumpLimit atomically counts one action in a fixed window; it runs in its own
// statement, so a refused request still counts.
func (s *Server) bumpLimit(db *gorm.DB, scope, id string, window time.Duration, now time.Time) (int, time.Time, error) {
	start := now.Truncate(window)
	end := start.Add(window)
	var count int
	err := db.Raw(fmt.Sprintf(`INSERT INTO %s AS b (scope, key, window_start, count, expires_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)
ON CONFLICT (scope, key, window_start) DO UPDATE SET count = b.count + 1, updated_at = EXCLUDED.updated_at RETURNING b.count`, s.cfg.Schema+".auth_rate_buckets"),
		scope, limitKey(scope, id), start, end, now).Scan(&count).Error
	return count, end, err
}

func (s *Server) peekLimit(db *gorm.DB, scope, id string, window time.Duration, now time.Time) (int, time.Time, error) {
	start := now.Truncate(window)
	var counts []int
	err := db.Raw(fmt.Sprintf(`SELECT count FROM %s WHERE scope = ? AND key = ? AND window_start = ?`, s.cfg.Schema+".auth_rate_buckets"), scope, limitKey(scope, id), start).Scan(&counts).Error
	if err != nil || len(counts) == 0 {
		return 0, start.Add(window), err
	}
	return counts[0], start.Add(window), nil
}

func retryAfter(c interface{ Header(string, string) }, until, now time.Time) {
	c.Header("Retry-After", fmt.Sprint(max(1, secondsUntil(until, now))))
}
