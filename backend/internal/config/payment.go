package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"vpn/backend/internal/payment"
)

// Payment is the gateway group of backend/.env (handoff_payment_account.md 4).
type Payment struct {
	Enabled bool
	// GatewayURL is the gateway root origin, without a trailing slash.
	GatewayURL, Channel, Secret string
	// NotifyURL is the absolute callback address handed to the gateway.
	NotifyURL string
	// NotifyAllowed is the gateway's source addresses. Empty means no callback is
	// trusted enough to fulfil an order on its own: every one goes to manual review.
	NotifyAllowed  []netip.Prefix
	ToleranceCents int64
	MaxPending     int
	PendingWindow  time.Duration
	// Test price: a real gateway payment at a lower price, for listed accounts only.
	TestPrice      bool
	TestPriceCents int64
	TestEmails     []string
}

const (
	DefaultPaymentGateway         = "https://pay.hyshentou.cn"
	DefaultPaymentToleranceCents  = 50
	DefaultPaymentMaxPending      = 5
	DefaultPaymentPendingSeconds  = 180
	MinPaymentPendingSeconds      = 120
	MaxPaymentPendingSeconds      = 600
	MaxPaymentTestPriceCents      = 100000
	minPaymentSecretLength        = 8
	maxPaymentChannelOrSecretSize = 128
)

