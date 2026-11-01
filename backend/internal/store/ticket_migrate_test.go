package store

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// T1-01 (handoff_ticket.md 5): the ticket tables arrive additively over a database from
// before them (75f906d). Repeating the migration changes nothing that existed; the new
// tables carry their foreign keys, checks and indexes, and the console queue uses its
// partial index.
func TestTicketMigrationOverExistingData(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	f := seedLegacy(t, db, s)
	mustExec(t, db, `INSERT INTO "`+s+`".orders (id, user_id, idempotency_key, plan_id, plan_name, price_cents, duration_days, traffic_bytes, status, provider, created_at, paid_at) VALUES (?, ?, ?, 'starter', '轻量计划', 1990, 30, 1073741824, 'paid', 'gateway', ?, ?)`,
		uuid.NewString(), f.userID, uuid.NewString(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 1, 0, 0, time.UTC))
	// The database as the release before tickets left it: fully migrated (servers grouped),
	// without the two ticket tables.
	if err := Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `DROP TABLE "`+s+`".ticket_messages`)
	mustExec(t, db, `DROP TABLE "`+s+`".tickets`)
	columns := legacyColumns(t, db, s)
	before := fingerprints(t, db, s, columns)
	for range 2 {
		if err := Migrate(db, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if after := fingerprints(t, db, s, columns); !maps.Equal(before, after) {
		for table := range before {
			if before[table] != after[table] {
				t.Errorf("%s changed: %s -> %s", table, before[table], after[table])
			}
		}
		t.FailNow()
	}

	// Foreign keys: both cascade.
	var cascades []string
	if err := db.Raw(`SELECT c.conrelid::regclass::text || ':' || c.confrelid::regclass::text FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
		WHERE n.nspname = ? AND c.contype = 'f' AND c.confdeltype = 'c' AND c.conrelid::regclass::text LIKE '%ticket%' ORDER BY 1`, s).Scan(&cascades).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{s + ".ticket_messages:" + s + ".tickets", s + ".tickets:" + s + ".users"}
	if !slices.Equal(cascades, want) {
		t.Fatalf("cascading foreign keys %v", cascades)
	}
	// Indexes.
	// (GORM names the single-column ones after the schema-qualified table.)
	var indexes []string
	if err := db.Raw(`SELECT regexp_replace(indexdef, ' ON [^(]*\(', ' (') FROM pg_indexes WHERE schemaname = ? AND tablename IN ('tickets', 'ticket_messages')`, s).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	defs := strings.Join(indexes, "\n")
	for _, def := range []string{
		"UNIQUE INDEX idx_ticket_idempotency (user_id, idempotency_key)", "INDEX idx_ticket_user_time (user_id, created_at DESC)",
		"INDEX idx_ticket_open_queue (waiting_since, id) WHERE ((status)::text = 'open'::text)", "_tickets_ticket_no (ticket_no)", "_tickets_expires_at (expires_at)", " (last_message_at)",
		"UNIQUE INDEX idx_ticket_message_position (ticket_id, \"position\")", "UNIQUE INDEX idx_ticket_message_idempotency (ticket_id, idempotency_key)",
	} {
		if !strings.Contains(defs, def) {
			t.Fatalf("index %q missing from\n%s", def, defs)
		}
	}
	// Checks refuse values outside the vocabulary and the bounds.
	insert := func(column, value string) error {
		row := map[string]string{"category": "'other'", "platform": "''", "status": "'open'", "message_count": "1", "closed_by": "''"}
		row[column] = value
		return db.Exec(`INSERT INTO "` + s + `".tickets (id, ticket_no, user_id, idempotency_key, category, platform, subject, status, user_unread, message_count, last_message_at, closed_by, version, created_at, updated_at, expires_at)
			VALUES (gen_random_uuid(), 'C' || substr(md5(random()::text), 1, 12), '` + f.userID + `', gen_random_uuid(), ` + row["category"] + `, ` + row["platform"] + `, 's', ` + row["status"] + `, false, ` + row["message_count"] + `, now(), ` + row["closed_by"] + `, 1, now(), now(), now())`).Error
	}
	if err := insert("status", "'open'"); err != nil {
		t.Fatalf("a valid row was refused: %v", err)
	}
	for column, value := range map[string]string{"category": "'refund'", "platform": "'linux'", "status": "'pending'", "message_count": "101", "closed_by": "'system'"} {
		if insert(column, value) == nil {
			t.Fatalf("%s = %s accepted", column, value)
		}
	}
	var ticketID string
	db.Raw(`SELECT id FROM "` + s + `".tickets LIMIT 1`).Scan(&ticketID)
	for _, bad := range []string{`'bot', 1`, `'user', 0`, `'user', 101`} {
		if db.Exec(`INSERT INTO "`+s+`".ticket_messages (id, ticket_id, author_kind, position, author_id, body, created_at) VALUES (gen_random_uuid(), ?, `+bad+`, ?, 'b', now())`, ticketID, f.userID).Error == nil {
			t.Fatalf("message (%s) accepted", bad)
		}
	}

	// The console queue (open tickets, longest wait first) reads the partial index.
	var plan []string
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL enable_seqscan = off`).Error; err != nil {
			return err
		}
		return tx.Raw(`EXPLAIN SELECT id FROM "`+s+`".tickets WHERE status = 'open' AND expires_at > ? ORDER BY waiting_since, id LIMIT 20`, time.Now()).Scan(&plan).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(plan, "\n"); !strings.Contains(text, "idx_ticket_open_queue") {
		t.Fatalf("queue plan:\n%s", text)
	}
}
