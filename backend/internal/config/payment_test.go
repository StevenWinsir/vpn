package config

import (
	"strings"
	"testing"
	"time"
)

func paymentBase(t *testing.T, production bool) {
	t.Helper()
	base := map[string]string{"DATABASE_HOST": "db.example.invalid", "DATABASE_USER": "test", "DATABASE_NAME": "test", "DATABASE_PASSWORD": "test-only", "JWT_SECRET": strings.Repeat("a", 64),
		"CLIENT_ALLOW_TEST_ENTITLEMENTS": "false", "CLIENT_NODE_CATALOG_ENABLED": "false", "CLIENT_PROFILE_DIR": "", "TEST_PURCHASE_ENABLED": "false"}
	if production {
		for k, v := range map[string]string{"APP_ENV": "production", "COOKIE_SECURE": "true", "AUTO_MIGRATE": "false", "DATABASE_SSLMODE": "verify-full", "ALLOWED_ORIGINS": "https://app.example.invalid", "WEBSITE_BASE_URL": "https://app.example.invalid"} {
			base[k] = v
		}
	} else {
		base["APP_ENV"] = "development"
		base["WEBSITE_BASE_URL"] = "http://localhost:3000"
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
	for _, k := range []string{"PAYMENT_ENABLED", "PAYMENT_GATEWAY_URL", "PAYMENT_CHANNEL", "PAYMENT_SECRET", "PAYMENT_NOTIFY_URL", "PAYMENT_NOTIFY_ALLOWED_IPS", "PAYMENT_AMOUNT_TOLERANCE_CENTS", "PAYMENT_MAX_PENDING_PER_USER", "PAYMENT_PENDING_LIMIT_SECONDS", "PAYMENT_TEST_PRICE_ENABLED", "PAYMENT_TEST_PRICE", "PAYMENT_TEST_PRICE_EMAILS"} {
		t.Setenv(k, "")
	}
}

func enablePayment(t *testing.T, notify string) {
	t.Setenv("PAYMENT_ENABLED", "true")
	t.Setenv("PAYMENT_CHANNEL", "1001")
	t.Setenv("PAYMENT_SECRET", "test-only-gateway-secret")
	t.Setenv("PAYMENT_NOTIFY_URL", notify)
}

// R1-02: the payment group and its production rules.
func TestPaymentConfiguration(t *testing.T) {
	paymentBase(t, false)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Payment.Enabled || c.Payment.GatewayURL != DefaultPaymentGateway || c.Payment.TestPriceCents != 1 || c.Payment.ToleranceCents != 50 || c.Payment.Pending() != 5 || c.Payment.Window() != 180*time.Second {
		t.Fatalf("defaults wrong: %+v", c.Payment)
	}
	enablePayment(t, "http://127.0.0.1:8080/api/v1/payments/notify")
	t.Setenv("PAYMENT_NOTIFY_ALLOWED_IPS", "203.0.113.7, 198.51.100.0/24, ::ffff:192.0.2.9")
	if c, err = Load(); err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]bool{"203.0.113.7": true, "198.51.100.200": true, "192.0.2.9": true, "::ffff:203.0.113.7": true, "203.0.113.8": false, "": false, "garbage": false} {
		if c.Payment.AllowsNotifySource(ip) != want {
			t.Fatalf("source %q: want %t", ip, want)
		}
	}
	// Each missing piece is a startup error that names the variable, never a value.
	for _, key := range []string{"PAYMENT_CHANNEL", "PAYMENT_SECRET", "PAYMENT_NOTIFY_URL", "WEBSITE_BASE_URL"} {
		saved := map[string]string{"PAYMENT_CHANNEL": "1001", "PAYMENT_SECRET": "test-only-gateway-secret", "PAYMENT_NOTIFY_URL": "http://127.0.0.1:8080/api/v1/payments/notify", "WEBSITE_BASE_URL": "http://localhost:3000"}[key]
		t.Setenv(key, "")
		if _, err = Load(); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("missing %s accepted: %v", key, err)
		}
		t.Setenv(key, saved)
	}
	for key, bad := range map[string]string{
		"PAYMENT_ENABLED": "yes", "PAYMENT_TEST_PRICE_ENABLED": "1x", "PAYMENT_GATEWAY_URL": "https://pay.example.com/submit", "PAYMENT_CHANNEL": "10 01",
		"PAYMENT_SECRET": "replace-me-please", "PAYMENT_NOTIFY_URL": "https://api.example.com/notify?x=1", "PAYMENT_NOTIFY_ALLOWED_IPS": "203.0.113.7,not-an-ip",
		"PAYMENT_AMOUNT_TOLERANCE_CENTS": "501", "PAYMENT_MAX_PENDING_PER_USER": "0", "PAYMENT_PENDING_LIMIT_SECONDS": "90", "PAYMENT_TEST_PRICE": "0.001",
		"PAYMENT_TEST_PRICE_EMAILS": "ok@example.com,not-an-email",
	} {
		t.Setenv(key, bad)
		_, err = Load()
		if err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
		if len(bad) >= 4 && strings.Contains(err.Error(), bad) || strings.Contains(err.Error(), "test-only-gateway-secret") {
			t.Fatalf("error echoes a value: %v", err)
		}
		t.Setenv(key, map[string]string{"PAYMENT_ENABLED": "true", "PAYMENT_CHANNEL": "1001", "PAYMENT_SECRET": "test-only-gateway-secret", "PAYMENT_NOTIFY_URL": "http://127.0.0.1:8080/api/v1/payments/notify", "PAYMENT_NOTIFY_ALLOWED_IPS": "203.0.113.7"}[key])
	}
	// Development: an empty test-price list means every account.
	t.Setenv("PAYMENT_TEST_PRICE_ENABLED", "true")
	t.Setenv("PAYMENT_TEST_PRICE", "0.5")
	if c, err = Load(); err != nil || c.Payment.TestPriceCents != 50 || !c.Payment.TestPriceFor("anyone@example.com") {
		t.Fatalf("development test price: %+v %v", c.Payment, err)
	}
	t.Setenv("PAYMENT_TEST_PRICE_EMAILS", " Tester@Example.com ")
	if c, err = Load(); err != nil || !c.Payment.TestPriceFor("tester@example.com") || c.Payment.TestPriceFor("other@example.com") {
		t.Fatal("test price list not applied")
	}
}

