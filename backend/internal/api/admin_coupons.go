package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// requireConsoleAdmin guards the account console (handoff_payment_account.md 9.1). Unlike
// requireAdmin it does not depend on the node catalog switch. Round 5 also refuses
// impersonation sessions here.
func (s *Server) requireConsoleAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if currentUser(c).Role != "admin" {
			fail(c, 403, "admin_required", "需要管理员权限")
			return
		}
		c.Next()
	}
}

// lockConsoleAdmin re-checks the acting administrator under its row lock, so a role
// removed meanwhile cannot still write. It is the first lock of the transaction.
func lockConsoleAdmin(tx *gorm.DB, c *gin.Context) error {
	var actor model.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&actor, "id = ? AND role = ? AND status = ?", currentUser(c).ID, "admin", "active").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &apiError{403, "admin_required", "需要管理员权限"}
	}
	return err
}

// consoleAudit writes one account-console action with a non-secret JSON detail.
func consoleAudit(tx *gorm.DB, actor, action, target string, version int64, detail any, now time.Time) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: actor, Action: action, TargetID: target, Version: version, Detail: string(data), CreatedAt: now}).Error
}

type couponAdminView struct {
	model.Coupon
	PlanIDs  []string `json:"plan_ids"`
	Reserved int64    `json:"reserved"`
	// Referenced: some order used this coupon, so its terms are frozen and deleting it
	// only hides it.
	Referenced bool `json:"referenced"`
}

func (s *Server) adminCouponList(c *gin.Context) {
	now := s.now()
	rows := []model.Coupon{}
	query := s.database(c).Order("created_at desc")
	if c.Query("include_deleted") != "true" {
		query = query.Where("deleted_at IS NULL")
	}
	if query.Limit(500).Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "优惠码暂时不可用")
		return
	}
	views := make([]couponAdminView, 0, len(rows))
	for _, row := range rows {
		view := couponAdminView{Coupon: row, PlanIDs: couponPlans(row)}
		if view.PlanIDs == nil {
			view.PlanIDs = []string{}
		}
		var refs int64
		if livePending(s.database(c).Model(&model.Order{}), now).Where("coupon_id = ?", row.ID).Count(&view.Reserved).Error != nil ||
			s.database(c).Model(&model.Order{}).Where("coupon_id = ?", row.ID).Count(&refs).Error != nil {
			fail(c, 503, "database_unavailable", "优惠码暂时不可用")
			return
		}
		view.Referenced = refs > 0
		views = append(views, view)
	}
	c.JSON(200, gin.H{"coupons": views})
}

