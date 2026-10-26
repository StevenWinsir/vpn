package api

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/model"
)

// User management of the console (handoff_payment_account.md 9.2).

// consoleSub is an administrator's view of a subscription. Version is the token the
// edit forms send back.
type consoleSub struct {
	PlanID            string    `json:"plan_id"`
	PlanName          string    `json:"plan_name"`
	StartsAt          time.Time `json:"starts_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	TrafficLimitBytes int64     `json:"traffic_limit_bytes"`
	UsedUnits         int64     `json:"used_units"`
	UploadBytes       int64     `json:"upload_bytes"`
	DownloadBytes     int64     `json:"download_bytes"`
	MaxDevices        int       `json:"max_devices"`
	AllowDedicated    bool      `json:"allow_dedicated"`
	IsTest            bool      `json:"is_test"`
	// State is active or expired.
	State   string `json:"state"`
	Version int64  `json:"version"`
}

func subView(sub *model.Subscription, now time.Time) *consoleSub {
	if sub == nil {
		return nil
	}
	state := "expired"
	if now.Before(sub.ExpiresAt) {
		state = "active"
	}
	return &consoleSub{PlanID: sub.PlanID, PlanName: sub.PlanName, StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt, TrafficLimitBytes: sub.TrafficLimitBytes, UsedUnits: sub.UsedUnits,
		UploadBytes: sub.UploadBytes, DownloadBytes: sub.DownloadBytes, MaxDevices: sub.MaxDevices, AllowDedicated: sub.AllowDedicated, IsTest: sub.IsTest, State: state, Version: sub.Version}
}

type consoleUser struct {
	ID                  string      `json:"id"`
	Email               string      `json:"email"`
	Name                string      `json:"name"`
	Role                string      `json:"role"`
	Status              string      `json:"status"`
	CreatedAt           time.Time   `json:"created_at"`
	RegisterIP          *string     `json:"register_ip"`
	LastLoginAt         *time.Time  `json:"last_login_at"`
	LastLoginIP         *string     `json:"last_login_ip"`
	LastLoginGeo        string      `json:"last_login_geo"`
	DeletionRequestedAt *time.Time  `json:"deletion_requested_at,omitempty"`
	Subscription        *consoleSub `json:"subscription"`
	OnlineDevices       int         `json:"online_devices"`
}

func userView(u model.User) consoleUser {
	return consoleUser{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Status: u.Status, CreatedAt: u.CreatedAt, RegisterIP: u.RegisterIP, LastLoginAt: u.LastLoginAt,
		LastLoginIP: u.LastLoginIP, DeletionRequestedAt: u.DeletionRequestedAt}
}

// userListRow is one row of the joined list query.
type userListRow struct {
	model.User
	SubID        *string
	SubPlanID    *string
	SubPlanName  *string
	SubStartsAt  *time.Time
	SubExpiresAt *time.Time
	SubLimit     *int64
	SubUsed      *int64
	SubUpload    *int64
	SubDownload  *int64
	SubDevices   *int
	SubDedicated *bool
	SubIsTest    *bool
	SubVersion   *int64
}

func (r userListRow) subscription(now time.Time) *consoleSub {
	if r.SubID == nil {
		return nil
	}
	sub := model.Subscription{ID: *r.SubID, PlanID: *r.SubPlanID, PlanName: *r.SubPlanName, StartsAt: *r.SubStartsAt, ExpiresAt: *r.SubExpiresAt, TrafficLimitBytes: *r.SubLimit,
		UsedUnits: *r.SubUsed, UploadBytes: *r.SubUpload, DownloadBytes: *r.SubDownload, MaxDevices: *r.SubDevices, AllowDedicated: *r.SubDedicated, IsTest: *r.SubIsTest, Version: *r.SubVersion}
	return subView(&sub, now)
}

// lastLoginGeo finds the location of each address in the latest resolved sign-ins.
func (s *Server) lastLoginGeo(db *gorm.DB, ips []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ips) == 0 {
		return out, nil
	}
	var rows []struct {
		IP      string
		GeoText string
	}
	err := db.Raw(`SELECT DISTINCT ON (ip) ip, geo_text FROM `+s.cfg.Schema+`.login_events WHERE ip IN ? AND geo_status = ? AND geo_text <> '' ORDER BY ip, occurred_at DESC`, ips, model.GeoDone).Scan(&rows).Error
	for _, r := range rows {
		out[r.IP] = r.GeoText
	}
	return out, err
}

func (s *Server) onlineCounts(db *gorm.DB, ids []string, now time.Time) (map[string]int, error) {
	out := map[string]int{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		UserID string
		N      int
	}
	err := s.liveLeases(db, now).Where("l.user_id IN ?", ids).Select("l.user_id AS user_id, count(*) AS n").Group("l.user_id").Scan(&rows).Error
	for _, r := range rows {
		out[r.UserID] = r.N
	}
	return out, err
}

func (s *Server) consoleUserList(c *gin.Context) {
	page, size, ok := consolePaging(c)
	if !ok {
		return
	}
	keyword, ok1 := keywordParam(c, "q")
	role, ok2 := enumParam(c, "role", "user", "admin")
	status, ok3 := enumParam(c, "status", "active", "deleting")
	sub, ok4 := enumParam(c, "sub", "active", "expired", "none", "test")
	sortKey, ok5 := enumParam(c, "sort", "created_at", "last_login_at", "expires_at")
	dir, ok6 := enumParam(c, "order", "asc", "desc")
	createdFrom, ok7 := timeParam(c, "created_from", false)
	createdTo, ok8 := timeParam(c, "created_to", true)
	loginFrom, ok9 := timeParam(c, "login_from", false)
	loginTo, ok10 := timeParam(c, "login_to", true)
	plan := c.Query("plan")
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8 && ok9 && ok10) || len(plan) > 32 {
		if ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8 && ok9 && ok10 {
			fail(c, 400, "invalid_filter", "筛选条件不正确")
		}
		return
	}
	now := s.now()
	schema := s.cfg.Schema
	base := func() *gorm.DB {
		q := s.database(c).Table(schema + ".users AS u").Joins("LEFT JOIN " + schema + ".subscriptions AS s ON s.user_id = u.id")
		if keyword != "" {
			pattern := likeContains(keyword)
			q = q.Where(`(u.email ILIKE ? ESCAPE '\' OR u.name ILIKE ? ESCAPE '\')`, pattern, pattern)
		}
		if role != "" {
			q = q.Where("u.role = ?", role)
		}
		if status != "" {
			q = q.Where("u.status = ?", status)
		}
		if plan != "" {
			q = q.Where("s.plan_id = ?", plan)
		}
		switch sub {
		case "active":
			q = q.Where("s.expires_at > ? AND NOT s.is_test", now)
		case "expired":
			q = q.Where("s.expires_at <= ?", now)
		case "none":
			q = q.Where("s.id IS NULL")
		case "test":
			q = q.Where("s.expires_at > ? AND s.is_test", now)
		}
		if createdFrom != nil {
			q = q.Where("u.created_at >= ?", *createdFrom)
		}
		if createdTo != nil {
			q = q.Where("u.created_at < ?", *createdTo)
		}
		if loginFrom != nil {
			q = q.Where("u.last_login_at >= ?", *loginFrom)
		}
		if loginTo != nil {
			q = q.Where("u.last_login_at < ?", *loginTo)
		}
		return q
	}
	var total int64
	if base().Count(&total).Error != nil {
		fail(c, 503, "console_unavailable", "用户列表暂时不可用")
		return
	}
	column := map[string]string{"": "u.created_at", "created_at": "u.created_at", "last_login_at": "u.last_login_at", "expires_at": "s.expires_at"}[sortKey]
	if dir == "" {
		dir = "desc"
	}
	var rows []userListRow
	err := base().Select(`u.*, s.id AS sub_id, s.plan_id AS sub_plan_id, s.plan_name AS sub_plan_name, s.starts_at AS sub_starts_at, s.expires_at AS sub_expires_at,
		s.traffic_limit_bytes AS sub_limit, s.used_units AS sub_used, s.upload_bytes AS sub_upload, s.download_bytes AS sub_download, s.max_devices AS sub_devices,
		s.allow_dedicated AS sub_dedicated, s.is_test AS sub_is_test, s.version AS sub_version`).
		Order(column + " " + dir + " NULLS LAST, u.id").Limit(size).Offset((page - 1) * size).Scan(&rows).Error
	if err != nil {
		fail(c, 503, "console_unavailable", "用户列表暂时不可用")
		return
	}
	ids, ips := make([]string, 0, len(rows)), []string{}
	for _, r := range rows {
		ids = append(ids, r.ID)
		if r.LastLoginIP != nil {
			ips = append(ips, *r.LastLoginIP)
		}
	}
	online, err1 := s.onlineCounts(s.database(c), ids, now)
	geo, err2 := s.lastLoginGeo(s.database(c), ips)
	if err1 != nil || err2 != nil {
		fail(c, 503, "console_unavailable", "用户列表暂时不可用")
		return
	}
	users := make([]consoleUser, 0, len(rows))
	for _, r := range rows {
		v := userView(r.User)
		v.Subscription = r.subscription(now)
		v.OnlineDevices = online[r.ID]
		if r.LastLoginIP != nil {
			v.LastLoginGeo = geo[*r.LastLoginIP]
		}
		users = append(users, v)
	}
	c.JSON(200, gin.H{"users": users, "total": total, "page": page, "page_size": size})
}

