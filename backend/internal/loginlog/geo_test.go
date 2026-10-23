package loginlog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/store"
)

func TestLocal(t *testing.T) {
	for ip, want := range map[string]bool{
		"10.0.0.1": true, "172.16.5.4": true, "192.168.1.1": true, "127.0.0.1": true, "::1": true, "169.254.1.1": true, "fe80::1": true, "fd00::1": true,
		"100.64.0.1": true, "0.0.0.0": true, "::ffff:10.0.0.1": true,
		"8.8.8.8": false, "203.0.113.5": false, "2001:db8::1": false, "": false, "not-an-ip": false,
	} {
		if Local(ip) != want {
			t.Errorf("Local(%q) = %t, want %t", ip, !want, want)
		}
	}
	if Valid("") || Valid("x") || !Valid("8.8.8.8") || !Valid("2001:db8::1") {
		t.Error("Valid is wrong")
	}
}

func TestParseLocation(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
		fail bool
	}{
		"ip-api":        {`{"status":"success","country":"Japan","regionName":"Tokyo","city":"Tokyo","isp":"NTT"}`, "Japan Tokyo NTT", false},
		"ipapi.co":      {`{"country_name":"Germany","region":"Hesse","city":"Frankfurt","org":"Hetzner"}`, "Germany Hesse Frankfurt Hetzner", false},
		"ipwho":         {`{"success":true,"country":"China","region":"Beijing","city":"Beijing","connection":{"isp":"China Telecom"}}`, "China Beijing China Telecom", false},
		"nested data":   {`{"code":0,"data":{"country":"中国","province":"广东","city":"深圳","isp":"电信"}}`, "中国 广东 深圳 电信", false},
		"place only":    {`{"country":"France"}`, "France", false},
		"control text":  {"{\"country\":\"Fr\\u0000ance\\n\",\"city\":\"Paris\"}", "France Paris", false},
		"failed":        {`{"status":"fail","message":"private range"}`, "", true},
		"success=false": {`{"success":false,"country":"X"}`, "", true},
		"error string":  {`{"error":"rate limit"}`, "", true},
		"error true":    {`{"error":true,"reason":"RateLimited"}`, "", true},
		"no place":      {`{"isp":"x","ip":"1.1.1.1"}`, "", true},
		"not json":      {`<html>`, "", true},
		"array":         {`[]`, "", true},
	} {
		got, err := ParseLocation([]byte(tc.body))
		if tc.fail != (err != nil) || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q fail=%t", name, got, err, tc.want, tc.fail)
		}
	}
	long := `{"country":"` + strings.Repeat("长", 300) + `"}`
	if got, err := ParseLocation([]byte(long)); err != nil || len([]rune(got)) != 120 {
		t.Errorf("long text: %d runes, %v", len([]rune(got)), err)
	}
}

type geoEnv struct {
	db     *gorm.DB
	cfg    config.Config
	worker *Worker
	now    time.Time
}

func newGeoEnv(t *testing.T, lookupURL string) *geoEnv {
	t.Helper()
	dsn := os.Getenv("NATIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("run with NATIVE_TEST_DSN (see scripts/test-native.py)")
	}
	cfg := config.Config{Env: "test", Schema: "geo_" + strings.ReplaceAll(uuid.NewString(), "-", ""), MigrationTimeout: time.Minute}
	cfg.Geo = config.Geo{URLTemplate: lookupURL, RequestsPerMinute: 600}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: cfg.Schema + "."}})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	t.Cleanup(func() { _ = db.Exec(`DROP SCHEMA IF EXISTS "` + cfg.Schema + `" CASCADE`).Error })
	if err = store.Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	e := &geoEnv{db: db, cfg: cfg, now: time.Now().UTC().Truncate(time.Second)}
	e.worker = New(db, cfg, func(string, ...any) {})
	e.worker.Now = func() time.Time { return e.now }
	return e
}

func (e *geoEnv) event(t *testing.T, ip string) model.LoginEvent {
	t.Helper()
	ev := model.LoginEvent{ID: uuid.NewString(), UserID: uuid.NewString(), Channel: "web", IP: ip, Result: "success", OccurredAt: e.now, GeoStatus: model.GeoPending}
	if err := e.db.Create(&ev).Error; err != nil {
		t.Fatal(err)
	}
	return ev
}

func (e *geoEnv) reload(t *testing.T, id string) model.LoginEvent {
	t.Helper()
	var ev model.LoginEvent
	if err := e.db.First(&ev, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return ev
}

func (e *geoEnv) resolve(t *testing.T) int {
	t.Helper()
	n, err := e.worker.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type geoService struct {
	*httptest.Server
	calls  atomic.Int32
	tokens chan string
}

func newGeoService(t *testing.T, handler func(w http.ResponseWriter, ip string, n int)) *geoService {
	s := &geoService{tokens: make(chan string, 100)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.calls.Add(1))
		s.tokens <- r.Header.Get("Authorization")
		handler(w, strings.TrimPrefix(r.URL.Path, "/json/"), n)
	}))
	t.Cleanup(s.Close)
	return s
}

