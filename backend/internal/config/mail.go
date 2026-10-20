package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultRegistrationDomains is the closed set of mailbox providers that may register
// new accounts when REGISTRATION_ALLOWED_EMAIL_DOMAINS is not set (handoff_auth.md 3.2).
// Existing accounts on other domains keep logging in and recovering their passwords.
var DefaultRegistrationDomains = []string{"qq.com", "foxmail.com", "163.com", "126.com", "yeah.net", "gmail.com", "outlook.com", "hotmail.com", "icloud.com", "yahoo.com"}

// MinAuthMailSecretBytes is the minimum decoded size of AUTH_MAIL_SECRET.
const MinAuthMailSecretBytes = 32

var (
	domainLabel  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	hostnameOnly = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

// MailEnabled reports whether outbound authentication mail is fully configured.
// Load guarantees that a configured SMTP server always comes with a website address
// and a mail secret, so this single flag gates verification codes and reset links.
func (c Config) MailEnabled() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPPass != "" && c.WebsiteBaseURL != "" && len(c.AuthMailSecret) >= MinAuthMailSecretBytes
}

// RegistrationOpen reports whether new accounts may be created right now.
func (c Config) RegistrationOpen() bool { return c.AllowRegistration && c.MailEnabled() }

// loadMail reads the "Registration and mail" group. Errors name the variable and the
// category only; values (including secrets and partial URLs) are never echoed.
func loadMail(c *Config) error {
	var err error
	if c.AllowRegistration, err = strconv.ParseBool(env("ALLOW_REGISTRATION", "false")); err != nil {
		return errors.New("invalid ALLOW_REGISTRATION (expected true or false)")
	}
	// PORT, SECURE and FROM_NAME have defaults (and ship filled in .env.example), so they
	// are always format-checked but do not by themselves configure SMTP. HOST, USER and
	// PASS decide: none = mail off, some = startup error, all = mail on.
	if c.SMTPPort, err = strconv.Atoi(env("SMTP_PORT", "465")); err != nil || c.SMTPPort < 1 || c.SMTPPort > 65535 {
		return errors.New("invalid SMTP_PORT (1-65535)")
	}
	if c.SMTPSecure, err = strconv.ParseBool(env("SMTP_SECURE", "true")); err != nil {
		return errors.New("invalid SMTP_SECURE (true = implicit TLS, false = mandatory STARTTLS)")
	}
	var present, missing []string
	for _, key := range []string{"SMTP_HOST", "SMTP_USER", "SMTP_PASS"} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		} else {
			present = append(present, key)
		}
	}
	if len(present) > 0 && len(missing) > 0 {
		return fmt.Errorf("incomplete SMTP configuration: missing %s", strings.Join(missing, ", "))
	}
	if len(present) > 0 {
		c.SMTPHost = strings.TrimSpace(os.Getenv("SMTP_HOST"))
		if strings.Contains(c.SMTPHost, "..") || !hostnameOnly.MatchString(c.SMTPHost) {
			return errors.New("invalid SMTP_HOST (plain host name, no scheme, port or path)")
		}
		c.SMTPUser = strings.TrimSpace(os.Getenv("SMTP_USER"))
		if parsed, e := mail.ParseAddress(c.SMTPUser); e != nil || parsed.Address != c.SMTPUser || parsed.Name != "" || !isASCII(c.SMTPUser) {
			return errors.New("invalid SMTP_USER (the complete sender mailbox address)")
		}
		// The password is used verbatim: authorization codes may legitimately contain spaces.
		c.SMTPPass = os.Getenv("SMTP_PASS")
		if strings.Contains(c.SMTPPass, "replace-") || strings.ContainsAny(c.SMTPPass, "\r\n\x00") {
			return errors.New("invalid SMTP_PASS")
		}
		c.SMTPFromName = strings.TrimSpace(env("SMTP_FROM_NAME", "AsterLink VPN"))
		if !validDisplayName(c.SMTPFromName) {
			return errors.New("invalid SMTP_FROM_NAME (1-64 characters, no control characters)")
		}
	}
	if raw := strings.TrimSpace(os.Getenv("WEBSITE_BASE_URL")); raw != "" {
		if c.WebsiteBaseURL, err = websiteOrigin(raw, c.Env == "production"); err != nil {
			return err
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AUTH_MAIL_SECRET")); raw != "" {
		secret, e := base64.StdEncoding.Strict().DecodeString(raw)
		if e != nil || len(secret) < MinAuthMailSecretBytes {
			return fmt.Errorf("AUTH_MAIL_SECRET must be standard base64 of at least %d random bytes (openssl rand -base64 32)", MinAuthMailSecretBytes)
		}
		if raw == strings.TrimSpace(c.NodeEncryptionKey) || raw == c.JWTSecret || string(secret) == c.JWTSecret {
			return errors.New("AUTH_MAIL_SECRET must be independent of JWT_SECRET and NODE_ENCRYPTION_KEY")
		}
		c.AuthMailSecret = secret
	}
	if c.SMTPHost != "" {
		var missing []string
		if c.WebsiteBaseURL == "" {
			missing = append(missing, "WEBSITE_BASE_URL")
		}
		if c.AuthMailSecret == nil {
			missing = append(missing, "AUTH_MAIL_SECRET")
		}
		if len(missing) > 0 {
			return fmt.Errorf("SMTP configuration also requires %s", strings.Join(missing, ", "))
		}
	}
	if c.AllowRegistration && !c.MailEnabled() {
		return errors.New("ALLOW_REGISTRATION=true requires complete SMTP_*, WEBSITE_BASE_URL and AUTH_MAIL_SECRET")
	}
	if raw, set := os.LookupEnv("REGISTRATION_ALLOWED_EMAIL_DOMAINS"); set {
		if c.RegistrationDomains, err = ParseDomainList(raw); err != nil {
			return err
		}
	} else {
		c.RegistrationDomains = append([]string(nil), DefaultRegistrationDomains...)
	}
	if raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if _, _, e := net.ParseCIDR(item); e != nil && net.ParseIP(item) == nil {
				return errors.New("invalid TRUSTED_PROXIES (comma-separated IP addresses or CIDR ranges)")
			}
			c.TrustedProxies = append(c.TrustedProxies, item)
		}
	}
	return nil
}

