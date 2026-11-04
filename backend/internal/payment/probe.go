package payment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
)

// Callback address self-check. The gateway posts each callback once, to whatever
// notify_url the order carried, and treats any 2xx as delivered. A PAYMENT_NOTIFY_URL
// that points at another server (an old deployment, a typo, the website) therefore loses
// every callback without a trace on this backend: the customer has paid, the gateway
// reports "paid", and the order can only end in manual review. The probe proves that
// the configured address reaches a backend holding this deployment's gateway secret.

// NotifyPath is the callback route. PAYMENT_NOTIFY_URL must end with it.
const NotifyPath = "/api/v1/payments/notify"

// Self-check results.
const (
	ProbeOK = "ok"
	// ProbeUnreachable: no answer (DNS, connection, TLS, timeout or a 5xx).
	ProbeUnreachable = "unreachable"
	// ProbeWrongTarget: a server answered, but not this backend (404, a redirect, a
	// different or older application, or another secret).
	ProbeWrongTarget = "wrong_target"
)

var probeChallenge = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidProbeChallenge reports whether s is a probe challenge (32 lower-case hex digits).
func ValidProbeChallenge(s string) bool { return probeChallenge.MatchString(s) }

// NewProbeChallenge returns a fresh random challenge.
func NewProbeChallenge() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ProbeAnswer is HMAC-SHA256 of the challenge under a key derived from the gateway
// secret. It reveals nothing about the secret and cannot be turned into a gateway
// signature (MD5 of trade_no and secret).
func ProbeAnswer(secret, challenge string) string {
	key := sha256.Sum256([]byte("asterlink payment notify probe key\x00" + secret))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte("notify-probe:" + challenge))
	return hex.EncodeToString(mac.Sum(nil))
}

// CheckNotifyURL asks the callback address to answer a fresh challenge (GET on the
// callback route) and reports ProbeOK only when the answer proves the same secret.
// Redirects are never followed: the gateway's POST would not survive one either.
func CheckNotifyURL(ctx context.Context, client *http.Client, notifyURL, secret string) string {
	challenge, err := NewProbeChallenge()
	if err != nil {
		return ProbeUnreachable
	}
	u, err := url.Parse(notifyURL)
	if err != nil {
		return ProbeWrongTarget
	}
	u.RawQuery = url.Values{"probe": {challenge}}.Encode()
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	probeClient := *client
	probeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ProbeWrongTarget
	}
	req.Header.Set("Accept", "application/json")
	resp, err := probeClient.Do(req)
	if err != nil {
		return ProbeUnreachable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if resp.StatusCode >= 500 {
		return ProbeUnreachable
	}
	if err != nil || len(body) > maxResponseBytes || resp.StatusCode != http.StatusOK {
		return ProbeWrongTarget
	}
	var out struct {
		Probe string `json:"probe"`
	}
	if json.Unmarshal(body, &out) != nil || !hmac.Equal([]byte(out.Probe), []byte(ProbeAnswer(secret, challenge))) {
		return ProbeWrongTarget
	}
	return ProbeOK
}
