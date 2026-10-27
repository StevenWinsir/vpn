package store

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// R1-01 (handoff_coupon.md 5): the referral migration over 8c311ee-shaped data adds the
// columns with their defaults and the new table with its indexes, changes no existing
// value and can run again.
func TestReferralMigrationOverLegacyData(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	q := `"` + s + `".`
	mustExec(t, db, `DROP TABLE `+q+`referrals`)
	mustExec(t, db, `ALTER TABLE `+q+`users DROP COLUMN referral_code`)
	mustExec(t, db, `ALTER TABLE `+q+`subscriptions DROP COLUMN bonus_bytes, DROP COLUMN bonus_starts_at`)

	userID, subID := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 10, 1, 2, 3, 4, 0, time.UTC)
	mustExec(t, db, `INSERT INTO `+q+`users (id, email, name, password_hash, role, status, created_at, updated_at, auth_version) VALUES (?, 'old@example.invalid', '旧用户', 'hash', 'user', 'active', ?, ?, 1)`, userID, created, created)
	mustExec(t, db, `INSERT INTO `+q+`subscriptions (id, user_id, plan_id, plan_name, starts_at, expires_at, traffic_limit_bytes, used_units, max_devices, allow_dedicated, is_test, created_at, updated_at, quota_cycle_days, quota_cycle_bytes, quota_cycle_starts_at)
		VALUES (?, ?, 'pro', '进阶计划', ?, ?, 322122547200, 5000, 5, true, false, ?, ?, 30, 322122547200, ?)`, subID, userID, created, created.AddDate(1, 0, 0), created, created, created)

	columns := legacyColumns(t, db, s)
	watched := map[string]string{"users": columns["users"], "subscriptions": columns["subscriptions"], "orders": columns["orders"], "plans": columns["plans"]}
	before := fingerprints(t, db, s, watched)
	for range 2 {
		if err := Migrate(db, cfg); err != nil {
			t.Fatal(err)
		}
	}
	after := fingerprints(t, db, s, watched)
	for table, want := range before {
		if after[table] != want {
			t.Errorf("%s changed by the migration: %s -> %s", table, want, after[table])
		}
	}
	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil || user.ReferralCode != nil {
		t.Fatalf("legacy user %+v %v", user, err)
	}
	var sub model.Subscription
	if err := db.First(&sub, "id = ?", subID).Error; err != nil || sub.BonusBytes != 0 || sub.BonusStartsAt != nil || sub.TrafficLimitBytes != 322122547200 {
		t.Fatalf("legacy subscription %+v %v", sub, err)
	}
	var indexes []string
	if err := db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = ? AND tablename IN ('referrals', 'users') ORDER BY indexname`, s).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	// GORM prefixes its generated index names with the schema.
	for _, want := range []string{"idx_referral_qualified", "idx_referral_registered", "idx_referral_referrer_time", "_referrals_referee_id", "_users_referral_code"} {
		found := false
		for _, name := range indexes {
			found = found || name == want || strings.HasPrefix(want, "_") && strings.HasSuffix(name, want)
		}
		if !found {
			t.Errorf("index %s missing (have %v)", want, indexes)
		}
	}
	// The database enforces one code per account and one referrer per referee, the
	// status vocabulary and the non-negative bonus.
	mustExec(t, db, `UPDATE `+q+`users SET referral_code = 'ABCD2345' WHERE id = ?`, userID)
	otherID := uuid.NewString()
	mustExec(t, db, `INSERT INTO `+q+`users (id, email, name, password_hash, role, status, created_at, updated_at, auth_version) VALUES (?, 'other@example.invalid', '新用户', 'hash', 'user', 'active', ?, ?, 1)`, otherID, created, created)
	if err := db.Exec(`UPDATE `+q+`users SET referral_code = 'ABCD2345' WHERE id = ?`, otherID).Error; err == nil {
		t.Fatal("a duplicate referral code was accepted")
	}
	insert := `INSERT INTO ` + q + `referrals (id, referrer_id, referee_id, referee_email_masked, code_snapshot, status, created_at, updated_at) VALUES (?, ?, ?, 'ot***@example.invalid', 'ABCD2345', ?, now(), now())`
	mustExec(t, db, insert, uuid.NewString(), userID, otherID, model.ReferralRegistered)
	if err := db.Exec(insert, uuid.NewString(), userID, otherID, model.ReferralRegistered).Error; err == nil {
		t.Fatal("a second referrer for one referee was accepted")
	}
	if err := db.Exec(`UPDATE `+q+`referrals SET status = 'paid' WHERE referee_id = ?`, otherID).Error; err == nil {
		t.Fatal("an unknown referral status was accepted")
	}
	if err := db.Exec(`UPDATE `+q+`subscriptions SET bonus_bytes = -1 WHERE id = ?`, subID).Error; err == nil {
		t.Fatal("a negative bonus was accepted")
	}
	// Foreign keys: removing the referee keeps the row anonymous, removing the referrer
	// removes it (this is what covers an older binary's purge).
	mustExec(t, db, `DELETE FROM `+q+`users WHERE id = ?`, otherID)
	var left []model.Referral
	if err := db.Find(&left).Error; err != nil || len(left) != 1 || left[0].RefereeID != nil || left[0].RefereeEmailMasked != "ot***@example.invalid" {
		t.Fatalf("referee removal %+v %v", left, err)
	}
	mustExec(t, db, `DELETE FROM `+q+`subscriptions WHERE user_id = ?`, userID)
	mustExec(t, db, `DELETE FROM `+q+`users WHERE id = ?`, userID)
	var count int64
	db.Model(&model.Referral{}).Count(&count)
	if count != 0 {
		t.Fatalf("referrer removal left %d rows", count)
	}
}
