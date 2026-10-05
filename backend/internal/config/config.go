package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"vpn/backend/internal/nodes"
)

type Config struct {
	ClientNodeCatalog                                                             bool
	NodeEncryptionKey                                                             string
	ClientProfileDir                                                              string
	ClientAllowTestEntitlements                                                   bool
	RequestTimeout, MigrationTimeout                                              time.Duration
	Env, Addr, Host, Port, Database, User, Password, Schema, SSLMode, SSLRootCert string
	JWTSecret, Issuer, Audience                                                   string
	Origins                                                                       []string
	CookieSecure, TestPurchase, AutoMigrate                                       bool
	AccessTTL, SessionTTL, RememberTTL                                            time.Duration
	MaxOpen, MaxIdle, BcryptCost, AuthRate, APIRate                               int
}

func Load() (Config, error) {
	_ = godotenv.Load()
	c := Config{Env: env("APP_ENV", "development"), Addr: env("HTTP_ADDR", "127.0.0.1:8080"), Host: os.Getenv("DATABASE_HOST"), Port: env("DATABASE_PORT", "5432"), Database: os.Getenv("DATABASE_NAME"), User: os.Getenv("DATABASE_USER"), Password: os.Getenv("DATABASE_PASSWORD"), Schema: env("DATABASE_SCHEMA", "vpn_app"), SSLMode: env("DATABASE_SSLMODE", "verify-full"), SSLRootCert: os.Getenv("DATABASE_SSLROOTCERT"), JWTSecret: os.Getenv("JWT_SECRET"), Issuer: env("JWT_ISSUER", "asterlink-api"), Audience: env("JWT_AUDIENCE", "asterlink-clients")}
	c.ClientProfileDir = strings.TrimSpace(os.Getenv("CLIENT_PROFILE_DIR"))
	c.NodeEncryptionKey = strings.TrimSpace(os.Getenv("NODE_ENCRYPTION_KEY"))
	var catalogErr error
	c.ClientNodeCatalog, catalogErr = strconv.ParseBool(env("CLIENT_NODE_CATALOG_ENABLED", "false"))
	if catalogErr != nil || c.ClientNodeCatalog && !nodes.ValidateKey(c.NodeEncryptionKey) {
		return c, fmt.Errorf("node catalog requires CLIENT_NODE_CATALOG_ENABLED boolean and a base64-encoded 32-byte NODE_ENCRYPTION_KEY")
	}
	for key, dest := range map[string]*bool{"COOKIE_SECURE": &c.CookieSecure, "TEST_PURCHASE_ENABLED": &c.TestPurchase, "AUTO_MIGRATE": &c.AutoMigrate, "CLIENT_ALLOW_TEST_ENTITLEMENTS": &c.ClientAllowTestEntitlements} {
		value, err := strconv.ParseBool(env(key, "false"))
		if err != nil {
			return c, fmt.Errorf("invalid %s", key)
		}
		*dest = value
	}
	ints := []struct {
		key           string
		dest          *int
		def, min, max int
	}{{"DATABASE_MAX_OPEN", &c.MaxOpen, 10, 1, 100}, {"DATABASE_MAX_IDLE", &c.MaxIdle, 3, 0, 100}, {"BCRYPT_COST", &c.BcryptCost, 12, 10, 15}, {"AUTH_REQUESTS_PER_MINUTE", &c.AuthRate, 20, 1, 10000}, {"API_REQUESTS_PER_MINUTE", &c.APIRate, 180, 1, 100000}}
	for _, x := range ints {
		v, err := strconv.Atoi(env(x.key, strconv.Itoa(x.def)))
		if err != nil || v < x.min || v > x.max {
			return c, fmt.Errorf("invalid %s", x.key)
		}
		*x.dest = v
	}
	for _, x := range []struct {
		key      string
		dest     *time.Duration
		def, max int
		unit     time.Duration
	}{{"ACCESS_TOKEN_MINUTES", &c.AccessTTL, 15, 60, time.Minute}, {"SESSION_HOURS", &c.SessionTTL, 12, 168, time.Hour}, {"REMEMBER_DAYS", &c.RememberTTL, 30, 90, 24 * time.Hour}, {"REQUEST_TIMEOUT_SECONDS", &c.RequestTimeout, 60, 120, time.Second}, {"MIGRATION_TIMEOUT_SECONDS", &c.MigrationTimeout, 180, 600, time.Second}} {
		v, err := strconv.Atoi(env(x.key, strconv.Itoa(x.def)))
		if err != nil || v < 1 || v > x.max {
			return c, fmt.Errorf("invalid %s", x.key)
		}
		*x.dest = time.Duration(v) * x.unit
	}
	for _, origin := range strings.Split(env("ALLOWED_ORIGINS", "http://localhost:3000"), ",") {
		origin = strings.TrimSpace(origin)
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return c, fmt.Errorf("invalid ALLOWED_ORIGINS")
		}
		c.Origins = append(c.Origins, origin)
	}
	if c.Host == "" || c.Database == "" || c.User == "" || c.Password == "" {
		return c, fmt.Errorf("database environment variables are required")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`).MatchString(c.Schema) {
		return c, fmt.Errorf("invalid DATABASE_SCHEMA")
	}
	if len(c.JWTSecret) < 32 || strings.Contains(c.JWTSecret, "replace-") {
		return c, fmt.Errorf("JWT_SECRET must contain at least 32 random bytes")
	}
	switch c.SSLMode {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		return c, fmt.Errorf("invalid DATABASE_SSLMODE")
	}
	if c.MaxIdle > c.MaxOpen {
		return c, fmt.Errorf("DATABASE_MAX_IDLE exceeds DATABASE_MAX_OPEN")
	}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, fmt.Errorf("invalid APP_ENV")
	}
	if c.Env == "production" {
		if c.ClientNodeCatalog || c.ClientProfileDir != "" {
			return c, fmt.Errorf("production proxy delivery requires node-side metering and credential revocation; client-reported proxy mode is development-only")
		}
		if c.ClientAllowTestEntitlements {
			return c, fmt.Errorf("production forbids CLIENT_ALLOW_TEST_ENTITLEMENTS")
		}
		if c.TestPurchase || c.AutoMigrate || !c.CookieSecure || c.SSLMode != "verify-full" {
			return c, fmt.Errorf("production requires secure cookies, verify-full database TLS, test purchases OFF and auto migration OFF")
		}
		for _, origin := range c.Origins {
			if !strings.HasPrefix(origin, "https://") {
				return c, fmt.Errorf("production origins must use HTTPS")
			}
		}
	}
	return c, nil
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func (c Config) DSN() string {
	u := url.URL{Scheme: "postgres", Host: c.Host + ":" + c.Port, Path: "/" + c.Database, User: url.UserPassword(c.User, c.Password)}
	q := u.Query()
	q.Set("sslmode", c.SSLMode)
	q.Set("connect_timeout", "8")
	q.Set("TimeZone", "UTC")
	if c.SSLRootCert != "" {
		q.Set("sslrootcert", c.SSLRootCert)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
