package authmail

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"slices"
	"strings"

	"vpn/backend/internal/config"
)

var (
	ErrInvalidEmail     = errors.New("invalid email address")
	ErrDomainNotAllowed = errors.New("email domain not allowed for registration")
)

// NormalizeEmail is the existing account identity rule (trim, lower-case, one bare
// address). Login and password recovery keep using it for historical accounts.
func NormalizeEmail(input string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(input))
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email && len(email) <= 254 && strings.Contains(email, ".")
}

// RegistrationEmail applies the strict policy for NEW accounts on top of the identity
// rule: plain ASCII, one address, no display name, and an exact match against the
// closed domain list. No suffix matching, MX lookups, dot or plus-tag rewriting.
func RegistrationEmail(input string, domains []string) (string, error) {
	email, ok := NormalizeEmail(input)
	if !ok || !asciiPrintable(email) || strings.Count(email, "@") != 1 {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(email, '@')
	local, domain := email[:at], email[at+1:]
	if local == "" || len(local) > 64 || !config.ValidDomain(domain) {
		return "", ErrInvalidEmail
	}
	if !slices.Contains(domains, domain) {
		return "", ErrDomainNotAllowed
	}
	return email, nil
}

func asciiPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// MaskEmail returns an operator hint such as a***@qq.com; it is never a lookup key.
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 1 {
		return "***"
	}
	hint := email[:1] + "***" + email[at:]
	if len(hint) > 64 {
		hint = hint[:64]
	}
	return hint
}

// NewCode returns a uniformly random six-digit code (000000-999999) from crypto/rand.
func NewCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// NewResetToken returns 32 random bytes, URL-safe base64 without padding (43 chars).
func NewResetToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidCodeFormat accepts exactly six ASCII digits, leading zeros included. Full-width
// digits, signs, spaces and numbers-as-integers never convert into a valid code.
func ValidCodeFormat(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// ValidTokenFormat accepts the exact shape produced by NewResetToken.
func ValidTokenFormat(token string) bool {
	if len(token) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(b) == 32
}
