package authmail

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// AuditPasswordReset is the AdminAudit action of a completed mail-link reset. ActorID
// and TargetID are the verified user; Version is the new AuthVersion.
const AuditPasswordReset = "auth.password_reset"

// CompleteReset is the only password-change path (handoff_auth.md 8.3). The caller has
// already resolved the token (ResolveReset), checked the new password and computed its
// bcrypt hash outside any lock. One transaction then takes User -> Challenge ->
// Session/NativeSession -> MailJob in that order and:
//
//  1. re-checks the account (active, same email, AuthVersion still the one bound to the
//     token) and the challenge (active, delivered, unexpired, same user and version);
//  2. lets admit veto the change against the locked row (for example a role change);
//  3. writes the new hash, PasswordChangedAt, AuthVersion+1 and a missing EmailVerifiedAt;
//  4. consumes this token, supersedes every other open reset of the account and cancels
//     their unsent mail;
//  5. revokes every web Session and NativeSession of the user;
//  6. writes the audit row and queues the "password changed" notice.
//
// Any failure rolls all of it back. It returns the new AuthVersion.
func (s *Service) CompleteReset(ctx context.Context, token string, ticket ResetTicket, hash string, admit func(model.User) error) (int64, error) {
	if !ValidTokenFormat(token) || !s.cfg.MailEnabled() {
		return 0, ErrResetTokenInvalid
	}
	now := s.now()
	digest := s.keys.TokenDigest(PurposeReset, token)
	var version int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", ticket.UserID).Take(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrResetTokenInvalid
		}
		if err != nil {
			return err
		}
		if user.Status != "active" || user.Email != ticket.Email || user.AuthVersion != ticket.AuthVersion {
			return ErrResetTokenInvalid
		}
		var challenge model.EmailAuthChallenge
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("purpose = ? AND digest = ?", PurposeReset, digest).Take(&challenge).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrResetTokenInvalid
		}
		if err != nil {
			return err
		}
		if !DigestEqual(challenge.Digest, digest) || challenge.ID != ticket.ChallengeID || challenge.Status != statusActive || !challenge.Delivered ||
			!now.Before(challenge.ExpiresAt) || challenge.UserID == nil || *challenge.UserID != user.ID || challenge.Email != user.Email ||
			challenge.BoundAuthVersion != user.AuthVersion {
			return ErrResetTokenInvalid
		}
		if admit != nil {
			if err = admit(user); err != nil {
				return err
			}
		}
		version = user.AuthVersion + 1
		update := map[string]any{"password_hash": hash, "password_changed_at": now, "auth_version": version, "updated_at": now}
		if user.EmailVerifiedAt == nil {
			// Holding the mailed link proves the mailbox, so the first reset may record it.
			update["email_verified_at"] = now
		}
		if err = tx.Model(&model.User{}).Where("id = ? AND auth_version = ?", user.ID, user.AuthVersion).Updates(update).Error; err != nil {
			return err
		}
		if err = tx.Model(&model.EmailAuthChallenge{}).Where("id = ?", challenge.ID).Updates(map[string]any{"status": statusConsumed, "consumed_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		var others []model.EmailAuthChallenge
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("purpose = ? AND (user_id = ? OR email = ?) AND status = ? AND id <> ?", PurposeReset, user.ID, user.Email, statusActive, challenge.ID).Order("id").Find(&others).Error; err != nil {
			return err
		}
		ids := []string{challenge.ID}
		if len(others) > 0 {
			stale := make([]string, len(others))
			for i, o := range others {
				stale[i] = o.ID
			}
			if err = tx.Model(&model.EmailAuthChallenge{}).Where("id IN ?", stale).Updates(map[string]any{"status": statusSuperseded, "updated_at": now}).Error; err != nil {
				return err
			}
			ids = append(ids, stale...)
		}
		if err = RevokeAllSessions(tx, user.ID, now); err != nil {
			return err
		}
		if err = s.cancelJobs(tx, ids, "consumed", now); err != nil {
			return err
		}
		if err = tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: user.ID, TargetID: user.ID, Action: AuditPasswordReset, Version: version, CreatedAt: now}).Error; err != nil {
			return err
		}
		return s.EnqueuePasswordChanged(tx, user.Email)
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// RevokeAllSessions ends every web Session and every client NativeSession of a user.
// The caller holds the user's row lock; rows are locked in primary-key order so two
// writers of the same sessions can never deadlock. Revoked native sessions answer
// 401 native_session_expired on their next request, which every client already treats
// as the end of the session.
func RevokeAllSessions(tx *gorm.DB, userID string, now time.Time) error {
	var web []model.Session
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("user_id = ? AND revoked_at IS NULL", userID).Order("id").Find(&web).Error; err != nil {
		return err
	}
	if len(web) > 0 {
		ids := make([]string, len(web))
		for i, w := range web {
			ids[i] = w.ID
		}
		if err := tx.Model(&model.Session{}).Where("id IN ?", ids).Update("revoked_at", now).Error; err != nil {
			return err
		}
	}
	var native []model.NativeSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("user_id = ? AND revoked_at IS NULL", userID).Order("id").Find(&native).Error; err != nil {
		return err
	}
	if len(native) > 0 {
		ids := make([]string, len(native))
		for i, n := range native {
			ids[i] = n.ID
		}
		if err := tx.Model(&model.NativeSession{}).Where("id IN ?", ids).Update("revoked_at", now).Error; err != nil {
			return err
		}
	}
	return nil
}
