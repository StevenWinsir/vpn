package authmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

// The SMTP password and secret below are synthetic test values; they also serve as
// canaries that must never appear in database rows, logs or errors.
const canarySMTPPass = "canary-smtp-authorization-code"

var canarySecret = []byte("canary-auth-mail-secret-32-bytes-long!!")

// testClock is a manually advanced clock shared by services and workers.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type env struct {
	db    *gorm.DB
	cfg   config.Config
	clock *testClock
	logs  *logSink
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func (l *logSink) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// newEnv creates an isolated schema in the private PostgreSQL started by
// scripts/test-native.py. It never reads backend/.env or real SMTP settings.
func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("NATIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("run scripts/test-native.py for an isolated PostgreSQL instance")
	}
	if !strings.HasPrefix(dsn, "host=/") {
		t.Fatal("database tests require an explicitly supplied private Unix socket")
	}
	cfg := config.Config{
		Env: "test", Schema: "authmail_" + strings.ReplaceAll(uuid.NewString(), "-", ""), MigrationTimeout: time.Minute,
		AllowRegistration: true, SMTPHost: "smtp.test.invalid", SMTPPort: 465, SMTPSecure: true, SMTPUser: "sender@qq.com", SMTPPass: canarySMTPPass, SMTPFromName: "AsterLink VPN",
		WebsiteBaseURL: "https://app.example.com", AuthMailSecret: canarySecret, RegistrationDomains: config.DefaultRegistrationDomains,
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: cfg.Schema + "."}})
	if err != nil {
		t.Fatal("isolated database connection failed")
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(40)
	t.Cleanup(func() {
		_ = db.Exec(`DROP SCHEMA IF EXISTS "` + cfg.Schema + `" CASCADE`).Error
		_ = sqlDB.Close()
	})
	if err = store.Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	return &env{db: db, cfg: cfg, clock: &testClock{now: time.Date(2026, 10, 20, 8, 0, 30, 0, time.UTC)}, logs: &logSink{}}
}

// service returns a new instance, as a separate API process would have.
func (e *env) service() *Service {
	s := NewService(e.db, e.cfg)
	s.Now = e.clock.Now
	return s
}

func (e *env) worker(s *Service, m Mailer) *Worker {
	w := NewWorker(s, m)
	w.Now = e.clock.Now
	w.Logf = e.logs.Logf
	return w
}

func (e *env) user(t *testing.T, email string) model.User {
	t.Helper()
	u := model.User{ID: uuid.NewString(), Email: email, Name: "Existing", PasswordHash: "$2a$10$abcdefghijklmnopqrstuuJ1P0b0i3pA1u9yH1qkJ0Wf6o5pJ9c1y", Role: "user", Status: "active"}
	if err := e.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) jobs(t *testing.T) []model.AuthMailJob {
	t.Helper()
	var jobs []model.AuthMailJob
	if err := e.db.Order("created_at, id").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	return jobs
}

func (e *env) challenge(t *testing.T, id string) model.EmailAuthChallenge {
	t.Helper()
	var c model.EmailAuthChallenge
	if err := e.db.Where("id = ?", id).Take(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

// queuedPayload decrypts a queued job as only the worker could, to obtain the code or
// token a real recipient would read in the mail.
func queuedPayload(t *testing.T, s *Service, job model.AuthMailJob) mailPayload {
	t.Helper()
	plain, err := s.Keys().Open(job.ID, job.Template, job.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var p mailPayload
	if err = json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeMailer records deliveries; behaviour is scripted per call.
type fakeMailer struct {
	mu    sync.Mutex
	sent  []Message
	calls atomic.Int64
	// script returns the error for call n (1-based); nil script means success.
	script func(n int64, ctx context.Context, msg Message) error
}

func (f *fakeMailer) Send(ctx context.Context, msg Message) error {
	n := f.calls.Add(1)
	if f.script != nil {
		if err := f.script(n, ctx, msg); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	return nil
}

func (f *fakeMailer) messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}

func codeFrom(t *testing.T, msg Message) string {
	t.Helper()
	i := strings.Index(msg.Text, "    ")
	if i < 0 || len(msg.Text) < i+10 {
		t.Fatal("no code in mail")
	}
	code := msg.Text[i+4 : i+10]
	if !ValidCodeFormat(code) {
		t.Fatalf("code not found in mail: %q", code)
	}
	return code
}

func asRateLimit(err error) *RateLimitError {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return rl
	}
	return nil
}

func noop(*gorm.DB) error { return nil }
