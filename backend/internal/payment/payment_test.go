package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// R1-01: the algorithm of 支付接入技术文档.md "签名算法" (MD5 of trade_no followed by the
// secret, lower-case hex), as every code sample in the document implements it. The
// document's worked example prints c237aabf7d689f6bbdded44662c8459d for this input, but
// MD5("1234567890123456789056Hhtcc1yfq39WhvsqMe") is becd5f2a12fcf4746840db6870131f3d
// (md5(1), Python hashlib and Go agree), so that printed value cannot come from the
// stated algorithm. Recorded in handoff_payment_account.md 18; the real gateway decides
// at deployment (U-01: a wrong signature is refused with 401).
func TestSignFollowsGatewayAlgorithm(t *testing.T) {
	if got := Sign("12345678901234567890", "56Hhtcc1yfq39WhvsqMe"); got != "becd5f2a12fcf4746840db6870131f3d" {
		t.Fatalf("sign %s", got)
	}
	if Sign("12345678901234567890", "other") == Sign("12345678901234567890", "56Hhtcc1yfq39WhvsqMe") {
		t.Fatal("secret not part of the signature")
	}
}

func TestAmounts(t *testing.T) {
	for text, cents := range map[string]int64{"12": 1200, "12.0": 1200, "12.00": 1200, "19.9": 1990, "0.01": 1, "0": 0, "99999.99": 9999999} {
		if got, err := ParseAmount(text); err != nil || got != cents {
			t.Fatalf("ParseAmount(%q) = %d, %v", text, got, err)
		}
	}
	for _, text := range []string{"", "-1", "+1", "1e3", "12.001", "012", "1,00", " 1", "1.", ".5", "0x10", "100000000.00", "NaN"} {
		if _, err := ParseAmount(text); err == nil {
			t.Fatalf("ParseAmount accepted %q", text)
		}
	}
	for cents, text := range map[int64]string{0: "0.00", 1: "0.01", 1200: "12.00", 1990: "19.90", 2001: "20.01"} {
		if got := FormatAmount(cents); got != text {
			t.Fatalf("FormatAmount(%d) = %s", cents, got)
		}
	}
	// The gateway serialises money as a float (FAQ Q7).
	for text, cents := range map[string]int64{"20.009999999999998": 2001, "20.010000000000002": 2001, "12": 1200, "1.2e1": 1200, "0.30000000000000004": 30} {
		if got, err := ParseCallbackAmount(text); err != nil || got != cents {
			t.Fatalf("ParseCallbackAmount(%q) = %d, %v", text, got, err)
		}
	}
	for _, text := range []string{"20.004", "20.0051", "-20.01", "0", "0.000001", "1e-2", "0x1p4", "1/2", "", "99999999999.99"} {
		if _, err := ParseCallbackAmount(text); err == nil {
			t.Fatalf("ParseCallbackAmount accepted %q", text)
		}
	}
}

