package authmail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// Mail job states.
const (
	jobQueued    = "queued"
	jobSending   = "sending"
	jobRetry     = "retry"
	jobSent      = "sent"
	jobFailed    = "failed"
	jobCancelled = "cancelled"
)

// Worker delivers queued authentication mail. Claims are short transactions with a
// lease; SMTP runs with no database lock held; results are written back only if the
// same lease (owner + version) still holds the job in "sending".
type Worker struct {
	db      *gorm.DB
	schema  string
	keys    Keys
	mailer  Mailer
	website string

	Owner        string
	Slots        int
	Lease        time.Duration
	SendTimeout  time.Duration
	MaxAttempts  int
	Backoff      []time.Duration
	Poll         time.Duration
	ExpiryMargin time.Duration
	Now          func() time.Time
	Logf         func(format string, args ...any)

	stats WorkerStats
}

// WorkerStats are process-local counters for logs and diagnostics (no raw emails).
type WorkerStats struct {
	Claimed, Sent, Retried, Failed, Cancelled, LeaseLost atomic.Int64
}

// NewWorker binds a worker to the service's database, keys and website.
func NewWorker(s *Service, mailer Mailer) *Worker {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	host, _ := os.Hostname()
	if len(host) > 40 {
		host = host[:40]
	}
	return &Worker{
		db: s.db, schema: s.schema, keys: s.keys, mailer: mailer, website: s.cfg.WebsiteBaseURL,
		Owner: fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(suffix)),
		Slots: WorkerSlots, Lease: LeaseTTL, SendTimeout: SendTimeout, MaxAttempts: MaxAttempts,
		Backoff: []time.Duration{15 * time.Second, 60 * time.Second}, Poll: 2 * time.Second, ExpiryMargin: ExpiryMargin,
		Now: time.Now, Logf: log.Printf,
	}
}

// Stats returns the live counters.
func (w *Worker) Stats() *WorkerStats { return &w.stats }

func (w *Worker) now() time.Time        { return w.Now().UTC() }
func (w *Worker) table(n string) string { return w.schema + "." + n }

type claimedJob struct {
	ID            string
	ChallengeID   *string
	Template      string
	Payload       []byte
	MessageID     string
	RecipientHint string
	Attempts      int
	LeaseVersion  int64
	DeliverBy     *time.Time
}

// Run delivers until ctx is cancelled. In-flight SMTP calls are bounded by
// SendTimeout and are also cancelled with ctx, so shutdown never waits on a stuck server.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, w.Slots)
	ticker := time.NewTicker(w.Poll)
	defer ticker.Stop()
	for {
		if ctx.Err() == nil {
			if err := w.sweep(ctx); err != nil && ctx.Err() == nil {
				w.Logf("authmail: sweep failed (will retry)")
			}
			free := w.Slots - len(slots)
			if free > 0 {
				jobs, err := w.claim(ctx, free)
				if err != nil && ctx.Err() == nil {
					w.Logf("authmail: claim failed (will retry)")
				}
				for _, job := range jobs {
					slots <- struct{}{}
					wg.Add(1)
					go func(job claimedJob) {
						defer func() { <-slots; wg.Done() }()
						w.process(ctx, job)
					}(job)
				}
			}
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
		}
	}
}

// RunOnce sweeps, claims up to Slots jobs and processes them synchronously.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	if err := w.sweep(ctx); err != nil {
		return 0, err
	}
	jobs, err := w.claim(ctx, w.Slots)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(job claimedJob) { defer wg.Done(); w.process(ctx, job) }(job)
	}
	wg.Wait()
	return len(jobs), nil
}

