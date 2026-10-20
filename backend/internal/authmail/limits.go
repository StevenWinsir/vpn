package authmail

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Timings of the delivery worker (handoff_auth.md 6.1).
const (
	SendTimeout  = 20 * time.Second
	LeaseTTL     = 45 * time.Second
	MaxAttempts  = 3
	WorkerSlots  = 2
	ExpiryMargin = time.Minute
)

// Limits are the first-version defaults (handoff_auth.md 6.2). They live here, in one
// place; the website reads the effective values from /meta instead of hard-coding them.
type Limits struct {
	CodeTTL, ResetTTL               time.Duration
	EmailCooldown                   time.Duration
	EmailPerHour, EmailPerDay       int
	IPPerHour, IPPerDay             int
	GlobalPerMinute, MaxPendingJobs int
	ChallengeMaxFailures            int
	EmailVerifyFailures             int
	EmailVerifyWindow               time.Duration
}

// DefaultLimits returns the agreed first-version values.
func DefaultLimits() Limits {
	return Limits{
		CodeTTL: 10 * time.Minute, ResetTTL: 30 * time.Minute, EmailCooldown: 60 * time.Second,
		EmailPerHour: 5, EmailPerDay: 10, IPPerHour: 20, IPPerDay: 100,
		GlobalPerMinute: 60, MaxPendingJobs: 1000,
		ChallengeMaxFailures: 5, EmailVerifyFailures: 5, EmailVerifyWindow: 30 * time.Minute,
	}
}

// Rate-limit scopes stored in auth_rate_buckets.Scope.
const (
	scopeEmailCoord  = "email_coord"
	scopeEmailHour   = "email_hour"
	scopeEmailDay    = "email_day"
	scopeIPHour      = "ip_hour"
	scopeIPDay       = "ip_day"
	scopeGlobalMin   = "global_minute"
	scopeEmailVerify = "email_verify"
)

// coordWindow is the fixed WindowStart of the stable per-email coordination row.
var coordWindow = time.Unix(0, 0).UTC()

// RateLimitError is returned when a shared limit is reached. RetryAfter is computed
// from the server clock; Scope is for logs only and is never shown to users.
type RateLimitError struct {
	RetryAfter time.Duration
	Scope      string
}

func (e *RateLimitError) Error() string { return "rate limited: " + e.Scope }