func (s *Server) consoleUserDetail(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	now := s.now()
	db := s.database(c)
	var user model.User
	if err := db.First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "user_not_found", "用户不存在")
			return
		}
		fail(c, 503, "console_unavailable", "用户详情暂时不可用")
		return
	}
	sub, err := readSubscription(db, id)
	var orders []consoleOrder
	var logins []model.LoginEvent
	var online []onlineDevice
	var idle []signedInDevice
	geo := map[string]string{}
	if err == nil {
		err = s.consoleOrderQuery(db).Where("o.user_id = ?", id).Order("o.created_at DESC, o.id").Limit(10).Scan(&orders).Error
	}
	if err == nil {
		err = db.Where("user_id = ?", id).Order("occurred_at DESC, id").Limit(10).Find(&logins).Error
	}
	if err == nil {
		online, err = s.onlineDevices(db, id, now)
	}
	if err == nil {
		idle, err = s.idleDevices(db, id, now)
	}
	if err == nil && user.LastLoginIP != nil {
		geo, err = s.lastLoginGeo(db, []string{*user.LastLoginIP})
	}
	if err != nil {
		fail(c, 503, "console_unavailable", "用户详情暂时不可用")
		return
	}
	view := userView(user)
	view.Subscription = subView(sub, now)
	if user.LastLoginIP != nil {
		view.LastLoginGeo = geo[*user.LastLoginIP]
	}
	view.OnlineDevices = len(online)
	if orders == nil {
		orders = []consoleOrder{}
	}
	events := make([]consoleLogin, 0, len(logins))
	for _, ev := range logins {
		events = append(events, loginView(ev, user.Email))
	}
	c.JSON(200, gin.H{"user": view, "orders": orders, "logins": events, "devices": gin.H{"online": online, "signed_in": idle}})
}

