package config

import (
	"encoding/base64"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

const testMailSecret = "c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0LTMyYnl0ZXMhIQ==" // 34 bytes, test only
const testSMTPPass = "smtp-test-authorization-code"

func mailBase(t *testing.T) {
	t.Helper()
	timingBase(t)
	for _, k := range []string{"ALLOW_REGISTRATION", "SMTP_HOST", "SMTP_PORT", "SMTP_SECURE", "SMTP_USER", "SMTP_PASS", "SMTP_FROM_NAME", "WEBSITE_BASE_URL", "AUTH_MAIL_SECRET", "TRUSTED_PROXIES"} {
		t.Setenv(k, "")
	}
	// t.Setenv cannot unset; restore "unset" semantics for the domain list explicitly.
	t.Setenv("REGISTRATION_ALLOWED_EMAIL_DOMAINS", "")
	unsetDomains(t)
}

// unsetDomains removes the variable for this test; the t.Setenv before it restores it.
func unsetDomains(t *testing.T) {
	t.Helper()
	if err := os.Unsetenv("REGISTRATION_ALLOWED_EMAIL_DOMAINS"); err != nil {
		t.Fatal(err)
	}
}

func fullMail(t *testing.T) {
	t.Helper()
	mailBase(t)
	for k, v := range map[string]string{"ALLOW_REGISTRATION": "true", "SMTP_HOST": "smtp.qq.com", "SMTP_PORT": "465", "SMTP_SECURE": "true", "SMTP_USER": "sender@qq.com", "SMTP_PASS": testSMTPPass, "SMTP_FROM_NAME": "AsterLink VPN", "WEBSITE_BASE_URL": "https://app.example.com", "AUTH_MAIL_SECRET": testMailSecret} {
		t.Setenv(k, v)
	}
}

func TestMailDefaultsKeepRegistrationClosed(t *testing.T) {
	mailBase(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AllowRegistration || c.RegistrationOpen() || c.MailEnabled() {
		t.Fatal("registration or mail enabled by default")
	}
	if !slices.Equal(c.RegistrationDomains, DefaultRegistrationDomains) || len(c.RegistrationDomains) != 10 {
		t.Fatalf("default domains wrong: %v", c.RegistrationDomains)
	}
	if len(c.TrustedProxies) != 0 {
		t.Fatal("proxies trusted by default")
	}
}

func TestMailCompleteConfiguration(t *testing.T) {
	fullMail(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.RegistrationOpen() || !c.MailEnabled() || c.SMTPPort != 465 || !c.SMTPSecure || c.WebsiteBaseURL != "https://app.example.com" || len(c.AuthMailSecret) < 32 {
		t.Fatalf("complete configuration not applied: %+v", c.SMTPPort)
	}
	// Trailing slash is normalized away; STARTTLS mode accepted.
	t.Setenv("WEBSITE_BASE_URL", "https://APP.example.com/")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_SECURE", "false")
	if c, err = Load(); err != nil || c.WebsiteBaseURL != "https://app.example.com" || c.SMTPSecure {
		t.Fatalf("normalization failed: %v %q", err, c.WebsiteBaseURL)
	}
	// Mail without registration is valid (password reset only).
	t.Setenv("ALLOW_REGISTRATION", "false")
	if c, err = Load(); err != nil || c.RegistrationOpen() || !c.MailEnabled() {
		t.Fatal("reset-only mail configuration rejected")
	}
}

func TestMailRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct{ name, key, value string }{
		{"bool yes", "ALLOW_REGISTRATION", "yes"},
		{"bool typo", "ALLOW_REGISTRATION", "ture"},
		{"secure bool", "SMTP_SECURE", "on"},
		{"missing host", "SMTP_HOST", ""},
		{"missing user", "SMTP_USER", ""},
		{"missing pass", "SMTP_PASS", ""},
		{"host url", "SMTP_HOST", "smtps://smtp.qq.com"},
		{"host path", "SMTP_HOST", "smtp.qq.com/x"},
		{"host port", "SMTP_HOST", "smtp.qq.com:465"},
		{"port zero", "SMTP_PORT", "0"},
		{"port big", "SMTP_PORT", "65536"},
		{"port text", "SMTP_PORT", "smtp"},
		{"user display", "SMTP_USER", "Sender <sender@qq.com>"},
		{"user bare", "SMTP_USER", "sender"},
		{"from name control", "SMTP_FROM_NAME", "AsterLink\r\nBcc: x@qq.com"},
		{"from name long", "SMTP_FROM_NAME", strings.Repeat("名", 65)},
		{"pass placeholder", "SMTP_PASS", "replace-me"},
		{"url missing", "WEBSITE_BASE_URL", ""},
		{"url path", "WEBSITE_BASE_URL", "https://app.example.com/app"},
		{"url query", "WEBSITE_BASE_URL", "https://app.example.com/?a=1"},
		{"url empty query", "WEBSITE_BASE_URL", "https://app.example.com?"},
		{"url fragment", "WEBSITE_BASE_URL", "https://app.example.com/#x"},
		{"url userinfo", "WEBSITE_BASE_URL", "https://user:pw@app.example.com"},
		{"url scheme", "WEBSITE_BASE_URL", "ftp://app.example.com"},
		{"url relative", "WEBSITE_BASE_URL", "app.example.com"},
		{"url http remote", "WEBSITE_BASE_URL", "http://app.example.com"},
		{"url idn", "WEBSITE_BASE_URL", "https://例子.com"},
		{"secret missing", "AUTH_MAIL_SECRET", ""},
		{"secret short", "AUTH_MAIL_SECRET", base64.StdEncoding.EncodeToString(make([]byte, 31))},
		{"secret not base64", "AUTH_MAIL_SECRET", strings.Repeat("!", 48)},
		{"secret is jwt", "AUTH_MAIL_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 64)))},
		{"domains empty", "REGISTRATION_ALLOWED_EMAIL_DOMAINS", " , "},
		{"domains wildcard", "REGISTRATION_ALLOWED_EMAIL_DOMAINS", "*.qq.com"},
		{"domains trailing dot", "REGISTRATION_ALLOWED_EMAIL_DOMAINS", "qq.com."},
		{"domains single label", "REGISTRATION_ALLOWED_EMAIL_DOMAINS", "localhost"},
		{"domains unicode", "REGISTRATION_ALLOWED_EMAIL_DOMAINS", "qq.cоm"},
		{"domains with at", "REGISTRATION_ALLOWED_EMAIL_DOMAINS", "user@qq.com"},
		{"proxies", "TRUSTED_PROXIES", "10.0.0.0/8, not-an-ip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fullMail(t)
			if tc.name == "secret is jwt" {
				t.Setenv("JWT_SECRET", strings.Repeat("a", 64))
			}
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil {
				t.Fatalf("%s=%q accepted", tc.key, tc.value)
			}
			// Startup errors name the variable/category, never the secret values.
			for _, secret := range []string{testSMTPPass, testMailSecret} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked a secret: %v", err)
				}
			}
			if tc.value != "" && len(tc.value) > 6 && strings.Contains(err.Error(), tc.value) {
				t.Fatalf("error echoed the configured value: %v", err)
			}
		})
	}
}