// RetryAfterSeconds rounds up so clients never retry early.
func (e *RateLimitError) RetryAfterSeconds() int {
	s := int((e.RetryAfter + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

var (
	ErrMailUnavailable    = errors.New("authentication mail is unavailable")
	ErrRegistrationClosed = errors.New("registration is closed")
)

func (s *Service) table(name string) string { return s.schema + "." + name }

// bump atomically increments a fixed-window counter and returns the new count. The
// upsert is a single statement, so concurrent instances can never lose an increment.
func (s *Service) bump(tx *gorm.DB, scope, key string, window time.Duration, now time.Time) (int, time.Time, error) {
	start := now.Truncate(window)
	end := start.Add(window)
	var count int
	err := tx.Raw(fmt.Sprintf(`INSERT INTO %s AS b (scope, key, window_start, count, expires_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)
ON CONFLICT (scope, key, window_start) DO UPDATE SET count = b.count + 1, updated_at = EXCLUDED.updated_at RETURNING b.count`, s.table("auth_rate_buckets")),
		scope, key, start, end, now).Scan(&count).Error
	return count, end, err
}

// peek reads a fixed-window counter without changing it.
func (s *Service) peek(tx *gorm.DB, scope, key string, window time.Duration, now time.Time) (int, time.Time, error) {
	start := now.Truncate(window)
	var counts []int
	err := tx.Raw(fmt.Sprintf(`SELECT count FROM %s WHERE scope = ? AND key = ? AND window_start = ?`, s.table("auth_rate_buckets")), scope, key, start).Scan(&counts).Error
	if err != nil || len(counts) == 0 {
		return 0, start.Add(window), err
	}
	return counts[0], start.Add(window), nil
}

// admit runs the source-address and global checks in their own short transaction,
// before any user or mailbox lock is taken, so it can never invert the lock order.
// Rejected requests still count: no cross-transaction refunds.
func (s *Service) admit(ctx context.Context, ip string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if ip != "" {
			key := s.keys.RateKey("ip", ip)
			for _, check := range []struct {
				scope  string
				window time.Duration
				limit  int
			}{{scopeIPHour, time.Hour, s.Limits.IPPerHour}, {scopeIPDay, 24 * time.Hour, s.Limits.IPPerDay}} {
				count, end, err := s.bump(tx, check.scope, key, check.window, now)
				if err != nil {
					return err
				}
				if count > check.limit {
					return &RateLimitError{RetryAfter: end.Sub(now), Scope: check.scope}
				}
			}
		}
		var pending int64
		if err := tx.Raw(fmt.Sprintf(`SELECT count(*) FROM %s WHERE status IN ('queued', 'retry', 'sending')`, s.table("auth_mail_jobs"))).Scan(&pending).Error; err != nil {
			return err
		}
		if pending >= int64(s.Limits.MaxPendingJobs) {
			return ErrMailUnavailable
		}
		count, end, err := s.bump(tx, scopeGlobalMin, "all", time.Minute, now)
		if err != nil {
			return err
		}
		if count > s.Limits.GlobalPerMinute {
			return &RateLimitError{RetryAfter: end.Sub(now), Scope: scopeGlobalMin}
		}
		return nil
	})
}

type coordRow struct {
	Count         int
	NextAllowedAt *time.Time
}

// lockMailbox creates (race-free) and locks the stable coordination row of one
// mailbox. Registration and recovery share it, so they share cooldown and quotas.
func (s *Service) lockMailbox(tx *gorm.DB, emailKey string, now time.Time) (coordRow, error) {
	var row coordRow
	err := tx.Exec(fmt.Sprintf(`INSERT INTO %s (scope, key, window_start, count, expires_at, updated_at) VALUES (?, ?, ?, 0, ?, ?) ON CONFLICT DO NOTHING`, s.table("auth_rate_buckets")),
		scopeEmailCoord, emailKey, coordWindow, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), now).Error
	if err != nil {
		return row, err
	}
	err = tx.Raw(fmt.Sprintf(`SELECT count, next_allowed_at FROM %s WHERE scope = ? AND key = ? AND window_start = ? FOR UPDATE`, s.table("auth_rate_buckets")),
		scopeEmailCoord, emailKey, coordWindow).Scan(&row).Error
	return row, err
}

// chargeMailbox enforces the shared cooldown and hourly/daily quotas while the
// coordination row is locked, then records this send. Reads are safe because every
// writer of these counters holds the same row lock.
func (s *Service) chargeMailbox(tx *gorm.DB, emailKey string, row coordRow, now time.Time) (time.Time, error) {
	if row.NextAllowedAt != nil && now.Before(*row.NextAllowedAt) {
		return time.Time{}, &RateLimitError{RetryAfter: row.NextAllowedAt.Sub(now), Scope: "email_cooldown"}
	}
	for _, check := range []struct {
		scope  string
		window time.Duration
		limit  int
	}{{scopeEmailHour, time.Hour, s.Limits.EmailPerHour}, {scopeEmailDay, 24 * time.Hour, s.Limits.EmailPerDay}} {
		count, end, err := s.peek(tx, check.scope, emailKey, check.window, now)
		if err != nil {
			return time.Time{}, err
		}
		if count >= check.limit {
			return time.Time{}, &RateLimitError{RetryAfter: end.Sub(now), Scope: check.scope}
		}
	}
	for _, check := range []struct {
		scope  string
		window time.Duration
	}{{scopeEmailHour, time.Hour}, {scopeEmailDay, 24 * time.Hour}} {
		if _, _, err := s.bump(tx, check.scope, emailKey, check.window, now); err != nil {
			return time.Time{}, err
		}
	}
	next := now.Add(s.Limits.EmailCooldown)
	err := tx.Exec(fmt.Sprintf(`UPDATE %s SET count = count + 1, next_allowed_at = ?, updated_at = ? WHERE scope = ? AND key = ? AND window_start = ?`, s.table("auth_rate_buckets")),
		next, now, scopeEmailCoord, emailKey, coordWindow).Error
	return next, err
}
