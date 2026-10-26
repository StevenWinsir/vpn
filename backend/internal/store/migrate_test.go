package store

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// newIsolatedSchema opens a fresh schema in the private PostgreSQL started by
// scripts/test-native.py. It never reads backend/.env.
func newIsolatedSchema(t *testing.T) (*gorm.DB, config.Config) {
	t.Helper()
	dsn := os.Getenv("NATIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("run scripts/test-native.py for an isolated PostgreSQL instance")
	}
	if !strings.HasPrefix(dsn, "host=/") {
		t.Fatal("database tests require an explicitly supplied private Unix socket")
	}
	cfg := config.Config{Env: "test", Schema: "store_" + strings.ReplaceAll(uuid.NewString(), "-", ""), MigrationTimeout: time.Minute}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: cfg.Schema + "."}})
	if err != nil {
		t.Fatal("isolated database connection failed")
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() {
		_ = db.Exec(`DROP SCHEMA IF EXISTS "` + cfg.Schema + `" CASCADE`).Error
		_ = sqlDB.Close()
	})
	if err = Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	return db, cfg
}

var newTables = []string{"node_groups", "connection_leases", "node_assignments", "usage_buckets", "node_health_events"}

// rewindToLegacy turns a migrated schema back into the shape a63a882 produced: no
// presence tables and no server columns added by this round.
func rewindToLegacy(t *testing.T, db *gorm.DB, s string) {
	t.Helper()
	for _, table := range []string{"connection_leases", "node_assignments", "usage_buckets", "node_health_events"} {
		mustExec(t, db, `DROP TABLE "`+s+`".`+table)
	}
	mustExec(t, db, `ALTER TABLE "`+s+`".nodes DROP COLUMN group_id, DROP COLUMN max_connections, DROP COLUMN label, DROP COLUMN unhealthy_until`)
	mustExec(t, db, `DROP TABLE "`+s+`".node_groups`)
}

