package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// The plan console (handoff_shop.md 6, 9): administrators define plans, their yearly
// tier and the shop-wide yearly discount. Plans are soft-deleted only (D7).

// Console audit actions for the shop.
const (
	AuditPlanCreate   = "admin.plan_create"
	AuditPlanUpdate   = "admin.plan_update"
	AuditPlanDelete   = "admin.plan_delete"
	AuditShopSettings = "admin.shop_settings"

	maxLivePlans   = 30
	maxActivePlans = 12
	// planWriteLock serialises plan creation and activation, which check counts.
	planWriteLock = 708614930
)

var planIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

const planIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func generatePlanID() (string, error) {
	out := make([]byte, 6)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(planIDAlphabet))))
		if err != nil {
			return "", err
		}
		out[i] = planIDAlphabet[n.Int64()]
	}
	return "plan-" + string(out), nil
}

// auditTarget maps a non-UUID target (a plan id, the settings row) to the UUID column
// of the audit table; the readable id is also in the detail.
func auditTarget(kind, id string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+":"+id)).String()
}

type planInput struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Description           string `json:"description"`
	Note                  string `json:"note"`
	PriceCents            int64  `json:"price_cents"`
	DurationDays          int    `json:"duration_days"`
	TrafficBytes          int64  `json:"traffic_bytes"`
	MaxDevices            int    `json:"max_devices"`
	AllowDedicated        bool   `json:"allow_dedicated"`
	YearlyEnabled         bool   `json:"yearly_enabled"`
	YearlyDurationDays    int    `json:"yearly_duration_days"`
	YearlyTrafficBytes    *int64 `json:"yearly_traffic_bytes"`
	YearlyDiscountPercent *int   `json:"yearly_discount_percent"`
	Featured              bool   `json:"featured"`
	Sort                  int    `json:"sort"`
	Active                bool   `json:"active"`
	// Version is required when editing (optimistic concurrency between administrators).
	Version int64 `json:"version"`
}

func textProblem(value string, limit int, required bool) string {
	n := len([]rune(value))
	switch {
	case required && n == 0:
		return "不能为空"
	case n > limit:
		return fmt.Sprintf("最多 %d 个字符", limit)
	case slices.ContainsFunc([]rune(value), func(r rune) bool { return unicode.IsControl(r) && r != '\n' }):
		return "不能包含控制字符"
	}
	return ""
}

// validatePlanInput checks and normalises a plan's terms. Every message names its field.
func validatePlanInput(in *planInput) error {
	in.Name, in.Description, in.Note = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), strings.TrimSpace(in.Note)
	if p := textProblem(in.Name, 40, true); p != "" {
		return fieldError("name", "名称"+p)
	}
	if p := textProblem(in.Description, 300, false); p != "" {
		return fieldError("description", "公开描述"+p)
	}
	if p := textProblem(in.Note, 200, false); p != "" {
		return fieldError("note", "内部备注"+p)
	}
	switch {
	case in.PriceCents < 1 || in.PriceCents > maxMonthlyCents:
		return fieldError("price_cents", "月付价格须为 0.01–100000.00 元")
	case in.DurationDays < 1 || in.DurationDays > 365:
		return fieldError("duration_days", "月付有效天数须为 1–365")
	case in.TrafficBytes < 1 || in.TrafficBytes > maxPlanBytes:
		return fieldError("traffic_bytes", "月付流量须为 1–1048576 GiB")
	case in.MaxDevices < 1 || in.MaxDevices > 100:
		return fieldError("max_devices", "设备额度须为 1–100")
	case in.Sort < 0 || in.Sort > 10000:
		return fieldError("sort", "排序须为 0–10000")
	}
	if in.YearlyDurationDays == 0 {
		in.YearlyDurationDays = 365
	}
	if in.YearlyDurationDays < 1 || in.YearlyDurationDays > 365 {
		return fieldError("yearly_duration_days", "年付有效天数须为 1–365")
	}
	if in.YearlyTrafficBytes != nil && (*in.YearlyTrafficBytes < 1 || *in.YearlyTrafficBytes > maxPlanBytes) {
		return fieldError("yearly_traffic_bytes", "年付流量须为 1–1048576 GiB")
	}
	if in.YearlyDiscountPercent != nil && (*in.YearlyDiscountPercent < minDiscount || *in.YearlyDiscountPercent > maxDiscount) {
		return fieldError("yearly_discount_percent", "年付折扣须为 50–100 的整数百分比（90 表示 9 折）")
	}
	if in.YearlyEnabled && in.YearlyTrafficBytes == nil && in.TrafficBytes*12 > maxPlanBytes {
		return fieldError("yearly_traffic_bytes", "月流量的 12 倍超出上限，请填写年付流量或关闭年付")
	}
	return nil
}