// sweep finalizes jobs that can no longer be delivered usefully: stale credentials
// that would expire before arrival, and abandoned leases whose attempts are used up.
func (w *Worker) sweep(ctx context.Context) error {
	now := w.now()
	db := w.db.WithContext(ctx)
	if err := db.Exec(fmt.Sprintf(`UPDATE %s SET status = ?, failure_class = 'expired', payload = NULL, finished_at = ?, lease_until = NULL
WHERE status IN (?, ?) AND deliver_by IS NOT NULL AND deliver_by <= ?`, w.table("auth_mail_jobs")), jobCancelled, now, jobQueued, jobRetry, now.Add(w.ExpiryMargin)).Error; err != nil {
		return err
	}
	if err := db.Exec(fmt.Sprintf(`UPDATE %s SET status = ?, failure_class = 'lease_expired', payload = NULL, finished_at = ?, lease_until = NULL
WHERE status = ? AND lease_until < ? AND attempts >= ?`, w.table("auth_mail_jobs")), jobFailed, now, jobSending, now, w.MaxAttempts).Error; err != nil {
		return err
	}
	// A job handed back after an interruption may have used its last attempt.
	return db.Exec(fmt.Sprintf(`UPDATE %s SET status = ?, failure_class = 'attempts_exhausted', payload = NULL, finished_at = ?, lease_until = NULL
WHERE status IN (?, ?) AND attempts >= ?`, w.table("auth_mail_jobs")), jobFailed, now, jobQueued, jobRetry, w.MaxAttempts).Error
}

// claim leases up to n due jobs. FOR UPDATE SKIP LOCKED means two workers (in one or
// many instances) never claim the same job; an expired lease is reclaimable.
func (w *Worker) claim(ctx context.Context, n int) ([]claimedJob, error) {
	if n <= 0 {
		return nil, nil
	}
	now := w.now()
	var jobs []claimedJob
	err := w.db.WithContext(ctx).Raw(fmt.Sprintf(`UPDATE %[1]s AS j SET status = ?, attempts = j.attempts + 1, lease_owner = ?, lease_version = j.lease_version + 1, lease_until = ?
WHERE j.id IN (SELECT id FROM %[1]s WHERE attempts < ? AND ((status IN (?, ?) AND next_attempt_at <= ?) OR (status = ? AND lease_until < ?))
ORDER BY next_attempt_at, id LIMIT ? FOR UPDATE SKIP LOCKED)
RETURNING j.id, j.challenge_id, j.template, j.payload, j.message_id, j.recipient_hint, j.attempts, j.lease_version, j.deliver_by`, w.table("auth_mail_jobs")),
		jobSending, w.Owner, now.Add(w.Lease), w.MaxAttempts, jobQueued, jobRetry, now, jobSending, now, n).Scan(&jobs).Error
	w.stats.Claimed.Add(int64(len(jobs)))
	return jobs, err
}

// stillUseful re-checks, without locking, that the credential in the mail can still be
// used. A worker holding a job never locks a challenge or user (lock order).
func (w *Worker) stillUseful(ctx context.Context, job claimedJob) (bool, string, error) {
	if job.DeliverBy != nil && !w.now().Add(w.ExpiryMargin).Before(*job.DeliverBy) {
		return false, "expired", nil
	}
	if job.ChallengeID == nil {
		return true, "", nil
	}
	var rows []struct {
		Status, Purpose  string
		BoundAuthVersion int64
		UserID           *string
		AuthVersion      *int64
		UserStatus       *string
	}
	err := w.db.WithContext(ctx).Raw(fmt.Sprintf(`SELECT c.status, c.purpose, c.bound_auth_version, c.user_id, u.auth_version, u.status AS user_status
FROM %s AS c LEFT JOIN %s AS u ON u.id = c.user_id WHERE c.id = ?`, w.table("email_auth_challenges"), w.table("users")), *job.ChallengeID).Scan(&rows).Error
	if err != nil {
		return false, "", err
	}
	if len(rows) == 0 || rows[0].Status != statusActive {
		return false, "superseded", nil
	}
	r := rows[0]
	if r.Purpose == PurposeReset && (r.UserID == nil || r.AuthVersion == nil || *r.AuthVersion != r.BoundAuthVersion || r.UserStatus == nil || *r.UserStatus != "active") {
		return false, "stale_account", nil
	}
	return true, "", nil
}

