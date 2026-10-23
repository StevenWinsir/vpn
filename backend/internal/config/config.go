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
	ClientNodeCatalog                                                     bool
	NodeEncryptionKey                                                     string
	ClientProfileDir                                                      string
	ClientAllowTestEntitlements                                           bool
	ClientReportInterval, ClientLease, ClientIdleTimeout, ClientNodeWatch time.Duration
	// Authoritative (node-reported) metering. The agent listener is a separate mTLS
	// endpoint; production proxy delivery is only possible through it.
	NodeAgentAddr, NodeAgentCertFile, NodeAgentKeyFile, NodeAgentClientCAFile     string
	NodeAgentStale, GrantSettleGrace                                              time.Duration
	GrantBudgetBytes                                                              int64
	ClientMinAppVersion                                                           string
	RequestTimeout, MigrationTimeout                                              time.Duration
	Env, Addr, Host, Port, Database, User, Password, Schema, SSLMode, SSLRootCert string
	JWTSecret, Issuer, Audience                                                   string
	Origins                                                                       []string
	CookieSecure, TestPurchase, AutoMigrate                                       bool
	AccessTTL, SessionTTL, RememberTTL                                            time.Duration
	MaxOpen, MaxIdle, BcryptCost, AuthRate, APIRate                               int
	// Registration and mail (handoff_auth.md 3.1). Secrets never leave the backend.
	AllowRegistration                   bool
	SMTPHost, SMTPUser, SMTPPass        string
	SMTPFromName                        string
	SMTPPort                            int
	SMTPSecure                          bool
	WebsiteBaseURL                      string
	AuthMailSecret                      []byte
	RegistrationDomains, TrustedProxies []string
	// Online presence and node groups (handoff_node_use_history_and_load_balancing_c.md 11).
	// The lease length is ClientLease and renewal follows ClientReportInterval.
	ClientMaxOnlineDevices, NodeHealthFailureUsers int
	NodeHealthCooldown                             time.Duration
	// Payment gateway (handoff_payment_account.md 4). The secret never leaves the backend.
	Payment Payment
	// Geo is the optional IP location lookup for login records.
	Geo Geo
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
	for _, x := range []struct {
		key      string
		dest     *time.Duration
		def, min int
		max      int
	}{{"CLIENT_REPORT_INTERVAL_SECONDS", &c.ClientReportInterval, DefaultReportSeconds, 10, 600}, {"CLIENT_LEASE_SECONDS", &c.ClientLease, DefaultLeaseSeconds, 20, 900}, {"CLIENT_IDLE_TIMEOUT_SECONDS", &c.ClientIdleTimeout, DefaultIdleSeconds, 60, 3600}, {"CLIENT_NODE_WATCH_SECONDS", &c.ClientNodeWatch, DefaultNodeWatchSeconds, 1, 30}} {
		v, err := strconv.Atoi(env(x.key, strconv.Itoa(x.def)))
		if err != nil || v < x.min || v > x.max {
			return c, fmt.Errorf("invalid %s (allowed %d-%d)", x.key, x.min, x.max)
		}
		*x.dest = time.Duration(v) * time.Second
	}
	c.NodeAgentAddr = strings.TrimSpace(os.Getenv("NODE_AGENT_ADDR"))
	c.NodeAgentCertFile = strings.TrimSpace(os.Getenv("NODE_AGENT_TLS_CERT_FILE"))
	c.NodeAgentKeyFile = strings.TrimSpace(os.Getenv("NODE_AGENT_TLS_KEY_FILE"))
	c.NodeAgentClientCAFile = strings.TrimSpace(os.Getenv("NODE_AGENT_CLIENT_CA_FILE"))
	c.ClientMinAppVersion = strings.TrimSpace(os.Getenv("CLIENT_MIN_APP_VERSION"))
	if c.ClientMinAppVersion != "" && !ValidVersion(c.ClientMinAppVersion) {
		return c, fmt.Errorf("invalid CLIENT_MIN_APP_VERSION (dotted numeric, e.g. 1.2.0)")
	}
	for _, x := range []struct {
		key      string
		dest     *time.Duration
		def, min int
		max      int
	}{{"NODE_AGENT_STALE_SECONDS", &c.NodeAgentStale, DefaultAgentStaleSeconds, 5, 300}, {"NODE_AGENT_SETTLE_GRACE_SECONDS", &c.GrantSettleGrace, DefaultSettleGraceSeconds, 10, 3600}} {
		v, err := strconv.Atoi(env(x.key, strconv.Itoa(x.def)))
		if err != nil || v < x.min || v > x.max {
			return c, fmt.Errorf("invalid %s (allowed %d-%d)", x.key, x.min, x.max)
		}
		*x.dest = time.Duration(v) * time.Second
	}
	for _, x := range []struct {
		key           string
		dest          *int
		def, min, max int
	}{{"CLIENT_MAX_ONLINE_DEVICES", &c.ClientMaxOnlineDevices, DefaultMaxOnlineDevices, 1, 100}, {"NODE_HEALTH_FAILURE_USERS", &c.NodeHealthFailureUsers, DefaultHealthFailureUsers, 2, 50}} {
		v, err := strconv.Atoi(env(x.key, strconv.Itoa(x.def)))
		if err != nil || v < x.min || v > x.max {
			return c, fmt.Errorf("invalid %s (allowed %d-%d)", x.key, x.min, x.max)
		}
		*x.dest = v
	}
	cooldown, err := strconv.Atoi(env("NODE_HEALTH_COOLDOWN_SECONDS", strconv.Itoa(DefaultHealthCooldownSeconds)))
	if err != nil || cooldown < 60 || cooldown > 3600 {
		return c, fmt.Errorf("invalid NODE_HEALTH_COOLDOWN_SECONDS (allowed 60-3600)")
	}
	c.NodeHealthCooldown = time.Duration(cooldown) * time.Second
	budget, err := strconv.ParseInt(env("GRANT_BUDGET_BYTES", strconv.FormatInt(DefaultGrantBudgetBytes, 10)), 10, 64)
	if err != nil || budget < 1<<20 || budget > 64<<30 {
		return c, fmt.Errorf("invalid GRANT_BUDGET_BYTES (allowed 1 MiB - 64 GiB)")
	}
	c.GrantBudgetBytes = budget
	if c.NodeAgentAddr != "" && (c.NodeAgentCertFile == "" || c.NodeAgentKeyFile == "" || c.NodeAgentClientCAFile == "") {
		return c, fmt.Errorf("NODE_AGENT_ADDR requires NODE_AGENT_TLS_CERT_FILE, NODE_AGENT_TLS_KEY_FILE and NODE_AGENT_CLIENT_CA_FILE (mTLS is mandatory)")
	}
	// A lease must outlive one report so a healthy client always renews before expiry,
	// and an idle window must outlive a lease so renewal gaps do not look like abandonment.
	if c.ClientLease <= c.ClientReportInterval || c.ClientIdleTimeout < c.ClientLease || c.ClientNodeWatch >= c.ClientLease {
		return c, fmt.Errorf("client timing must satisfy REPORT_INTERVAL < LEASE <= IDLE_TIMEOUT and NODE_WATCH < LEASE")
	}
	for _, origin := range strings.Split(env("ALLOWED_ORIGINS", "http://localhost:3000"), ",") {
		origin = strings.TrimSpace(origin)
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return c, fmt.Errorf("invalid ALLOWED_ORIGINS")
		}
		c.Origins = append(c.Origins, origin)
	}
	if err := loadMail(&c); err != nil {
		return c, err
	}
	if err := loadGeo(&c); err != nil {
		return c, err
	}
	if err := loadPayment(&c); err != nil {
		return c, err
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
		// Static plan-wide files are shared credentials and can never be revoked per user.
		if c.ClientProfileDir != "" {
			return c, fmt.Errorf("production forbids CLIENT_PROFILE_DIR: shared static proxy files cannot be metered or revoked per user")
		}
		// The catalog may be enabled in production only together with the mTLS agent
		// listener: delivery then goes through per-user grants confirmed by the node.
		// Client-reported /config and /traffic are refused by code in production.
		if c.ClientNodeCatalog && c.NodeAgentAddr == "" {
			return c, fmt.Errorf("production proxy delivery requires node-side metering (NODE_AGENT_ADDR with mTLS); client-reported proxy mode is development-only")
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

// Defaults reproduce the previously hard-coded native protocol timings.
const (
	DefaultReportSeconds    = 60
	DefaultLeaseSeconds     = 90
	DefaultIdleSeconds      = 180
	DefaultNodeWatchSeconds = 5

	DefaultAgentStaleSeconds  = 30
	DefaultSettleGraceSeconds = 60
	DefaultGrantBudgetBytes   = int64(512 << 20)

	// D1: the effective cap is min(subscription max_devices, this value).
	DefaultMaxOnlineDevices      = 2
	DefaultHealthFailureUsers    = 3
	DefaultHealthCooldownSeconds = 300
)

// ValidVersion accepts dotted numeric versions such as 1, 1.2 or 1.2.3.
func ValidVersion(v string) bool {
	return versionPattern.MatchString(v)
}

var versionPattern = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,6}){0,3}$`)

// VersionLess reports whether a < b for dotted numeric versions; anything that does
// not parse is treated as older than every valid version.
func VersionLess(a, b string) bool {
	if !ValidVersion(a) {
		return ValidVersion(b)
	}
	if !ValidVersion(b) {
		return false
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// DevMetering reports whether the development client-reported ledger (/config +
// /traffic with shared node credentials) may be used. Production never allows it.
func (c Config) DevMetering() bool { return c.Env != "production" }

// AgentEnabled reports whether the mTLS agent API is configured.
func (c Config) AgentEnabled() bool { return c.NodeAgentAddr != "" }

func (c Config) AgentStale() time.Duration {
	return orDefault(c.NodeAgentStale, DefaultAgentStaleSeconds)
}
func (c Config) SettleGrace() time.Duration {
	return orDefault(c.GrantSettleGrace, DefaultSettleGraceSeconds)
}
func (c Config) GrantBudget() int64 {
	if c.GrantBudgetBytes <= 0 {
		return DefaultGrantBudgetBytes
	}
	return c.GrantBudgetBytes
}

func (c Config) MaxOnlineDevices() int {
	return orDefaultInt(c.ClientMaxOnlineDevices, DefaultMaxOnlineDevices)
}
func (c Config) HealthFailureUsers() int {
	return orDefaultInt(c.NodeHealthFailureUsers, DefaultHealthFailureUsers)
}
func (c Config) HealthCooldown() time.Duration {
	return orDefault(c.NodeHealthCooldown, DefaultHealthCooldownSeconds)
}

func orDefaultInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func orDefault(value time.Duration, seconds int) time.Duration {
	if value <= 0 {
		return time.Duration(seconds) * time.Second
	}
	return value
}

// The accessors treat an unset (zero) value as the default so a Config built without
// Load (tests, tools) still behaves like production defaults.
func (c Config) ReportInterval() time.Duration {
	return orDefault(c.ClientReportInterval, DefaultReportSeconds)
}
func (c Config) Lease() time.Duration   { return orDefault(c.ClientLease, DefaultLeaseSeconds) }
func (c Config) IdleTTL() time.Duration { return orDefault(c.ClientIdleTimeout, DefaultIdleSeconds) }
func (c Config) NodeWatch() time.Duration {
	return orDefault(c.ClientNodeWatch, DefaultNodeWatchSeconds)
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
