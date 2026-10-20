package api

import (
	"errors"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/model"
)

type forgotPasswordInput struct {
	Email string `json:"email"`
}

type resetTokenInput struct {
	Token string `json:"token"`
}

type resetPasswordInput struct {
	Token                string `json:"token"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// Administrators keep the 16-byte minimum of the CLI that creates them.
const adminPasswordMinBytes = 16

// forgotAccepted is identical for every address: existing, unknown or disabled.
const forgotAccepted = "如果该邮箱已注册，重置密码的链接会在几分钟内送达，请同时检查垃圾邮件箱。"

// passwordMinFor is the byte minimum that applies to an account's new password.
func passwordMinFor(user model.User) int {
	if user.Role == "admin" {
		return adminPasswordMinBytes
	}
	return passwordMinBytes
}

// failReset maps reset-token outcomes. Expired, used, superseded, wrong-purpose and
// unknown tokens are deliberately one answer (handoff_auth.md 5).
func failReset(c *gin.Context, err error) {
	if errors.Is(err, authmail.ErrResetTokenInvalid) {
		fail(c, 400, "reset_token_invalid", "重置链接无效或已过期，请重新申请找回密码")
		return
	}
	failAuthMail(c, err)
}

// forgotPassword queues a reset link for an active account (handoff_auth.md 8.1). The
// answer, its fields and the limits applied never depend on whether the account exists.
func (s *Server) forgotPassword(c *gin.Context) {
	var in forgotPasswordInput
	if !decode(c, &in) {
		return
	}
	receipt, err := s.mail.IssueReset(c.Request.Context(), in.Email, c.ClientIP())
	if err != nil {
		failAuthMail(c, err)
		return
	}
	c.JSON(202, gin.H{
		"message":              forgotAccepted,
		"next_send_at":         receipt.NextSendAt,
		"resend_after_seconds": secondsUntil(receipt.NextSendAt, s.mail.Now().UTC()),
	})
}

// validateReset is read-only: opening the mail, scanners, prefetches and repeated
// checks never consume the token. Only a holder of a usable token learns the rules.
func (s *Server) validateReset(c *gin.Context) {
	var in resetTokenInput
	if !decode(c, &in) {
		return
	}
	ticket, err := s.mail.ResolveReset(c.Request.Context(), in.Token)
	if err != nil {
		failReset(c, err)
		return
	}
	var user model.User
	if err = s.database(c).Select("role").Where("id = ?", ticket.UserID).Take(&user).Error; err != nil {
		failReset(c, authmail.ErrResetTokenInvalid)
		return
	}
	c.JSON(200, gin.H{
		"valid":              true,
		"expires_at":         ticket.ExpiresAt,
		"expires_in_seconds": secondsUntil(ticket.ExpiresAt, s.mail.Now().UTC()),
		"password_min_bytes": passwordMinFor(user),
		"password_max_bytes": passwordMaxBytes,
	})
}

// resetPassword sets a new password from a mailed link (handoff_auth.md 8.3). Field
// errors never consume the token. On success every web and client session of the
// account is revoked, this browser's cookies are cleared, and nobody is signed in.
func (s *Server) resetPassword(c *gin.Context) {
	var in resetPasswordInput
	if !decode(c, &in) {
		return
	}
	ticket, err := s.mail.ResolveReset(c.Request.Context(), in.Token)
	if err != nil {
		failReset(c, err)
		return
	}
	var user model.User
	if err = s.database(c).Where("id = ?", ticket.UserID).Take(&user).Error; err != nil {
		failReset(c, authmail.ErrResetTokenInvalid)
		return
	}
	if in.Password != in.PasswordConfirmation {
		fail(c, 400, "password_mismatch", "两次输入的新密码不一致")
		return
	}
	if !passwordFits(in.Password, user) {
		fail(c, 400, "invalid_password", passwordRule(user))
		return
	}
	// bcrypt work happens before any row lock. Reusing the current password is refused
	// so a saved old password cannot keep signing in after the reset.
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.Password)) == nil {
		fail(c, 400, "password_unchanged", "新密码不能与当前密码相同")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cfg.BcryptCost)
	if err != nil {
		fail(c, 500, "password_error", "无法处理密码")
		return
	}
	_, err = s.mail.CompleteReset(c.Request.Context(), in.Token, ticket, string(hash), func(locked model.User) error {
		// The role may have changed since the rule above was checked.
		user = locked
		if !passwordFits(in.Password, locked) {
			return errPasswordRule
		}
		return nil
	})
	if errors.Is(err, errPasswordRule) {
		fail(c, 400, "invalid_password", passwordRule(user))
		return
	}
	if err != nil {
		failReset(c, err)
		return
	}
	s.clearCookies(c)
	c.JSON(200, gin.H{"password_reset": true, "message": "密码已重置，所有设备上的原有登录均已退出，请使用新密码登录。"})
}

var errPasswordRule = errors.New("password does not meet the account's rule")

func passwordFits(password string, user model.User) bool {
	return len(password) >= passwordMinFor(user) && len(password) <= passwordMaxBytes
}

func passwordRule(user model.User) string {
	if passwordMinFor(user) == adminPasswordMinBytes {
		return "管理员密码需要 16–72 字节"
	}
	return "密码需要 10–72 字节"
}
