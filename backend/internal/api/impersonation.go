package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Impersonation (handoff_payment_account.md 9.7): an administrator looks at the site as
// an ordinary user. The session is read-only, ends at a fixed time, writes no sign-in
// record and shows no announcement. The way back is a separate HttpOnly cookie that
// holds the administrator's own (rotated) refresh token and is sent to one path only.

const (
	AuditImpersonateStart = "admin.impersonate_start"
	AuditImpersonateEnd   = "admin.impersonate_end"

	impersonationKey  = "impersonation"
	returnCookieName  = "vpn_return"
	returnCookiePath  = "/api/v1/auth/impersonation/return"
	errImpersonation  = "impersonation_read_only"
	msgImpersonation  = "模拟登录期间只能查看，不能执行此操作"
	errReturnFailed   = "impersonation_return_failed"
	msgReturnFailed   = "无法返回管理员，请重新登录"
	returnCookieBytes = 128
)

// impersonation is what requireAuth learned about an impersonation session.
type impersonation struct {
	AdminID, AdminSessionID string
	ExpiresAt               time.Time
}

func impersonationOf(c *gin.Context) *impersonation {
	v, ok := c.Get(impersonationKey)
	if !ok {
		return nil
	}
	imp := v.(impersonation)
	return &imp
}

// refuseImpersonated answers 403 and reports true for an impersonation session.
func refuseImpersonated(c *gin.Context) bool {
	if impersonationOf(c) == nil {
		return false
	}
	fail(c, 403, errImpersonation, msgImpersonation)
	return true
}

// readOnlyWhenImpersonating turns every state-changing request of an impersonation
// session into 403, except the way back. The decision belongs to the server: the
// browser's banner is only a courtesy.
func (s *Server) readOnlyWhenImpersonating() gin.HandlerFunc {
	return func(c *gin.Context) {
		if impersonationOf(c) != nil {
			switch c.Request.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
			default:
				if c.FullPath() != returnCookiePath {
					fail(c, 403, errImpersonation, msgImpersonation)
					return
				}
			}
		}
		c.Next()
	}
}

func (s *Server) me(c *gin.Context) {
	out := gin.H{"user": currentUser(c)}
	if imp := impersonationOf(c); imp != nil {
		var admin model.User
		if err := s.database(c).Select("email").First(&admin, "id = ?", imp.AdminID).Error; err == nil {
			out["impersonation"] = gin.H{"admin_email": admin.Email, "expires_at": imp.ExpiresAt}
		}
	}
	c.JSON(200, out)
}

func (s *Server) setReturnCookie(c *gin.Context, raw string, expires time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{Name: returnCookieName, Value: raw, Path: returnCookiePath, HttpOnly: true, Secure: s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: max(1, int(time.Until(expires).Seconds()))})
}

func (s *Server) clearReturnCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: returnCookieName, Value: "", Path: returnCookiePath, MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
}

// endSession revokes the session behind a refresh token. Signing out of an
// impersonation also ends the administrator's own session that opened it: nothing is
// left behind that only the discarded return cookie could reach.
func (s *Server) endSession(c *gin.Context, hash string, now time.Time) error {
	return s.database(c).Transaction(func(tx *gorm.DB) error {
		var sess model.Session
		err := tx.Select("id", "impersonator_session_id").Where("refresh_hash = ? AND revoked_at IS NULL", hash).Take(&sess).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		ids := []string{sess.ID}
		if sess.ImpersonatorSessionID != nil {
			ids = append(ids, *sess.ImpersonatorSessionID)
		}
		return tx.Model(&model.Session{}).Where("id IN ? AND revoked_at IS NULL", ids).Update("revoked_at", now).Error
	})
}

type impersonateInput struct {
	AdminPassword string `json:"admin_password"`
}

