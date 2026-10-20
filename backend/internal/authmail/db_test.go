package authmail

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
	"vpn/backend/internal/store"
)

// R1-02: additive, repeatable, concurrent-safe migration over pre-auth data.
func TestMigrationRepeatableOverLegacyData(t *testing.T) {
	e := newEnv(t)
	s := e.cfg.Schema
	// Rewind the schema to its pre-round-1 shape, then add legacy business data.
	for _, stmt := range []string{
		`DROP TABLE "` + s + `".email_auth_challenges`, `DROP TABLE "` + s + `".auth_mail_jobs`, `DROP TABLE "` + s + `".auth_rate_buckets`,
		`ALTER TABLE "` + s + `".users DROP COLUMN email_verified_at, DROP COLUMN password_changed_at, DROP COLUMN auth_version`,
	} {
		if err := e.db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	type legacy struct {
		ID, Email, Name, PasswordHash, Role, Status string
		CreatedAt, UpdatedAt                        time.Time
	}
	ids := []string{uuid.NewString(), uuid.NewString()}
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, id := range ids {
		if err := e.db.Exec(`INSERT INTO "`+s+`".users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, fmt.Sprintf("legacy%d@corp.example", i), "旧用户", "$2a$10$legacyhashlegacyhashlegacyhashlegacyhashlegacyhash12", []string{"user", "admin"}[i], []string{"active", "disabled"}[i], created, created).Error; err != nil {
			t.Fatal(err)
		}
	}
	order := model.Order{ID: uuid.NewString(), UserID: ids[0], IdempotencyKey: "legacy-order", PlanID: "pro", PlanName: "进阶计划", PriceCents: 3990, DurationDays: 30, TrafficBytes: 1 << 30, Status: "paid_test", Provider: "test", CreatedAt: created, PaidAt: created}
	if err := e.db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := func() []legacy {
		var rows []legacy
		if err := e.db.Raw(`SELECT id, email, name, password_hash, role, status, created_at, updated_at FROM "` + s + `".users ORDER BY id`).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before := snapshot()
	// Two instances migrate at once, then a third run.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- store.Migrate(e.db, e.cfg) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(e.db, e.cfg); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); !slices.Equal(before, after) {
		t.Fatalf("legacy columns changed:\n%v\n%v", before, after)
	}
	var added []struct {
		AuthVersion       int64
		EmailVerifiedAt   *time.Time
		PasswordChangedAt *time.Time
	}
	if err := e.db.Raw(`SELECT auth_version, email_verified_at, password_changed_at FROM "` + s + `".users`).Scan(&added).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range added {
		if row.AuthVersion != 1 || row.EmailVerifiedAt != nil || row.PasswordChangedAt != nil {
			t.Fatalf("new columns not defaulted correctly (no fake verification): %+v", row)
		}
	}
	var gotOrder model.Order
	if err := e.db.Where("id = ?", order.ID).Take(&gotOrder).Error; err != nil || gotOrder.Status != "paid_test" || gotOrder.PriceCents != 3990 || !gotOrder.PaidAt.Equal(created) {
		t.Fatal("order changed by migration")
	}
	// A later migration never resets AuthVersion.
	if err := e.db.Exec(`UPDATE "`+s+`".users SET auth_version = 3 WHERE id = ?`, ids[0]).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(e.db, e.cfg); err != nil {
		t.Fatal(err)
	}
	var version int64
	e.db.Raw(`SELECT auth_version FROM "`+s+`".users WHERE id = ?`, ids[0]).Scan(&version)
	if version != 3 {
		t.Fatal("migration reset AuthVersion")
	}
	var indexes []string
	if err := e.db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = ? AND tablename IN ('email_auth_challenges', 'auth_mail_jobs', 'auth_rate_buckets') ORDER BY indexname`, s).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"idx_auth_challenge_active", "idx_auth_challenge_digest", "idx_auth_challenge_lookup", "idx_auth_mail_job_due", "idx_auth_mail_job_lease", "auth_rate_buckets_pkey"} {
		if !slices.Contains(indexes, want) {
			t.Fatalf("missing index %s in %v", want, indexes)
		}
	}
	var definition string
	e.db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = ? AND indexname = 'idx_auth_challenge_active'`, s).Scan(&definition)
	if !strings.Contains(definition, "UNIQUE") || !strings.Contains(definition, "'active'") || strings.Contains(strings.ToLower(definition), "now()") {
		t.Fatalf("active-challenge index wrong: %s", definition)
	}
	var fks int64
	e.db.Raw(`SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = ? AND constraint_type = 'FOREIGN KEY' AND table_name IN ('email_auth_challenges', 'auth_mail_jobs', 'auth_rate_buckets')`, s).Scan(&fks)
	if fks != 0 {
		t.Fatal("auth tables must not have cascading foreign keys")
	}
}

// R1-05: leases, concurrency, crash recovery, bounded retries, write-back failure.
func TestWorkerConcurrentClaimsDeliverOnce(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	s.Limits.IPPerHour, s.Limits.IPPerDay = 1000, 1000
	for i := 0; i < 12; i++ {
		if _, err := s.IssueRegistration(context.Background(), fmt.Sprintf("user%02d@qq.com", i), "198.51.100.1"); err != nil {
			t.Fatal(err)
		}
	}
	mailer := &fakeMailer{}
	a, b := e.worker(s, mailer), e.worker(s, mailer)
	a.Slots, b.Slots = 4, 4
	var wg sync.WaitGroup
	for _, w := range []*Worker{a, b} {
		wg.Add(1)
		go func(w *Worker) {
			defer wg.Done()
			for {
				n, err := w.RunOnce(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		}(w)
	}
	wg.Wait()
	sent := mailer.messages()
	ids := map[string]int{}
	for _, m := range sent {
		ids[m.MessageID]++
	}
	if len(sent) != 12 || len(ids) != 12 {
		t.Fatalf("delivered %d messages, %d distinct; want 12 each", len(sent), len(ids))
	}
	for _, job := range e.jobs(t) {
		if job.Status != jobSent || job.Attempts != 1 || job.Payload != nil || job.FinishedAt == nil {
			t.Fatalf("job not finalized once: %+v", job.Status)
		}
	}
}

func TestWorkerCrashRecoveryAndStaleWriteBack(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	if _, err := s.IssueRegistration(context.Background(), "crash@qq.com", ""); err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{}
	crashed := e.worker(s, mailer)
	jobs, err := crashed.claim(context.Background(), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v %d", err, len(jobs))
	}
	// Within the lease nobody else may take it.
	other := e.worker(s, mailer)
	if n, _ := other.RunOnce(context.Background()); n != 0 {
		t.Fatal("job claimed twice within its lease")
	}
	e.clock.Advance(LeaseTTL + time.Second)
	if n, _ := other.RunOnce(context.Background()); n != 1 || len(mailer.messages()) != 1 {
		t.Fatal("expired lease was not recovered")
	}
	// The crashed worker wakes up and tries to write its stale result.
	crashed.finish(jobs[0], jobRetry, "late", ptr(e.clock.Now()))
	if job := e.jobs(t)[0]; job.Status != jobSent || job.Attempts != 2 || crashed.Stats().LeaseLost.Load() != 1 {
		t.Fatalf("stale worker overwrote the result: %s attempts=%d", job.Status, job.Attempts)
	}
}

func ptr[T any](v T) *T { return &v }

func TestWorkerRetriesAreBoundedAndReuseTheCredential(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	if _, err := s.IssueRegistration(context.Background(), "retry@qq.com", ""); err != nil {
		t.Fatal(err)
	}
	var seen []Message
	var mu sync.Mutex
	mailer := &fakeMailer{script: func(n int64, ctx context.Context, msg Message) error {
		mu.Lock()
		seen = append(seen, msg)
		mu.Unlock()
		return &DeliveryError{Class: "connection", Temporary: true}
	}}
	w := e.worker(s, mailer)
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if n, err := w.RunOnce(context.Background()); n != 1 || err != nil {
			t.Fatalf("attempt %d not claimed: %v", attempt, err)
		}
		job := e.jobs(t)[0]
		if attempt < MaxAttempts {
			wait := job.NextAttemptAt.Sub(e.clock.Now())
			base := []time.Duration{15 * time.Second, 60 * time.Second}[attempt-1]
			if job.Status != jobRetry || wait < base || wait > base+base/5+time.Second {
				t.Fatalf("attempt %d: status %s wait %v", attempt, job.Status, wait)
			}
			// Not before the backoff elapses.
			if n, _ := w.RunOnce(context.Background()); n != 0 {
				t.Fatal("retried before backoff")
			}
			e.clock.Advance(wait)
		} else if job.Status != jobFailed || job.Attempts != MaxAttempts || job.Payload != nil || job.FailureClass != "connection" {
			t.Fatalf("final state wrong: %s attempts=%d class=%s", job.Status, job.Attempts, job.FailureClass)
		}
	}
	e.clock.Advance(time.Hour)
	if n, _ := w.RunOnce(context.Background()); n != 0 || mailer.calls.Load() != MaxAttempts {
		t.Fatal("more than MaxAttempts deliveries")
	}
	for _, m := range seen[1:] {
		if m.MessageID != seen[0].MessageID || codeFrom(t, m) != codeFrom(t, seen[0]) {
			t.Fatal("retry changed the Message-ID or the code")
		}
	}
	if c := e.challenge(t, *e.jobs(t)[0].ChallengeID); !c.ExpiresAt.Equal(c.IssuedAt.Add(s.Limits.CodeTTL)) {
		t.Fatal("retries extended the challenge")
	}
}

func TestWorkerPermanentTimeoutAndWriteBackFailure(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	s.Limits.EmailCooldown = 0
	for _, email := range []string{"perm@qq.com", "slow@qq.com", "dbfail@qq.com"} {
		if _, err := s.IssueRegistration(context.Background(), email, ""); err != nil {
			t.Fatal(err)
		}
	}
	jobByHint := func(hint string) model.AuthMailJob {
		for _, j := range e.jobs(t) {
			if j.RecipientHint == hint {
				return j
			}
		}
		t.Fatalf("no job %s", hint)
		return model.AuthMailJob{}
	}
	mailer := &fakeMailer{script: func(n int64, ctx context.Context, msg Message) error {
		switch msg.To {
		case "perm@qq.com":
			return &DeliveryError{Class: "smtp_rejected"}
		case "slow@qq.com":
			<-ctx.Done() // a stuck server: only the per-send deadline ends it
			return &DeliveryError{Class: "timeout", Temporary: true}
		}
		return nil
	}}
	w := e.worker(s, mailer)
	w.Slots, w.SendTimeout = 3, 300*time.Millisecond
	// Make the write-back of the successful delivery fail once (database outage).
	var injected sync.Once
	failing := true
	if err := e.db.Callback().Update().Before("gorm:update").Register("authmail_test_fail", func(tx *gorm.DB) {
		if failing && strings.HasSuffix(tx.Statement.Table, "auth_mail_jobs") {
			if dest, ok := tx.Statement.Dest.(map[string]any); ok && dest["status"] == jobSent {
				injected.Do(func() { tx.AddError(errors.New("injected database failure")) })
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if n, err := w.RunOnce(context.Background()); n != 3 || err != nil {
		t.Fatalf("claimed %d: %v", n, err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("stuck send not bounded")
	}
	failing = false
	if j := jobByHint("p***@qq.com"); j.Status != jobFailed || j.Attempts != 1 || j.Payload != nil {
		t.Fatalf("permanent rejection retried: %s", j.Status)
	}
	if j := jobByHint("s***@qq.com"); j.Status != jobRetry || j.FailureClass != "timeout" {
		t.Fatalf("timeout not retried: %s", j.Status)
	}
	// SMTP accepted the message but confirming it failed: the job stays leased and is
	// delivered again after the lease, with the same Message-ID and the same code.
	if j := jobByHint("d***@qq.com"); j.Status != jobSending {
		t.Fatalf("unconfirmed delivery finalized: %s", j.Status)
	}
	e.clock.Advance(LeaseTTL + time.Second)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var copies []Message
	for _, m := range mailer.messages() {
		if m.To == "dbfail@qq.com" {
			copies = append(copies, m)
		}
	}
	if len(copies) != 2 || copies[0].MessageID != copies[1].MessageID || codeFrom(t, copies[0]) != codeFrom(t, copies[1]) || jobByHint("d***@qq.com").Status != jobSent {
		t.Fatalf("bounded duplicate must reuse the credential: %d copies", len(copies))
	}
}

// R1-06: only the newest credential works; stale or expired mail is not delivered.
func TestSupersededAndStaleMail(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	ctx := context.Background()
	mailer := &fakeMailer{}
	w := e.worker(s, mailer)

	// (a) Re-request right after the cooldown: the first job never leaves the queue.
	first, err := s.IssueRegistration(ctx, "again@qq.com", "")
	if err != nil {
		t.Fatal(err)
	}
	firstCode := queuedPayload(t, s, e.jobs(t)[0]).Code
	e.clock.Advance(61 * time.Second)
	second, err := s.IssueRegistration(ctx, "again@qq.com", "")
	if err != nil || second.VerificationID == first.VerificationID {
		t.Fatal(err)
	}
	if j := e.jobs(t)[0]; j.Status != jobCancelled || j.Payload != nil || j.FailureClass != "superseded" {
		t.Fatalf("superseded job not cancelled: %s", j.Status)
	}
	if c := e.challenge(t, first.VerificationID); c.Status != statusSuperseded || c.Generation != 1 || e.challenge(t, second.VerificationID).Generation != 2 {
		t.Fatal("generations not tracked")
	}
	if _, err = w.RunOnce(ctx); err != nil || len(mailer.messages()) != 1 {
		t.Fatalf("expected only the current mail: %v", err)
	}
	secondCode := codeFrom(t, mailer.messages()[0])
	if err = s.VerifyRegistration(ctx, first.VerificationID, "again@qq.com", firstCode, noop); !errors.Is(err, ErrVerificationExpired) {
		t.Fatalf("old code: %v", err)
	}
	if err = s.VerifyRegistration(ctx, second.VerificationID, "again@qq.com", secondCode, noop); err != nil {
		t.Fatalf("current code rejected: %v", err)
	}
	if err = s.VerifyRegistration(ctx, second.VerificationID, "again@qq.com", secondCode, noop); !errors.Is(err, ErrVerificationUsed) {
		t.Fatalf("code reused: %v", err)
	}

	// (b) The old mail is already inside an SMTP call when a new request arrives: it
	// is still delivered, but its code no longer validates.
	e.clock.Advance(61 * time.Second)
	old, _ := s.IssueRegistration(ctx, "inflight@qq.com", "")
	inflight, err := w.claim(ctx, 1)
	if err != nil || len(inflight) != 1 {
		t.Fatal("claim failed")
	}
	e.clock.Advance(61 * time.Second)
	if _, err = s.IssueRegistration(ctx, "inflight@qq.com", ""); err != nil {
		t.Fatal(err)
	}
	w.process(ctx, inflight[0]) // the challenge is superseded now -> the worker cancels
	if job := jobByID(t, e, inflight[0].ID); job.Status != jobCancelled || job.FailureClass != "superseded" {
		t.Fatalf("in-flight stale job: %s", job.Status)
	}
	// Even if the stale mail had gone out (worker already past its pre-check):
	plain, _ := s.Keys().Open(inflight[0].ID, inflight[0].Template, inflight[0].Payload)
	oldCode := string(plain[strings.Index(string(plain), `"code":"`)+8:][:6])
	if err = s.VerifyRegistration(ctx, old.VerificationID, "inflight@qq.com", oldCode, noop); !errors.Is(err, ErrVerificationExpired) {
		t.Fatalf("stale delivered code accepted: %v", err)
	}

	// (c) A job queued until near expiry is cancelled, never sent.
	e.clock.Advance(61 * time.Second)
	if _, err = s.IssueRegistration(ctx, "late@qq.com", ""); err != nil {
		t.Fatal(err)
	}
	sentBefore := len(mailer.messages())
	e.clock.Advance(s.Limits.CodeTTL - ExpiryMargin + time.Second)
	if _, err = w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, j := range e.jobs(t) {
		if j.RecipientHint == "l***@qq.com" && (j.Status != jobCancelled || j.FailureClass != "expired" || j.Payload != nil) {
			t.Fatalf("near-expiry job not cancelled: %s %s", j.Status, j.FailureClass)
		}
	}
	for _, m := range mailer.messages()[sentBefore:] {
		if m.To == "late@qq.com" {
			t.Fatal("expired credential delivered")
		}
	}

	// (d) A password change (AuthVersion bump) while a reset mail is queued.
	u := e.user(t, "member@corp.example")
	if _, err = s.IssueReset(ctx, "Member@Corp.Example", ""); err != nil {
		t.Fatal(err)
	}
	resetJob := e.jobs(t)[len(e.jobs(t))-1]
	token := queuedPayload(t, s, resetJob).Link[len("https://app.example.com/reset-password#token="):]
	if ticket, err := s.ResolveReset(ctx, token); err != nil || ticket.UserID != u.ID || ticket.AuthVersion != 1 {
		t.Fatalf("fresh token not resolvable: %v", err)
	}
	if _, err = s.ResolveReset(ctx, token); err != nil {
		t.Fatal("validate consumed the token")
	}
	if err = e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("auth_version", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if j := jobByID(t, e, resetJob.ID); j.Status != jobCancelled || j.FailureClass != "stale_account" {
		t.Fatalf("reset mail for an old password version: %s", j.Status)
	}
	if _, err = s.ResolveReset(ctx, token); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatal("token survived a password change")
	}
}

func jobByID(t *testing.T, e *env, id string) model.AuthMailJob {
	t.Helper()
	var j model.AuthMailJob
	if err := e.db.Where("id = ?", id).Take(&j).Error; err != nil {
		t.Fatal(err)
	}
	return j
}

func TestResetRequestsHaveEqualShape(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	ctx := context.Background()
	e.user(t, "present@corp.example")
	disabled := e.user(t, "disabled@corp.example")
	e.db.Model(&disabled).Update("status", "disabled")
	for _, email := range []string{"present@corp.example", "absent@corp.example", "disabled@corp.example"} {
		r, err := s.IssueReset(ctx, email, "")
		if err != nil || r.VerificationID != "" || !r.NextSendAt.Equal(e.clock.Now().Add(time.Minute)) {
			t.Fatalf("%s: %+v %v", email, r, err)
		}
	}
	jobs := e.jobs(t)
	if len(jobs) != 1 || jobs[0].RecipientHint != "p***@corp.example" {
		t.Fatalf("only the active account may get a link, got %d jobs", len(jobs))
	}
	var challenges int64
	e.db.Model(&model.EmailAuthChallenge{}).Count(&challenges)
	if challenges != 3 {
		t.Fatal("undeliverable requests must still write equal-shape challenges")
	}
	// Registration for an existing account: same receipt shape, no mail.
	e.user(t, "taken@qq.com")
	r, err := s.IssueRegistration(ctx, "taken@qq.com", "")
	if err != nil || r.VerificationID == "" || r.ExpiresAt.IsZero() {
		t.Fatalf("existing-account registration receipt differs: %+v %v", r, err)
	}
	if len(e.jobs(t)) != 1 {
		t.Fatal("registration mail queued for an existing account")
	}
}

// R1-07: shared cooldown and quotas across purposes, letter case, instances and IPs.
func TestSharedMailboxCooldownAndQuotas(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user(t, "shared@qq.com") // reset path exists; registration path is the no-mail variant
	a, b := e.service(), e.service()
	accepted := 0
	for i := 0; i < 40; i++ {
		var err error
		switch i % 4 { // alternate instance, purpose and case
		case 0:
			_, err = a.IssueRegistration(ctx, "shared@qq.com", "")
		case 1:
			_, err = b.IssueReset(ctx, "SHARED@QQ.COM", "")
		case 2:
			_, err = b.IssueRegistration(ctx, " Shared@qq.com ", "")
		default:
			_, err = a.IssueReset(ctx, "shared@QQ.com", "")
		}
		if err == nil {
			accepted++
			// Immediately after acceptance the other purpose and instance are cooling down.
			if _, err2 := b.IssueReset(ctx, "shared@qq.com", ""); asRateLimit(err2) == nil || asRateLimit(err2).Scope != "email_cooldown" {
				t.Fatalf("cooldown not shared: %v", err2)
			}
		} else if asRateLimit(err) == nil {
			t.Fatal(err)
		}
		e.clock.Advance(61 * time.Second)
	}
	// 40 minutes of attempts: the hourly cap (5) binds, whatever the purpose or instance.
	if accepted != 5 {
		t.Fatalf("accepted %d within an hour, want 5", accepted)
	}
	// Restart: a brand-new instance sees the same counters.
	if _, err := e.service().IssueReset(ctx, "shared@qq.com", ""); asRateLimit(err) == nil || asRateLimit(err).Scope != "email_hour" {
		t.Fatalf("restart cleared the quota: %v", err)
	}
	// Daily cap (10) across hours.
	daily := accepted
	for h := 0; h < 6; h++ {
		e.clock.Advance(time.Hour)
		for i := 0; i < 6; i++ {
			if _, err := a.IssueReset(ctx, "shared@qq.com", ""); err == nil {
				daily++
			}
			e.clock.Advance(61 * time.Second)
		}
	}
	if daily != 10 {
		t.Fatalf("daily cap exceeded: %d", daily)
	}
}

func TestConcurrentLimitsNeverOvershoot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	run := func(n int, f func(i int) error) (ok int, limited int) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				err := f(i)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case asRateLimit(err) != nil || errors.Is(err, ErrMailUnavailable):
					limited++
				default:
					t.Error(err)
				}
			}(i)
		}
		wg.Wait()
		return
	}
	// Same mailbox from many instances at once: exactly one wins the cooldown.
	if ok, _ := run(20, func(i int) error {
		_, err := e.service().IssueRegistration(ctx, "race@qq.com", fmt.Sprintf("203.0.113.%d", i))
		return err
	}); ok != 1 {
		t.Fatalf("same mailbox accepted %d times concurrently", ok)
	}
	if n := len(e.jobs(t)); n != 1 {
		t.Fatalf("%d jobs for one accepted request", n)
	}
	// One source address rotating mailboxes: the hourly IP cap (20) holds exactly.
	if ok, _ := run(30, func(i int) error {
		_, err := e.service().IssueRegistration(ctx, fmt.Sprintf("ip%02d@qq.com", i), "192.0.2.7")
		return err
	}); ok != 20 {
		t.Fatalf("one IP accepted %d, want 20", ok)
	}
	// Global per-minute cap across rotating IPs and mailboxes.
	e.clock.Advance(time.Minute)
	if ok, _ := run(12, func(i int) error {
		svc := e.service()
		svc.Limits.GlobalPerMinute = 5
		_, err := svc.IssueRegistration(ctx, fmt.Sprintf("global%02d@qq.com", i), fmt.Sprintf("198.18.0.%d", i))
		return err
	}); ok != 5 {
		t.Fatalf("global cap accepted %d, want 5", ok)
	}
	// Queue-size admission: past the threshold every mail request is a 503.
	e.clock.Advance(time.Minute)
	capped := e.service()
	capped.Limits.MaxPendingJobs = len(e.jobs(t)) // all queued now
	if _, err := capped.IssueReset(ctx, "queue@corp.example", "198.18.1.1"); !errors.Is(err, ErrMailUnavailable) {
		t.Fatalf("full queue accepted a request: %v", err)
	}
}