type consolePasswordInput struct {
	AdminPassword string `json:"admin_password"`
	NewPassword   string `json:"new_password"`
	Notify        bool   `json:"notify"`
}

// consoleUserPassword sets a new password for an ordinary user. The administrator
// proves themself first; bcrypt (the check and the new hash) runs before the
// transaction, and the locked rows are re-checked inside it.
func (s *Server) consoleUserPassword(c *gin.Context) {
	var in consolePasswordInput
	if !decode(c, &in) {
		return
	}
	targetID := c.Param("id")
	if _, err := uuid.Parse(targetID); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	admin, now := currentUser(c), s.now()
	if targetID == admin.ID {
		fail(c, 403, "self_protected", "不能对自己的账号执行此操作")
		return
	}
	// The length rule is the target's; role is checked again under the lock.
	if !passwordFits(in.NewPassword, model.User{Role: "user"}) {
		fail(c, 400, "invalid_password", passwordRule(model.User{Role: "user"}))
		return
	}
	if !s.reauth(c, admin, in.AdminPassword, now) {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.NewPassword), s.cfg.BcryptCost)
	if err != nil {
		fail(c, 500, "password_error", "无法处理密码")
		return
	}
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		locked, target, e := lockConsoleUsers(tx, admin.ID, admin.PasswordHash, targetID)
		if e != nil {
			return e
		}
		if target == nil {
			return errUserNotFound
		}
		if e = protectedTarget(locked, target); e != nil {
			return e
		}
		if target.Status != "active" {
			return &apiError{409, "user_deleting", "该账号已被删除，无法修改"}
		}
		version := target.AuthVersion + 1
		if e = tx.Model(&model.User{}).Where("id = ?", target.ID).
			Updates(map[string]any{"password_hash": string(hash), "password_changed_at": now, "auth_version": version, "updated_at": now}).Error; e != nil {
			return e
		}
		if e = authmail.RevokeAllSessions(tx, target.ID, now); e != nil {
			return e
		}
		if e = consoleAudit(tx, admin.ID, AuditUserPassword, target.ID, version, gin.H{"notify": in.Notify}, now); e != nil {
			return e
		}
		if in.Notify {
			return s.mail.EnqueuePasswordChanged(tx, target.Email)
		}
		return nil
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"password_changed": true, "message": "密码已修改，该用户所有设备上的登录已退出。"})
}

