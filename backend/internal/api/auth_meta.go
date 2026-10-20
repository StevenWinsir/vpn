package api

import (
	"github.com/gin-gonic/gin"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
)

// authMeta is the public part of the registration and mail configuration. It carries
// effective switches and display values only: never SMTP details, secrets or URLs.
func authMeta(cfg config.Config) gin.H {
	limits := authmail.DefaultLimits()
	domains := cfg.RegistrationDomains
	if domains == nil {
		domains = config.DefaultRegistrationDomains
	}
	return gin.H{
		"allow_registration":                cfg.RegistrationOpen(),
		"registration_email_domains":        domains,
		"password_reset_enabled":            cfg.MailEnabled(),
		"verification_code_ttl_seconds":     int(limits.CodeTTL.Seconds()),
		"verification_resend_after_seconds": int(limits.EmailCooldown.Seconds()),
	}
}