func TestVerificationFailuresAreCommittedAndBudgeted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	s := e.service()
	mailer := &fakeMailer{}
	w := e.worker(s, mailer)
	issue := func() (Receipt, string) {
		r, err := s.IssueRegistration(ctx, "guess@qq.com", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		msgs := mailer.messages()
		return r, codeFrom(t, msgs[len(msgs)-1])
	}
	wrong := func(code string) string {
		if code == "000000" {
			return "000001"
		}
		return "000000"
	}
	r, code := issue()
	// A failing onSuccess (e.g. duplicate account) rolls back and leaves the code usable.
	if err := s.VerifyRegistration(ctx, r.VerificationID, "guess@qq.com", code, func(*gorm.DB) error { return errors.New("account insert failed") }); err == nil || err.Error() != "account insert failed" {
		t.Fatalf("callback error not returned: %v", err)
	}
	if c := e.challenge(t, r.VerificationID); c.Status != statusActive || c.Failures != 0 {
		t.Fatal("failed account creation consumed or penalised the code")
	}
	for i := 1; i <= 3; i++ {
		if err := s.VerifyRegistration(ctx, r.VerificationID, "guess@qq.com", wrong(code), noop); !errors.Is(err, ErrVerificationInvalid) {
			t.Fatalf("wrong code %d: %v", i, err)
		}
		if c := e.challenge(t, r.VerificationID); c.Failures != i {
			t.Fatalf("failure %d not committed (got %d)", i, c.Failures)
		}
	}
	// Re-requesting a code does not reset the mailbox's verification budget.
	e.clock.Advance(61 * time.Second)
	r2, code2 := issue()
	for i := 4; i <= 5; i++ { // letter case does not create a separate budget
		if err := s.VerifyRegistration(ctx, r2.VerificationID, " GUESS@qq.com", wrong(code2), noop); !errors.Is(err, ErrVerificationInvalid) {
			t.Fatalf("wrong code %d: %v", i, err)
		}
	}
	err := s.VerifyRegistration(ctx, r2.VerificationID, "guess@qq.com", code2, noop)
	if rl := asRateLimit(err); rl == nil || rl.Scope != scopeEmailVerify || rl.RetryAfter <= 0 || rl.RetryAfter > 30*time.Minute {
		t.Fatalf("mailbox budget not enforced across challenges: %v", err)
	}
	// Another instance (or a restart) sees the same budget; correct codes stay blocked.
	if err = e.service().VerifyRegistration(ctx, r2.VerificationID, "guess@qq.com", code2, noop); asRateLimit(err) == nil {
		t.Fatal("budget lost across instances")
	}
	e.clock.Advance(31 * time.Minute)
	if err = s.VerifyRegistration(ctx, r2.VerificationID, "guess@qq.com", code2, noop); !errors.Is(err, ErrVerificationExpired) {
		t.Fatalf("after the window the (now expired) code must be reported expired: %v", err)
	}
	// Per-challenge lock: the fifth failure on one challenge locks it for good.
	e.clock.Advance(31 * time.Minute)
	r4, code4 := issue()
	c4 := e.challenge(t, r4.VerificationID)
	if err = e.db.Model(&c4).Update("failures", 4).Error; err != nil {
		t.Fatal(err)
	}
	if err = s.VerifyRegistration(ctx, r4.VerificationID, "guess@qq.com", wrong(code4), noop); !errors.Is(err, ErrVerificationLocked) {
		t.Fatalf("fifth failure must lock: %v", err)
	}
	if err = s.VerifyRegistration(ctx, r4.VerificationID, "guess@qq.com", code4, noop); !errors.Is(err, ErrVerificationLocked) {
		t.Fatalf("locked challenge accepted the right code: %v", err)
	}
	if err = s.VerifyRegistration(ctx, r4.VerificationID, "guess@qq.com", "12345", noop); !errors.Is(err, ErrCodeFormat) {
		t.Fatal("malformed code not rejected before lookup")
	}
}