type consoleDeleteInput struct {
	AdminPassword string `json:"admin_password"`
	Email         string `json:"email"`
}

// consoleUserDelete runs phase one of the deletion for an ordinary user; the purge
// loop does the rest (account_purge.go).
func (s *Server) consoleUserDelete(c *gin.Context) {
	var in consoleDeleteInput
	if !decode(c, &in) {
		return
	}
	targetID := c.Param("id")
	if _, err := uuid.Parse(targetID); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	admin, now := currentUser(c), s.now()
	if targetID == admin.ID {
		fail(c, 403, "self_protected", "不能对自己的账号执行此操作")
		return
	}
	if !s.reauth(c, admin, in.AdminPassword, now) {
		return
	}
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		locked, target, e := lockConsoleUsers(tx, admin.ID, admin.PasswordHash, targetID)
		if e != nil {
			return e
		}
		if target == nil {
			return errUserNotFound
		}
		if e = protectedTarget(locked, target); e != nil {
			return e
		}
		if strings.ToLower(strings.TrimSpace(in.Email)) != target.Email {
			return &apiError{400, "email_mismatch", "输入的邮箱与目标账号邮箱不一致"}
		}
		if target.Status != "active" {
			return &apiError{409, "user_deleting", "该账号已被删除"}
		}
		if e = beginDeletion(tx, c, target.ID, now); e != nil {
			return e
		}
		if e = consoleAudit(tx, admin.ID, AuditUserDelete, target.ID, target.AuthVersion, gin.H{}, now); e != nil {
			return e
		}
		return s.mail.EnqueueAccountDeleted(tx, target.Email)
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"deleted": true, "message": "账号已删除，所有关联数据将在节点结清后自动清除。"})
}