func TestPartialSMTPWithoutRegistrationStillFails(t *testing.T) {
	mailBase(t)
	t.Setenv("SMTP_HOST", "smtp.qq.com")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SMTP_USER") || !strings.Contains(err.Error(), "SMTP_PASS") {
		t.Fatalf("partial SMTP accepted or missing keys not named: %v", err)
	}
	// The .env.example defaults (port, TLS mode, display name) alone do not configure mail.
	mailBase(t)
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_SECURE", "true")
	t.Setenv("SMTP_FROM_NAME", "AsterLink VPN")
	if c, err := Load(); err != nil || c.MailEnabled() {
		t.Fatalf("example defaults broke startup or enabled mail: %v", err)
	}
	t.Setenv("SMTP_SECURE", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("invalid SMTP_SECURE accepted while mail is off")
	}
	mailBase(t)
	t.Setenv("SMTP_USER", "sender@qq.com")
	t.Setenv("SMTP_PASS", "x")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SMTP_HOST") {
		t.Fatalf("missing SMTP_HOST not reported: %v", err)
	}
	mailBase(t)
	t.Setenv("ALLOW_REGISTRATION", "true")
	if _, err := Load(); err == nil {
		t.Fatal("registration enabled without mail")
	}
}

func TestEnvExampleMailGroupStartsWithMailOff(t *testing.T) {
	mailBase(t)
	values, err := godotenv.Read("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for key, value := range values {
		if strings.HasPrefix(key, "SMTP_") || key == "ALLOW_REGISTRATION" || key == "WEBSITE_BASE_URL" || key == "AUTH_MAIL_SECRET" || key == "TRUSTED_PROXIES" || key == "REGISTRATION_ALLOWED_EMAIL_DOMAINS" {
			t.Setenv(key, value)
			seen++
		}
	}
	if seen < 10 {
		t.Fatalf("mail group missing from .env.example (%d keys)", seen)
	}
	c, err := Load()
	if err != nil || c.MailEnabled() || c.RegistrationOpen() || len(c.RegistrationDomains) != 10 {
		t.Fatalf(".env.example mail group must start with mail off: %v", err)
	}
}

func TestDomainListAndDevelopmentLoopback(t *testing.T) {
	fullMail(t)
	t.Setenv("REGISTRATION_ALLOWED_EMAIL_DOMAINS", " QQ.com, gmail.com ,qq.com,, ")
	c, err := Load()
	if err != nil || !slices.Equal(c.RegistrationDomains, []string{"qq.com", "gmail.com"}) {
		t.Fatalf("domain list not normalized: %v %v", err, c.RegistrationDomains)
	}
	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000"} {
		t.Setenv("WEBSITE_BASE_URL", origin)
		if c, err = Load(); err != nil || c.WebsiteBaseURL != origin {
			t.Fatalf("development loopback %s rejected: %v", origin, err)
		}
	}
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1, 10.0.0.0/8, ::1")
	if c, err = Load(); err != nil || len(c.TrustedProxies) != 3 {
		t.Fatalf("trusted proxies rejected: %v", err)
	}
}

func TestProductionMailRequiresHTTPS(t *testing.T) {
	fullMail(t)
	for k, v := range map[string]string{"APP_ENV": "production", "COOKIE_SECURE": "true", "AUTO_MIGRATE": "false", "TEST_PURCHASE_ENABLED": "false", "DATABASE_SSLMODE": "verify-full", "ALLOWED_ORIGINS": "https://app.example.com", "CLIENT_ALLOW_TEST_ENTITLEMENTS": "false", "CLIENT_PROFILE_DIR": ""} {
		t.Setenv(k, v)
	}
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBSITE_BASE_URL", "http://localhost:3000")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted an HTTP website address")
	}
}
