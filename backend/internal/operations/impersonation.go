package operations

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// AuditImpersonateRevoked is written once per session RevokeImpersonationSessions ends.
const AuditImpersonateRevoked = "admin.impersonate_revoked"

var ErrRevoke = errors.New("revoking impersonation sessions failed; verify the schema is migrated")

// RevokeImpersonationSessions ends every impersonation session that is still usable
// (handoff_payment_account.md 16). An older release does not know the read-only
// rule and would treat such a session as an ordinary, writable login, so this runs
// before a rollback. Each revoked session leaves an audit row naming the administrator
// who opened it and the user it showed. It returns how many sessions it ended.
func RevokeImpersonationSessions(ctx context.Context, db *gorm.DB) (int64, error) {
	var revoked int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var live []model.Session
		if err := tx.Select("id", "user_id", "impersonator_id").
			Where("impersonator_id IS NOT NULL AND revoked_at IS NULL AND expires_at > ?", now).Find(&live).Error; err != nil {
			return err
		}
		for _, session := range live {
			if err := tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: *session.ImpersonatorID, TargetID: session.UserID, Action: AuditImpersonateRevoked, Version: 1, Detail: "{}"}).Error; err != nil {
				return err
			}
		}
		// Expired but unrevoked rows are closed too, so the count of open ones stays honest.
		if err := tx.Model(&model.Session{}).Where("impersonator_id IS NOT NULL AND revoked_at IS NULL").Update("revoked_at", now).Error; err != nil {
			return err
		}
		revoked = int64(len(live))
		return nil
	})
	if err != nil {
		return 0, ErrRevoke
	}
	return revoked, nil
}
