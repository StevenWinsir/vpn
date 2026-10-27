package api

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/referral"
)

// Referral programme (handoff_coupon.md): a referee is bound at registration, the
// first real paid order qualifies the row inside the fulfilment transaction, and the
// reward worker settles it on the referrer's subscription outside every request path.

const (
	referralCodeAttempts = 5
	referralClaimBatch   = 20
	referralClaimLease   = time.Minute
	referralSweepBatch   = 100
	referralPageSize     = 20
	consoleReferralLimit = 50
	// ReferralSweepInterval is how often cmd/api looks for first purchases a binding
	// missed (for example while an older binary was running).
	ReferralSweepInterval = 10 * time.Minute
)

var errReferralUnavailable = &apiError{503, "referral_unavailable", "推荐码暂时无法生成，请稍后重试"}

func (s *Server) table(name string) string { return `"` + s.cfg.Schema + `".` + name }

// newReferralCode draws a code (tests inject a source to force collisions).
func (s *Server) newReferralCode() (string, error) {
	if s.referralCodes != nil {
		return s.referralCodes()
	}
	return referral.Generate(nil)
}

// ensureReferralCode returns the user's code, creating it on first use (D2). The write
// only fills a NULL, so two concurrent first visits end with the same code; a unique
// conflict with another account's code draws again.
func (s *Server) ensureReferralCode(db *gorm.DB, userID string) (string, error) {
	read := func() (*string, error) {
		var u model.User
		if err := db.Select("id", "referral_code").First(&u, "id = ?", userID).Error; err != nil {
			return nil, err
		}
		return u.ReferralCode, nil
	}
	if code, err := read(); err != nil || code != nil {
		if err != nil {
			return "", err
		}
		return *code, nil
	}
	for range referralCodeAttempts {
		code, err := s.newReferralCode()
		if err != nil {
			return "", err
		}
		res := db.Model(&model.User{}).Where("id = ? AND referral_code IS NULL", userID).UpdateColumn("referral_code", code)
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			continue
		}
		if res.Error != nil {
			return "", res.Error
		}
		if res.RowsAffected == 1 {
			return code, nil
		}
		// Someone else filled it meanwhile.
		current, err := read()
		if err != nil {
			return "", err
		}
		if current != nil {
			return *current, nil
		}
	}
	log.Printf("referral: code generation gave up after %d collisions", referralCodeAttempts)
	return "", errReferralUnavailable
}

// bindReferral records that the account being registered in tx was referred by the
// owner of code (handoff_coupon.md 6). It never fails the registration: an unknown
// code or an unusable referrer inserts nothing, and a statement error is rolled back to
// a savepoint and only logged by category.
func (s *Server) bindReferral(tx *gorm.DB, user model.User, code string, now time.Time) {
	if tx.SavePoint("referral").Error != nil {
		log.Printf("referral: binding skipped (savepoint)")
		return
	}
	err := tx.Exec(`INSERT INTO `+s.table("referrals")+` (id, referrer_id, referee_id, referee_email_masked, code_snapshot, status, created_at, updated_at)
		SELECT ?, u.id, ?, ?, ?, ?, ?, ? FROM `+s.table("users")+` AS u
		WHERE u.referral_code = ? AND u.status = 'active' AND u.id <> ?
		ON CONFLICT (referee_id) DO NOTHING`,
		uuid.NewString(), user.ID, referral.MaskEmail(user.Email), code, model.ReferralRegistered, now, now, code, user.ID).Error
	if err != nil {
		log.Printf("referral: binding skipped (statement failed)")
		_ = tx.RollbackTo("referral").Error
	}
}

