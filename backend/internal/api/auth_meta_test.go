package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"vpn/backend/internal/config"
)

func metaConfig() config.Config {
	return config.Config{Env: "test", BcryptCost: 4, APIRate: 1000, AuthRate: 1000, RequestTimeout: time.Minute, Origins: []string{"https://app.example.com"}, JWTSecret: strings.Repeat("j", 64), Issuer: "t", Audience: "t"}
}

func getMeta(t *testing.T, cfg config.Config) (map[string]any, string) {
	t.Helper()
	w := httptest.NewRecorder()
	New(nil, cfg).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/meta", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("meta status %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body, w.Body.String()
}

func TestMetaAuthCapabilities(t *testing.T) {
	// Defaults: registration closed, reset unavailable, existing fields intact.
	body, _ := getMeta(t, metaConfig())
	if body["allow_registration"] != false || body["password_reset_enabled"] != false || body["currency"] != "CNY" || body["test_purchase_enabled"] != false {
		t.Fatalf("default meta wrong: %v", body)
	}
	if domains, _ := body["registration_email_domains"].([]any); len(domains) != 10 || domains[0] != "qq.com" {
		t.Fatalf("default domains not published: %v", body["registration_email_domains"])
	}
	if body["verification_code_ttl_seconds"] != float64(600) || body["verification_resend_after_seconds"] != float64(60) {
		t.Fatal("timing hints missing")
	}

	cfg := metaConfig()
	cfg.AllowRegistration = true
	cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPSecure = "smtp.secret-host.example", 465, true
	cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFromName = "sender-mailbox@qq.com", "smtp-pass-canary", "AsterLink VPN"
	cfg.WebsiteBaseURL = "https://website-canary.example.com"
	cfg.AuthMailSecret = []byte("auth-mail-secret-canary-32-bytes!!")
	cfg.RegistrationDomains = []string{"qq.com", "gmail.com"}
	body, raw := getMeta(t, cfg)
	if body["allow_registration"] != true || body["password_reset_enabled"] != true {
		t.Fatalf("configured meta wrong: %v", body)
	}
	if domains, _ := body["registration_email_domains"].([]any); len(domains) != 2 {
		t.Fatal("effective domain list not published")
	}
	for _, secret := range []string{"secret-host", "sender-mailbox", "smtp-pass-canary", "website-canary", "auth-mail-secret", "465"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("meta leaked internal configuration (%s)", secret)
		}
	}
	// ALLOW_REGISTRATION without working mail can never be reported as open.
	cfg.SMTPPass = ""
	if body, _ = getMeta(t, cfg); body["allow_registration"] != false || body["password_reset_enabled"] != false {
		t.Fatal("registration reported open without complete mail")
	}
}

// R1-08 (backend half): only configured proxies may supply the client address.
func TestClientAddressTrust(t *testing.T) {
	probe := func(trusted []string, peer string, headers map[string]string) string {
		cfg := metaConfig()
		cfg.TrustedProxies = trusted
		engine := New(nil, cfg)
		engine.GET("/api/v1/test-client-ip", func(c *gin.Context) { c.String(200, c.ClientIP()) })
		req := httptest.NewRequest("GET", "/api/v1/test-client-ip", nil)
		req.RemoteAddr = peer + ":40000"
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Body.String()
	}
	forged := map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8", "Forwarded": "for=9.9.9.9"}
	// Default: no proxy is trusted; forged headers never change the address.
	if got := probe(nil, "10.0.0.5", forged); got != "10.0.0.5" {
		t.Fatalf("untrusted forwarding header honoured: %s", got)
	}
	trusted := []string{"10.0.0.0/8"}
	// Two different users behind the trusted ingress stay distinguishable.
	a := probe(trusted, "10.0.0.5", map[string]string{"X-Forwarded-For": "203.0.113.10"})
	b := probe(trusted, "10.0.0.5", map[string]string{"X-Forwarded-For": "203.0.113.20"})
	if a != "203.0.113.10" || b != "203.0.113.20" {
		t.Fatalf("trusted proxy chain not used: %s %s", a, b)
	}
	// A client-forged prefix is ignored: the right-most untrusted hop wins.
	if got := probe(trusted, "10.0.0.5", map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.10"}); got != "203.0.113.10" {
		t.Fatalf("forged prefix accepted: %s", got)
	}
	// X-Real-IP / Forwarded are not consulted even from a trusted peer.
	if got := probe(trusted, "10.0.0.5", map[string]string{"X-Real-IP": "5.6.7.8"}); got != "10.0.0.5" {
		t.Fatalf("X-Real-IP honoured: %s", got)
	}
	// Bypassing the ingress (connecting directly from outside) cannot spoof an address.
	if got := probe(trusted, "198.51.100.77", forged); got != "198.51.100.77" {
		t.Fatalf("direct connection spoofed its address: %s", got)
	}
}
