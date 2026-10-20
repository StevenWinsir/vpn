package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
)

// testMailDomain is a reserved, undeliverable domain used by fixtures that are not
// about the allow-list itself. Production configuration never contains it.
const testMailDomain = "qa.asterlink.test"

// withTestMail returns cfg with synthetic mail settings: registration open, a test
// secret, and an SMTP host that cannot resolve. Real backend/.env values never apply,
// and nothing here can reach a real mailbox (deliveries go to a capture Mailer).
func withTestMail(cfg config.Config, domains ...string) config.Config {
	cfg.AllowRegistration = true
	cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPSecure = "smtp.test.invalid", 465, true
	cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFromName = "sender@qq.com", "test-only-smtp-authorization", "AsterLink VPN"
	cfg.WebsiteBaseURL = "https://app.example.com"
	cfg.AuthMailSecret = []byte("api-test-auth-mail-secret-32-bytes!!")
	if len(domains) == 0 {
		domains = config.DefaultRegistrationDomains
	}
	cfg.RegistrationDomains = append([]string(nil), domains...)
	return cfg
}

// captureMailer is the Fake Mailer: it records what a recipient would read.
type captureMailer struct {
	mu   sync.Mutex
	sent []authmail.Message
}

func (m *captureMailer) Send(_ context.Context, msg authmail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *captureMailer) to(email string) []authmail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []authmail.Message
	for _, msg := range m.sent {
		if msg.To == email {
			out = append(out, msg)
		}
	}
	return out
}

// testClock is a manually advanced clock shared by the service and the worker.
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

// mailHarness wires one mail service (optionally on a test clock) to a worker that
// delivers into captureMailer, exactly as cmd/api wires the SMTP worker.
type mailHarness struct {
	service *authmail.Service
	worker  *authmail.Worker
	box     *captureMailer
}

func newMailHarness(db *gorm.DB, cfg config.Config, now func() time.Time) *mailHarness {
	service := authmail.NewService(db, cfg)
	box := &captureMailer{}
	worker := authmail.NewWorker(service, box)
	if now != nil {
		service.Now, worker.Now = now, now
	}
	worker.Logf = func(string, ...any) {}
	return &mailHarness{service: service, worker: worker, box: box}
}

// deliver drains the queue through the worker.
func (h *mailHarness) deliver(t *testing.T) {
	t.Helper()
	for {
		n, err := h.worker.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("mail worker: %v", err)
		}
		if n == 0 {
			return
		}
	}
}

// latestCode delivers queued mail and returns the code of the newest registration
// mail to email, as the recipient would read it.
func (h *mailHarness) latestCode(t *testing.T, email string) string {
	t.Helper()
	h.deliver(t)
	msgs := h.box.to(email)
	if len(msgs) == 0 {
		t.Fatalf("no registration mail delivered")
	}
	return mailCode(t, msgs[len(msgs)-1])
}

func mailCode(t *testing.T, msg authmail.Message) string {
	t.Helper()
	code, ok := codeIn(msg)
	if !ok {
		t.Fatal("no code in registration mail")
	}
	return code
}

// codeIn reads the indented code line of the registration template.
func codeIn(msg authmail.Message) (string, bool) {
	i := strings.Index(msg.Text, "    ")
	if i < 0 || len(msg.Text) < i+10 || !authmail.ValidCodeFormat(msg.Text[i+4:i+10]) {
		return "", false
	}
	return msg.Text[i+4 : i+10], true
}
