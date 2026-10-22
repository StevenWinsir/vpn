package payment

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Payment types accepted by the gateway's type parameter.
const (
	TypeWeChat = "wxpay"
	TypeAlipay = "alipay"
)

// ValidType reports whether t is a gateway payment type. The gateway defaults to
// wxpay when type is missing; this project always sends it explicitly.
func ValidType(t string) bool { return t == TypeWeChat || t == TypeAlipay }

// Gateway order status values (GET /api/order/status/:trade_no).
const (
	StatusPending  = "pending"
	StatusPaid     = "paid"
	StatusTimeout  = "timeout"
	StatusNotFound = "not_found"
)

// Callback trade_status values.
const (
	TradeSuccess = "TRADE_SUCCESS"
	TradeFailed  = "TRADE_FAILED"
)

// OrderLifetime is fixed by the gateway: an unpaid order is cancelled after 2 minutes.
const OrderLifetime = 2 * time.Minute

const (
	maxResponseBytes = 4 << 10
	requestTimeout   = 5 * time.Second
)

var tradeNoPattern = regexp.MustCompile(`^[0-9]{20}$`)

// ValidTradeNo reports whether s has this project's trade_no shape (20 digits).
func ValidTradeNo(s string) bool { return tradeNoPattern.MatchString(s) }

// NewTradeNo returns a 20-digit order number: the UTC date (8 digits) and 12 digits
// from crypto/rand, so the number cannot be predicted before the order exists.
func NewTradeNo(now time.Time) (string, error) {
	limit := big.NewInt(1_000_000_000_000)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	return now.UTC().Format("20060102") + fmt.Sprintf("%012d", n.Int64()), nil
}

// Sign is the gateway signature: lower-case hex MD5 of trade_no followed by the secret.
// It covers only trade_no; amounts must therefore be verified on the callback.
func Sign(tradeNo, secret string) string {
	sum := md5.Sum([]byte(tradeNo + secret))
	return hex.EncodeToString(sum[:])
}

type Gateway struct {
	BaseURL, Channel, Secret string
	HTTP                     *http.Client
}

// SubmitRequest is one gateway order. Amounts are integer cents.
type SubmitRequest struct {
	Type, TradeNo, NotifyURL, ReturnURL, Name string
	AmountCents                               int64
}

// queryEscape percent-encodes like the reference client (urlencode with quote): spaces
// become %20, never '+'.
func queryEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// SubmitURL builds the browser redirect to GET /submit, parameters in the documented
// order. The URL contains the signature: never log it.
func (g Gateway) SubmitURL(r SubmitRequest) string {
	params := [][2]string{
		{"channel", g.Channel},
		{"type", r.Type},
		{"trade_no", r.TradeNo},
		{"notify_url", r.NotifyURL},
		{"return_url", r.ReturnURL},
		{"name", r.Name},
		{"money", FormatAmount(r.AmountCents)},
		{"sign", Sign(r.TradeNo, g.Secret)},
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(g.BaseURL, "/"))
	b.WriteString("/submit?")
	for i, p := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p[0])
		b.WriteByte('=')
		b.WriteString(queryEscape(p[1]))
	}
	return b.String()
}

// StatusResult is the gateway's view of one order.
type StatusResult struct {
	Status  string
	Expired bool
}

var ErrGateway = errors.New("payment gateway unavailable")

func (g Gateway) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Status queries GET /api/order/status/:trade_no. A 404 is reported as not_found.
// Redirects are never followed and the body is bounded.
func (g Gateway) Status(ctx context.Context, tradeNo string) (StatusResult, error) {
	if !ValidTradeNo(tradeNo) {
		return StatusResult{}, ErrGateway
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.BaseURL, "/")+"/api/order/status/"+tradeNo, nil)
	if err != nil {
		return StatusResult{}, ErrGateway
	}
	req.Header.Set("Accept", "application/json")
	resp, err := g.client().Do(req)
	if err != nil {
		return StatusResult{}, ErrGateway
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return StatusResult{}, ErrGateway
	}
	if resp.StatusCode == http.StatusNotFound {
		return StatusResult{Status: StatusNotFound, Expired: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return StatusResult{}, ErrGateway
	}
	var out struct {
		Status  *string `json:"status"`
		Expired bool    `json:"expired"`
	}
	if json.Unmarshal(body, &out) != nil || out.Status == nil {
		return StatusResult{}, ErrGateway
	}
	switch *out.Status {
	case StatusPending, StatusPaid, StatusTimeout, StatusNotFound:
		return StatusResult{Status: *out.Status, Expired: out.Expired}, nil
	}
	return StatusResult{}, ErrGateway
}

// Notification is one parsed gateway callback. MoneyText is the amount exactly as
// received; Cents is valid only when Err is nil.
type Notification struct {
	TradeNo, Name, MoneyText, TradeStatus string
	Cents                                 int64
}

var ErrNotification = errors.New("invalid payment notification")

// ParseNotification reads the callback JSON. money is accepted as a JSON number or a
// numeric string and converted to cents without floating point. Unknown fields are
// ignored so a gateway that adds fields keeps working; the four documented fields are
// required. On error the returned value still carries whatever could be read, for the
// callback log.
func ParseNotification(body []byte) (Notification, error) {
	var raw struct {
		TradeNo     json.RawMessage `json:"trade_no"`
		Name        json.RawMessage `json:"name"`
		Money       json.RawMessage `json:"money"`
		TradeStatus json.RawMessage `json:"trade_status"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&raw); err != nil {
		return Notification{}, ErrNotification
	}
	if _, err := dec.Token(); err != io.EOF {
		return Notification{}, ErrNotification
	}
	var n Notification
	strField := func(r json.RawMessage, dest *string) bool { return len(r) > 0 && json.Unmarshal(r, dest) == nil }
	okTrade := strField(raw.TradeNo, &n.TradeNo)
	okName := strField(raw.Name, &n.Name)
	okStatus := strField(raw.TradeStatus, &n.TradeStatus)
	okMoney := false
	if len(raw.Money) > 0 {
		text := string(raw.Money)
		if raw.Money[0] == '"' {
			okMoney = json.Unmarshal(raw.Money, &text) == nil
		} else {
			okMoney = true
		}
		n.MoneyText = text
	}
	n.TradeNo, n.Name, n.MoneyText, n.TradeStatus = clip(n.TradeNo, 32), clip(n.Name, 80), clip(n.MoneyText, 32), clip(n.TradeStatus, 24)
	if !okTrade || !okName || !okStatus || !okMoney {
		return n, ErrNotification
	}
	cents, err := ParseCallbackAmount(n.MoneyText)
	if err != nil || cents <= 0 || !ValidTradeNo(n.TradeNo) || (n.TradeStatus != TradeSuccess && n.TradeStatus != TradeFailed) {
		return n, ErrNotification
	}
	n.Cents = cents
	return n, nil
}

// clip bounds a logged field to max bytes without splitting a UTF-8 sequence.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
