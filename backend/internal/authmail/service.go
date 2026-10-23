package authmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// Challenge states. Usability is decided by state AND expiry AND failures at read
// time; nothing depends on a background job having run.
const (
	statusActive     = "active"
	statusConsumed   = "consumed"
	statusSuperseded = "superseded"
	statusLocked     = "locked"
)

// Stable verification outcomes (mapped to verification_* / reset_token_invalid by the API).
var (
	ErrVerificationInvalid = errors.New("verification code does not match")
	ErrVerificationExpired = errors.New("verification code expired or replaced")
	ErrVerificationUsed    = errors.New("verification code already used")
	ErrVerificationLocked  = errors.New("verification code locked after too many failures")
	ErrCodeFormat          = errors.New("verification code must be six ASCII digits")
	ErrResetTokenInvalid   = errors.New("reset token is not usable")
)

// Service issues and checks mailbox proofs. All state is in PostgreSQL, so every API
// instance sharing the database and AUTH_MAIL_SECRET behaves as one.
type Service struct {
	db            *gorm.DB
	schema        string
	cfg           config.Config
	keys          Keys
	messageDomain string
	// Limits and Now are exported for tests; production uses DefaultLimits and the wall clock.
	Limits Limits
	Now    func() time.Time
}

// NewService builds the service. Without complete mail configuration it still
// answers capability questions, but every issuance returns ErrMailUnavailable.
func NewService(db *gorm.DB, cfg config.Config) *Service {
	s := &Service{db: db, schema: cfg.Schema, cfg: cfg, Limits: DefaultLimits(), Now: time.Now}
	if cfg.MailEnabled() {
		s.keys, _ = DeriveKeys(cfg.AuthMailSecret)
		s.messageDomain = cfg.SMTPUser[strings.LastIndexByte(cfg.SMTPUser, '@')+1:]
	}
	return s
}

// Keys exposes the derived keys to the worker in the same process.
func (s *Service) Keys() Keys { return s.keys }

func (s *Service) now() time.Time { return s.Now().UTC() }

// Receipt is the public, account-independent answer to a mail request.
type Receipt struct {
	VerificationID string
	ExpiresAt      time.Time
	NextSendAt     time.Time
}

// IssueRegistration accepts a registration-code request. Existing accounts get an
// equal-shape challenge that queues no mail, so the response never reveals them.
func (s *Service) IssueRegistration(ctx context.Context, input, ip string) (Receipt, error) {
	email, err := RegistrationEmail(input, s.cfg.RegistrationDomains)
	if err != nil {
		return Receipt{}, err
	}
	if !s.cfg.AllowRegistration {
		return Receipt{}, ErrRegistrationClosed
	}
	if !s.cfg.MailEnabled() {
		return Receipt{}, ErrMailUnavailable
	}
	now := s.now()
	if err = s.admit(ctx, ip, now); err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		emailKey := s.keys.RateKey("email", email)
		row, err := s.lockMailbox(tx, emailKey, now)
		if err != nil {
			return err
		}
		next, err := s.chargeMailbox(tx, emailKey, row, now)
		if err != nil {
			return err
		}
		var existing int64
		if err = tx.Model(&model.User{}).Where("email = ?", email).Count(&existing).Error; err != nil {
			return err
		}
		code, err := NewCode()
		if err != nil {
			return err
		}
		challenge, err := s.newChallenge(tx, PurposeRegister, email, nil, 0, existing == 0, s.Limits.CodeTTL, now)
		if err != nil {
			return err
		}
		challenge.Digest = s.keys.CodeDigest(PurposeRegister, email, challenge.ID, code)
		if err = tx.Create(&challenge).Error; err != nil {
			return err
		}
		if challenge.Delivered {
			if err = s.enqueue(tx, &challenge.ID, TemplateRegisterCode, mailPayload{To: email, Code: code, Minutes: int(s.Limits.CodeTTL / time.Minute)}, &challenge.ExpiresAt, now); err != nil {
				return err
			}
		}
		receipt = Receipt{VerificationID: challenge.ID, ExpiresAt: challenge.ExpiresAt, NextSendAt: next}
		return nil
	})
	return receipt, err
}