func (w *Worker) process(ctx context.Context, job claimedJob) {
	useful, reason, err := w.stillUseful(ctx, job)
	if err != nil {
		w.release(job, "precheck_failed")
		return
	}
	if !useful {
		w.finish(job, jobCancelled, reason, nil)
		return
	}
	plain, err := w.keys.Open(job.ID, job.Template, job.Payload)
	var p mailPayload
	if err != nil || json.Unmarshal(plain, &p) != nil {
		w.finish(job, jobFailed, "payload_invalid", nil)
		return
	}
	rendered, err := Render(job.Template, p, w.website)
	if err != nil {
		w.finish(job, jobFailed, "render_failed", nil)
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, w.SendTimeout)
	err = w.mailer.Send(sendCtx, Message{To: p.To, MessageID: job.MessageID, Rendered: rendered})
	cancel()
	if err == nil {
		w.finish(job, jobSent, "", nil)
		return
	}
	if ctx.Err() != nil {
		// Shutdown interrupted the call: hand the job back without waiting for the lease.
		w.release(job, "shutdown")
		return
	}
	var delivery *DeliveryError
	if !errors.As(err, &delivery) {
		delivery = &DeliveryError{Class: "unknown", Temporary: true}
	}
	if delivery.Temporary && job.Attempts < w.MaxAttempts {
		next := w.now().Add(w.backoff(job.Attempts))
		w.finish(job, jobRetry, delivery.Class, &next)
		return
	}
	w.finish(job, jobFailed, delivery.Class, nil)
}

func (w *Worker) backoff(attempt int) time.Duration {
	base := w.Backoff[len(w.Backoff)-1]
	if attempt-1 < len(w.Backoff) {
		base = w.Backoff[attempt-1]
	}
	jitter, _ := rand.Int(rand.Reader, big.NewInt(int64(base/5)+1))
	return base + time.Duration(jitter.Int64())
}

// release returns a claimed job for an immediate retry after an internal interruption.
func (w *Worker) release(job claimedJob, class string) {
	now := w.now()
	w.writeBack(job, map[string]any{"status": jobRetry, "failure_class": class, "next_attempt_at": now, "lease_until": nil}, "released")
}

// finish records a final or retry state, conditioned on still owning the lease. A
// stale worker can never turn a cancelled or re-claimed job into sent/retry.
func (w *Worker) finish(job claimedJob, status, class string, next *time.Time) {
	now := w.now()
	update := map[string]any{"status": status, "failure_class": class, "lease_until": nil}
	if status == jobRetry {
		update["next_attempt_at"] = *next
	} else {
		update["payload"] = nil
		update["finished_at"] = now
	}
	w.writeBack(job, update, status)
}

func (w *Worker) writeBack(job claimedJob, update map[string]any, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := w.db.WithContext(ctx).Table(w.table("auth_mail_jobs")).
		Where("id = ? AND lease_owner = ? AND lease_version = ? AND status = ?", job.ID, w.Owner, job.LeaseVersion, jobSending).Updates(update)
	switch {
	case result.Error != nil:
		w.Logf("authmail: job=%s template=%s write-back failed; lease will expire", job.ID, job.Template)
		return
	case result.RowsAffected == 0:
		w.stats.LeaseLost.Add(1)
		w.Logf("authmail: job=%s template=%s lease lost; result discarded", job.ID, job.Template)
		return
	}
	switch status {
	case jobSent:
		w.stats.Sent.Add(1)
	case jobRetry:
		w.stats.Retried.Add(1)
	case jobFailed:
		w.stats.Failed.Add(1)
	case jobCancelled:
		w.stats.Cancelled.Add(1)
	}
	w.Logf("authmail: job=%s template=%s to=%s attempt=%d status=%s class=%v", job.ID, job.Template, job.RecipientHint, job.Attempts, status, update["failure_class"])
}