// R3-06: records resolve in the background, local addresses never leave the machine,
// and a cached address is not asked twice.
func TestWorkerResolvesCachesAndSkipsLocal(t *testing.T) {
	svc := newGeoService(t, func(w http.ResponseWriter, ip string, n int) {
		fmt.Fprintf(w, `{"country":"Japan","regionName":"Tokyo","city":"Tokyo","isp":"ISP-%s"}`, ip)
	})
	e := newGeoEnv(t, svc.URL+"/json/{ip}")
	e.worker.Geo.Token = "secret-token"
	first := e.event(t, "198.51.100.5")
	second := e.event(t, "198.51.100.5")
	other := e.event(t, "203.0.113.9")
	private := e.event(t, "10.0.0.8")
	v6 := e.event(t, "2001:db8::7")
	if n := e.resolve(t); n != 5 {
		t.Fatalf("resolved %d of 5", n)
	}
	for _, id := range []string{first.ID, second.ID} {
		if ev := e.reload(t, id); ev.GeoStatus != model.GeoDone || ev.GeoText != "Japan Tokyo ISP-198.51.100.5" || ev.GeoResolvedAt == nil {
			t.Fatalf("event %+v", ev)
		}
	}
	if ev := e.reload(t, other.ID); ev.GeoText != "Japan Tokyo ISP-203.0.113.9" {
		t.Fatalf("other: %+v", ev)
	}
	if ev := e.reload(t, v6.ID); ev.GeoStatus != model.GeoDone {
		t.Fatalf("ipv6: %+v", ev)
	}
	if ev := e.reload(t, private.ID); ev.GeoStatus != model.GeoDone || ev.GeoText != model.GeoLocal {
		t.Fatalf("private: %+v", ev)
	}
	// 3 distinct public addresses: one call each; the second event for .5 came from cache.
	if svc.calls.Load() != 3 {
		t.Fatalf("%d external calls, want 3", svc.calls.Load())
	}
	if token := <-svc.tokens; token != "Bearer secret-token" {
		t.Fatalf("token header: %q", token)
	}
	// Later, the same address is answered from the cache alone.
	again := e.event(t, "203.0.113.9")
	if e.resolve(t) != 1 || svc.calls.Load() != 3 || e.reload(t, again.ID).GeoStatus != model.GeoDone {
		t.Fatalf("cached address caused a call or was not resolved (%d calls)", svc.calls.Load())
	}
	// After the cache lifetime it is asked again.
	e.now = e.now.Add(model.GeoCacheTTL + time.Minute)
	last := e.event(t, "203.0.113.9")
	if e.resolve(t) != 1 || svc.calls.Load() != 4 || e.reload(t, last.ID).GeoStatus != model.GeoDone {
		t.Fatalf("expired cache not refreshed (%d calls)", svc.calls.Load())
	}
}

func TestWorkerWithoutServiceSkips(t *testing.T) {
	e := newGeoEnv(t, "")
	public, private, junk := e.event(t, "198.51.100.5"), e.event(t, "192.168.0.4"), e.event(t, "garbage")
	if e.resolve(t) != 3 {
		t.Fatal("not all settled")
	}
	if e.reload(t, public.ID).GeoStatus != model.GeoSkipped || e.reload(t, private.ID).GeoText != model.GeoLocal || e.reload(t, junk.ID).GeoStatus != model.GeoFailed {
		t.Fatal("wrong outcomes without a service")
	}
}

// A failing service is retried with growing delays, then the record fails and the
// failure is remembered for an hour so the service is not hammered.
func TestWorkerRetriesThenFails(t *testing.T) {
	svc := newGeoService(t, func(w http.ResponseWriter, ip string, n int) { w.WriteHeader(500) })
	e := newGeoEnv(t, svc.URL+"/json/{ip}")
	ev := e.event(t, "198.51.100.5")
	for attempt := 1; attempt < MaxAttempts; attempt++ {
		if e.resolve(t) != 0 {
			t.Fatal("record settled on a failed lookup")
		}
		got := e.reload(t, ev.ID)
		if got.GeoStatus != model.GeoPending || got.GeoAttempts != attempt || got.GeoNextAt == nil || !got.GeoNextAt.Equal(e.now.Add(time.Duration(attempt)*RetryStep)) {
			t.Fatalf("after attempt %d: %+v", attempt, got)
		}
		// Not due yet: nothing is claimed.
		if e.resolve(t) != 0 || int(svc.calls.Load()) != attempt {
			t.Fatalf("retried too early (%d calls)", svc.calls.Load())
		}
		e.now = e.now.Add(time.Duration(attempt)*RetryStep + time.Second)
	}
	if e.resolve(t) != 1 {
		t.Fatal("record did not fail after the last attempt")
	}
	if got := e.reload(t, ev.ID); got.GeoStatus != model.GeoFailed || svc.calls.Load() != MaxAttempts {
		t.Fatalf("final: %+v after %d calls", got, svc.calls.Load())
	}
	// The failure is cached: a new record for the address fails without a call.
	next := e.event(t, "198.51.100.5")
	if e.resolve(t) != 1 || svc.calls.Load() != MaxAttempts || e.reload(t, next.ID).GeoStatus != model.GeoFailed {
		t.Fatal("cached failure not used")
	}
	e.now = e.now.Add(model.GeoFailureTTL + time.Minute)
	later := e.event(t, "198.51.100.5")
	e.resolve(t)
	if svc.calls.Load() != MaxAttempts+1 || e.reload(t, later.ID).GeoAttempts != 1 {
		t.Fatalf("failure cache did not expire (%d calls)", svc.calls.Load())
	}
}