// qualifyReferral marks the referee's binding qualified by the referee's first real
// paid order (D5, D6). The caller holds the referee's user lock (fulfilment, or the
// sweep), so concurrent payments of one referee are serialised; the row's status makes
// it happen once. It never reads or locks anything of the referrer.
func (s *Server) qualifyReferral(tx *gorm.DB, refereeID string, now time.Time) error {
	var row model.Referral
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("referee_id = ? AND status = ?", refereeID, model.ReferralRegistered).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var first struct {
		ID          string
		AmountCents int64
		PaidCents   *int64
	}
	err = tx.Model(&model.Order{}).Select("id", "amount_cents", "paid_cents").
		Where("user_id = ? AND status = ? AND provider <> ?", refereeID, model.OrderPaid, "test").
		Order("paid_at, id").Limit(1).Take(&first).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	cents := first.AmountCents
	if first.PaidCents != nil && *first.PaidCents < cents {
		cents = *first.PaidCents
	}
	updates := map[string]any{"qualified_at": now, "first_order_id": first.ID, "first_order_cents": cents, "updated_at": now}
	if cents <= 0 {
		updates["status"], updates["forfeit_reason"] = model.ReferralForfeited, model.ForfeitZeroAmount
	} else {
		updates["status"], updates["reward_bytes"], updates["next_attempt_at"] = model.ReferralQualified, referral.RewardBytes(cents, s.cfg.Referral.MaxRewardGiB), now
	}
	return tx.Model(&row).Updates(updates).Error
}

// referrerHasPlan is D7: a subscription inside its validity window, traffic used up or
// not; a test subscription only counts where test entitlements are allowed.
func (s *Server) referrerHasPlan(sub *model.Subscription, now time.Time) bool {
	return sub != nil && !now.Before(sub.StartsAt) && now.Before(sub.ExpiresAt) && (!sub.IsTest || s.allowTest())
}

// ProcessReferrals settles one batch of qualified referrals (handoff_coupon.md 8). It
// runs in every API instance; claims are leases taken with SKIP LOCKED.
func ProcessReferrals(ctx context.Context, db *gorm.DB, cfg config.Config) (int, error) {
	s := &Server{db: db, cfg: cfg}
	return s.processReferrals(ctx)
}

// SweepReferrals qualifies bindings whose referee already has a paid order that was
// fulfilled without the check (handoff_coupon.md 7.3).
func SweepReferrals(ctx context.Context, db *gorm.DB, cfg config.Config) (int, error) {
	s := &Server{db: db, cfg: cfg}
	return s.sweepReferrals(ctx)
}

func (s *Server) processReferrals(ctx context.Context) (int, error) {
	if !s.cfg.ReferralEnabled() {
		return 0, nil
	}
	now := s.now()
	var ids []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.Referral
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Select("id").
			Where("status = ? AND next_attempt_at <= ?", model.ReferralQualified, now).
			Order("next_attempt_at, id").Limit(referralClaimBatch).Find(&rows).Error; e != nil {
			return e
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		return tx.Model(&model.Referral{}).Where("id IN ?", ids).Update("next_attempt_at", now.Add(referralClaimLease)).Error
	})
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, id := range ids {
		done, err := s.grantReferral(ctx, id, now)
		if err != nil {
			s.retryReferral(ctx, id, now)
			continue
		}
		if done {
			settled++
		}
	}
	return settled, nil
}

// grantReferral settles one qualified row. Lock order: User(referrer) -> Referral ->
// Subscription -> Period, the same as the referrer's own purchases.
func (s *Server) grantReferral(ctx context.Context, id string, now time.Time) (bool, error) {
	var head model.Referral
	if err := s.db.WithContext(ctx).Select("id", "referrer_id").Take(&head, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	done := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, e := lockUserRow(tx, head.ReferrerID)
		if e != nil {
			return e
		}
		var row model.Referral
		e = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", id, model.ReferralQualified).Take(&row).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		done = true
		forfeit := func(reason string) error {
			return tx.Model(&row).Updates(map[string]any{"status": model.ReferralForfeited, "forfeit_reason": reason, "next_attempt_at": nil, "updated_at": now}).Error
		}
		if user == nil || user.Status != "active" {
			return forfeit(model.ForfeitUnavailable)
		}
		sub, e := readSubscription(tx, user.ID, now)
		if e != nil {
			return e
		}
		if !s.referrerHasPlan(sub, now) {
			return forfeit(model.ForfeitNoPlan)
		}
		granted := max(0, min(row.RewardBytes, maxPlanBytes-sub.TrafficLimitBytes))
		billing.AddBonus(sub, granted)
		sub.Version++
		if _, e = saveSubscription(tx, sub); e != nil {
			return e
		}
		return tx.Model(&row).Updates(map[string]any{"status": model.ReferralRewarded, "granted_bytes": granted, "granted_at": now,
			"granted_subscription_id": sub.ID, "granted_plan_name": sub.PlanName, "next_attempt_at": nil, "last_error_kind": "", "updated_at": now}).Error
	})
	return done && err == nil, err
}