// planStats are the per-plan counts of the list, each from one grouped query.
type planStats struct {
	subs, pending, coupons map[string]int64
	groups                 map[string][]string
}

func (s *Server) loadPlanStats(db *gorm.DB, now time.Time) (planStats, error) {
	st := planStats{subs: map[string]int64{}, pending: map[string]int64{}, coupons: map[string]int64{}, groups: map[string][]string{}}
	var rows []struct {
		PlanID string
		N      int64
	}
	if err := db.Model(&model.Subscription{}).Select("plan_id, count(*) AS n").Where("expires_at > ?", now).Group("plan_id").Scan(&rows).Error; err != nil {
		return st, err
	}
	for _, r := range rows {
		st.subs[r.PlanID] = r.N
	}
	rows = nil
	if err := livePending(db.Model(&model.Order{}), now).Select("plan_id, count(*) AS n").Group("plan_id").Scan(&rows).Error; err != nil {
		return st, err
	}
	for _, r := range rows {
		st.pending[r.PlanID] = r.N
	}
	var coupons []model.Coupon
	if err := db.Select("id", "plan_ids").Where("deleted_at IS NULL").Find(&coupons).Error; err != nil {
		return st, err
	}
	for _, c := range coupons {
		for _, id := range couponPlans(c) {
			st.coupons[id]++
		}
	}
	var groups []model.NodeGroup
	if err := db.Select("id", "name", "plan_ids", "sort").Order("sort asc, name asc, id asc").Find(&groups).Error; err != nil {
		return st, err
	}
	for _, g := range groups {
		var ids []string
		if json.Unmarshal([]byte(g.PlanIDs), &ids) != nil {
			continue
		}
		for _, id := range ids {
			st.groups[id] = append(st.groups[id], g.Name)
		}
	}
	return st, nil
}