// Slow, oversized, redirecting and malformed answers are failures, never hangs.
func TestWorkerRejectsBadAnswers(t *testing.T) {
	for name, handler := range map[string]func(w http.ResponseWriter, ip string, n int){
		"redirect": func(w http.ResponseWriter, ip string, n int) {
			http.Redirect(w, &http.Request{}, "http://127.0.0.1:1/x", 302)
		},
		"oversized": func(w http.ResponseWriter, ip string, n int) {
			fmt.Fprintf(w, `{"country":"A","pad":%q}`, strings.Repeat("x", MaxResponseSize))
		},
		"html":   func(w http.ResponseWriter, ip string, n int) { fmt.Fprint(w, "<html>captcha</html>") },
		"status": func(w http.ResponseWriter, ip string, n int) { w.WriteHeader(429) },
	} {
		t.Run(name, func(t *testing.T) {
			svc := newGeoService(t, handler)
			e := newGeoEnv(t, svc.URL+"/json/{ip}")
			ev := e.event(t, "198.51.100.5")
			if e.resolve(t) != 0 || e.reload(t, ev.ID).GeoAttempts != 1 {
				t.Fatal("a bad answer was accepted")
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		svc := newGeoService(t, func(w http.ResponseWriter, ip string, n int) { <-release })
		defer close(release)
		e := newGeoEnv(t, svc.URL+"/json/{ip}")
		e.worker.Client.Timeout = 300 * time.Millisecond
		ev := e.event(t, "198.51.100.5")
		start := time.Now()
		if e.resolve(t) != 0 || e.reload(t, ev.ID).GeoAttempts != 1 || time.Since(start) > 5*time.Second {
			t.Fatalf("slow answer: %s", time.Since(start))
		}
	})
}

// Two workers share the table without resolving a record twice.
func TestWorkersDoNotShareARecord(t *testing.T) {
	svc := newGeoService(t, func(w http.ResponseWriter, ip string, n int) {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, `{"country":"Japan"}`)
	})
	e := newGeoEnv(t, svc.URL+"/json/{ip}")
	other := New(e.db, e.cfg, func(string, ...any) {})
	other.Now = e.worker.Now
	for i := 0; i < 8; i++ {
		e.event(t, fmt.Sprintf("198.51.100.%d", i+1))
	}
	done := make(chan int, 2)
	for _, w := range []*Worker{e.worker, other} {
		go func() {
			n, err := w.Resolve(context.Background())
			if err != nil {
				t.Error(err)
			}
			done <- n
		}()
	}
	if total := <-done + <-done; total != 8 || svc.calls.Load() != 8 {
		t.Fatalf("resolved %d records with %d calls", total, svc.calls.Load())
	}
}

func TestPrune(t *testing.T) {
	e := newGeoEnv(t, "")
	old := e.event(t, "198.51.100.1")
	fresh := e.event(t, "198.51.100.2")
	e.db.Model(&model.LoginEvent{}).Where("id = ?", old.ID).Update("occurred_at", e.now.Add(-model.LoginEventRetention-time.Hour))
	e.db.Create(&model.IPGeoCache{IP: "198.51.100.1", GeoText: "x", ResolvedAt: e.now, ExpiresAt: e.now.Add(-time.Minute)})
	e.db.Create(&model.IPGeoCache{IP: "198.51.100.2", GeoText: "x", ResolvedAt: e.now, ExpiresAt: e.now.Add(time.Hour)})
	for i, age := range []time.Duration{model.LoginEventRetention + time.Hour, time.Hour} {
		e.db.Create(&model.PaymentNotification{ID: uuid.NewString(), TradeNo: fmt.Sprint("T", i), MoneyText: "1.00", TradeStatus: "TRADE_SUCCESS", Name: "n", SourceIP: "203.0.113.10", Result: "ignored", ReceivedAt: e.now.Add(-age)})
	}
	n, err := e.worker.Prune(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	var left int64
	e.db.Model(&model.LoginEvent{}).Count(&left)
	var caches, notes int64
	e.db.Model(&model.IPGeoCache{}).Count(&caches)
	e.db.Model(&model.PaymentNotification{}).Count(&notes)
	if left != 1 || caches != 1 || notes != 1 || e.reload(t, fresh.ID).ID == "" {
		t.Fatalf("left: %d events, %d caches, %d notifications", left, caches, notes)
	}
}
