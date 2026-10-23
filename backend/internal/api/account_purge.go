package api

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// PurgeLockKey serialises purge passes across API instances. It is distinct from the
// migration (708614921), admin catalog (708614922) and maintenance (708614923) locks.
const PurgeLockKey = 708614924

// AuditPurged is written when an account's rows are gone. TargetID is the former user
// id; no email or other identifier is kept.
const AuditPurged = "account.purged"

const purgeBatch = 20

// PurgeResult reports one pass. Skipped means another instance held the purge lock.
type PurgeResult struct {
	Skipped bool
	Purged  int
	// Waiting counts deleting accounts whose node grants have not settled yet.
	Waiting int
	// Failed lists accounts whose purge transaction failed (rolled back, retried next pass).
	Failed []string
}

// PurgeDeletedAccounts is phase two of account deletion (handoff_payment_account.md
// 7.3). For every account in the deleting state whose connection grants have settled,
// one transaction deletes all of its rows in dependency order, leaving only the
// anonymous payment callback ledger. A failure rolls that account back whole; the next
// pass retries it.
func PurgeDeletedAccounts(ctx context.Context, db *gorm.DB, cfg config.Config) (PurgeResult, error) {
	s := &Server{db: db, cfg: cfg}
	return s.purgeDeletedAccounts(ctx)
}

func (s *Server) purgeDeletedAccounts(ctx context.Context) (PurgeResult, error) {
	var out PurgeResult
	guard := s.db.WithContext(ctx).Begin(&sql.TxOptions{})
	if guard.Error != nil {
		return out, guard.Error
	}
	defer guard.Rollback()
	var locked bool
	if err := guard.Raw("SELECT pg_try_advisory_xact_lock(?)", PurgeLockKey).Scan(&locked).Error; err != nil {
		return out, err
	}
	if !locked {
		out.Skipped = true
		return out, nil
	}
	var ids []string
	if err := s.db.WithContext(ctx).Model(&model.User{}).Where("status = ?", model.UserDeleting).Order("deletion_requested_at, id").Limit(purgeBatch).Pluck("id", &ids).Error; err != nil {
		return out, err
	}
	for _, id := range ids {
		purged, err := s.purgeAccount(ctx, id)
		switch {
		case err != nil:
			out.Failed = append(out.Failed, id)
		case purged:
			out.Purged++
		default:
			out.Waiting++
		}
	}
	return out, nil
}

var errGrantsUnsettled = errors.New("connection grants have not settled")

// purgeAccount reports whether the account was removed (false: still waiting).
func (s *Server) purgeAccount(ctx context.Context, id string) (bool, error) {
	now := s.now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", id, model.UserDeleting).Take(&user).Error; e != nil {
			return e
		}
		// A grant is settled once the node confirmed its removal (released), or it has
		// been expired for longer than the settle grace: by then the node has stopped
		// forwarding on its own, which is what the reconcile loop would conclude too.
		var unsettled int64
		if e := tx.Model(&model.ConnectionGrant{}).
			Where("user_id = ? AND state IN ? AND released_at IS NULL AND expires_at >= ?", id, []string{grantPending, grantReady, grantRevoking}, now.Add(-s.cfg.SettleGrace())).
			Count(&unsettled).Error; e != nil {
			return e
		}
		if unsettled > 0 {
			return errGrantsUnsettled
		}
		t := func(name string) string { return `"` + s.cfg.Schema + `".` + name }
		steps := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM ` + t("node_usage_reports") + ` WHERE grant_id IN (SELECT id FROM ` + t("connection_grants") + ` WHERE user_id = ?)`, []any{id}},
			{`DELETE FROM ` + t("connection_grants") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("subscription_periods") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("client_traffic_reports") + ` WHERE user_id = ? OR session_id IN (SELECT id FROM ` + t("native_sessions") + ` WHERE user_id = ?)`, []any{id, id}},
			{`DELETE FROM ` + t("connection_leases") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("native_sessions") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("sessions") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("usage_buckets") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("node_assignments") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("node_health_events") + ` WHERE user_id = ?`, []any{id}},
			// announcement_reads joins this list when announcements exist (round 5).
			{`DELETE FROM ` + t("login_events") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("auth_mail_jobs") + ` WHERE challenge_id IN (SELECT id FROM ` + t("email_auth_challenges") + ` WHERE user_id = ? OR email = ?)`, []any{id, user.Email}},
			{`DELETE FROM ` + t("email_auth_challenges") + ` WHERE user_id = ? OR email = ?`, []any{id, user.Email}},
			// The callback ledger stays, without the link to the account.
			{`UPDATE ` + t("payment_notifications") + ` SET order_id = NULL WHERE order_id IN (SELECT id FROM ` + t("orders") + ` WHERE user_id = ?)`, []any{id}},
			{`DELETE FROM ` + t("orders") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("subscriptions") + ` WHERE user_id = ?`, []any{id}},
			{`DELETE FROM ` + t("users") + ` WHERE id = ?`, []any{id}},
		}
		for _, step := range steps {
			if e := tx.Exec(step.sql, step.args...).Error; e != nil {
				return e
			}
		}
		return tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: id, TargetID: id, Action: AuditPurged, Version: user.AuthVersion, CreatedAt: now}).Error
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, errGrantsUnsettled), errors.Is(err, gorm.ErrRecordNotFound):
		return false, nil
	}
	return false, err
}

// PurgeInterval is how often cmd/api runs the purge pass.
const PurgeInterval = time.Minute