// adminPlanView is the full management view of a plan, with the values the shop derives
// from it. The yearly tier is nil when yearly is off or its terms are unusable.
type adminPlanView struct {
	ID                    string      `json:"id"`
	Name                  string      `json:"name"`
	Description           string      `json:"description"`
	Note                  string      `json:"note"`
	PriceCents            int64       `json:"price_cents"`
	DurationDays          int         `json:"duration_days"`
	TrafficBytes          int64       `json:"traffic_bytes"`
	MaxDevices            int         `json:"max_devices"`
	EffectiveDevices      int         `json:"effective_devices"`
	DevicesOverCap        bool        `json:"devices_over_cap"`
	AllowDedicated        bool        `json:"allow_dedicated"`
	Active                bool        `json:"active"`
	Featured              bool        `json:"featured"`
	Sort                  int         `json:"sort"`
	YearlyEnabled         bool        `json:"yearly_enabled"`
	YearlyDurationDays    int         `json:"yearly_duration_days"`
	YearlyTrafficBytes    *int64      `json:"yearly_traffic_bytes"`
	YearlyDiscountPercent *int        `json:"yearly_discount_percent"`
	DiscountSource        string      `json:"yearly_discount_source"`
	Yearly                *publicTier `json:"yearly"`
	Version               int64       `json:"version"`
	ActiveSubscriptions   int64       `json:"active_subscriptions"`
	PendingOrders         int64       `json:"pending_orders"`
	CouponCount           int64       `json:"coupon_count"`
	Groups                []string    `json:"groups"`
	GroupCount            int         `json:"group_count"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
	DeletedAt             *time.Time  `json:"deleted_at"`
}

func (s *Server) adminPlanOf(plan model.Plan, global int, st planStats) adminPlanView {
	effective := s.deviceCap(plan.MaxDevices)
	v := adminPlanView{ID: plan.ID, Name: plan.Name, Description: plan.Description, Note: plan.Note, PriceCents: plan.PriceCents, DurationDays: plan.DurationDays, TrafficBytes: plan.TrafficBytes,
		MaxDevices: plan.MaxDevices, EffectiveDevices: effective, DevicesOverCap: plan.MaxDevices > effective, AllowDedicated: plan.AllowDedicated, Active: plan.Active, Featured: plan.Featured, Sort: plan.Sort,
		YearlyEnabled: plan.YearlyEnabled, YearlyDurationDays: plan.YearlyDurationDays, YearlyTrafficBytes: plan.YearlyTrafficBytes, YearlyDiscountPercent: plan.YearlyDiscountPercent,
		DiscountSource: "global", Version: plan.Version, ActiveSubscriptions: st.subs[plan.ID], PendingOrders: st.pending[plan.ID], CouponCount: st.coupons[plan.ID],
		Groups: []string{}, CreatedAt: plan.CreatedAt, UpdatedAt: plan.UpdatedAt, DeletedAt: plan.DeletedAt}
	if plan.YearlyDiscountPercent != nil {
		v.DiscountSource = "plan"
	}
	if terms, err := termsFor(plan, model.CycleYearly, global); err == nil {
		v.Yearly = tierOf(terms)
	}
	names := st.groups[plan.ID]
	v.GroupCount = len(names)
	v.Groups = append(v.Groups, names[:min(len(names), 5)]...)
	return v
}

type shopSettingsView struct {
	YearlyDiscountPercent int        `json:"yearly_discount_percent"`
	Version               int64      `json:"version"`
	UpdatedBy             *string    `json:"updated_by"`
	UpdatedAt             *time.Time `json:"updated_at"`
	// RowMissing: the settings row was deleted; the default applies until it is saved.
	RowMissing bool `json:"row_missing"`
}

func readShopSettings(db *gorm.DB) (shopSettingsView, error) {
	var row model.ShopSettings
	err := db.First(&row, "id = 1").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shopSettingsView{YearlyDiscountPercent: model.DefaultYearlyDiscountPercent, RowMissing: true}, nil
	}
	if err != nil {
		return shopSettingsView{}, err
	}
	return shopSettingsView{YearlyDiscountPercent: row.YearlyDiscountPercent, Version: row.Version, UpdatedBy: row.UpdatedBy, UpdatedAt: &row.UpdatedAt}, nil
}

func (s *Server) consolePlanList(c *gin.Context) {
	now := s.now()
	db := s.database(c)
	query := db.Order("sort asc, created_at asc, id asc")
	if c.Query("include_deleted") != "true" {
		query = query.Where("deleted_at IS NULL")
	}
	var rows []model.Plan
	if query.Limit(200).Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "套餐暂时不可用")
		return
	}
	settings, err := readShopSettings(db)
	if err != nil {
		fail(c, 503, "database_unavailable", "套餐暂时不可用")
		return
	}
	st, err := s.loadPlanStats(db, now)
	if err != nil {
		fail(c, 503, "database_unavailable", "套餐暂时不可用")
		return
	}
	views := make([]adminPlanView, 0, len(rows))
	followers := 0
	for _, plan := range rows {
		views = append(views, s.adminPlanOf(plan, settings.YearlyDiscountPercent, st))
		if plan.DeletedAt == nil && plan.Active && plan.YearlyEnabled && plan.YearlyDiscountPercent == nil {
			followers++
		}
	}
	c.JSON(200, gin.H{"plans": views, "settings": settings, "client_max_online_devices": s.cfg.MaxOnlineDevices(), "following_global_plans": followers})
}

// planDiff lists the changed terms as {from, to} pairs for the audit trail and the
// no-change check. Text fields that can be long are reported as "changed" only.
func planDiff(before, after model.Plan) gin.H {
	d := gin.H{}
	pair := func(key string, a, b any) {
		if a != b {
			d[key] = gin.H{"from": a, "to": b}
		}
	}
	opt := func(p *int64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	optInt := func(p *int) any {
		if p == nil {
			return nil
		}
		return *p
	}
	pair("name", before.Name, after.Name)
	pair("price_cents", before.PriceCents, after.PriceCents)
	pair("duration_days", before.DurationDays, after.DurationDays)
	pair("traffic_bytes", before.TrafficBytes, after.TrafficBytes)
	pair("max_devices", before.MaxDevices, after.MaxDevices)
	pair("allow_dedicated", before.AllowDedicated, after.AllowDedicated)
	pair("active", before.Active, after.Active)
	pair("featured", before.Featured, after.Featured)
	pair("sort", before.Sort, after.Sort)
	pair("yearly_enabled", before.YearlyEnabled, after.YearlyEnabled)
	pair("yearly_duration_days", before.YearlyDurationDays, after.YearlyDurationDays)
	pair("yearly_traffic_bytes", opt(before.YearlyTrafficBytes), opt(after.YearlyTrafficBytes))
	pair("yearly_discount_percent", optInt(before.YearlyDiscountPercent), optInt(after.YearlyDiscountPercent))
	if before.Description != after.Description {
		d["description"] = "changed"
	}
	if before.Note != after.Note {
		d["note"] = "changed"
	}
	return d
}

func lockPlanWrites(tx *gorm.DB) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", planWriteLock).Error
}

func (s *Server) consolePlanCreate(c *gin.Context) {
	var in planInput
	if !decode(c, &in) {
		return
	}
	id := strings.TrimSpace(in.ID)
	if id != "" && !planIDPattern.MatchString(id) {
		planFailure(c, fieldError("id", "套餐 ID 须以小写字母或数字开头，只含小写字母、数字和连字符，长度 2–32"))
		return
	}
	now, actor := s.now(), currentUser(c).ID
	var saved model.Plan
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		if err := validatePlanInput(&in); err != nil {
			return err
		}
		if err := lockPlanWrites(tx); err != nil {
			return err
		}
		if id == "" {
			for range 5 {
				candidate, err := generatePlanID()
				if err != nil {
					return err
				}
				var taken int64
				if err = tx.Model(&model.Plan{}).Where("id = ?", candidate).Count(&taken).Error; err != nil {
					return err
				}
				if taken == 0 {
					id = candidate
					break
				}
			}
			if id == "" {
				return errors.New("plan id space exhausted")
			}
		} else {
			var taken int64
			if err := tx.Model(&model.Plan{}).Where("id = ?", id).Count(&taken).Error; err != nil {
				return err
			}
			if taken > 0 {
				return &apiError{409, "plan_exists", "套餐 ID 已存在（包括已删除的套餐）"}
			}
		}
		var live, active int64
		if err := tx.Model(&model.Plan{}).Where("deleted_at IS NULL").Count(&live).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Plan{}).Where("deleted_at IS NULL AND active = ?", true).Count(&active).Error; err != nil {
			return err
		}
		if live >= maxLivePlans {
			return &apiError{409, "plan_limit", fmt.Sprintf("套餐最多 %d 个，请先删除不用的套餐", maxLivePlans)}
		}
		if in.Active && active >= maxActivePlans {
			return &apiError{409, "plan_limit", fmt.Sprintf("上架的套餐最多 %d 个", maxActivePlans)}
		}
		saved = model.Plan{ID: id, Name: in.Name, Description: in.Description, Note: in.Note, PriceCents: in.PriceCents, DurationDays: in.DurationDays, TrafficBytes: in.TrafficBytes,
			MaxDevices: in.MaxDevices, AllowDedicated: in.AllowDedicated, Active: in.Active, Sort: in.Sort, Featured: in.Featured, YearlyEnabled: in.YearlyEnabled,
			YearlyDurationDays: in.YearlyDurationDays, YearlyTrafficBytes: in.YearlyTrafficBytes, YearlyDiscountPercent: in.YearlyDiscountPercent, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Select("*").Create(&saved).Error; err != nil {
			return err
		}
		// GORM swaps a zero value for the column default on insert, which would turn an
		// explicit "no yearly" back on.
		saved.YearlyEnabled = in.YearlyEnabled
		if !saved.YearlyEnabled {
			if err := tx.Model(&model.Plan{}).Where("id = ?", saved.ID).UpdateColumn("yearly_enabled", false).Error; err != nil {
				return err
			}
		}
		return consoleAudit(tx, actor, AuditPlanCreate, auditTarget("plan", saved.ID), saved.Version,
			gin.H{"plan_id": saved.ID, "name": saved.Name, "price_cents": saved.PriceCents, "duration_days": saved.DurationDays, "traffic_bytes": saved.TrafficBytes, "max_devices": saved.MaxDevices,
				"allow_dedicated": saved.AllowDedicated, "active": saved.Active, "yearly_enabled": saved.YearlyEnabled, "note": noteMark(saved.Note)}, now)
	})
	if planFailure(c, err) {
		return
	}
	s.respondPlan(c, 201, saved)
}

func noteMark(note string) string {
	if note == "" {
		return "empty"
	}
	return "set"
}

func (s *Server) respondPlan(c *gin.Context, status int, plan model.Plan) {
	db := s.database(c)
	settings, err := readShopSettings(db)
	if err == nil {
		var st planStats
		if st, err = s.loadPlanStats(db, s.now()); err == nil {
			c.JSON(status, gin.H{"plan": s.adminPlanOf(plan, settings.YearlyDiscountPercent, st)})
			return
		}
	}
	fail(c, 503, "database_unavailable", "套餐暂时不可用")
}

var errPlanNotFound = &apiError{404, "plan_not_found", "套餐不存在"}

func planParam(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !nativePlanID.MatchString(id) {
		fail(c, 404, "plan_not_found", "套餐不存在")
		return "", false
	}
	return id, true
}

// lockPlan reads a not-deleted plan under its update lock and checks the version.
func lockPlan(tx *gorm.DB, id string, version int64) (model.Plan, error) {
	var plan model.Plan
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&plan, "id = ? AND deleted_at IS NULL", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return plan, errPlanNotFound
	}
	if err != nil {
		return plan, err
	}
	if version != plan.Version {
		return plan, &apiError{409, "version_conflict", "套餐已被其他管理员修改，请刷新后重试"}
	}
	return plan, nil
}

func (s *Server) consolePlanUpdate(c *gin.Context) {
	id, ok := planParam(c)
	if !ok {
		return
	}
	var in planInput
	if !decode(c, &in) {
		return
	}
	if in.ID != "" && in.ID != id {
		planFailure(c, fieldError("id", "套餐 ID 创建后不能修改"))
		return
	}
	now, actor := s.now(), currentUser(c).ID
	var saved model.Plan
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		if err := validatePlanInput(&in); err != nil {
			return err
		}
		if err := lockPlanWrites(tx); err != nil {
			return err
		}
		before, err := lockPlan(tx, id, in.Version)
		if err != nil {
			return err
		}
		if in.Active && !before.Active {
			var active int64
			if err = tx.Model(&model.Plan{}).Where("deleted_at IS NULL AND active = ?", true).Count(&active).Error; err != nil {
				return err
			}
			if active >= maxActivePlans {
				return &apiError{409, "plan_limit", fmt.Sprintf("上架的套餐最多 %d 个", maxActivePlans)}
			}
		}
		saved = before
		saved.Name, saved.Description, saved.Note, saved.PriceCents, saved.DurationDays, saved.TrafficBytes = in.Name, in.Description, in.Note, in.PriceCents, in.DurationDays, in.TrafficBytes
		saved.MaxDevices, saved.AllowDedicated, saved.Active, saved.Featured, saved.Sort = in.MaxDevices, in.AllowDedicated, in.Active, in.Featured, in.Sort
		saved.YearlyEnabled, saved.YearlyDurationDays, saved.YearlyTrafficBytes, saved.YearlyDiscountPercent = in.YearlyEnabled, in.YearlyDurationDays, in.YearlyTrafficBytes, in.YearlyDiscountPercent
		diff := planDiff(before, saved)
		if len(diff) == 0 {
			return &apiError{400, "no_change", "没有任何改动"}
		}
		saved.Version++
		saved.UpdatedAt = now
		if err = tx.Save(&saved).Error; err != nil {
			return err
		}
		diff["plan_id"] = saved.ID
		return consoleAudit(tx, actor, AuditPlanUpdate, auditTarget("plan", saved.ID), saved.Version, diff, now)
	})
	if planFailure(c, err) {
		return
	}
	s.respondPlan(c, 200, saved)
}

type planDeleteInput struct {
	Version int64 `json:"version"`
}

// consolePlanDelete soft-deletes a plan nothing still depends on (6.4). The checks and
// the write share one transaction that holds the plan's update lock, which excludes
// the shared lock an order takes.
func (s *Server) consolePlanDelete(c *gin.Context) {
	id, ok := planParam(c)
	if !ok {
		return
	}
	var in planDeleteInput
	if !decode(c, &in) {
		return
	}
	now, actor := s.now(), currentUser(c).ID
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		if err := lockPlanWrites(tx); err != nil {
			return err
		}
		plan, err := lockPlan(tx, id, in.Version)
		if err != nil {
			return err
		}
		st, err := s.loadPlanStats(tx, now)
		if err != nil {
			return err
		}
		var reasons []string
		blockers := gin.H{}
		if plan.Active {
			reasons = append(reasons, "套餐尚未下架")
			blockers["active"] = true
		}
		if n := st.subs[id]; n > 0 {
			reasons = append(reasons, fmt.Sprintf("有效订阅 %d 个", n))
			blockers["active_subscriptions"] = n
		}
		if n := st.pending[id]; n > 0 {
			reasons = append(reasons, fmt.Sprintf("待付订单 %d 笔", n))
			blockers["pending_orders"] = n
		}
		if names := st.groups[id]; len(names) > 0 {
			reasons = append(reasons, "线路组 "+strings.Join(names, "、"))
			blockers["groups"] = names
		}
		if len(reasons) > 0 {
			blockers["reasons"] = reasons
			return &detailedError{apiError{409, "plan_in_use", "无法删除：" + strings.Join(reasons, "；")}, blockers}
		}
		if err = tx.Model(&model.Plan{}).Where("id = ?", id).Updates(map[string]any{"deleted_at": now, "active": false, "version": plan.Version + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		return consoleAudit(tx, actor, AuditPlanDelete, auditTarget("plan", id), plan.Version+1, gin.H{"plan_id": id, "name": plan.Name, "mode": "soft"}, now)
	})
	if planFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"deleted": "soft"})
}

func (s *Server) consoleShopSettings(c *gin.Context) {
	settings, err := readShopSettings(s.database(c))
	if err != nil {
		fail(c, 503, "database_unavailable", "套餐设置暂时不可用")
		return
	}
	c.JSON(200, gin.H{"settings": settings})
}

type shopSettingsInput struct {
	YearlyDiscountPercent int   `json:"yearly_discount_percent"`
	Version               int64 `json:"version"`
}

func (s *Server) consoleShopSettingsSave(c *gin.Context) {
	var in shopSettingsInput
	if !decode(c, &in) {
		return
	}
	if in.YearlyDiscountPercent < minDiscount || in.YearlyDiscountPercent > maxDiscount {
		planFailure(c, fieldError("yearly_discount_percent", "年付折扣须为 50–100 的整数百分比（90 表示 9 折）"))
		return
	}
	now, actor := s.now(), currentUser(c).ID
	var row model.ShopSettings
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = 1").Error
		from := model.DefaultYearlyDiscountPercent
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// The row was removed by hand: recreate it rather than refuse.
			row = model.ShopSettings{ID: 1, YearlyDiscountPercent: in.YearlyDiscountPercent, Version: 1, UpdatedBy: &actor, UpdatedAt: now}
			if err = tx.Select("*").Create(&row).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if in.Version != row.Version {
				return &apiError{409, "version_conflict", "设置已被其他管理员修改，请刷新后重试"}
			}
			if in.YearlyDiscountPercent == row.YearlyDiscountPercent {
				return &apiError{400, "no_change", "没有任何改动"}
			}
			from = row.YearlyDiscountPercent
			row.YearlyDiscountPercent, row.Version, row.UpdatedBy, row.UpdatedAt = in.YearlyDiscountPercent, row.Version+1, &actor, now
			if err = tx.Save(&row).Error; err != nil {
				return err
			}
		}
		return consoleAudit(tx, actor, AuditShopSettings, auditTarget("shop", "settings"), row.Version, gin.H{"yearly_discount_percent": gin.H{"from": from, "to": row.YearlyDiscountPercent}}, now)
	})
	if planFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"settings": shopSettingsView{YearlyDiscountPercent: row.YearlyDiscountPercent, Version: row.Version, UpdatedBy: row.UpdatedBy, UpdatedAt: &row.UpdatedAt}})
}