func TestTradeNumbers(t *testing.T) {
	now := time.Date(2026, 10, 22, 23, 59, 0, 0, time.UTC)
	seen := map[string]bool{}
	for range 200 {
		n, err := NewTradeNo(now)
		if err != nil || !ValidTradeNo(n) || !strings.HasPrefix(n, "20261022") || seen[n] {
			t.Fatalf("trade no %q %v", n, err)
		}
		seen[n] = true
	}
	for _, bad := range []string{"", "1234", "1234567890123456789a", "123456789012345678901"} {
		if ValidTradeNo(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// The submit URL follows the documented parameter order and percent-encoding.
func TestSubmitURL(t *testing.T) {
	g := Gateway{BaseURL: "https://pay.example.test/", Channel: "1001", Secret: "56Hhtcc1yfq39WhvsqMe"}
	raw := g.SubmitURL(SubmitRequest{Type: TypeAlipay, TradeNo: "12345678901234567890", NotifyURL: "https://api.example.com/notify", ReturnURL: "https://app.example.com/pay/result?order=abc", Name: "月 套餐", AmountCents: 1200})
	want := "https://pay.example.test/submit?channel=1001&type=alipay&trade_no=12345678901234567890&notify_url=https%3A%2F%2Fapi.example.com%2Fnotify&return_url=https%3A%2F%2Fapp.example.com%2Fpay%2Fresult%3Forder%3Dabc&name=%E6%9C%88%20%E5%A5%97%E9%A4%90&money=12.00&sign=becd5f2a12fcf4746840db6870131f3d"
	if raw != want {
		t.Fatalf("submit URL\n got %s\nwant %s", raw, want)
	}
	u, _ := url.Parse(raw)
	if q := u.Query(); q.Get("name") != "月 套餐" || q.Get("return_url") != "https://app.example.com/pay/result?order=abc" {
		t.Fatal("parameters do not round-trip")
	}
}

func TestStatusClient(t *testing.T) {
	var reply func(w http.ResponseWriter, r *http.Request)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reply(w, r) }))
	defer server.Close()
	g := Gateway{BaseURL: server.URL}
	ctx := context.Background()
	trade := "20261022000000000001"
	cases := []struct {
		name    string
		handler func(w http.ResponseWriter, r *http.Request)
		want    StatusResult
		err     bool
	}{
		{"paid", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/order/status/"+trade || r.Method != http.MethodGet {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"status":"paid","expired":false}`))
		}, StatusResult{Status: StatusPaid}, false},
		{"timeout", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"timeout","expired":true}`))
		}, StatusResult{Status: StatusTimeout, Expired: true}, false},
		{"404", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"未找到对应订单"}`))
		}, StatusResult{Status: StatusNotFound, Expired: true}, false},
		{"500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, StatusResult{}, true},
		{"unknown status", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"refunded"}`)) }, StatusResult{}, true},
		{"missing status", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"expired":true}`)) }, StatusResult{}, true},
		{"not json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>`)) }, StatusResult{}, true},
		{"oversized", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"paid","pad":"` + strings.Repeat("x", 5000) + `"}`))
		}, StatusResult{}, true},
		{"redirect not followed", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/elsewhere") {
				_, _ = w.Write([]byte(`{"status":"paid"}`))
				return
			}
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}, StatusResult{}, true},
	}
	for _, tc := range cases {
		reply = tc.handler
		got, err := g.Status(ctx, trade)
		if (err != nil) != tc.err || got != tc.want {
			t.Fatalf("%s: %+v %v", tc.name, got, err)
		}
	}
	if _, err := g.Status(ctx, "../../etc"); err == nil {
		t.Fatal("malformed trade number queried")
	}
}

func TestParseNotification(t *testing.T) {
	ok := map[string]int64{
		`{"trade_no":"12345678901234567890","name":"月套餐","money":12.00,"trade_status":"TRADE_SUCCESS"}`:                    1200,
		`{"trade_no":"12345678901234567890","name":"月套餐","money":20.009999999999998,"trade_status":"TRADE_SUCCESS"}`:       2001,
		`{"trade_no":"12345678901234567890","name":"月套餐","money":"19.90","trade_status":"TRADE_FAILED"}`:                   1990,
		`{"trade_no":"12345678901234567890","name":"x","money":1,"trade_status":"TRADE_SUCCESS","extra":{"ignored":true}}`: 100,
	}
	for body, cents := range ok {
		n, err := ParseNotification([]byte(body))
		if err != nil || n.Cents != cents {
			t.Fatalf("%s: %+v %v", body, n, err)
		}
	}
	for _, body := range []string{
		``, `[]`, `null`, `{}`,
		`{"trade_no":"12345678901234567890","name":"x","money":12.001,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","name":"x","money":-1,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","name":"x","money":0,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","name":"x","money":1e20,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","name":"x","money":"abc","trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","name":"x","money":12,"trade_status":"SUCCESS"}`,
		`{"trade_no":"1234","name":"x","money":12,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":12345678901234567890,"name":"x","money":12,"trade_status":"TRADE_SUCCESS"}`,
		`{"name":"x","money":12,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","money":12,"trade_status":"TRADE_SUCCESS"}`,
		`{"trade_no":"12345678901234567890","name":"x","money":12,"trade_status":"TRADE_SUCCESS"} {}`,
	} {
		if _, err := ParseNotification([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	n, err := ParseNotification([]byte(`{"trade_no":"12345678901234567890","name":"` + strings.Repeat("长", 100) + `","money":12,"trade_status":"TRADE_SUCCESS"}`))
	if err != nil || len(n.Name) > 80 || !strings.HasPrefix(n.Name, "长") {
		t.Fatalf("long name not clipped on a rune boundary: %q %v", n.Name, err)
	}
}
