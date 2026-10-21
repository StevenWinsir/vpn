// Package maintenance runs the presence and usage-history housekeeping inside the API
// process (handoff_node_use_history_and_load_balancing_c.md 4.4): ending lapsed
// connection leases, enforcing the strict 24-hour usage window, and pruning health
// events, ended leases and stale sticky assignments.
//
// Correctness never depends on this loop running on time: every reader checks
// expiry and the window itself. The loop only records end reasons and bounds table
// size. All times come from the injected Go clock, never from the database's now().
package maintenance

import (
	"context"
	"database/sql"
	"log"
	"time"

	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// LockKey serialises sweeps across API instances (pg_try_advisory_xact_lock). It is
// distinct from the migration (708614921) and admin catalog (708614922) locks.
const LockKey = 708614923

const (
	LeaseInterval  = 15 * time.Second
	UsageInterval  = 5 * time.Minute
	HourlyInterval = time.Hour
	// DefaultBatch bounds every UPDATE/DELETE so no sweep holds a long transaction.
	DefaultBatch = 1000
)

type Sweeper struct {
	DB     *gorm.DB
	Schema string
	Batch  int
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

// Result reports one pass. Skipped means another instance held the sweep lock and
// this pass changed nothing.
type Result struct {
	Skipped        bool
	Expired        int64
	SessionRevoked int64
	UsageBuckets   int64
	HealthEvents   int64
	EndedLeases    int64
	Assignments    int64
}

func New(db *gorm.DB, schema string) *Sweeper {
	return &Sweeper{DB: db, Schema: schema, Batch: DefaultBatch, Now: time.Now, Logf: log.Printf}
}

func (s *Sweeper) table(name string) string { return `"` + s.Schema + `".` + name }

func (s *Sweeper) batch() int {
	if s.Batch <= 0 {
		return DefaultBatch
	}
	return s.Batch
}

// exclusive runs pass while holding the sweep lock in a guard transaction. The guard
// does nothing but hold the lock, so the batches themselves stay short transactions
// and a second instance skips the whole pass instead of interleaving with it.
func (s *Sweeper) exclusive(ctx context.Context, pass func() error) (skipped bool, err error) {
	guard := s.DB.WithContext(ctx).Begin(&sql.TxOptions{})
	if guard.Error != nil {
		return false, guard.Error
	}
	defer guard.Rollback()
	var locked bool
	if err = guard.Raw("SELECT pg_try_advisory_xact_lock(?)", LockKey).Scan(&locked).Error; err != nil {
		return false, err
	}
	if !locked {
		return true, nil
	}
	return false, pass()
}

// repeat runs one bounded statement until it affects fewer rows than a batch.
func (s *Sweeper) repeat(ctx context.Context, statement string, args ...any) (int64, error) {
	var total int64
	for {
		result := s.DB.WithContext(ctx).Exec(statement, args...)
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(s.batch()) {
			return total, nil
		}
	}
}

// ExpireLeases ends every active lease that has lapsed or whose native session was
// revoked or expired, recording expired or session_revoked. Rows a client request is
// currently writing are skipped and picked up by the next pass.
func (s *Sweeper) ExpireLeases(ctx context.Context) (Result, error) {
	now := s.Now().UTC()
	var out Result
	var err error
	out.Skipped, err = s.exclusive(ctx, func() error {
		for {
			var reasons []string
			e := s.DB.WithContext(ctx).Raw(`UPDATE `+s.table("connection_leases")+` AS l
				SET state = @ended, ended_at = @now, end_reason = CASE WHEN ns.revoked_at IS NOT NULL THEN @revoked ELSE @expired END
				FROM `+s.table("native_sessions")+` AS ns
				WHERE ns.id = l.session_id AND l.state = 'active' AND l.id IN (
					SELECT x.id FROM `+s.table("connection_leases")+` AS x JOIN `+s.table("native_sessions")+` AS y ON y.id = x.session_id
					WHERE x.state = 'active' AND (x.expires_at <= @now OR y.revoked_at IS NOT NULL OR y.expires_at <= @now)
					ORDER BY x.expires_at LIMIT @batch FOR UPDATE OF x SKIP LOCKED)
				RETURNING l.end_reason`,
				sql.Named("ended", model.LeaseEnded), sql.Named("revoked", model.LeaseSessionRevoked), sql.Named("expired", model.LeaseExpired), sql.Named("now", now), sql.Named("batch", s.batch())).Scan(&reasons).Error
			if e != nil {
				return e
			}
			for _, reason := range reasons {
				if reason == model.LeaseSessionRevoked {
					out.SessionRevoked++
				} else {
					out.Expired++
				}
			}
			if len(reasons) < s.batch() {
				return nil
			}
		}
	})
	return out, err
}

// PruneUsage deletes usage buckets that start strictly before T-24h, T being the
// current 5-minute baseline. The bucket at exactly T-24h is kept.
func (s *Sweeper) PruneUsage(ctx context.Context) (Result, error) {
	cutoff := model.UsageCutoff(s.Now())
	var out Result
	var err error
	out.Skipped, err = s.exclusive(ctx, func() error {
		var e error
		out.UsageBuckets, e = s.repeat(ctx, `DELETE FROM `+s.table("usage_buckets")+` WHERE (user_id, bucket_start) IN (
			SELECT user_id, bucket_start FROM `+s.table("usage_buckets")+` WHERE bucket_start < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, cutoff, s.batch())
		return e
	})
	return out, err
}

// PruneHourly removes health events older than one hour, ended leases older than
// 24 hours and sticky assignments unused for 30 days.
func (s *Sweeper) PruneHourly(ctx context.Context) (Result, error) {
	now := s.Now().UTC()
	var out Result
	var err error
	out.Skipped, err = s.exclusive(ctx, func() error {
		var e error
		if out.HealthEvents, e = s.repeat(ctx, `DELETE FROM `+s.table("node_health_events")+` WHERE id IN (
			SELECT id FROM `+s.table("node_health_events")+` WHERE occurred_at < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, now.Add(-model.HealthEventRetention), s.batch()); e != nil {
			return e
		}
		if out.EndedLeases, e = s.repeat(ctx, `DELETE FROM `+s.table("connection_leases")+` WHERE id IN (
			SELECT id FROM `+s.table("connection_leases")+` WHERE state = 'ended' AND ended_at < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, now.Add(-model.EndedLeaseRetention), s.batch()); e != nil {
			return e
		}
		out.Assignments, e = s.repeat(ctx, `DELETE FROM `+s.table("node_assignments")+` WHERE (user_id, group_id) IN (
			SELECT user_id, group_id FROM `+s.table("node_assignments")+` WHERE last_used_at < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, now.Add(-model.AssignmentRetention), s.batch())
		return e
	})
	return out, err
}

// Run executes every pass once at start and then on its own interval until ctx ends.
// Failures are logged without database details and retried on the next tick.
func (s *Sweeper) Run(ctx context.Context) {
	passes := []struct {
		name     string
		interval time.Duration
		run      func(context.Context) (Result, error)
	}{{"lease expiry", LeaseInterval, s.ExpireLeases}, {"usage history", UsageInterval, s.PruneUsage}, {"hourly cleanup", HourlyInterval, s.PruneHourly}}
	tickers := make([]*time.Ticker, len(passes))
	for i, p := range passes {
		tickers[i] = time.NewTicker(p.interval)
		defer tickers[i].Stop()
		s.runPass(ctx, p.name, p.run)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tickers[0].C:
			s.runPass(ctx, passes[0].name, passes[0].run)
		case <-tickers[1].C:
			s.runPass(ctx, passes[1].name, passes[1].run)
		case <-tickers[2].C:
			s.runPass(ctx, passes[2].name, passes[2].run)
		}
	}
}

func (s *Sweeper) runPass(ctx context.Context, name string, run func(context.Context) (Result, error)) {
	result, err := run(ctx)
	switch {
	case err != nil && ctx.Err() == nil:
		s.Logf("maintenance: %s failed (will retry)", name)
	case err == nil && !result.Skipped:
		if n := result.Expired + result.SessionRevoked + result.UsageBuckets + result.HealthEvents + result.EndedLeases + result.Assignments; n > 0 {
			s.Logf("maintenance: %s changed %d rows", name, n)
		}
	}
}
