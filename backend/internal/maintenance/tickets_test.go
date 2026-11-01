package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// T1-07 (handoff_ticket.md 8): expired tickets are deleted in bounded batches with their
// messages; a ticket at exactly its expiry is expired; a locked ticket waits for the next
// pass; another instance holding the sweep lock skips the pass.

func (e *env) ticket(t *testing.T, expires time.Time, messages int) string {
	t.Helper()
	id := uuid.NewString()
	row := model.Ticket{ID: id, TicketNo: "T" + id[:12], UserID: e.user.ID, IdempotencyKey: uuid.NewString(), Category: "other", Subject: "s", Status: model.TicketOpen,
		MessageCount: messages, LastMessageAt: e.now, Version: 1, CreatedAt: expires.Add(-model.TicketRetention), UpdatedAt: e.now, ExpiresAt: expires}
	if err := e.db.Omit("User").Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	for i := range messages {
		if err := e.db.Omit("Ticket").Create(&model.TicketMessage{ID: uuid.NewString(), TicketID: id, Position: i + 1, AuthorKind: model.TicketAuthorUser, AuthorID: e.user.ID, Body: "b", CreatedAt: e.now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (e *env) ticketCounts(t *testing.T) (tickets, messages int64) {
	t.Helper()
	if e.db.Model(&model.Ticket{}).Count(&tickets).Error != nil || e.db.Model(&model.TicketMessage{}).Count(&messages).Error != nil {
		t.Fatal("count failed")
	}
	return tickets, messages
}

func TestPruneTicketsAtExpiry(t *testing.T) {
	e := newEnv(t)
	atExpiry := e.ticket(t, e.now, 2)
	gone := e.ticket(t, e.now.Add(-time.Hour), 3)
	live := e.ticket(t, e.now.Add(time.Second), 2)
	result, err := e.sweeper().PruneTickets(context.Background())
	if err != nil || result.Skipped || result.Tickets != 2 {
		t.Fatalf("prune: %+v %v", result, err)
	}
	var left []string
	e.db.Model(&model.Ticket{}).Pluck("id", &left)
	if len(left) != 1 || left[0] != live {
		t.Fatalf("left %v (expired %s %s)", left, atExpiry, gone)
	}
	if _, messages := e.ticketCounts(t); messages != 2 {
		t.Fatalf("%d messages left (want the live ticket's 2)", messages)
	}
}

func TestPruneTicketsInBatches(t *testing.T) {
	e := newEnv(t)
	statement := `INSERT INTO "` + e.schema + `".tickets (id, ticket_no, user_id, idempotency_key, category, platform, subject, status, user_unread, message_count, last_message_at, closed_by, version, created_at, updated_at, expires_at)
		SELECT gen_random_uuid(), 'B' || lpad(g::text, 12, '0'), ?, gen_random_uuid(), 'other', '', 's', 'closed', false, 1, ?, 'user', 1, ?, ?, ? FROM generate_series(1, 2500) AS g`
	if err := e.db.Exec(statement, e.user.ID, e.now, e.now.Add(-model.TicketRetention-time.Hour), e.now, e.now.Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.db.Exec(`INSERT INTO "` + e.schema + `".ticket_messages (id, ticket_id, position, author_kind, author_id, body, created_at) SELECT gen_random_uuid(), id, 1, 'user', user_id, 'b', created_at FROM "` + e.schema + `".tickets`).Error; err != nil {
		t.Fatal(err)
	}
	keep := e.ticket(t, e.now.Add(time.Hour), 1)
	result, err := e.sweeper().PruneTickets(context.Background())
	if err != nil || result.Tickets != 2500 {
		t.Fatalf("prune: %+v %v", result, err)
	}
	if tickets, messages := e.ticketCounts(t); tickets != 1 || messages != 1 {
		t.Fatalf("left %d tickets, %d messages (kept %s)", tickets, messages, keep)
	}
}

func TestPruneTicketsSkipsLockedAndOtherInstances(t *testing.T) {
	e := newEnv(t)
	locked := e.ticket(t, e.now.Add(-time.Minute), 1)
	e.ticket(t, e.now.Add(-time.Minute), 1)
	writer := e.db.Begin()
	var held model.Ticket
	if err := writer.Raw(`SELECT * FROM "`+e.schema+`".tickets WHERE id = ? FOR UPDATE`, locked).Scan(&held).Error; err != nil {
		t.Fatal(err)
	}
	result, err := e.sweeper().PruneTickets(context.Background())
	if err != nil || result.Tickets != 1 {
		t.Fatalf("with a locked ticket: %+v %v", result, err)
	}
	writer.Rollback()
	if result, err = e.sweeper().PruneTickets(context.Background()); err != nil || result.Tickets != 1 {
		t.Fatalf("next pass: %+v %v", result, err)
	}

	e.ticket(t, e.now.Add(-time.Minute), 1)
	holder := e.db.Begin()
	if err := holder.Exec("SELECT pg_advisory_xact_lock(?)", LockKey).Error; err != nil {
		t.Fatal(err)
	}
	if result, err = e.sweeper().PruneTickets(context.Background()); err != nil || !result.Skipped || result.Tickets != 0 {
		t.Fatalf("ran while another instance held the lock: %+v %v", result, err)
	}
	holder.Rollback()
	if result, err = e.sweeper().PruneTickets(context.Background()); err != nil || result.Skipped || result.Tickets != 1 {
		t.Fatalf("after release: %+v %v", result, err)
	}
}

// Run includes the ticket pass, on its own (shortened) interval.
func TestRunPrunesTickets(t *testing.T) {
	e := newEnv(t)
	s := e.sweeper()
	s.Tickets = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	// Created after the initial pass ran: only the shortened interval can remove it.
	time.Sleep(100 * time.Millisecond)
	e.ticket(t, e.now.Add(-time.Minute), 1)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if tickets, _ := e.ticketCounts(t); tickets == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the ticket pass did not run on its interval")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