var paymentChannel = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// AllowsNotifySource reports whether ip is one of the configured gateway addresses.
func (p Payment) AllowsNotifySource(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range p.NotifyAllowed {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// TestPriceFor reports whether the test price applies to this (normalized) email.
func (p Payment) TestPriceFor(email string) bool {
	if !p.TestPrice {
		return false
	}
	if len(p.TestEmails) == 0 {
		return true
	}
	for _, allowed := range p.TestEmails {
		if allowed == email {
			return true
		}
	}
	return false
}

// Window is the local "live pending order" span; it never undercuts the gateway's 2 minutes.
func (p Payment) Window() time.Duration {
	if p.PendingWindow < MinPaymentPendingSeconds*time.Second {
		return DefaultPaymentPendingSeconds * time.Second
	}
	return p.PendingWindow
}

func (p Payment) Pending() int { return orDefaultInt(p.MaxPending, DefaultPaymentMaxPending) }

// loadPayment reads the Payment group. Errors name the variable only, never a value.
func loadPayment(c *Config) error {
	p := &c.Payment
	production := c.Env == "production"
	var err error
	if p.Enabled, err = strconv.ParseBool(env("PAYMENT_ENABLED", "false")); err != nil {
		return errors.New("invalid PAYMENT_ENABLED (expected true or false)")
	}
	if p.TestPrice, err = strconv.ParseBool(env("PAYMENT_TEST_PRICE_ENABLED", "false")); err != nil {
		return errors.New("invalid PAYMENT_TEST_PRICE_ENABLED (expected true or false)")
	}
	tolerance, err := strconv.Atoi(env("PAYMENT_AMOUNT_TOLERANCE_CENTS", strconv.Itoa(DefaultPaymentToleranceCents)))
	if err != nil || tolerance < 0 || tolerance > 500 {
		return errors.New("invalid PAYMENT_AMOUNT_TOLERANCE_CENTS (allowed 0-500)")
	}
	p.ToleranceCents = int64(tolerance)
	if p.MaxPending, err = strconv.Atoi(env("PAYMENT_MAX_PENDING_PER_USER", strconv.Itoa(DefaultPaymentMaxPending))); err != nil || p.MaxPending < 1 || p.MaxPending > 20 {
		return errors.New("invalid PAYMENT_MAX_PENDING_PER_USER (allowed 1-20)")
	}
	window, err := strconv.Atoi(env("PAYMENT_PENDING_LIMIT_SECONDS", strconv.Itoa(DefaultPaymentPendingSeconds)))
	if err != nil || window < MinPaymentPendingSeconds || window > MaxPaymentPendingSeconds {
		return fmt.Errorf("invalid PAYMENT_PENDING_LIMIT_SECONDS (allowed %d-%d)", MinPaymentPendingSeconds, MaxPaymentPendingSeconds)
	}
	p.PendingWindow = time.Duration(window) * time.Second
	if p.TestPriceCents, err = payment.ParseAmount(env("PAYMENT_TEST_PRICE", "0.01")); err != nil || p.TestPriceCents < 1 || p.TestPriceCents > MaxPaymentTestPriceCents {
		return errors.New("invalid PAYMENT_TEST_PRICE (yuan with at most two decimals, 0.01-1000.00)")
	}
	if raw := strings.TrimSpace(os.Getenv("PAYMENT_TEST_PRICE_EMAILS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			email, ok := normalizeEmail(item)
			if !ok {
				return errors.New("invalid PAYMENT_TEST_PRICE_EMAILS (comma-separated email addresses)")
			}
			p.TestEmails = append(p.TestEmails, email)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PAYMENT_NOTIFY_ALLOWED_IPS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			prefix, e := parsePrefix(strings.TrimSpace(item))
			if e != nil {
				return errors.New("invalid PAYMENT_NOTIFY_ALLOWED_IPS (comma-separated IP addresses or CIDR ranges)")
			}
			p.NotifyAllowed = append(p.NotifyAllowed, prefix)
		}
	}
	if p.GatewayURL, err = gatewayOrigin(env("PAYMENT_GATEWAY_URL", DefaultPaymentGateway), production); err != nil {
		return err
	}
	p.Channel = strings.TrimSpace(os.Getenv("PAYMENT_CHANNEL"))
	p.Secret = os.Getenv("PAYMENT_SECRET")
	if raw := strings.TrimSpace(os.Getenv("PAYMENT_NOTIFY_URL")); raw != "" {
		if p.NotifyURL, err = notifyURL(raw, production); err != nil {
			return err
		}
	}
	if p.Channel != "" && !paymentChannel.MatchString(p.Channel) {
		return errors.New("invalid PAYMENT_CHANNEL (letters, digits, '-' or '_')")
	}
	if p.Secret != "" && (len(p.Secret) < minPaymentSecretLength || len(p.Secret) > maxPaymentChannelOrSecretSize || strings.Contains(p.Secret, "replace-") || strings.ContainsAny(p.Secret, " \t\r\n\x00")) {
		return errors.New("invalid PAYMENT_SECRET")
	}
	if !p.Enabled {
		return nil
	}
	var missing []string
	for key, value := range map[string]string{"PAYMENT_CHANNEL": p.Channel, "PAYMENT_SECRET": p.Secret, "PAYMENT_NOTIFY_URL": p.NotifyURL, "WEBSITE_BASE_URL": c.WebsiteBaseURL} {
		if value == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("PAYMENT_ENABLED=true requires %s", strings.Join(missing, ", "))
	}
	if production && p.TestPrice && len(p.TestEmails) == 0 {
		return errors.New("production PAYMENT_TEST_PRICE_ENABLED=true requires PAYMENT_TEST_PRICE_EMAILS")
	}
	return nil
}

// gatewayOrigin accepts a root origin only; production requires HTTPS.
func gatewayOrigin(raw string, production bool) (string, error) {
	invalid := errors.New("invalid PAYMENT_GATEWAY_URL (absolute root origin such as https://pay.example.com)")
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" || !isASCII(u.Host) {
		return "", invalid
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && !production:
	default:
		return "", errors.New("PAYMENT_GATEWAY_URL must use HTTPS (plain HTTP only outside production)")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// notifyURL accepts an absolute callback address with a path and nothing else.
func notifyURL(raw string, production bool) (string, error) {
	invalid := errors.New("invalid PAYMENT_NOTIFY_URL (absolute URL of /api/v1/payments/notify, without query or fragment)")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" || u.Path == "" || u.Path == "/" || !isASCII(raw) {
		return "", invalid
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && !production:
	default:
		return "", errors.New("PAYMENT_NOTIFY_URL must use HTTPS in production")
	}
	return u.String(), nil
}

func parsePrefix(item string) (netip.Prefix, error) {
	if strings.Contains(item, "/") {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(item)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// normalizeEmail follows the account identity rule: trimmed and lower-cased.
func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	at := strings.LastIndex(email, "@")
	return email, at > 0 && at < len(email)-1 && len(email) <= 254 && isASCII(email) && !strings.ContainsAny(email, " \t\r\n,<>")
}