// ParseDomainList parses an exact, closed domain allow-list: comma separated, trimmed,
// lower-cased and de-duplicated. Empty lists, wildcards, trailing dots and non-ASCII
// (IDN) names are refused rather than silently widened or narrowed.
func ParseDomainList(raw string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		domain := strings.ToLower(strings.TrimSpace(item))
		if domain == "" {
			continue
		}
		if !ValidDomain(domain) {
			return nil, errors.New("invalid REGISTRATION_ALLOWED_EMAIL_DOMAINS (exact ASCII domains such as qq.com; no wildcards or trailing dots)")
		}
		if !seen[domain] {
			seen[domain] = true
			out = append(out, domain)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("REGISTRATION_ALLOWED_EMAIL_DOMAINS is set but empty; remove it to use the default list")
	}
	return out, nil
}

// ValidDomain accepts a lower-case LDH domain with at least two labels.
func ValidDomain(domain string) bool {
	if len(domain) > 253 || !isASCII(domain) {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return false
		}
	}
	// The top-level label is never all digits; this also rejects bare IPv4 addresses.
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

// websiteOrigin accepts only an absolute root origin. Production requires HTTPS;
// plain HTTP is allowed only for loopback development hosts.
func websiteOrigin(raw string, production bool) (string, error) {
	invalid := errors.New("invalid WEBSITE_BASE_URL (absolute root origin such as https://vpn.example.net, without user info, query, fragment or path)")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return "", invalid
	}
	host := u.Hostname()
	if !isASCII(host) || host == "" {
		return "", invalid
	}
	if port := u.Port(); port != "" {
		if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
			return "", invalid
		}
	}
	switch u.Scheme {
	case "https":
	case "http":
		if production {
			return "", errors.New("production WEBSITE_BASE_URL must use HTTPS")
		}
		if !loopbackHost(host) {
			return "", errors.New("WEBSITE_BASE_URL may use plain HTTP only for loopback development hosts")
		}
	default:
		return "", invalid
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validDisplayName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 64 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
