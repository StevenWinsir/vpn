package config

import (
	"strings"
	"testing"
)

func TestProductionGuard(t *testing.T) {
	t.Setenv("CLIENT_ALLOW_TEST_ENTITLEMENTS", "false")
	t.Setenv("CLIENT_NODE_CATALOG_ENABLED", "false")
	t.Setenv("CLIENT_PROFILE_DIR", "")
	for k, v := range map[string]string{"APP_ENV": "production", "DATABASE_HOST": "db.example.invalid", "DATABASE_USER": "test", "DATABASE_NAME": "test", "DATABASE_PASSWORD": "test-only", "JWT_SECRET": strings.Repeat("a", 64), "COOKIE_SECURE": "true", "AUTO_MIGRATE": "false", "TEST_PURCHASE_ENABLED": "false", "DATABASE_SSLMODE": "verify-full", "ALLOWED_ORIGINS": "https://app.example.invalid"} {
		t.Setenv(k, v)
	}
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ k, bad, good string }{{"COOKIE_SECURE", "false", "true"}, {"TEST_PURCHASE_ENABLED", "true", "false"}, {"AUTO_MIGRATE", "true", "false"}, {"DATABASE_SSLMODE", "disable", "verify-full"}, {"ALLOWED_ORIGINS", "http://app.example.invalid", "https://app.example.invalid"}} {
		t.Setenv(tc.k, tc.bad)
		if _, err := Load(); err == nil {
			t.Fatalf("unsafe production value accepted: %s", tc.k)
		}
		t.Setenv(tc.k, tc.good)
	}
	t.Setenv("CLIENT_PROFILE_DIR", "/private/profiles")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted client-reported proxy delivery")
	}
	t.Setenv("CLIENT_PROFILE_DIR", "")
	t.Setenv("CLIENT_NODE_CATALOG_ENABLED", "true")
	t.Setenv("NODE_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted shared node credentials without node enforcement")
	}
	t.Setenv("CLIENT_NODE_CATALOG_ENABLED", "false")
	t.Setenv("CLIENT_ALLOW_TEST_ENTITLEMENTS", "true")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted native test entitlements")
	}
}
