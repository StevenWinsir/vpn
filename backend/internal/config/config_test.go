package config

import (
	"strings"
	"testing"
	"time"
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

func timingBase(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{"APP_ENV": "development", "DATABASE_HOST": "db.example.invalid", "DATABASE_USER": "test", "DATABASE_NAME": "test", "DATABASE_PASSWORD": "test-only", "JWT_SECRET": strings.Repeat("a", 64), "DATABASE_SSLMODE": "disable", "CLIENT_NODE_CATALOG_ENABLED": "false"} {
		t.Setenv(k, v)
	}
	for _, k := range []string{"CLIENT_REPORT_INTERVAL_SECONDS", "CLIENT_LEASE_SECONDS", "CLIENT_IDLE_TIMEOUT_SECONDS", "CLIENT_NODE_WATCH_SECONDS"} {
		t.Setenv(k, "")
	}
}

func TestClientTimingDefaultsKeepLegacyBehaviour(t *testing.T) {
	timingBase(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ReportInterval() != 60*time.Second || c.Lease() != 90*time.Second || c.IdleTTL() != 180*time.Second || c.NodeWatch() != 5*time.Second {
		t.Fatalf("defaults changed: %v %v %v %v", c.ReportInterval(), c.Lease(), c.IdleTTL(), c.NodeWatch())
	}
	// A Config built without Load (as many tests do) must behave identically.
	var zero Config
	if zero.ReportInterval() != 60*time.Second || zero.Lease() != 90*time.Second || zero.IdleTTL() != 180*time.Second || zero.NodeWatch() != 5*time.Second {
		t.Fatal("zero-value Config does not fall back to defaults")
	}
}

func TestClientNodeWatchMustBeShorterThanLease(t *testing.T) {
	timingBase(t)
	t.Setenv("CLIENT_REPORT_INTERVAL_SECONDS", "10")
	t.Setenv("CLIENT_LEASE_SECONDS", "20")
	t.Setenv("CLIENT_NODE_WATCH_SECONDS", "20")
	if _, err := Load(); err == nil {
		t.Fatal("node watch equal to the lease accepted")
	}
}

func TestClientTimingOverridesAndBounds(t *testing.T) {
	timingBase(t)
	t.Setenv("CLIENT_REPORT_INTERVAL_SECONDS", "30")
	t.Setenv("CLIENT_LEASE_SECONDS", "60")
	t.Setenv("CLIENT_IDLE_TIMEOUT_SECONDS", "120")
	t.Setenv("CLIENT_NODE_WATCH_SECONDS", "3")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ReportInterval() != 30*time.Second || c.Lease() != 60*time.Second || c.IdleTTL() != 120*time.Second || c.NodeWatch() != 3*time.Second {
		t.Fatal("overrides were not applied")
	}
	for _, tc := range []struct{ name, key, value string }{
		{"non-numeric", "CLIENT_LEASE_SECONDS", "abc"},
		{"report below minimum", "CLIENT_REPORT_INTERVAL_SECONDS", "9"},
		{"report above maximum", "CLIENT_REPORT_INTERVAL_SECONDS", "601"},
		{"lease not above report", "CLIENT_LEASE_SECONDS", "30"},
		{"idle below lease", "CLIENT_IDLE_TIMEOUT_SECONDS", "59"},
		{"watch zero", "CLIENT_NODE_WATCH_SECONDS", "0"},
		{"watch above maximum", "CLIENT_NODE_WATCH_SECONDS", "31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timingBase(t)
			t.Setenv("CLIENT_REPORT_INTERVAL_SECONDS", "30")
			t.Setenv("CLIENT_LEASE_SECONDS", "60")
			t.Setenv("CLIENT_IDLE_TIMEOUT_SECONDS", "120")
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s accepted", tc.key, tc.value)
			}
		})
	}
}
