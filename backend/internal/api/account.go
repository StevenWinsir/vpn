package api

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/model"
)

// Account settings (handoff_payment_account.md 7): change the password, delete the
// account, read the recent sign-ins. Every action starts from the verified session,
// repeats the password check the sign-in uses, and re-validates under the user lock.

// Audit actions of this file.
const (
	AuditPasswordChange   = "account.password_change"
	AuditDeleteRequested  = "account.delete_requested"
	scopePasswordCheck    = "pwd_check"
	passwordCheckWindow   = 15 * time.Minute
	passwordCheckLimit    = 5
	recentLoginsLimit     = 10
	incorrectPasswordText = "当前密码不正确"
)

type changePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type deleteAccountInput struct {
	Password string `json:"password"`
	Email    string `json:"email"`
}

// passwordGate refuses the request while this account has used up its wrong-password
// allowance (5 per 15 minutes, counted in the database so it holds across instances).
func (s *Server) passwordGate(c *gin.Context, user model.User, now time.Time) bool {
	count, until, err := s.peekLimit(s.database(c), scopePasswordCheck, user.ID, passwordCheckWindow, now)
	if err != nil {
		fail(c, 503, "account_unavailable", "账号服务暂时不可用，请稍后重试")
		return false
	}
	if count >= passwordCheckLimit {
		retryAfter(c, until, now)
		fail(c, 429, "password_rate_limited", "密码错误次数过多，请稍后再试")
		return false
	}
	return true
}

// passwordMatches verifies the current password outside any lock and counts a miss.
func (s *Server) passwordMatches(c *gin.Context, user model.User, password string, now time.Time) bool {
	if len(password) <= passwordMaxBytes && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil {
		return true
	}
	_, _, _ = s.bumpLimit(s.database(c), scopePasswordCheck, user.ID, passwordCheckWindow, now)
	fail(c, 403, "incorrect_password", incorrectPasswordText)
	return false
}

func accountFailure(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var business *apiError
	switch {
	case errors.As(err, &business):
		fail(c, business.Status, business.Code, business.Message)
	case errors.Is(err, errStaleCredentials):
		fail(c, 409, "account_changed", "账号状态已变化，请刷新页面后重试")
	default:
		fail(c, 503, "account_unavailable", "账号服务暂时不可用，请稍后重试")
	}
	return true
}

// changePassword replaces the password. bcrypt (the old password's check and the new
// hash) runs before the transaction; inside it lockVerifiedUser confirms the account
// is still the one that was checked, so a login or reset that raced ahead wins and
// no old-password session survives. Every other web and client session is revoked;
// this browser gets a fresh session and stays signed in.
func (s *Server) changePassword(c *gin.Context) {
	var in changePasswordInput
	if !decode(c, &in) {
		return
	}
	if refuseImpersonated(c) {
		return
	}
	user := currentUser(c)
	if !passwordFits(in.NewPassword, user) {
		fail(c, 400, "invalid_password", passwordRule(user))
		return
	}
	now := s.now()
	if !s.passwordGate(c, user, now) || !s.passwordMatches(c, user, in.CurrentPassword, now) {
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.NewPassword)) == nil {
		fail(c, 400, "password_unchanged", "新密码不能与当前密码相同")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.NewPassword), s.cfg.BcryptCost)
	if err != nil {
		fail(c, 500, "password_error", "无法处理密码")
		return
	}
	var sess model.Session
	var raw string
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockVerifiedUser(tx, &user); e != nil {
			return e
		}
		remember := false
		var current model.Session
		if id, ok := c.Get("session_id"); ok {
			if e := tx.Select("remember").Where("id = ? AND user_id = ?", id, user.ID).Take(&current).Error; e == nil {
				remember = current.Remember
			}
		}
		version := user.AuthVersion + 1
		if e := tx.Model(&model.User{}).Where("id = ? AND auth_version = ?", user.ID, user.AuthVersion).
			Updates(map[string]any{"password_hash": string(hash), "password_changed_at": now, "auth_version": version, "updated_at": now}).Error; e != nil {
			return e
		}
		if e := authmail.RevokeAllSessions(tx, user.ID, now); e != nil {
			return e
		}
		var e error
		if sess, raw, e = s.createSession(tx, user.ID, remember); e != nil {
			return e
		}
		if e = tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: user.ID, TargetID: user.ID, Action: AuditPasswordChange, Version: version, CreatedAt: now}).Error; e != nil {
			return e
		}
		return s.mail.EnqueuePasswordChanged(tx, user.Email)
	})
	if accountFailure(c, err) {
		return
	}
	if s.writeSession(c, sess, raw) != nil {
		// The password did change and every old session is gone; signing in again works.
		s.clearCookies(c)
		fail(c, 500, "session_failed", "密码已修改，但无法保持当前登录，请使用新密码重新登录")
		return
	}
	c.JSON(200, gin.H{"password_changed": true, "message": "密码已修改，其他设备上的登录已退出。"})
}

