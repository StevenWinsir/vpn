package api

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
	"vpn/backend/internal/config"
)

func TestEmailNormalization(t *testing.T) {
	email, ok := validEmail("  USER@Example.com ")
	if !ok || email != "user@example.com" {
		t.Fatal("email not normalized")
	}
	for _, s := range []string{"bad", "User <u@example.com>", "a\nb@example.com"} {
		if _, ok := validEmail(s); ok {
			t.Fatalf("invalid email accepted: %q", s)
		}
	}
}
func TestJWTValidation(t *testing.T) {
	s := Server{cfg: config.Config{JWTSecret: strings.Repeat("s", 48), Issuer: "test-api", Audience: "test-client"}}
	makeClaims := func() claims {
		return claims{SessionID: uuid.NewString(), RegisteredClaims: jwt.RegisteredClaims{Issuer: "test-api", Subject: uuid.NewString(), Audience: jwt.ClaimStrings{"test-client"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
	}
	sign := func(c claims, key string) string {
		v, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(key))
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	valid := makeClaims()
	if _, err := s.parseAccess(sign(valid, s.cfg.JWTSecret)); err != nil {
		t.Fatal(err)
	}
	wrongIssuer := makeClaims()
	wrongIssuer.Issuer = "other"
	wrongAudience := makeClaims()
	wrongAudience.Audience = jwt.ClaimStrings{"other"}
	expired := makeClaims()
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	missingExp := makeClaims()
	missingExp.ExpiresAt = nil
	for _, cl := range []claims{wrongIssuer, wrongAudience, expired, missingExp} {
		if _, err := s.parseAccess(sign(cl, s.cfg.JWTSecret)); err == nil {
			t.Fatal("invalid JWT accepted")
		}
	}
	if _, err := s.parseAccess(sign(valid, "wrong-secret")); err == nil {
		t.Fatal("forged JWT accepted")
	}
	if refreshHash("one") == refreshHash("two") {
		t.Fatal("refresh hash collision")
	}
}