// consoleImpersonate opens a read-only session as an ordinary, active user. The
// administrator's refresh token is rotated and handed back only through the return
// cookie; the browser then carries the user's session.
func (s *Server) consoleImpersonate(c *gin.Context) {
	var in impersonateInput
	if !decode(c, &in) {
		return
	}
	targetID := c.Param("id")
	if _, err := uuid.Parse(targetID); err != nil {
		fail(c, 404, "user_not_found", "用户不存在")
		return
	}
	admin, now := currentUser(c), s.now()
	adminSID := c.GetString("session_id")
	if targetID == admin.ID {
		fail(c, 403, "self_protected", "不能对自己的账号执行此操作")
		return
	}
	if !s.reauth(c, admin, in.AdminPassword, now) {
		return
	}
	adminNext, err1 := randomToken()
	userRaw, err2 := randomToken()
	if err1 != nil || err2 != nil {
		fail(c, 500, "session_failed", "无法创建会话")
		return
	}
	hard := now.Add(s.cfg.ImpersonationWindow())
	imp := model.Session{ID: uuid.NewString(), RefreshHash: refreshHash(userRaw), ExpiresAt: hard, ImpersonatorID: &admin.ID, ImpersonatorSessionID: &adminSID, HardExpiresAt: &hard}
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
		if target.Status != "active" {
			return &apiError{409, "user_unavailable", "该账号已停用或正在删除"}
		}
		var adminSess model.Session
		e = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?", adminSID, admin.ID, now).Take(&adminSess).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return errStaleCredentials
		}
		if e != nil {
			return e
		}
		if e = tx.Model(&adminSess).Update("refresh_hash", refreshHash(adminNext)).Error; e != nil {
			return e
		}
		imp.UserID = target.ID
		if e = tx.Omit("User").Create(&imp).Error; e != nil {
			return e
		}
		return consoleAudit(tx, admin.ID, AuditImpersonateStart, target.ID, target.AuthVersion, gin.H{"minutes": int(s.cfg.ImpersonationWindow() / time.Minute)}, now)
	})
	if consoleFailure(c, err) {
		return
	}
	if s.writeSession(c, imp, userRaw) != nil {
		s.clearCookies(c)
		fail(c, 500, "session_failed", "无法创建会话，请重新登录")
		return
	}
	s.setReturnCookie(c, adminNext, hard)
	c.JSON(200, gin.H{"impersonating": true, "expires_at": hard})
}

// impersonationReturn ends the impersonation and signs the administrator back in. Every
// check must hold: the return cookie matches the administrator's session (which is not
// revoked or expired) and the administrator is still an active administrator. If any
// fails, the impersonation session is revoked, every cookie is cleared and the caller
// must sign in again.
func (s *Server) impersonationReturn(c *gin.Context) {
	imp := impersonationOf(c)
	if imp == nil {
		fail(c, 403, "not_impersonating", "当前不是模拟登录")
		return
	}
	now, sid, target := s.now(), c.GetString("session_id"), currentUser(c)
	abandon := func() {
		_ = s.database(c).Model(&model.Session{}).Where("id = ? AND revoked_at IS NULL", sid).Update("revoked_at", now).Error
		s.clearCookies(c)
		fail(c, 401, errReturnFailed, msgReturnFailed)
	}
	raw, err := c.Cookie(returnCookieName)
	if err != nil || len(raw) > returnCookieBytes {
		abandon()
		return
	}
	next, err := randomToken()
	if err != nil {
		fail(c, 500, "session_failed", "无法创建会话")
		return
	}
	var adminSess model.Session
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		var admin model.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND role = ? AND status = ?", imp.AdminID, "admin", "active").Take(&admin).Error; e != nil {
			return e
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND refresh_hash = ? AND revoked_at IS NULL AND expires_at > ?",
			imp.AdminSessionID, imp.AdminID, refreshHash(raw), now).Take(&adminSess).Error; e != nil {
			return e
		}
		if e := tx.Model(&adminSess).Update("refresh_hash", refreshHash(next)).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.Session{}).Where("id = ? AND revoked_at IS NULL", sid).Update("revoked_at", now).Error; e != nil {
			return e
		}
		return consoleAudit(tx, admin.ID, AuditImpersonateEnd, target.ID, target.AuthVersion, gin.H{}, now)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		abandon()
		return
	}
	if err != nil {
		fail(c, 503, "auth_unavailable", "会话服务暂时不可用，请重试")
		return
	}
	if s.writeSession(c, adminSess, next) != nil {
		s.clearCookies(c)
		fail(c, 500, "session_failed", "无法恢复管理员登录，请重新登录")
		return
	}
	s.clearReturnCookie(c)
	c.JSON(200, gin.H{"returned": true})
}