// IssueReset accepts a forgot-password request under the historical identity rule
// (no registration allow-list). Only an existing active account gets a usable link;
// every other case writes an undeliverable challenge with the same timing profile.
func (s *Service) IssueReset(ctx context.Context, input, ip string) (Receipt, error) {
	email, ok := NormalizeEmail(input)
	if !ok {
		return Receipt{}, ErrInvalidEmail
	}
	if !s.cfg.MailEnabled() {
		return Receipt{}, ErrMailUnavailable
	}
	now := s.now()
	if err := s.admit(ctx, ip, now); err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock order: User -> mailbox coordination row -> Challenge -> MailJob.
		// FOR SHARE holds off a concurrent password change while the version is bound.
		var users []model.User
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("email = ?", email).Limit(1).Find(&users).Error; err != nil {
			return err
		}
		emailKey := s.keys.RateKey("email", email)
		row, err := s.lockMailbox(tx, emailKey, now)
		if err != nil {
			return err
		}
		next, err := s.chargeMailbox(tx, emailKey, row, now)
		if err != nil {
			return err
		}
		token, err := NewResetToken()
		if err != nil {
			return err
		}
		var userID *string
		var version int64
		deliver := len(users) == 1 && users[0].Status == "active"
		if deliver {
			userID, version = &users[0].ID, users[0].AuthVersion
		}
		challenge, err := s.newChallenge(tx, PurposeReset, email, userID, version, deliver, s.Limits.ResetTTL, now)
		if err != nil {
			return err
		}
		challenge.Digest = s.keys.TokenDigest(PurposeReset, token)
		if err = tx.Create(&challenge).Error; err != nil {
			return err
		}
		if deliver {
			link := s.cfg.WebsiteBaseURL + "/reset-password#token=" + token
			if err = s.enqueue(tx, &challenge.ID, TemplateResetLink, mailPayload{To: email, Link: link, Minutes: int(s.Limits.ResetTTL / time.Minute)}, &challenge.ExpiresAt, now); err != nil {
				return err
			}
		}
		receipt = Receipt{NextSendAt: next}
		return nil
	})
	return receipt, err
}

// newChallenge supersedes the previous generation for (purpose, email) and cancels its
// undelivered mail in the same transaction. Mail already inside an SMTP call cannot be
// recalled, but its credential no longer validates. The caller holds the mailbox row
// lock; the partial unique index on active challenges is the backstop.
func (s *Service) newChallenge(tx *gorm.DB, purpose, email string, userID *string, version int64, deliver bool, ttl time.Duration, now time.Time) (model.EmailAuthChallenge, error) {
	var previous []model.EmailAuthChallenge
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "generation").Where("purpose = ? AND email = ? AND status = ?", purpose, email, statusActive).Order("id").Find(&previous).Error; err != nil {
		return model.EmailAuthChallenge{}, err
	}
	var generation int64
	if err := tx.Model(&model.EmailAuthChallenge{}).Where("purpose = ? AND email = ?", purpose, email).Select("COALESCE(MAX(generation), 0)").Scan(&generation).Error; err != nil {
		return model.EmailAuthChallenge{}, err
	}
	if len(previous) > 0 {
		ids := make([]string, len(previous))
		for i, p := range previous {
			ids[i] = p.ID
		}
		if err := tx.Model(&model.EmailAuthChallenge{}).Where("id IN ?", ids).Updates(map[string]any{"status": statusSuperseded, "updated_at": now}).Error; err != nil {
			return model.EmailAuthChallenge{}, err
		}
		if err := s.cancelJobs(tx, ids, "superseded", now); err != nil {
			return model.EmailAuthChallenge{}, err
		}
	}
	return model.EmailAuthChallenge{ID: uuid.NewString(), Purpose: purpose, Email: email, UserID: userID, Generation: generation + 1, BoundAuthVersion: version, Delivered: deliver, Status: statusActive, IssuedAt: now, ExpiresAt: now.Add(ttl), UpdatedAt: now}, nil
}

// cancelJobs stops not-yet-claimed mail of the given challenges and erases its secrets.
func (s *Service) cancelJobs(tx *gorm.DB, challengeIDs []string, class string, now time.Time) error {
	return tx.Model(&model.AuthMailJob{}).Where("challenge_id IN ? AND status IN ?", challengeIDs, []string{jobQueued, jobRetry}).
		Updates(map[string]any{"status": jobCancelled, "failure_class": class, "payload": nil, "finished_at": now, "lease_until": nil}).Error
}

// enqueue seals the payload (bound to the job ID and template) and stores the job in
// the caller's transaction: no commit, no mail.
func (s *Service) enqueue(tx *gorm.DB, challengeID *string, template string, p mailPayload, deliverBy *time.Time, now time.Time) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	job := model.AuthMailJob{ID: uuid.NewString(), ChallengeID: challengeID, Template: template, RecipientHint: MaskEmail(p.To), Status: jobQueued, NextAttemptAt: now, DeliverBy: deliverBy, CreatedAt: now}
	job.MessageID = job.ID + "@" + s.messageDomain
	if job.Payload, err = s.keys.Seal(job.ID, template, data); err != nil {
		return err
	}
	return tx.Create(&job).Error
}

// EnqueuePasswordChanged queues the change notice inside the caller's password-change
// transaction. It has no challenge, so challenge cleanup can never drop it.
func (s *Service) EnqueuePasswordChanged(tx *gorm.DB, email string) error {
	if !s.cfg.MailEnabled() {
		return nil
	}
	return s.enqueue(tx, nil, TemplatePasswordChanged, mailPayload{To: email}, nil, s.now())
}

// EnqueueAccountDeleted queues the deletion notice inside the deletion transaction. It
// has no challenge, so neither challenge cleanup nor the purge pipeline drops it.
func (s *Service) EnqueueAccountDeleted(tx *gorm.DB, email string) error {
	if !s.cfg.MailEnabled() {
		return nil
	}
	return s.enqueue(tx, nil, TemplateAccountDeleted, mailPayload{To: email}, nil, s.now())
}

