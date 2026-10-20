package authmail

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Retention (handoff_auth.md 4.3).
const (
	ChallengeRetention = 24 * time.Hour
	JobRetention       = 7 * 24 * time.Hour
	CoordIdleRetention = 30 * 24 * time.Hour
	cleanupBatch       = 1000
)

// CleanupResult counts what one pass removed or scrubbed.
type CleanupResult struct {
	Challenges, Jobs, Buckets, ScrubbedPayloads int64
}

// Cleanup deletes finished authentication data in bounded batches. It touches only
// the three auth tables: there are no foreign keys from them, so nothing can cascade
// into users, sessions, orders, subscriptions or traffic records.
func Cleanup(ctx context.Context, db *gorm.DB, schema string, now time.Time) (CleanupResult, error) {
	now = now.UTC()
	var out CleanupResult
	db = db.WithContext(ctx)
	scrub := db.Exec(fmt.Sprintf(`UPDATE %s.auth_mail_jobs SET payload = NULL WHERE payload IS NOT NULL AND (finished_at IS NOT NULL OR status IN ('sent', 'failed', 'cancelled'))`, schema))
	if scrub.Error != nil {
		return out, scrub.Error
	}
	out.ScrubbedPayloads = scrub.RowsAffected
	steps := []struct {
		count *int64
		sql   string
		args  []any
	}{
		{&out.Challenges, `DELETE FROM %[1]s.email_auth_challenges WHERE id IN (SELECT id FROM %[1]s.email_auth_challenges WHERE expires_at < ? OR (status <> 'active' AND updated_at < ?) LIMIT %[2]d)`, []any{now.Add(-ChallengeRetention), now.Add(-ChallengeRetention)}},
		{&out.Jobs, `DELETE FROM %[1]s.auth_mail_jobs WHERE id IN (SELECT id FROM %[1]s.auth_mail_jobs WHERE finished_at < ? LIMIT %[2]d)`, []any{now.Add(-JobRetention)}},
		{&out.Buckets, `DELETE FROM %[1]s.auth_rate_buckets WHERE ctid IN (SELECT ctid FROM %[1]s.auth_rate_buckets WHERE scope <> 'email_coord' AND expires_at < ? LIMIT %[2]d)`, []any{now}},
		// Coordination rows are stable across windows; only long-idle ones go, and
		// re-creating one later is race-free (INSERT ... ON CONFLICT + row lock).
		{&out.Buckets, `DELETE FROM %[1]s.auth_rate_buckets WHERE ctid IN (SELECT ctid FROM %[1]s.auth_rate_buckets WHERE scope = 'email_coord' AND updated_at < ? AND (next_allowed_at IS NULL OR next_allowed_at < ?) LIMIT %[2]d)`, []any{now.Add(-CoordIdleRetention), now}},
	}
	for _, step := range steps {
		statement := fmt.Sprintf(step.sql, schema, cleanupBatch)
		for {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			result := db.Exec(statement, step.args...)
			if result.Error != nil {
				return out, result.Error
			}
			*step.count += result.RowsAffected
			if result.RowsAffected < cleanupBatch {
				break
			}
		}
	}
	return out, nil
}