type subscriptionEdit struct {
	// Version is the subscription version the form was loaded with (0: no subscription).
	Version           int64      `json:"version"`
	PlanID            string     `json:"plan_id"`
	StartsAt          *time.Time `json:"starts_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	TrafficLimitBytes *int64     `json:"traffic_limit_bytes"`
	// Used is keep (default), reset or set.
	Used      string `json:"used"`
	UsedUnits int64  `json:"used_units"`
}

// maxUsedUnits bounds an administrator-set usage figure well inside int64.
const maxUsedUnits = int64(1) << 60

var errSubscriptionConflict = &apiError{409, "version_conflict", "该用户的套餐刚刚被其他操作修改，请刷新后重试"}

// consoleUserSubscription changes a user's subscription by hand. It is written under
// the user lock with a version check, and the current period's ledger row follows in
// the same transaction so a node's budget sees the new figures.
func (s *Server) consoleUserSubscription(c *gin.Context) {
	var in subscriptionEdit
	if !decode(c, &in) {
		return
	}
	targetID := c.Param("id")
	if _, err := uuid.Parse(targetID); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	if in.Used == "" {
		in.Used = "keep"
	}
	if (in.Used != "keep" && in.Used != "reset" && in.Used != "set") || in.UsedUnits < 0 || in.UsedUnits > maxUsedUnits || len(in.PlanID) > 32 {
		fail(c, 400, "invalid_subscription", "套餐参数不正确")
		return
	}
	admin, now := currentUser(c), s.now()
	var out *model.Subscription
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		_, target, e := lockConsoleUsers(tx, admin.ID, "", targetID)
		if e != nil {
			return e
		}
		if target == nil {
			return errUserNotFound
		}
		if target.Status != "active" {
			return &apiError{409, "user_deleting", "该账号已被删除，无法修改"}
		}
		current, e := readSubscription(tx, target.ID)
		if e != nil {
			return e
		}
		have := int64(0)
		if current != nil {
			have = current.Version
		}
		if in.Version != have {
			return errSubscriptionConflict
		}
		var next model.Subscription
		detail := gin.H{}
		if current != nil {
			next = *current
			detail["plan_from"], detail["expires_from"], detail["limit_from"], detail["used_from"] = current.PlanID, current.ExpiresAt, current.TrafficLimitBytes, current.UsedUnits
		} else {
			if in.PlanID == "" || in.ExpiresAt == nil {
				return &apiError{400, "invalid_subscription", "新开套餐需要选择套餐并填写到期时间"}
			}
			next = model.Subscription{ID: uuid.NewString(), UserID: target.ID, StartsAt: now}
		}
		if in.PlanID != "" && (current == nil || in.PlanID != current.PlanID) {
			var plan model.Plan
			if e = tx.Where("id = ? AND deleted_at IS NULL", in.PlanID).First(&plan).Error; errors.Is(e, gorm.ErrRecordNotFound) {
				return &apiError{400, "plan_not_found", "套餐不存在"}
			} else if e != nil {
				return e
			}
			next.PlanID, next.PlanName, next.MaxDevices, next.AllowDedicated, next.IsTest = plan.ID, plan.Name, plan.MaxDevices, plan.AllowDedicated, false
			if current == nil {
				next.TrafficLimitBytes = plan.TrafficBytes
			}
		}
		if in.StartsAt != nil {
			next.StartsAt = in.StartsAt.UTC()
		}
		if in.ExpiresAt != nil {
			next.ExpiresAt = in.ExpiresAt.UTC()
		}
		if in.TrafficLimitBytes != nil {
			next.TrafficLimitBytes = *in.TrafficLimitBytes
		}
		switch in.Used {
		case "reset":
			next.UsedUnits, next.UploadBytes, next.DownloadBytes = 0, 0, 0
		case "set":
			next.UsedUnits = in.UsedUnits
		}
		if !next.ExpiresAt.After(now) {
			return &apiError{400, "invalid_expiry", "到期时间必须晚于现在；如需立即结束，请使用“结束订阅”"}
		}
		if !next.ExpiresAt.After(next.StartsAt) || next.ExpiresAt.After(now.AddDate(10, 0, 0)) {
			return &apiError{400, "invalid_expiry", "到期时间需晚于开始时间，且不超过 10 年"}
		}
		if next.TrafficLimitBytes < 1 || next.TrafficLimitBytes > maxPlanBytes {
			return &apiError{400, "invalid_traffic", "总流量超出允许范围"}
		}
		if current != nil && current.PlanID == next.PlanID && current.StartsAt.Equal(next.StartsAt) && current.ExpiresAt.Equal(next.ExpiresAt) &&
			current.TrafficLimitBytes == next.TrafficLimitBytes && current.UsedUnits == next.UsedUnits && current.UploadBytes == next.UploadBytes {
			return &apiError{400, "no_change", "没有需要保存的修改"}
		}
		next.Version++
		next.UpdatedAt = now
		saved, e := saveSubscription(tx, &next)
		if e != nil {
			return e
		}
		out = saved
		detail["plan_to"], detail["expires_to"], detail["limit_to"], detail["used_to"], detail["used_mode"] = next.PlanID, next.ExpiresAt, next.TrafficLimitBytes, next.UsedUnits, in.Used
		return consoleAudit(tx, admin.ID, AuditUserSubscription, target.ID, next.Version, detail, now)
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"subscription": subView(out, now), "message": "套餐已保存。更换套餐或开新周期会使该用户的客户端重新授权。"})
}

type subscriptionEndInput struct {
	Version int64 `json:"version"`
}

// consoleUserSubscriptionEnd ends an active subscription now.
func (s *Server) consoleUserSubscriptionEnd(c *gin.Context) {
	var in subscriptionEndInput
	if !decode(c, &in) {
		return
	}
	targetID := c.Param("id")
	if _, err := uuid.Parse(targetID); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	admin, now := currentUser(c), s.now()
	var out *model.Subscription
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		_, target, e := lockConsoleUsers(tx, admin.ID, "", targetID)
		if e != nil {
			return e
		}
		if target == nil {
			return errUserNotFound
		}
		current, e := readSubscription(tx, target.ID)
		if e != nil {
			return e
		}
		if current == nil || !now.Before(current.ExpiresAt) {
			return &apiError{409, "subscription_not_active", "该用户当前没有有效的订阅"}
		}
		if in.Version != current.Version {
			return errSubscriptionConflict
		}
		from := current.ExpiresAt
		current.ExpiresAt, current.Version, current.UpdatedAt = now, current.Version+1, now
		saved, e := saveSubscription(tx, current)
		if e != nil {
			return e
		}
		out = saved
		return consoleAudit(tx, admin.ID, AuditUserSubscriptionEnd, target.ID, current.Version, gin.H{"plan": current.PlanID, "expires_from": from}, now)
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"subscription": subView(out, now), "message": "订阅已结束。"})
}