// referralBackoff is 30 s, 1 min, 2 min ... up to 30 min.
func referralBackoff(attempts int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempts && d < 30*time.Minute; i++ {
		d *= 2
	}
	return min(d, 30*time.Minute)
}

func (s *Server) retryReferral(ctx context.Context, id string, now time.Time) {
	var row model.Referral
	db := s.db.WithContext(ctx)
	if db.Select("id", "attempts").Take(&row, "id = ?", id).Error != nil {
		return
	}
	attempts := row.Attempts + 1
	if db.Model(&model.Referral{}).Where("id = ? AND status = ?", id, model.ReferralQualified).
		Updates(map[string]any{"attempts": attempts, "next_attempt_at": now.Add(referralBackoff(attempts)), "last_error_kind": "database", "updated_at": now}).Error != nil {
		log.Printf("referral: could not schedule a retry")
	}
}

func (s *Server) sweepReferrals(ctx context.Context) (int, error) {
	now := s.now()
	var referees []string
	err := s.db.WithContext(ctx).Raw(`SELECT r.referee_id FROM `+s.table("referrals")+` AS r
		WHERE r.status = ? AND r.referee_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM `+s.table("orders")+` AS o WHERE o.user_id = r.referee_id AND o.status = ? AND o.provider <> 'test')
		ORDER BY r.created_at LIMIT ?`, model.ReferralRegistered, model.OrderPaid, referralSweepBatch).Scan(&referees).Error
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range referees {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if _, e := lockUserRow(tx, id); e != nil {
				return e
			}
			return s.qualifyReferral(tx, id, now)
		})
		if err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// referralStats is the per-status summary of one referrer's rows.
type referralStats struct {
	RegisteredCount int64 `json:"registered_count"`
	PurchasedCount  int64 `json:"purchased_count"`
	PendingCount    int64 `json:"pending_count"`
	RewardedCount   int64 `json:"rewarded_count"`
	RewardedBytes   int64 `json:"rewarded_bytes"`
	ForfeitedCount  int64 `json:"forfeited_count"`
}

func (s *Server) referralStats(db *gorm.DB, referrerID string) (referralStats, error) {
	var rows []struct {
		Status  string
		Count   int64
		Granted int64
	}
	var out referralStats
	if err := db.Model(&model.Referral{}).Select("status, count(*) AS count, COALESCE(sum(granted_bytes), 0) AS granted").
		Where("referrer_id = ?", referrerID).Group("status").Scan(&rows).Error; err != nil {
		return out, err
	}
	for _, r := range rows {
		out.RegisteredCount += r.Count
		switch r.Status {
		case model.ReferralQualified:
			out.PendingCount += r.Count
		case model.ReferralRewarded:
			out.RewardedCount += r.Count
			out.RewardedBytes += r.Granted
		case model.ReferralForfeited:
			out.ForfeitedCount += r.Count
		}
		if r.Status != model.ReferralRegistered {
			out.PurchasedCount += r.Count
		}
	}
	return out, nil
}

// myReferral is the signed-in user's code, rules and summary (handoff_coupon.md 11).
func (s *Server) myReferral(c *gin.Context) {
	user, now, db := currentUser(c), s.now(), s.database(c)
	code, err := s.ensureReferralCode(db, user.ID)
	if err != nil {
		var business *apiError
		if errors.As(err, &business) {
			fail(c, business.Status, business.Code, business.Message)
			return
		}
		fail(c, 503, "database_unavailable", "推荐信息暂时不可用")
		return
	}
	stats, err := s.referralStats(db, user.ID)
	if err != nil {
		fail(c, 503, "database_unavailable", "推荐信息暂时不可用")
		return
	}
	sub, err := readSubscription(db, user.ID, now)
	if err != nil {
		fail(c, 503, "database_unavailable", "推荐信息暂时不可用")
		return
	}
	c.JSON(200, gin.H{"enabled": s.cfg.ReferralEnabled(), "code": code, "share_path": "/register?ref=" + code,
		"reward_rule":     gin.H{"gib_per_yuan": 1, "max_gib": s.cfg.Referral.MaxRewardGiB},
		"has_active_plan": s.referrerHasPlan(sub, now), "stats": stats, "current_bonus_bytes": billing.CurrentBonus(sub)})
}

