package managed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	mu        sync.Mutex
	status    Status
	reports   []Counters
	dropNext  bool
	failNext  bool
	loggedOut bool
	up        atomic.Int64
	down      atomic.Int64
	stops     atomic.Int64
	meter     *Meter
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	expiry := time.Now().Add(time.Hour)
	f := &fixture{status: Status{SessionID: "fixture-session", ExpiresAt: expiry, SubscriptionExpiresAt: &expiry, SessionIdleTimeout: 180, ProfileVersion: strings.Repeat("a", 64), CanConnect: true, RemainingBytes: 1000, TotalBytes: 1000, ReportInterval: 60, LeaseSeconds: 90, MeteringSource: "client_reported", RatePermille: 1000}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 43) {
			t.Error("missing test bearer")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/session":
			f.refreshAuthorizationLocked()
			_ = json.NewEncoder(w).Encode(f.status)
		case "/traffic":
			if f.failNext {
				f.failNext = false
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"code":"test_unavailable"}}`))
				return
			}
			var in Counters
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				t.Error("bad test report")
				w.WriteHeader(400)
				return
			}
			f.reports = append(f.reports, in)
			if in.Sequence == f.status.LastSequence+1 {
				f.status.RemainingBytes = max(0, f.status.RemainingBytes-(in.UploadBytes-f.status.UploadBytes)-(in.DownloadBytes-f.status.DownloadBytes))
				f.status.LastSequence = in.Sequence
				f.status.UploadBytes = in.UploadBytes
				f.status.DownloadBytes = in.DownloadBytes
			} else if in.Sequence != f.status.LastSequence || in.UploadBytes != f.status.UploadBytes || in.DownloadBytes != f.status.DownloadBytes {
				t.Errorf("out of sequence: %+v / %+v", in, f.status)
				w.WriteHeader(409)
				return
			}
			if f.status.RemainingBytes == 0 {
				f.status.CanConnect = false
				f.status.Reason = "quota_exhausted"
			}
			if f.dropNext {
				f.dropNext = false
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
				} else {
					_ = connection.Close()
				}
				return
			}
			f.refreshAuthorizationLocked()
			_ = json.NewEncoder(w).Encode(map[string]any{"session": f.status})
		case "/logout":
			f.loggedOut = true
			_, _ = w.Write([]byte(`{"logged_out":true}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, strings.Repeat("t", 43), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.meter, err = Start(context.Background(), client, func() (int64, int64) { return f.up.Load(), f.down.Load() }, func() { f.stops.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		f.meter.Close(ctx, false)
	})
	return f
}

func (f *fixture) refreshAuthorizationLocked() {
	f.status.ServerTime = time.Now().UTC()
	f.status.AuthorizationExpiresAt = nil
	if f.status.CanConnect {
		deadline := f.status.ServerTime.Add(90 * time.Second)
		for _, limit := range []time.Time{f.status.ExpiresAt, *f.status.SubscriptionExpiresAt} {
			if limit.Before(deadline) {
				deadline = limit
			}
		}
		f.status.AuthorizationExpiresAt = &deadline
	}
}

func TestMeterCumulativeAccountingAndZero(t *testing.T) {
	f := newFixture(t)
	if !f.meter.Status().CanConnect {
		t.Fatal("initial lease unavailable")
	}
	baseline := f.stops.Load()
	f.up.Store(250)
	f.down.Store(350)
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.meter.Status().RemainingBytes != 400 {
		t.Fatal("wrong remaining quota")
	}
	f.up.Store(450)
	f.down.Store(550)
	f.meter.sample()
	if f.meter.Status().CanConnect || f.stops.Load() <= baseline {
		t.Fatal("local zero did not stop the proxy")
	}
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status.RemainingBytes != 0 || f.status.UploadBytes != 450 || f.status.DownloadBytes != 550 {
		t.Fatal("bad server accounting")
	}
}

func TestMeterRetriesAnUncertainCommitWithoutDoubleCounting(t *testing.T) {
	f := newFixture(t)
	f.up.Store(100)
	f.down.Store(200)
	f.mu.Lock()
	f.dropNext = true
	f.mu.Unlock()
	if err := f.meter.Flush(context.Background()); err == nil {
		t.Fatal("dropped acknowledgement accepted")
	}
	if f.meter.Status().CanConnect {
		t.Fatal("network failure did not fail closed")
	}
	f.up.Store(150)
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	last := len(f.reports) - 1
	if f.reports[last] != f.reports[last-1] {
		t.Error("uncertain report was mutated instead of retried")
	}
	if f.status.RemainingBytes != 700 {
		t.Error("retry charged twice")
	}
	f.mu.Unlock()
	if f.meter.Status().RemainingBytes != 650 {
		t.Fatal("unreported bytes not reserved locally")
	}
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.meter.Status().RemainingBytes != 650 {
		t.Fatal("new delta was lost")
	}
}

func TestMeterLeaseExpiryAndCounterReset(t *testing.T) {
	f := newFixture(t)
	f.meter.mu.Lock()
	f.meter.deadline = time.Now().Add(-time.Second)
	f.meter.mu.Unlock()
	if f.meter.sample() || f.meter.Status().CanConnect {
		t.Fatal("expired lease allowed traffic")
	}
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.up.Store(100)
	f.meter.sample()
	f.up.Store(50)
	f.meter.sample()
	if err := f.meter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := f.meter.Status(); status.CanConnect || status.Reason != "invalid_core_counters" {
		t.Fatalf("reset counters revived a session: %+v", status)
	}
}

func TestMeterConcurrentFlushAndClose(t *testing.T) {
	f := newFixture(t)
	f.up.Store(100)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = f.meter.Flush(context.Background()) }()
	}
	wg.Wait()
	f.mu.Lock()
	if f.status.RemainingBytes != 900 {
		t.Error("concurrent flush double charged")
	}
	f.mu.Unlock()
	f.down.Store(50)
	f.meter.Close(context.Background(), true)
	f.mu.Lock()
	if !f.loggedOut || f.status.DownloadBytes != 50 {
		t.Error("final flush/logout missing")
	}
	count := len(f.reports)
	f.mu.Unlock()
	stops := f.stops.Load()
	if err := f.meter.Flush(context.Background()); err == nil {
		t.Fatal("closed session accepted a late flush")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reports) != count || f.stops.Load() != stops {
		t.Fatal("closed callback could affect a newer session")
	}
}

func TestClientRejectsUnsafeURLsRedirectsAndUntrustedTLS(t *testing.T) {
	for _, base := range []string{"http://demo.hyshentou.cn/api/v1/client", "https://user:pass@example.invalid/api", "https://example.invalid/?token=x", "file:///tmp/private"} {
		if _, err := NewClient(base, strings.Repeat("t", 43), nil); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewClient(redirect.URL, strings.Repeat("t", 43), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Status(context.Background()); err == nil || hits.Load() != 0 {
		t.Fatal("redirect followed or accepted")
	}
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer untrusted.Close()
	client, err = NewClient(untrusted.URL, strings.Repeat("t", 43), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Status(context.Background()); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}