// requestAccountDeletion is phase one of the two-phase deletion: in one transaction the
// account becomes "deleting" (sign-in refused, password reset silent), every session is
// revoked, leases end and open grants go to revoking so the nodes stop forwarding. The
// purge loop removes the rows once the grants have settled (account_purge.go).
func (s *Server) requestAccountDeletion(c *gin.Context) {
	var in deleteAccountInput
	if !decode(c, &in) {
		return
	}
	if refuseImpersonated(c) {
		return
	}
	user := currentUser(c)
	if user.Role != "user" {
		fail(c, 403, "admin_account_protected", "管理员账号不能自助删除")
		return
	}
	if strings.ToLower(strings.TrimSpace(in.Email)) != user.Email {
		fail(c, 400, "email_mismatch", "输入的邮箱与账号邮箱不一致")
		return
	}
	now := s.now()
	if !s.passwordGate(c, user, now) || !s.passwordMatches(c, user, in.Password, now) {
		return
	}
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockVerifiedUser(tx, &user); e != nil {
			return e
		}
		if e := beginDeletion(tx, c, user.ID, now); e != nil {
			return e
		}
		if e := tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: user.ID, TargetID: user.ID, Action: AuditDeleteRequested, Version: user.AuthVersion, CreatedAt: now}).Error; e != nil {
			return e
		}
		return s.mail.EnqueueAccountDeleted(tx, user.Email)
	})
	if accountFailure(c, err) {
		return
	}
	s.clearCookies(c)
	c.JSON(200, gin.H{"deleted": true, "message": "账号已删除，所有设备上的登录均已退出。"})
}

// myLogins lists the account's latest sign-in attempts, newest first.
func (s *Server) myLogins(c *gin.Context) {
	user := currentUser(c)
	var events []model.LoginEvent
	if err := s.database(c).Where("user_id = ?", user.ID).Order("occurred_at desc, id desc").Limit(recentLoginsLimit).Find(&events).Error; err != nil {
		fail(c, 503, "account_unavailable", "登录记录暂时不可用")
		return
	}
	items := make([]gin.H, 0, len(events))
	for _, ev := range events {
		items = append(items, gin.H{"occurred_at": ev.OccurredAt, "channel": ev.Channel, "platform": ev.Platform, "ip": ev.IP,
			"result": ev.Result, "geo_status": ev.GeoStatus, "geo_text": ev.GeoText})
	}
	c.JSON(200, gin.H{"logins": items})
}

// beginDeletion is phase one of a deletion, shared by the account owner and the
// administrator console. The caller holds the user's row lock and writes the audit.
// The pending-order check runs under that lock: a payment order created meanwhile
// waits for it, and then finds the account no longer active.
func beginDeletion(tx *gorm.DB, hdr interface{ Header(string, string) }, userID string, now time.Time) error {
	var live []model.Order
	if e := livePending(tx.Select("id", "expires_at"), now).Where("user_id = ?", userID).Order("expires_at desc").Find(&live).Error; e != nil {
		return e
	}
	if len(live) > 0 {
		wait := max(1, secondsUntil(*live[0].ExpiresAt, now))
		retryAfter(hdr, *live[0].ExpiresAt, now)
		return &apiError{409, "pending_orders", fmt.Sprintf("还有未完成的订单，请等待约 %d 秒，待订单失效后再删除账号", wait)}
	}
	if e := tx.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{"status": model.UserDeleting, "deletion_requested_at": now, "updated_at": now}).Error; e != nil {
		return e
	}
	if e := authmail.RevokeAllSessions(tx, userID, now); e != nil {
		return e
	}
	return tx.Model(&model.ConnectionGrant{}).Where("user_id = ? AND state IN ?", userID, []string{grantPending, grantReady}).
		Updates(map[string]any{"state": grantRevoking, "updated_at": now}).Error
}
