package api

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Shared parts of the administrator console (handoff_payment_account.md 9). Every
// route here sits behind requireConsoleAdmin; the actions that move money or end an
// account repeat the administrator's own password (9.1), counted per administrator.

// Console audit actions.
const (
	AuditUserPassword        = "admin.user_password"
	AuditUserSubscription    = "admin.user_subscription"
	AuditUserSubscriptionEnd = "admin.user_subscription_end"
	AuditUserDelete          = "admin.user_delete"
	AuditPaymentConfirm      = "payment.confirm_manual"
	AuditPaymentClose        = "payment.close"

	scopeAdminReauth  = "admin_reauth"
	adminReauthWindow = 15 * time.Minute
	adminReauthLimit  = 5

	consolePageDefault = 20
	consolePageLarge   = 50
	consoleMaxPage     = 100000
)

// consoleLocation draws the "today" and "this month" lines of the order summary.
var consoleLocation = time.FixedZone("UTC+8", 8*3600)

// consoleFailure maps a transaction error to the API answer.
func consoleFailure(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var business *apiError
	switch {
	case errors.As(err, &business):
		fail(c, business.Status, business.Code, business.Message)
	case errors.Is(err, errStaleCredentials):
		fail(c, 409, "account_changed", "管理员账号状态已变化，请刷新页面后重试")
	default:
		fail(c, 503, "console_unavailable", "管理后台暂时不可用，请稍后重试")
	}
	return true
}

// reauth demands the administrator's own password for a dangerous action. The check
// runs outside any lock; a miss is counted in the database (5 per 15 minutes per
// administrator), so it holds across instances.
func (s *Server) reauth(c *gin.Context, admin model.User, password string, now time.Time) bool {
	count, until, err := s.peekLimit(s.database(c), scopeAdminReauth, admin.ID, adminReauthWindow, now)
	if err != nil {
		fail(c, 503, "console_unavailable", "管理后台暂时不可用，请稍后重试")
		return false
	}
	if count >= adminReauthLimit {
		retryAfter(c, until, now)
		fail(c, 429, "reauth_rate_limited", "密码错误次数过多，请稍后再试")
		return false
	}
	if len(password) <= passwordMaxBytes && bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)) == nil {
		return true
	}
	_, _, _ = s.bumpLimit(s.database(c), scopeAdminReauth, admin.ID, adminReauthWindow, now)
	fail(c, 403, "incorrect_password", "管理员密码不正确")
	return false
}

// lockConsoleUsers locks the acting administrator and the target (in primary-key
// order, the console's rule for several user rows) and re-checks the administrator:
// still an active administrator, and, when verified is non-empty, still holding the
// password that was just checked. The target is nil when it does not exist.
func lockConsoleUsers(tx *gorm.DB, adminID, verified, targetID string) (*model.User, *model.User, error) {
	ids := []string{adminID}
	if targetID != adminID {
		ids = append(ids, targetID)
	}
	var rows []model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	var admin, target *model.User
	for i := range rows {
		if rows[i].ID == adminID {
			admin = &rows[i]
		}
		if rows[i].ID == targetID {
			target = &rows[i]
		}
	}
	if admin == nil || admin.Role != "admin" || admin.Status != "active" {
		return nil, nil, &apiError{403, "admin_required", "需要管理员权限"}
	}
	if verified != "" && admin.PasswordHash != verified {
		return nil, nil, errStaleCredentials
	}
	return admin, target, nil
}

// protectedTarget refuses the actions D9 withholds from other administrators and from
// the acting administrator themself.
func protectedTarget(admin, target *model.User) error {
	switch {
	case target.ID == admin.ID:
		return &apiError{403, "self_protected", "不能对自己的账号执行此操作"}
	case target.Role != "user":
		return &apiError{403, "admin_account_protected", "不能对管理员账号执行此操作"}
	}
	return nil
}

var errUserNotFound = &apiError{404, "user_not_found", "用户不存在"}

// consolePaging reads page and page_size (20 or 50).
func consolePaging(c *gin.Context) (page, size int, ok bool) {
	page, size = 1, consolePageDefault
	if raw := c.Query("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > consoleMaxPage {
			fail(c, 400, "invalid_page", "页码超出范围")
			return 0, 0, false
		}
		page = n
	}
	switch c.Query("page_size") {
	case "", "20":
	case "50":
		size = consolePageLarge
	default:
		fail(c, 400, "invalid_page_size", "每页只能是 20 或 50 条")
		return 0, 0, false
	}
	return page, size, true
}

// likeContains turns a keyword into a LIKE pattern that matches it literally: the
// wildcards % and _ and the escape character itself lose their meaning.
func likeContains(keyword string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(keyword) + "%"
}

// keywordParam reads a free-text filter, trimmed and bounded.
func keywordParam(c *gin.Context, name string) (string, bool) {
	v := strings.TrimSpace(c.Query(name))
	if len([]rune(v)) > 80 {
		fail(c, 400, "invalid_filter", "筛选关键词过长")
		return "", false
	}
	return v, true
}

// timeParam reads an optional time filter: RFC 3339, or a bare UTC date (a bare "to"
// date includes that whole day). The result is nil when the parameter is absent.
func timeParam(c *gin.Context, name string, upper bool) (*time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		t = t.UTC()
		return &t, true
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		if upper {
			t = t.AddDate(0, 0, 1)
		}
		return &t, true
	}
	fail(c, 400, "invalid_filter", "时间筛选格式不正确")
	return nil, false
}

// enumParam accepts "" or one of the allowed values.
func enumParam(c *gin.Context, name string, allowed ...string) (string, bool) {
	v := c.Query(name)
	if v == "" {
		return "", true
	}
	for _, a := range allowed {
		if v == a {
			return v, true
		}
	}
	fail(c, 400, "invalid_filter", "筛选条件不正确")
	return "", false
}

// idleDevices lists signed-in devices that hold no lease (shared with /me/devices).
func (s *Server) idleDevices(db *gorm.DB, userID string, now time.Time) ([]signedInDevice, error) {
	var idle []struct {
		Platform, AppVersion, DeviceID string
		LastSeenAt                     time.Time
	}
	err := db.Table(s.cfg.Schema+".native_sessions AS ns").
		Select("ns.platform, ns.app_version, ns.device_id::text AS device_id, ns.last_seen_at").
		Where("ns.user_id = ? AND ns.revoked_at IS NULL AND ns.expires_at > ? AND ns.last_seen_at > ?", userID, now, now.Add(-s.cfg.IdleTTL())).
		Where("NOT EXISTS (SELECT 1 FROM "+s.cfg.Schema+".connection_leases AS l WHERE l.session_id = ns.id AND l.state = ? AND l.expires_at > ?)", model.LeaseActive, now).
		Order("ns.last_seen_at DESC, ns.id").Limit(50).Scan(&idle).Error
	out := make([]signedInDevice, 0, len(idle))
	for _, r := range idle {
		out = append(out, signedInDevice{Platform: r.Platform, AppVersion: r.AppVersion, Device: shortDevice(r.DeviceID), LastSeenAt: r.LastSeenAt.UTC()})
	}
	return out, err
}