// R1-09: no plaintext secrets at rest or in logs; cleanup is bounded to auth tables;
// the worker stops promptly even with a stuck SMTP call.
func TestNoSecretsAtRestAndCleanup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	s := e.service()
	u := e.user(t, "keep@corp.example")
	order := model.Order{ID: uuid.NewString(), UserID: u.ID, IdempotencyKey: "keep", PlanID: "pro", PlanName: "进阶计划", PriceCents: 3990, DurationDays: 30, TrafficBytes: 1 << 30, Status: "paid_test", Provider: "test", PaidAt: e.clock.Now()}
	if err := e.db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueRegistration(ctx, "secret@qq.com", "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueReset(ctx, "keep@corp.example", "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	var secrets []string
	for _, j := range e.jobs(t) {
		p := queuedPayload(t, s, j)
		secrets = append(secrets, p.Code, p.Link, p.To)
	}
	secrets = append(secrets, canarySMTPPass, string(canarySecret), "198.51.100.9")
	dump := func() string {
		var rows []string
		for _, table := range []string{"email_auth_challenges", "auth_mail_jobs", "auth_rate_buckets"} {
			var part []string
			if err := e.db.Raw(`SELECT row_to_json(t)::text FROM "` + e.cfg.Schema + `".` + table + ` AS t`).Scan(&part).Error; err != nil {
				t.Fatal(err)
			}
			rows = append(rows, part...)
		}
		return strings.Join(rows, "\n")
	}
	check := func(where, text string, includeEmails bool) {
		for _, secret := range secrets {
			if secret == "" || (!includeEmails && strings.Contains(secret, "@") && !strings.Contains(secret, "://")) {
				continue
			}
			if strings.Contains(text, secret) {
				t.Fatalf("%s contains a plaintext secret or address", where)
			}
		}
	}
	// Challenges keep the normalized mailbox for lookup; jobs and buckets must not
	// contain the recipient, the code, the link, the IP or SMTP/secret material.
	var jobRows []string
	e.db.Raw(`SELECT row_to_json(t)::text FROM "` + e.cfg.Schema + `".auth_mail_jobs AS t`).Scan(&jobRows)
	check("queued jobs", strings.Join(jobRows, "\n"), true)
	check("auth tables", dump(), false)

	mailer := &fakeMailer{}
	w := e.worker(s, mailer)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, j := range e.jobs(t) {
		if j.Status != jobSent || j.Payload != nil {
			t.Fatal("payload not erased after delivery")
		}
	}
	check("worker logs", e.logs.all(), true)

	// Cleanup after retention removes auth rows only.
	e.clock.Advance(8 * 24 * time.Hour)
	result, err := Cleanup(ctx, e.db, e.cfg.Schema, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	var left int64
	for _, table := range []string{"email_auth_challenges", "auth_mail_jobs"} {
		e.db.Table(e.cfg.Schema + "." + table).Count(&left)
		if left != 0 {
			t.Fatalf("%s not cleaned: %d rows", table, left)
		}
	}
	e.db.Table(e.cfg.Schema+".auth_rate_buckets").Where("scope <> ?", scopeEmailCoord).Count(&left)
	if left != 0 || result.Challenges != 2 || result.Jobs != 2 {
		t.Fatalf("cleanup incomplete: %+v, %d buckets left", result, left)
	}
	var users, orders int64
	e.db.Model(&model.User{}).Count(&users)
	e.db.Model(&model.Order{}).Count(&orders)
	if users != 1 || orders != 1 {
		t.Fatal("cleanup touched accounts or orders")
	}
	// Coordination rows outlive counter windows (idle 30 days before removal).
	e.db.Table(e.cfg.Schema+".auth_rate_buckets").Where("scope = ?", scopeEmailCoord).Count(&left)
	if left != 2 {
		t.Fatalf("coordination rows removed with counters: %d", left)
	}
	e.clock.Advance(31 * 24 * time.Hour)
	if _, err = Cleanup(ctx, e.db, e.cfg.Schema, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.db.Table(e.cfg.Schema + ".auth_rate_buckets").Count(&left)
	if left != 0 {
		t.Fatal("idle coordination rows never cleaned")
	}
}

func TestWorkerShutdownIsBounded(t *testing.T) {
	e := newEnv(t)
	s := e.service()
	if _, err := s.IssueRegistration(context.Background(), "stuck@qq.com", ""); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	mailer := &fakeMailer{script: func(n int64, ctx context.Context, msg Message) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}
	w := e.worker(s, mailer)
	w.Poll = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never claimed the job")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker shutdown waited on a stuck SMTP call")
	}
	if j := e.jobs(t)[0]; j.Status != jobRetry || j.FailureClass != "shutdown" || j.Payload == nil {
		t.Fatalf("interrupted job not handed back for another instance: %s", j.Status)
	}
	// Handed back after its last attempt: finalized instead of lingering as "pending".
	if err := e.db.Model(&model.AuthMailJob{}).Where("1 = 1").Update("attempts", MaxAttempts).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := e.worker(s, &fakeMailer{}).RunOnce(context.Background()); n != 0 || err != nil {
		t.Fatalf("exhausted job claimed: %d %v", n, err)
	}
	if j := e.jobs(t)[0]; j.Status != jobFailed || j.FailureClass != "attempts_exhausted" || j.Payload != nil {
		t.Fatalf("exhausted job not finalized: %s %s", j.Status, j.FailureClass)
	}
}