func mustExec(t *testing.T, db *gorm.DB, statement string, args ...any) {
	t.Helper()
	if err := db.Exec(statement, args...).Error; err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// fingerprints hashes every row of every pre-existing table over the columns it had
// before the migration, so any changed value, added row or removed row shows up.
func fingerprints(t *testing.T, db *gorm.DB, s string, columns map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, cols := range columns {
		var value string
		if err := db.Raw(`SELECT count(*) || ':' || md5(COALESCE(string_agg(row_to_json(t)::text, E'\n' ORDER BY row_to_json(t)::text), '')) FROM (SELECT ` + cols + ` FROM "` + s + `".` + table + `) t`).Scan(&value).Error; err != nil {
			t.Fatal(err)
		}
		out[table] = value
	}
	return out
}

func legacyColumns(t *testing.T, db *gorm.DB, s string) map[string]string {
	t.Helper()
	var rows []struct{ TableName, Columns string }
	if err := db.Raw(`SELECT table_name, string_agg(quote_ident(column_name), ',' ORDER BY ordinal_position) AS columns FROM information_schema.columns WHERE table_schema = ? GROUP BY table_name`, s).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, row := range rows {
		out[row.TableName] = row.Columns
	}
	return out
}

type legacyFixture struct {
	nodes      []model.Node
	plans      map[string]string // node id -> plan_ids ("" means no node_configs row)
	userID     string
	sessionID  string
	subscribed string
}

// seedLegacy writes the kind of data a63a882 holds: servers with and without plan
// lists, a disabled dedicated one, a server whose config row is missing, a paid
// subscription, a bound native session, a ledger row and an agent.
func seedLegacy(t *testing.T, db *gorm.DB, s string) legacyFixture {
	t.Helper()
	created := time.Date(2026, 9, 1, 2, 3, 4, 0, time.UTC)
	f := legacyFixture{plans: map[string]string{}, userID: uuid.NewString(), sessionID: uuid.NewString(), subscribed: uuid.NewString()}
	f.nodes = []model.Node{
		{ID: uuid.NewString(), Name: "JP1", Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, Version: 3},
		{ID: uuid.NewString(), Name: "HK 专线", Region: "HK", LineType: "dedicated", RatePermille: 1500, Enabled: false, Version: 7},
		{ID: uuid.NewString(), Name: "US-West", Region: "US", LineType: "direct", RatePermille: 500, Enabled: true, Version: 1},
	}
	f.plans[f.nodes[0].ID] = `["starter"]`
	f.plans[f.nodes[1].ID] = `["max","pro"]`
	f.plans[f.nodes[2].ID] = ""
	for i, n := range f.nodes {
		updated := created.Add(time.Duration(i) * time.Hour)
		mustExec(t, db, `INSERT INTO "`+s+`".nodes (id, name, region, line_type, rate_permille, enabled, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, n.ID, n.Name, n.Region, n.LineType, n.RatePermille, n.Enabled, n.Version, created, updated)
		if plans := f.plans[n.ID]; plans != "" {
			mustExec(t, db, `INSERT INTO "`+s+`".node_configs (node_id, ciphertext, plan_ids, updated_at) VALUES (?, ?, ?, ?)`, n.ID, "sealed-"+n.ID, plans, updated)
		}
	}
	mustExec(t, db, `INSERT INTO "`+s+`".node_agents (id, node_id, name, cert_fingerprint, enabled, protocols, agent_version, epoch, created_at, updated_at) VALUES (?, ?, 'agent', ?, true, '["vless"]', '1.0', 2, ?, ?)`, uuid.NewString(), f.nodes[0].ID, strings.Repeat("a", 64), created, created)
	mustExec(t, db, `INSERT INTO "`+s+`".users (id, email, name, password_hash, role, status, created_at, updated_at, auth_version) VALUES (?, 'legacy@example.invalid', '旧用户', 'hash', 'user', 'active', ?, ?, 1)`, f.userID, created, created)
	mustExec(t, db, `INSERT INTO "`+s+`".subscriptions (id, user_id, plan_id, plan_name, starts_at, expires_at, traffic_limit_bytes, used_units, upload_bytes, download_bytes, max_devices, allow_dedicated, is_test, created_at, updated_at) VALUES (?, ?, 'starter', '轻量计划', ?, ?, 1073741824, 300000, 100, 200, 2, false, false, ?, ?)`, f.subscribed, f.userID, created, created.Add(30*24*time.Hour), created, created)
	mustExec(t, db, `INSERT INTO "`+s+`".native_sessions (id, user_id, token_hash, device_id, platform, app_version, subscription_id, subscription_starts_at, node_id, rate_permille, metering_source, last_sequence, upload_bytes, download_bytes, last_seen_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, 'macos', '1.0.0', ?, ?, ?, 1000, 'client_reported', 1, 100, 200, ?, ?, ?, ?)`, f.sessionID, f.userID, strings.Repeat("b", 64), uuid.NewString(), f.subscribed, created, f.nodes[0].ID, created, created.Add(12*time.Hour), created, created)
	mustExec(t, db, `INSERT INTO "`+s+`".client_traffic_reports (id, node_id, session_id, sequence, user_id, subscription_id, upload_bytes, download_bytes, upload_delta, download_delta, charged_units, rate_permille, source, created_at) VALUES (?, ?, ?, 1, ?, ?, 100, 200, 100, 200, 300000, 1000, 'client_reported', ?)`, uuid.NewString(), f.nodes[0].ID, f.sessionID, f.userID, f.subscribed, created)
	return f
}

// Round 1: additive, repeatable migration over a pre-group database. Each server
// becomes a one-server group with the same id (D3); nothing that existed changes.
func TestNodeGroupMigrationOverLegacyData(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	rewindToLegacy(t, db, s)
	f := seedLegacy(t, db, s)
	columns := legacyColumns(t, db, s)
	if _, ok := columns["node_groups"]; ok {
		t.Fatal("legacy rewind left node_groups behind")
	}
	before := fingerprints(t, db, s, columns)

	// Two instances migrate at once, then a third run.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Migrate(db, cfg) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	if after := fingerprints(t, db, s, columns); !mapsEqual(before, after) {
		t.Fatalf("pre-existing data changed by migration:\n%v\n%v", before, after)
	}
	for _, table := range newTables {
		var exists bool
		db.Raw(`SELECT to_regclass(?) IS NOT NULL`, `"`+s+`".`+table).Scan(&exists)
		if !exists {
			t.Fatalf("table %s missing", table)
		}
	}
	type pair struct {
		model.Node
		GName, GRegion, GLineType, GPlanIDs string
		GRate, GVersion                     int64
		GEnabled                            bool
		GSort                               int
	}
	var rows []pair
	if err := db.Raw(`SELECT n.*, g.name AS g_name, g.region AS g_region, g.line_type AS g_line_type, g.plan_ids AS g_plan_ids, g.rate_permille AS g_rate, g.version AS g_version, g.enabled AS g_enabled, g.sort AS g_sort
		FROM "` + s + `".nodes n JOIN "` + s + `".node_groups g ON g.id = n.group_id ORDER BY n.name`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	var groups int64
	db.Model(&model.NodeGroup{}).Count(&groups)
	if len(rows) != len(f.nodes) || groups != int64(len(f.nodes)) {
		t.Fatalf("want %d one-server groups, got %d joined rows and %d groups", len(f.nodes), len(rows), groups)
	}
	for _, row := range rows {
		want := f.plans[row.ID]
		if want == "" {
			want = "[]"
		}
		if row.GroupID == nil || *row.GroupID != row.ID || row.MaxConnections != 1000 || row.Label != "" || row.UnhealthyUntil != nil {
			t.Fatalf("server columns not back-filled as specified: %+v", row.Node)
		}
		if row.GName != row.Name || row.GRegion != row.Region || row.GLineType != row.LineType || row.GRate != row.RatePermille || row.GEnabled != row.Enabled || row.GVersion != row.Version || row.GPlanIDs != want || row.GSort != 0 {
			t.Fatalf("group %s does not copy its server: %+v", row.ID, row)
		}
	}

	// An older binary after a rollback inserts and deletes servers with only the
	// columns it knows; the defaults keep those rows valid.
	adopted := uuid.NewString()
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO "`+s+`".nodes (id, name, region, line_type, rate_permille, enabled, version, created_at, updated_at) VALUES (?, 'SG1', 'SG', 'direct', 800, true, 1, ?, ?)`, adopted, now, now)
	mustExec(t, db, `INSERT INTO "`+s+`".node_configs (node_id, ciphertext, plan_ids, updated_at) VALUES (?, 'sealed', '["pro"]', ?)`, adopted, now)
	mustExec(t, db, `DELETE FROM "`+s+`".node_configs WHERE node_id = ?`, f.nodes[2].ID)
	mustExec(t, db, `DELETE FROM "`+s+`".nodes WHERE id = ?`, f.nodes[2].ID)
	var limit int
	db.Raw(`SELECT max_connections FROM "`+s+`".nodes WHERE id = ?`, adopted).Scan(&limit)
	if limit != 1000 {
		t.Fatalf("legacy insert got max_connections %d", limit)
	}
	// A group edited by the new release wins over its server's legacy columns.
	mustExec(t, db, `UPDATE "`+s+`".node_groups SET name = 'JP1 东京', version = version + 1 WHERE id = ?`, f.nodes[0].ID)
	if err := Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	var group model.NodeGroup
	if err := db.First(&group, "id = ?", adopted).Error; err != nil || group.Name != "SG1" || group.PlanIDs != `["pro"]` || group.RatePermille != 800 || group.Version != 1 {
		t.Fatalf("server written by an older binary not adopted: %+v %v", group, err)
	}
	var edited model.NodeGroup
	if err := db.First(&edited, "id = ?", f.nodes[0].ID).Error; err != nil || edited.Name != "JP1 东京" || edited.Version != 4 {
		t.Fatalf("existing group overwritten by a later migration: %+v", edited)
	}
	var ungrouped int64
	db.Model(&model.Node{}).Where("group_id IS NULL").Count(&ungrouped)
	if ungrouped != 0 {
		t.Fatal("migration left a server without a group")
	}
	// An empty group stays (deleting it is an admin decision) and is harmless.
	if err := db.First(&model.NodeGroup{}, "id = ?", f.nodes[2].ID).Error; err != nil {
		t.Fatal("group of a server deleted by an older binary vanished")
	}
	// A group with servers cannot be deleted.
	if err := db.Exec(`DELETE FROM "`+s+`".node_groups WHERE id = ?`, adopted).Error; err == nil {
		t.Fatal("group with a server was deletable")
	}

	var indexes []string
	if err := db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = ? AND tablename IN ('connection_leases', 'usage_buckets', 'node_health_events', 'node_assignments', 'nodes')`, s).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"idx_lease_active_device", "idx_lease_active_session", "idx_lease_active_node", "idx_lease_active_user", "idx_lease_ended", "usage_buckets_pkey", "idx_usage_bucket_start", "idx_node_health_recent", "idx_node_health_occurred", "node_assignments_pkey", "idx_assignment_node", "idx_assignment_last_used", "idx_lease_session", "idx_lease_group", "idx_nodes_group_id"} {
		if !slices.Contains(indexes, want) {
			t.Fatalf("index %s missing; have %v", want, indexes)
		}
	}
}

// The partial unique indexes allow one active lease per device and per session while
// keeping any number of ended rows; the check constraints reject unknown values.
func TestConnectionLeaseConstraints(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	user := model.User{ID: uuid.NewString(), Email: "lease@example.invalid", Name: "lease", PasswordHash: "x", Role: "user", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	device := uuid.NewString()
	session := func() string {
		id := uuid.NewString()
		if err := db.Omit("User").Create(&model.NativeSession{ID: id, UserID: user.ID, TokenHash: uuid.NewString(), DeviceID: device, Platform: "macos", AppVersion: "1", LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := session(), session()
	lease := func(sessionID, deviceID, state, method string) error {
		row := model.ConnectionLease{ID: uuid.NewString(), UserID: user.ID, SessionID: sessionID, DeviceID: deviceID, Platform: "macos", AppVersion: "1", GroupID: uuid.NewString(), NodeID: uuid.NewString(), Method: method, State: state, AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(90 * time.Second)}
		if state == model.LeaseEnded {
			row.EndedAt, row.EndReason = &now, model.LeaseReleased
		}
		return db.Omit("User", "Session").Create(&row).Error
	}
	if err := lease(first, device, model.LeaseActive, model.LeaseByConnections); err != nil {
		t.Fatal(err)
	}
	if lease(second, device, model.LeaseActive, model.LeaseByConnections) == nil {
		t.Fatal("two active leases for one device")
	}
	if lease(first, uuid.NewString(), model.LeaseActive, model.LeaseByGrant) == nil {
		t.Fatal("two active leases for one session")
	}
	for range 3 {
		if err := lease(first, device, model.LeaseEnded, model.LeaseByLegacy); err != nil {
			t.Fatalf("ended history rejected: %v", err)
		}
	}
	if lease(second, uuid.NewString(), "paused", model.LeaseByConnections) == nil || lease(second, uuid.NewString(), model.LeaseActive, "magic") == nil {
		t.Fatal("unknown state or method accepted")
	}
	// Leases go with their session and their user.
	mustExec(t, db, `DELETE FROM "`+s+`".native_sessions WHERE id = ?`, first)
	var left int64
	db.Model(&model.ConnectionLease{}).Count(&left)
	if left != 0 {
		t.Fatalf("%d leases survived their session", left)
	}
	if err := db.Create(&model.UsageBucket{UserID: user.ID, BucketStart: now, UploadBytes: -1, UpdatedAt: now}).Error; err == nil {
		t.Fatal("negative usage accepted")
	}
	if err := db.Create(&model.UsageBucket{UserID: user.ID, BucketStart: now, UploadBytes: 5, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `DELETE FROM "`+s+`".users WHERE id = ?`, user.ID)
	db.Model(&model.UsageBucket{}).Count(&left)
	if left != 0 {
		t.Fatal("usage history survived its user")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// handoff_shop.md S1-01: the shop columns and table arrive additively, the one-off
// "recommended" back-fill runs once, and nothing an administrator set is undone by a
// repeated migration.
func TestShopMigrationOverLegacyData(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	q := `"` + s + `".`
	mustExec(t, db, `ALTER TABLE `+q+`plans DROP COLUMN yearly_enabled, DROP COLUMN yearly_duration_days, DROP COLUMN yearly_traffic_bytes, DROP COLUMN yearly_discount_percent, DROP COLUMN featured, DROP COLUMN note, DROP COLUMN version, DROP COLUMN deleted_at`)
	mustExec(t, db, `ALTER TABLE `+q+`orders DROP COLUMN billing_cycle, DROP COLUMN cycle_discount_percent, DROP COLUMN original_cents, DROP COLUMN ent_max_devices, DROP COLUMN ent_allow_dedicated`)
	mustExec(t, db, `ALTER TABLE `+q+`coupons DROP COLUMN billing_cycles`)
	mustExec(t, db, `DROP TABLE `+q+`shop_settings`)

	userID, orderID, couponID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 9, 1, 2, 3, 4, 0, time.UTC)
	mustExec(t, db, `INSERT INTO `+q+`users (id, email, name, password_hash, role, status, created_at, updated_at, auth_version) VALUES (?, 'old@example.invalid', '旧用户', 'hash', 'user', 'active', ?, ?, 1)`, userID, created, created)
	mustExec(t, db, `INSERT INTO `+q+`orders (id, user_id, idempotency_key, plan_id, plan_name, price_cents, duration_days, traffic_bytes, status, provider, created_at, list_price_cents, amount_cents) VALUES (?, ?, 'k1', 'starter', '轻量计划', 1990, 30, 107374182400, 'paid', 'gateway', ?, 1990, 1990)`, orderID, userID, created)
	mustExec(t, db, `INSERT INTO `+q+`coupons (id, code, discount_type, percent_off, enabled, created_by, created_at, updated_at) VALUES (?, 'OLD-CODE', 'percent', 10, true, ?, ?, ?)`, couponID, userID, created, created)

	columns := legacyColumns(t, db, s)
	watched := map[string]string{"users": columns["users"], "orders": columns["orders"], "coupons": columns["coupons"], "plans": columns["plans"], "subscriptions": columns["subscriptions"]}
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
	if err := db.First(&order, "id = ?", orderID).Error; err != nil || order.BillingCycle != "monthly" || order.CycleDiscountPercent != 100 || order.OriginalCents != 0 || order.EntMaxDevices != nil || order.EntAllowDedicated != nil {
		t.Fatalf("legacy order %+v %v", order, err)
	}
	var coupon model.Coupon
	if err := db.First(&coupon, "id = ?", couponID).Error; err != nil || coupon.BillingCycles != "all" {
		t.Fatalf("legacy coupon %+v %v", coupon, err)
	}
	var settings model.ShopSettings
	if err := db.First(&settings, "id = 1").Error; err != nil || settings.YearlyDiscountPercent != 90 || settings.Version != 1 {
		t.Fatalf("settings %+v %v", settings, err)
	}
	featured := func() map[string]bool {
		var rows []model.Plan
		if err := db.Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, p := range rows {
			out[p.ID] = p.Featured
			if !p.YearlyEnabled || p.YearlyDurationDays != 365 || p.Version != 1 && p.ID != "max" || p.Note != "" || p.YearlyDiscountPercent != nil || p.YearlyTrafficBytes != nil {
				t.Errorf("plan %s defaults %+v", p.ID, p)
			}
		}
		return out
	}
	if got := featured(); !got["pro"] || got["starter"] || got["max"] || len(got) != 3 {
		t.Fatalf("recommended after the first migration %v", got)
	}

	// The administrator unfeatures pro, sets 8.5折 and soft-deletes max.
	mustExec(t, db, `UPDATE `+q+`plans SET featured = false WHERE id = 'pro'`)
	mustExec(t, db, `UPDATE `+q+`plans SET active = false, deleted_at = now(), version = 2 WHERE id = 'max'`)
	mustExec(t, db, `UPDATE `+q+`shop_settings SET yearly_discount_percent = 85, version = 4`)
	for range 2 {
		if err := Migrate(db, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if got := featured(); got["pro"] {
		t.Fatalf("a repeated migration re-featured pro: %v", got)
	}
	var gone model.Plan
	if err := db.First(&gone, "id = 'max'").Error; err != nil || gone.DeletedAt == nil || gone.Active || gone.Version != 2 {
		t.Fatalf("a soft-deleted seed plan came back: %+v %v", gone, err)
	}
	var total int64
	db.Model(&model.Plan{}).Count(&total)
	if err := db.First(&settings, "id = 1").Error; err != nil || settings.YearlyDiscountPercent != 85 || settings.Version != 4 || total != 3 {
		t.Fatalf("settings or plan count reset: %+v %d", settings, total)
	}
	// Fresh installs carry the recommended flag from the seed.
	fresh, _ := newIsolatedSchema(t)
	var pro model.Plan
	if err := fresh.First(&pro, "id = 'pro'").Error; err != nil || !pro.Featured || pro.CreatedAt.IsZero() {
		t.Fatalf("fresh seed %+v %v", pro, err)
	}
	// The single-row table refuses a second row, and the database refuses a bad discount.
	if err := db.Exec(`INSERT INTO ` + q + `shop_settings (id, yearly_discount_percent, version, updated_at) VALUES (2, 90, 1, now())`).Error; err == nil {
		t.Fatal("a second settings row was accepted")
	}
	if err := db.Exec(`UPDATE ` + q + `shop_settings SET yearly_discount_percent = 49`).Error; err == nil {
		t.Fatal("a discount below 50 was accepted by the database")
	}
}
