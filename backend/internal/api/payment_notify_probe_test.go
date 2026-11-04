package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/payment"
)

// The 2026-11-03 real-gateway incident: PAYMENT_NOTIFY_URL named the old test backend
// (demo.hyshentou.cn). The gateway posted every callback there, got 200 and never
// retried; this backend saw no callback, the result page stayed on "paid, confirming"
// and the reconciler moved each order to review (callback_missing). The callback route
// now answers a signed self-check, and a wrong address is reported by the API at start
// and by the doctor.
func TestPaymentNotifyURLSelfCheck(t *testing.T) {
	e := newPayEnv(t, nil)
	this := httptest.NewServer(e.router)
	t.Cleanup(this.Close)
	if got := payment.CheckNotifyURL(t.Context(), nil, this.URL+paymentNotifyPath, e.cfg.Payment.Secret); got != payment.ProbeOK {
		t.Fatalf("this backend's own callback address: %s", got)
	}
	// The probe stores nothing and is not mistaken for a callback.
	var logged int64
	if err := e.db.Model(&model.PaymentNotification{}).Count(&logged).Error; err != nil || logged != 0 {
		t.Fatalf("probe wrote %d callback records (%v)", logged, err)
	}
	// Malformed challenges are refused; a deployment with payments off has no probe.
	for _, query := range []string{"", "?probe=", "?probe=xyz", "?probe=" + strings.Repeat("A", 32)} {
		w := e.call(nil, "GET", paymentNotifyPath+query, nil, "")
		if w.Code != 400 {
			t.Fatalf("probe %q: %d", query, w.Code)
		}
	}
	off := newPayEnv(t, func(c *config.Config) { c.Payment.Enabled = false })
	if w := off.call(nil, "GET", paymentNotifyPath+"?probe="+strings.Repeat("a", 32), nil, ""); w.Code != 404 {
		t.Fatalf("probe with payments off: %d", w.Code)
	}
	// The answer is bound to the secret: a deployment with another secret fails it.
	other := newPayEnv(t, func(c *config.Config) { c.Payment.Secret = "another-gateway-secret" })
	otherServer := httptest.NewServer(other.router)
	t.Cleanup(otherServer.Close)
	if got := payment.CheckNotifyURL(t.Context(), nil, otherServer.URL+paymentNotifyPath, e.cfg.Payment.Secret); got != payment.ProbeWrongTarget {
		t.Fatalf("another deployment's address: %s", got)
	}

	// The old deployment: 200 to every callback POST, 404 to the probe.
	var oldCallbacks int
	var mu sync.Mutex
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			oldCallbacks++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "处理成功"})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(old.Close)
	wrong := e.cfg
	wrong.Payment.NotifyURL = old.URL + paymentNotifyPath

	// Doctor: the report names the host and the result, never the full address.
	var report operations.ReadinessReport
	operations.CheckPaymentNotify(t.Context(), wrong, &report)
	if report.Payment.NotifySelfCheck != payment.ProbeWrongTarget || !strings.Contains(strings.Join(report.Warnings, ","), "payment_notify_url_not_this_backend") || report.Payment.NotifyHost != strings.TrimPrefix(old.URL, "http://") {
		t.Fatalf("doctor on the wrong address: %+v %v", report.Payment, report.Warnings)
	}
	report = operations.ReadinessReport{}
	right := e.cfg
	right.Payment.NotifyURL = this.URL + paymentNotifyPath
	operations.CheckPaymentNotify(t.Context(), right, &report)
	if report.Payment.NotifySelfCheck != payment.ProbeOK || len(report.Warnings) != 0 {
		t.Fatalf("doctor on the right address: %+v %v", report.Payment, report.Warnings)
	}

	// API start: the watcher warns about the wrong address (after its quiet early
	// retries) and confirms the right one.
	savedDelays := notifyCheckDelays
	notifyCheckDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { notifyCheckDelays = savedDelays })
	watch := func(cfg config.Config) string {
		ctx, cancel := context.WithCancel(t.Context())
		lines := make(chan string, 4)
		done := make(chan struct{})
		go func() {
			defer close(done)
			WatchNotifyURL(ctx, cfg, func(format string, args ...any) { lines <- fmt.Sprintf(format, args...) })
		}()
		defer func() { cancel(); <-done }()
		select {
		case line := <-lines:
			return line
		case <-time.After(15 * time.Second):
			t.Fatal("notify URL watcher logged nothing")
			return ""
		}
	}
	if line := watch(wrong); !strings.HasPrefix(line, "WARNING: PAYMENT_NOTIFY_URL") || !strings.Contains(line, payment.ProbeWrongTarget) || strings.Contains(line, wrong.Payment.NotifyURL) {
		t.Fatalf("watcher on the wrong address: %q", line)
	}
	if line := watch(right); !strings.Contains(line, "self-check passed") {
		t.Fatalf("watcher on the right address: %q", line)
	}
	if oldCallbacks != 0 {
		t.Fatalf("self-check posted %d callbacks", oldCallbacks)
	}
}
