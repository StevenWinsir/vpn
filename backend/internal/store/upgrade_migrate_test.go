package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// handoff_plans.md R1-01 (migration): the upgrade columns arrive additively over a
// dbe75ca schema, rows written before (and by an older binary after) read as ordinary
// purchases, and the database enforces the new value ranges.
func TestPlansUpgradeMigrationOverLegacyData(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	q := `"` + s + `".`
	mustExec(t, db, `ALTER TABLE `+q+`orders DROP COLUMN kind, DROP COLUMN from_plan_id, DROP COLUMN from_plan_name, DROP COLUMN credit_cents, DROP COLUMN upgrade_days, DROP COLUMN upgrade_traffic_bytes, DROP COLUMN upgrade_snapshot, DROP COLUMN covers_from, DROP COLUMN covers_to`)
	mustExec(t, db, `ALTER TABLE `+q+`shop_settings DROP COLUMN upgrade_enabled, DROP COLUMN upgrade_min_charge_cents`)
	mustExec(t, db, `UPDATE `+q+`shop_settings SET yearly_discount_percent = 85, version = 3`)

	userID, orderID := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 10, 1, 2, 3, 4, 0, time.UTC)
	mustExec(t, db, `INSERT INTO `+q+`users (id, email, name, password_hash, role, status, created_at, updated_at, auth_version) VALUES (?, 'old@example.invalid', '旧用户', 'hash', 'user', 'active', ?, ?, 1)`, userID, created, created)
	mustExec(t, db, `INSERT INTO `+q+`orders (id, user_id, idempotency_key, plan_id, plan_name, price_cents, duration_days, traffic_bytes, status, provider, created_at, paid_at, list_price_cents, amount_cents) VALUES (?, ?, 'k1', 'starter', '轻量计划', 1990, 30, 107374182400, 'paid', 'gateway', ?, ?, 1990, 1990)`, orderID, userID, created, created)

	columns := legacyColumns(t, db, s)
	watched := map[string]string{"orders": columns["orders"], "shop_settings": columns["shop_settings"]}
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
	var order model.Order
	if err := db.First(&order, "id = ?", orderID).Error; err != nil || order.Kind != model.OrderKindPurchase || order.FromPlanID != "" || order.CreditCents != 0 || order.UpgradeDays != 0 ||
		order.UpgradeTrafficBytes != 0 || order.UpgradeSnapshot != "" || order.CoversFrom != nil || order.CoversTo != nil {
		t.Fatalf("legacy order %+v %v", order, err)
	}
	var settings model.ShopSettings
	if err := db.First(&settings, "id = 1").Error; err != nil || !settings.UpgradeEnabled || settings.UpgradeMinChargeCents != 100 || settings.YearlyDiscountPercent != 85 || settings.Version != 3 {
		t.Fatalf("settings %+v %v", settings, err)
	}
	// An older binary keeps inserting orders without the new columns.
	later := uuid.NewString()
	mustExec(t, db, `INSERT INTO `+q+`orders (id, user_id, idempotency_key, plan_id, plan_name, price_cents, duration_days, traffic_bytes, status, provider, created_at, list_price_cents, amount_cents) VALUES (?, ?, 'k2', 'pro', '进阶计划', 3990, 30, 1, 'pending', 'gateway', ?, 3990, 3990)`, later, userID, created)
	var olderOrder model.Order
	if err := db.First(&olderOrder, "id = ?", later).Error; err != nil || olderOrder.Kind != model.OrderKindPurchase {
		t.Fatalf("older binary's order %+v %v", olderOrder, err)
	}
	// The database refuses values outside the ranges.
	if err := db.Exec(`UPDATE `+q+`orders SET kind = 'refund' WHERE id = ?`, later).Error; err == nil {
		t.Fatal("an unknown order kind was accepted")
	}
	for _, cents := range []int{0, 10001} {
		if err := db.Exec(`UPDATE `+q+`shop_settings SET upgrade_min_charge_cents = ?`, cents).Error; err == nil {
			t.Fatalf("a minimum charge of %d was accepted", cents)
		}
	}
	// A fresh install has the defaults.
	fresh, _ := newIsolatedSchema(t)
	if err := fresh.First(&settings, "id = 1").Error; err != nil || !settings.UpgradeEnabled || settings.UpgradeMinChargeCents != 100 {
		t.Fatalf("fresh settings %+v %v", settings, err)
	}
}