type couponInput struct {
	Code           string     `json:"code"`
	Generate       bool       `json:"generate"`
	Note           string     `json:"note"`
	DiscountType   string     `json:"discount_type"`
	PercentOff     int        `json:"percent_off"`
	AmountOffCents int64      `json:"amount_off_cents"`
	PlanIDs        []string   `json:"plan_ids"`
	StartsAt       *time.Time `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
	MaxUses        int        `json:"max_uses"`
	PerUserLimit   int        `json:"per_user_limit"`
	Enabled        bool       `json:"enabled"`
	// Version is required when editing (optimistic concurrency between administrators).
	Version int64 `json:"version"`
}

// couponAlphabet avoids look-alike characters (0/O, 1/I).
const couponAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func generateCouponCode() (string, error) {
	out := make([]byte, 10)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(couponAlphabet))))
		if err != nil {
			return "", err
		}
		out[i] = couponAlphabet[n.Int64()]
	}
	return string(out), nil
}

// validateCoupon checks the terms; plan ids must name existing plans.
func validateCoupon(tx *gorm.DB, in *couponInput) error {
	bad := func(message string) error { return &apiError{400, "invalid_coupon", message} }
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 120 {
		return bad("备注最多 120 个字符")
	}
	switch in.DiscountType {
	case model.DiscountPercent:
		if in.PercentOff < 1 || in.PercentOff > 99 || in.AmountOffCents != 0 {
			return bad("百分比折扣须为 1–99")
		}
	case model.DiscountAmount:
		if in.AmountOffCents < 1 || in.AmountOffCents > 100_000_000 || in.PercentOff != 0 {
			return bad("立减金额须为 0.01–1000000.00 元")
		}
	default:
		return bad("请选择折扣类型")
	}
	if in.StartsAt != nil && in.EndsAt != nil && !in.EndsAt.After(*in.StartsAt) {
		return bad("结束时间必须晚于开始时间")
	}
	if in.MaxUses < 0 || in.MaxUses > 1_000_000 {
		return bad("总次数须为 0（不限）到 1000000")
	}
	if in.PerUserLimit < 1 || in.PerUserLimit > 100 {
		return bad("每人限用次数须为 1–100")
	}
	if in.PlanIDs == nil {
		in.PlanIDs = []string{}
	}
	if len(in.PlanIDs) > 50 {
		return bad("适用套餐过多")
	}
	slices.Sort(in.PlanIDs)
	in.PlanIDs = slices.Compact(in.PlanIDs)
	if len(in.PlanIDs) > 0 {
		var found int64
		if err := tx.Model(&model.Plan{}).Where("id IN ?", in.PlanIDs).Count(&found).Error; err != nil {
			return err
		}
		if found != int64(len(in.PlanIDs)) {
			return bad("适用套餐不存在")
		}
	}
	return nil
}

func (s *Server) adminCouponSave(c *gin.Context) {
	var in couponInput
	if !decode(c, &in) {
		return
	}
	id := c.Param("id")
	creating := id == ""
	if !creating {
		if _, err := uuid.Parse(id); err != nil {
			fail(c, 404, "coupon_not_found", "优惠码不存在")
			return
		}
	}
	now := s.now()
	actor := currentUser(c).ID
	var saved model.Coupon
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		if err := validateCoupon(tx, &in); err != nil {
			return err
		}
		plans, _ := json.Marshal(in.PlanIDs)
		if creating {
			code := normalizeCoupon(in.Code)
			if in.Generate {
				if code != "" {
					return &apiError{400, "invalid_coupon", "自动生成时不要填写优惠码"}
				}
				var err error
				if code, err = generateCouponCode(); err != nil {
					return err
				}
			}
			if !couponCodePattern.MatchString(code) {
				return &apiError{400, "invalid_coupon", "优惠码须为 4–32 位字母、数字或连字符"}
			}
			var taken int64
			if err := tx.Model(&model.Coupon{}).Where("code = ?", code).Count(&taken).Error; err != nil {
				return err
			}
			if taken > 0 {
				return &apiError{409, "coupon_exists", "该优惠码已存在"}
			}
			saved = model.Coupon{ID: uuid.NewString(), Code: code, Note: in.Note, DiscountType: in.DiscountType, PercentOff: in.PercentOff, AmountOffCents: in.AmountOffCents, PlanIDs: string(plans),
				StartsAt: in.StartsAt, EndsAt: in.EndsAt, MaxUses: in.MaxUses, PerUserLimit: in.PerUserLimit, Enabled: in.Enabled, CreatedBy: actor, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&saved).Error; err != nil {
				return err
			}
			return consoleAudit(tx, actor, "admin.coupon_create", saved.ID, saved.Version, gin.H{"code": saved.Code, "type": saved.DiscountType}, now)
		}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&saved, "id = ? AND deleted_at IS NULL", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &apiError{404, "coupon_not_found", "优惠码不存在"}
		}
		if err != nil {
			return err
		}
		if in.Version != saved.Version {
			return &apiError{409, "version_conflict", "优惠码已被其他管理员修改，请刷新后重试"}
		}
		if in.Code != "" && normalizeCoupon(in.Code) != saved.Code || in.Generate {
			return &apiError{400, "invalid_coupon", "优惠码创建后不能修改"}
		}
		var refs int64
		if err = tx.Model(&model.Order{}).Where("coupon_id = ?", saved.ID).Count(&refs).Error; err != nil {
			return err
		}
		if refs > 0 && (in.DiscountType != saved.DiscountType || in.PercentOff != saved.PercentOff || in.AmountOffCents != saved.AmountOffCents) {
			return &apiError{409, "coupon_in_use", "已被订单使用的优惠码不能修改折扣，请停用后新建"}
		}
		saved.Note, saved.DiscountType, saved.PercentOff, saved.AmountOffCents, saved.PlanIDs = in.Note, in.DiscountType, in.PercentOff, in.AmountOffCents, string(plans)
		saved.StartsAt, saved.EndsAt, saved.MaxUses, saved.PerUserLimit, saved.Enabled = in.StartsAt, in.EndsAt, in.MaxUses, in.PerUserLimit, in.Enabled
		saved.Version++
		saved.UpdatedAt = now
		if err = tx.Save(&saved).Error; err != nil {
			return err
		}
		return consoleAudit(tx, actor, "admin.coupon_update", saved.ID, saved.Version, gin.H{"code": saved.Code, "enabled": saved.Enabled}, now)
	})
	if paymentFailure(c, err) {
		return
	}
	status := 200
	if creating {
		status = 201
	}
	view := couponAdminView{Coupon: saved, PlanIDs: couponPlans(saved)}
	c.JSON(status, gin.H{"coupon": view})
}

// adminCouponDelete removes a coupon no order has referenced; otherwise it disables and
// hides it, so order snapshots and usage counts stay intact.
func (s *Server) adminCouponDelete(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "coupon_not_found", "优惠码不存在")
		return
	}
	now := s.now()
	actor := currentUser(c).ID
	mode := "hard"
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		var coupon model.Coupon
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&coupon, "id = ? AND deleted_at IS NULL", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &apiError{404, "coupon_not_found", "优惠码不存在"}
		}
		if err != nil {
			return err
		}
		var refs int64
		if err = tx.Model(&model.Order{}).Where("coupon_id = ?", coupon.ID).Count(&refs).Error; err != nil {
			return err
		}
		if refs > 0 || coupon.UsedCount > 0 {
			mode = "soft"
			err = tx.Model(&coupon).Updates(map[string]any{"deleted_at": now, "enabled": false, "version": coupon.Version + 1, "updated_at": now}).Error
		} else {
			err = tx.Delete(&coupon).Error
		}
		if err != nil {
			return err
		}
		return consoleAudit(tx, actor, "admin.coupon_delete", coupon.ID, coupon.Version+1, gin.H{"code": coupon.Code, "mode": mode}, now)
	})
	if paymentFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"deleted": mode})
}
