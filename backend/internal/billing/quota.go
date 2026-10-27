package billing

import (
	"math"
	"time"

	"vpn/backend/internal/model"
)

// Refreshing traffic (handoff_shop.md D2, revised): a subscription bought yearly gets
// one month's allowance per 30-day cycle counted from the purchase. When a cycle ends
// the balance is reset to the cycle allowance, whether or not anything was left; an
// allowance used up early stays exhausted until the next reset.

// QuotaCycleActive reports whether sub carries a refreshing allowance for its current
// period. An anchor older than StartsAt belongs to an earlier period.
func QuotaCycleActive(s *model.Subscription) bool {
	return s != nil && s.QuotaCycleDays > 0 && s.QuotaCycleBytes > 0 && s.QuotaCycleStartsAt != nil && !s.QuotaCycleStartsAt.Before(s.StartsAt)
}

func quotaCycleLength(s *model.Subscription) time.Duration {
	return time.Duration(s.QuotaCycleDays) * 24 * time.Hour
}

// RollQuotaCycle moves sub to the traffic cycle that contains now, resetting the
// balance when at least one cycle boundary has passed. It changes sub in memory only
// and reports whether it did; the caller persists it (under the user's lock) or uses
// it as a read-only view. An expired subscription is left as it is.
func RollQuotaCycle(s *model.Subscription, now time.Time) bool {
	if !QuotaCycleActive(s) || !now.Before(s.ExpiresAt) {
		return false
	}
	start, length := *s.QuotaCycleStartsAt, quotaCycleLength(s)
	if now.Before(start.Add(length)) {
		return false
	}
	next := start.Add(now.Sub(start) / length * length).UTC()
	// Referral rewards survive the reset and are spent last (handoff_coupon.md 9): what
	// is kept is the bonus, but no more than was left of the ending cycle.
	var keep int64
	if BonusActive(s) {
		keep = min(s.BonusBytes, remainingBytes(s))
	}
	s.QuotaCycleStartsAt = &next
	s.TrafficLimitBytes, s.UsedUnits, s.UploadBytes, s.DownloadBytes = s.QuotaCycleBytes+keep, 0, 0, 0
	s.BonusBytes = keep
	return true
}

// BonusActive reports whether sub carries referral reward traffic for its current
// period. An anchor other than StartsAt belongs to another period (an older binary may
// have started a new period without clearing it).
func BonusActive(s *model.Subscription) bool {
	return s != nil && s.BonusBytes > 0 && s.BonusStartsAt != nil && s.BonusStartsAt.Equal(s.StartsAt)
}

// CurrentBonus is the reward traffic sub still carries in its current period.
func CurrentBonus(s *model.Subscription) int64 {
	if !BonusActive(s) {
		return 0
	}
	return s.BonusBytes
}

// remainingBytes is the unspent allowance in whole bytes (usage is counted in
// thousandths of a byte).
func remainingBytes(s *model.Subscription) int64 {
	if s.TrafficLimitBytes <= 0 || s.TrafficLimitBytes > math.MaxInt64/1000 {
		return 0
	}
	return max(0, (s.TrafficLimitBytes*1000-s.UsedUnits)/1000)
}

// AddBonus adds granted reward bytes to sub's allowance and to its bonus for the current
// period (a bonus anchored to another period is dropped first).
func AddBonus(s *model.Subscription, granted int64) {
	if !BonusActive(s) {
		start := s.StartsAt
		s.BonusBytes, s.BonusStartsAt = 0, &start
	}
	s.TrafficLimitBytes += granted
	s.BonusBytes += granted
}

// NextQuotaReset is when the current cycle's allowance is next refreshed, or nil when
// it never is (a one-shot allowance, or the subscription ends first).
func NextQuotaReset(s *model.Subscription) *time.Time {
	if !QuotaCycleActive(s) {
		return nil
	}
	next := s.QuotaCycleStartsAt.Add(quotaCycleLength(s)).UTC()
	if !next.Before(s.ExpiresAt) {
		return nil
	}
	return &next
}