// VerifyRegistration checks a code and, only if it matches, runs onSuccess (which
// creates the account) in the same transaction as consuming the challenge. A wrong
// code is recorded and COMMITTED before the error is returned, so failures can never
// be rolled back by the caller, a restart or another instance.
func (s *Service) VerifyRegistration(ctx context.Context, verificationID, email, code string, onSuccess func(tx *gorm.DB) error) error {
	if !ValidCodeFormat(code) {
		return ErrCodeFormat
	}
	if _, err := uuid.Parse(verificationID); err != nil {
		return ErrVerificationInvalid
	}
	email, ok := NormalizeEmail(email)
	if !ok {
		return ErrVerificationInvalid
	}
	if !s.cfg.MailEnabled() {
		return ErrMailUnavailable
	}
	now := s.now()
	var outcome error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		emailKey := s.keys.RateKey("email", email)
		if _, err := s.lockMailbox(tx, emailKey, now); err != nil {
			return err
		}
		failures, end, err := s.peek(tx, scopeEmailVerify, emailKey, s.Limits.EmailVerifyWindow, now)
		if err != nil {
			return err
		}
		if failures >= s.Limits.EmailVerifyFailures {
			return &RateLimitError{RetryAfter: end.Sub(now), Scope: scopeEmailVerify}
		}
		recordFailure := func() error {
			_, _, e := s.bump(tx, scopeEmailVerify, emailKey, s.Limits.EmailVerifyWindow, now)
			return e
		}
		var challenge model.EmailAuthChallenge
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", verificationID).Take(&challenge).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && (challenge.Purpose != PurposeRegister || challenge.Email != email) {
			outcome = ErrVerificationInvalid
			return recordFailure()
		}
		if err != nil {
			return err
		}
		switch {
		case challenge.Status == statusConsumed:
			outcome = ErrVerificationUsed
			return nil
		case challenge.Status == statusLocked || challenge.Failures >= s.Limits.ChallengeMaxFailures:
			outcome = ErrVerificationLocked
			return nil
		case challenge.Status != statusActive || !now.Before(challenge.ExpiresAt):
			outcome = ErrVerificationExpired
			return nil
		}
		if !DigestEqual(challenge.Digest, s.keys.CodeDigest(PurposeRegister, email, challenge.ID, code)) {
			failures := challenge.Failures + 1
			update := map[string]any{"failures": failures, "updated_at": now}
			outcome = ErrVerificationInvalid
			if failures >= s.Limits.ChallengeMaxFailures {
				update["status"] = statusLocked
				outcome = ErrVerificationLocked
			}
			if err = tx.Model(&challenge).Updates(update).Error; err != nil {
				return err
			}
			return recordFailure()
		}
		if err = onSuccess(tx); err != nil {
			return err
		}
		if err = tx.Model(&challenge).Updates(map[string]any{"status": statusConsumed, "consumed_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return s.cancelJobs(tx, []string{challenge.ID}, "consumed", now)
	})
	if err != nil {
		return err
	}
	return outcome
}

// ResetTicket identifies a usable reset challenge for the read-only validate step.
type ResetTicket struct {
	ChallengeID, UserID, Email string
	AuthVersion                int64
	ExpiresAt                  time.Time
}

// ResolveReset checks a reset token without consuming it: browsers, scanners and
// prefetches can call it any number of times. Every unusable case is one error.
func (s *Service) ResolveReset(ctx context.Context, token string) (ResetTicket, error) {
	if !ValidTokenFormat(token) || !s.cfg.MailEnabled() {
		return ResetTicket{}, ErrResetTokenInvalid
	}
	now := s.now()
	digest := s.keys.TokenDigest(PurposeReset, token)
	var row struct {
		model.EmailAuthChallenge
		CurrentVersion int64
		UserStatus     string
		UserEmail      string
	}
	err := s.db.WithContext(ctx).Table(s.table("email_auth_challenges")+" AS c").
		Select("c.*, u.auth_version AS current_version, u.status AS user_status, u.email AS user_email").
		Joins("JOIN "+s.table("users")+" AS u ON u.id = c.user_id").
		Where("c.purpose = ? AND c.digest = ?", PurposeReset, digest).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ResetTicket{}, ErrResetTokenInvalid
	}
	if err != nil {
		return ResetTicket{}, fmt.Errorf("reset lookup: %w", err)
	}
	c := row.EmailAuthChallenge
	if !DigestEqual(c.Digest, digest) || c.Status != statusActive || !c.Delivered || !now.Before(c.ExpiresAt) || c.UserID == nil ||
		row.UserStatus != "active" || row.UserEmail != c.Email || row.CurrentVersion != c.BoundAuthVersion {
		return ResetTicket{}, ErrResetTokenInvalid
	}
	return ResetTicket{ChallengeID: c.ID, UserID: *c.UserID, Email: c.Email, AuthVersion: c.BoundAuthVersion, ExpiresAt: c.ExpiresAt}, nil
}