// referralItem is what a referrer may see of one referee: never an id, an address or
// an order.
type referralItem struct {
	EmailMasked    string     `json:"email_masked"`
	RegisteredAt   time.Time  `json:"registered_at"`
	Status         string     `json:"status"`
	RewardBytes    *int64     `json:"reward_bytes"`
	GrantedBytes   int64      `json:"granted_bytes"`
	GrantedAt      *time.Time `json:"granted_at"`
	ForfeitReason  string     `json:"forfeit_reason"`
	RefereeDeleted bool       `json:"referee_deleted"`
}

func (s *Server) myReferrals(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		fail(c, 400, "invalid_page", "页码超出范围")
		return
	}
	var rows []model.Referral
	var total int64
	db := s.database(c).Model(&model.Referral{}).Where("referrer_id = ?", currentUser(c).ID)
	if db.Count(&total).Error != nil || db.Order("created_at DESC, id").Limit(referralPageSize).Offset((page-1)*referralPageSize).Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "推荐记录暂时不可用")
		return
	}
	items := make([]referralItem, 0, len(rows))
	for _, r := range rows {
		item := referralItem{EmailMasked: r.RefereeEmailMasked, RegisteredAt: r.CreatedAt, Status: r.Status, GrantedBytes: r.GrantedBytes, GrantedAt: r.GrantedAt, ForfeitReason: r.ForfeitReason, RefereeDeleted: r.RefereeID == nil}
		if r.Status != model.ReferralRegistered {
			reward := r.RewardBytes
			item.RewardBytes = &reward
		}
		items = append(items, item)
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": referralPageSize})
}

// consoleReferral is one referee as an administrator sees it.
type consoleReferral struct {
	ID              string     `json:"id"`
	RefereeID       *string    `json:"referee_id"`
	RefereeEmail    string     `json:"referee_email"`
	Status          string     `json:"status"`
	FirstOrderCents int64      `json:"first_order_cents"`
	RewardBytes     int64      `json:"reward_bytes"`
	GrantedBytes    int64      `json:"granted_bytes"`
	ForfeitReason   string     `json:"forfeit_reason"`
	LastErrorKind   string     `json:"last_error_kind"`
	CreatedAt       time.Time  `json:"created_at"`
	QualifiedAt     *time.Time `json:"qualified_at"`
	GrantedAt       *time.Time `json:"granted_at"`
}

// consoleUserReferrals is the read-only referral section of a user's detail.
func (s *Server) consoleUserReferrals(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	db := s.database(c)
	var user model.User
	if err := db.Select("id", "referral_code").First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "user_not_found", "用户不存在")
			return
		}
		fail(c, 503, "console_unavailable", "推荐信息暂时不可用")
		return
	}
	var referrer []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	err := db.Raw(`SELECT u.id, u.email FROM `+s.table("referrals")+` AS r JOIN `+s.table("users")+` AS u ON u.id = r.referrer_id WHERE r.referee_id = ?`, id).Scan(&referrer).Error
	var items []consoleReferral
	if err == nil {
		err = db.Raw(`SELECT r.id, r.referee_id, COALESCE(u.email, r.referee_email_masked) AS referee_email, r.status, r.first_order_cents, r.reward_bytes,
			r.granted_bytes, r.forfeit_reason, r.last_error_kind, r.created_at, r.qualified_at, r.granted_at
			FROM `+s.table("referrals")+` AS r LEFT JOIN `+s.table("users")+` AS u ON u.id = r.referee_id
			WHERE r.referrer_id = ? ORDER BY r.created_at DESC, r.id LIMIT ?`, id, consoleReferralLimit).Scan(&items).Error
	}
	stats, statErr := s.referralStats(db, id)
	if err != nil || statErr != nil {
		fail(c, 503, "console_unavailable", "推荐信息暂时不可用")
		return
	}
	if items == nil {
		items = []consoleReferral{}
	}
	out := gin.H{"code": user.ReferralCode, "referrer": nil, "stats": stats, "items": items}
	if len(referrer) == 1 {
		out["referrer"] = referrer[0]
	}
	c.JSON(200, out)
}
