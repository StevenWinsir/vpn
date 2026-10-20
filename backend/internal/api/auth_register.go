package api

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/model"
)

type registerCodeInput struct {
	Email string `json:"email"`
}

// registerInput is the only accepted shape: role, status, verified, user_id or plan
// fields are unknown and rejected by decode before anything else runs.
type registerInput struct {
	Name           string `json:"name"`
	Email          string `json:"email"`
	Password       string `json:"password"`
	Remember       bool   `json:"remember"`
	VerificationID string `json:"verification_id"`
	Code           string `json:"code"`
}

// Password byte limits for ordinary accounts (bcrypt truncates beyond 72 bytes).
const (
	passwordMinBytes = 10
	passwordMaxBytes = 72
)

// registerCodeAccepted is identical for new and existing addresses; only an eligible
// new mailbox actually receives mail, which the response never reveals.
const registerCodeAccepted = "如果该邮箱可以注册，6 位验证码会在几分钟内送达，请同时检查垃圾邮件箱。"

// failAuthMail maps mailbox-proof errors to the stable public contract. Internal
// causes stay out of responses; none of these branches depends on account existence.
func failAuthMail(c *gin.Context, err error) {
	var limited *authmail.RateLimitError
	switch {
	case errors.As(err, &limited):
		c.Header("Retry-After", strconv.Itoa(limited.RetryAfterSeconds()))
		fail(c, 429, "rate_limited", "请求过于频繁，请稍后重试")
	case errors.Is(err, authmail.ErrInvalidEmail):
		fail(c, 400, "invalid_email", "请输入有效的邮箱地址")
	case errors.Is(err, authmail.ErrDomainNotAllowed):
		fail(c, 400, "email_domain_not_allowed", "该邮箱服务商暂不支持注册，请使用页面列出的邮箱")
	case errors.Is(err, authmail.ErrRegistrationClosed):
		fail(c, 403, "registration_disabled", "当前暂未开放注册")
	case errors.Is(err, authmail.ErrCodeFormat):
		fail(c, 400, "invalid_verification_code", "请输入邮件中的 6 位数字验证码")
	case errors.Is(err, authmail.ErrVerificationInvalid):
		fail(c, 400, "verification_invalid", "验证码不正确，请检查后重新输入")
	case errors.Is(err, authmail.ErrVerificationExpired):
		fail(c, 400, "verification_expired", "验证码已过期或已被新验证码取代，请重新获取")
	case errors.Is(err, authmail.ErrVerificationUsed):
		fail(c, 400, "verification_used", "验证码已使用。如果已完成注册，请直接登录")
	case errors.Is(err, authmail.ErrVerificationLocked):
		fail(c, 400, "verification_locked", "验证码错误次数过多，请重新获取验证码")
	case errors.Is(err, authmail.ErrMailUnavailable):
		fail(c, 503, "mail_unavailable", "邮件服务暂时不可用，请稍后重试")
	default:
		fail(c, 503, "auth_unavailable", "服务暂时不可用，请稍后重试")
	}
}

// registerCode accepts a registration-code request (handoff_auth.md 7.1). The order
// of checks is fixed: format, allow-list, switch, mail capability, shared limits.
func (s *Server) registerCode(c *gin.Context) {
	var in registerCodeInput
	if !decode(c, &in) {
		return
	}
	receipt, err := s.mail.IssueRegistration(c.Request.Context(), in.Email, c.ClientIP())
	if err != nil {
		failAuthMail(c, err)
		return
	}
	now := s.mail.Now().UTC()
	c.JSON(202, gin.H{
		"message":              registerCodeAccepted,
		"verification_id":      receipt.VerificationID,
		"expires_at":           receipt.ExpiresAt,
		"next_send_at":         receipt.NextSendAt,
		"expires_in_seconds":   secondsUntil(receipt.ExpiresAt, now),
		"resend_after_seconds": secondsUntil(receipt.NextSendAt, now),
	})
}

// secondsUntil lets the browser count down without trusting its own clock.
func secondsUntil(t, now time.Time) int {
	d := t.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// register creates an account only after consuming a valid mailbox proof in the same
// transaction (handoff_auth.md 7.2). There is no path that skips the code.
func (s *Server) register(c *gin.Context) {
	var in registerInput
	if !decode(c, &in) {
		return
	}
	// The switch and the allow-list are re-evaluated now, not when the code was sent.
	if !s.cfg.RegistrationOpen() {
		failAuthMail(c, authmail.ErrRegistrationClosed)
		return
	}
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 2 || n > 40 || len(in.Password) < passwordMinBytes || len(in.Password) > passwordMaxBytes {
		fail(c, 400, "invalid_registration", "请输入 2–40 字姓名和 10–72 字节密码")
		return
	}
	email, err := authmail.RegistrationEmail(in.Email, s.cfg.RegistrationDomains)
	if errors.Is(err, authmail.ErrInvalidEmail) {
		fail(c, 400, "invalid_registration", "请输入有效的邮箱地址")
		return
	}
	if err != nil {
		failAuthMail(c, err)
		return
	}
	if in.VerificationID == "" {
		fail(c, 400, "verification_required", "请先获取并填写邮箱验证码")
		return
	}
	if !authmail.ValidCodeFormat(in.Code) {
		failAuthMail(c, authmail.ErrCodeFormat)
		return
	}
	// bcrypt runs before any row lock is taken.
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cfg.BcryptCost)
	if err != nil {
		fail(c, 500, "password_error", "无法处理密码")
		return
	}
	var user model.User
	var sess model.Session
	var raw string
	err = s.mail.VerifyRegistration(c.Request.Context(), in.VerificationID, email, in.Code, func(tx *gorm.DB) error {
		verified := s.mail.Now().UTC()
		// Role, status and verification are fixed by the server, never by the request.
		user = model.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: string(hash), Role: "user", Status: "active", EmailVerifiedAt: &verified, AuthVersion: 1}
		if e := tx.Create(&user).Error; e != nil {
			return e
		}
		var e error
		sess, raw, e = s.createSession(tx, user.ID, in.Remember)
		return e
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		// Reached only after the code matched: proof first, then the conflict.
		fail(c, 409, "email_exists", "此邮箱已注册，请直接登录或找回密码")
		return
	}
	if err != nil {
		failAuthMail(c, err)
		return
	}
	if err = s.writeSession(c, sess, raw); err != nil {
		fail(c, 500, "session_failed", "注册已完成，但创建登录会话失败，请直接登录")
		return
	}
	c.JSON(201, gin.H{"user": user})
}