func TestPaymentProductionRules(t *testing.T) {
	paymentBase(t, true)
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	enablePayment(t, "http://api.example.invalid/api/v1/payments/notify")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted a plain-HTTP callback URL")
	}
	t.Setenv("PAYMENT_NOTIFY_URL", "https://api.example.invalid/api/v1/payments/notify")
	// An empty source list is allowed: callbacks then only reach manual review.
	c, err := Load()
	if err != nil || len(c.Payment.NotifyAllowed) != 0 {
		t.Fatalf("production without a source list: %v", err)
	}
	t.Setenv("PAYMENT_GATEWAY_URL", "http://pay.example.invalid")
	if _, err = Load(); err == nil {
		t.Fatal("production accepted a plain-HTTP gateway")
	}
	t.Setenv("PAYMENT_GATEWAY_URL", "")
	t.Setenv("PAYMENT_TEST_PRICE_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("production test price without an account list accepted")
	}
	t.Setenv("PAYMENT_TEST_PRICE_EMAILS", "owner@example.com")
	if c, err = Load(); err != nil || !c.Payment.TestPriceFor("owner@example.com") || c.Payment.TestPriceFor("customer@example.com") {
		t.Fatalf("production test price list: %v", err)
	}
	// The mock purchase stays forbidden in production regardless of payment.
	t.Setenv("TEST_PURCHASE_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("production accepted the mock test purchase")
	}
}
