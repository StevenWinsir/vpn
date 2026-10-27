package store

import (
	"context"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

func Open(c config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(c.DSN()), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: c.Schema + "."}})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(c.MaxOpen)
	sqlDB.SetMaxIdleConns(c.MaxIdle)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Explicit, additive schema setup. Never drops or truncates existing tables.
func Migrate(db *gorm.DB, c config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.MigrationTimeout)
	defer cancel()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(708614921)").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE SCHEMA IF NOT EXISTS "` + c.Schema + `"`).Error; err != nil {
			return err
		}
		// The one-off "recommended" back-fill below must only run on the migration that
		// adds the column, or it would undo the administrator's choice (handoff_shop.md 4.1).
		firstShopMigration, err := plansLackColumn(tx, c.Schema, "featured")
		if err != nil {
			return err
		}
		if err := tx.AutoMigrate(&model.User{}, &model.Plan{}, &model.Session{}, &model.Subscription{}, &model.Order{}, &model.Node{}, &model.NativeSession{}, &model.ClientTrafficReport{}, &model.NodeConfig{}, &model.AdminAudit{}, &model.NodeAgent{}, &model.SubscriptionPeriod{}, &model.ConnectionGrant{}, &model.NodeUsageReport{}, &model.EmailAuthChallenge{}, &model.AuthMailJob{}, &model.AuthRateBucket{}, &model.NodeGroup{}, &model.ConnectionLease{}, &model.NodeAssignment{}, &model.UsageBucket{}, &model.NodeHealthEvent{}, &model.PaymentNotification{}, &model.Coupon{}, &model.LoginEvent{}, &model.IPGeoCache{}, &model.Announcement{}, &model.AnnouncementRead{}, &model.ShopSettings{}, &model.Referral{}); err != nil {
			return err
		}
		s := `"` + c.Schema + `".`
		// Partial indexes use constant predicates, never the current time: expiry is
		// always checked when reading. One active proof per (purpose, email)
		// (handoff_auth.md 4.1); one active lease per device and per native session.
		for _, stmt := range []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_challenge_active ON ` + s + `email_auth_challenges (purpose, email) WHERE status = 'active'`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_lease_active_device ON ` + s + `connection_leases (user_id, device_id) WHERE state = 'active'`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_lease_active_session ON ` + s + `connection_leases (session_id) WHERE state = 'active'`,
			`CREATE INDEX IF NOT EXISTS idx_lease_active_node ON ` + s + `connection_leases (node_id, expires_at) WHERE state = 'active'`,
			`CREATE INDEX IF NOT EXISTS idx_lease_active_user ON ` + s + `connection_leases (user_id, expires_at) WHERE state = 'active'`,
			`CREATE INDEX IF NOT EXISTS idx_lease_ended ON ` + s + `connection_leases (ended_at) WHERE state = 'ended'`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_lease_idempotency ON ` + s + `connection_leases (session_id, idempotency_key) WHERE idempotency_key <> ''`,
			`CREATE INDEX IF NOT EXISTS idx_lease_grant ON ` + s + `connection_leases (grant_id) WHERE grant_id <> ''`,
			`CREATE INDEX IF NOT EXISTS idx_lease_session_latest ON ` + s + `connection_leases (session_id, acquired_at DESC)`,
			// Live pending gateway orders: the per-user cap and the reconciler's scan
			// (handoff_payment_account.md 5.1).
			`CREATE INDEX IF NOT EXISTS idx_order_pending_user ON ` + s + `orders (user_id, expires_at) WHERE status = 'pending'`,
			`CREATE INDEX IF NOT EXISTS idx_order_pending_expiry ON ` + s + `orders (expires_at) WHERE status = 'pending'`,
			// Referral reward queue and the sweep for first purchases a binding missed
			// (handoff_coupon.md 5.3).
			`CREATE INDEX IF NOT EXISTS idx_referral_qualified ON ` + s + `referrals (next_attempt_at) WHERE status = 'qualified'`,
			`CREATE INDEX IF NOT EXISTS idx_referral_registered ON ` + s + `referrals (created_at) WHERE status = 'registered'`,
		} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		if err := backfillNodeGroups(tx, s); err != nil {
			return err
		}
		if firstShopMigration {
			if err := tx.Exec(`UPDATE ` + s + `plans SET featured = true WHERE id = 'pro'`).Error; err != nil {
				return err
			}
		}
		// The settings row is only inserted, never overwritten: a repeated migration keeps
		// the administrator's discount.
		if err := tx.Exec(`INSERT INTO `+s+`shop_settings (id, yearly_discount_percent, version, updated_at) VALUES (1, ?, 1, ?) ON CONFLICT (id) DO NOTHING`, model.DefaultYearlyDiscountPercent, time.Now().UTC()).Error; err != nil {
			return err
		}
		const gib = int64(1024 * 1024 * 1024)
		plans := []model.Plan{
			{ID: "starter", Name: "轻量计划", Description: "适合轻量浏览与日常连接", PriceCents: 1990, DurationDays: 30, TrafficBytes: 100 * gib, MaxDevices: 2, AllowDedicated: false, Active: true, Sort: 1, YearlyEnabled: true, YearlyDurationDays: 365, Version: 1},
			{ID: "pro", Name: "进阶计划", Description: "更充裕的流量，支持专线权益", PriceCents: 3990, DurationDays: 30, TrafficBytes: 300 * gib, MaxDevices: 5, AllowDedicated: true, Active: true, Sort: 2, YearlyEnabled: true, YearlyDurationDays: 365, Featured: true, Version: 1},
			{ID: "max", Name: "旗舰计划", Description: "面向多设备和高流量需求", PriceCents: 7990, DurationDays: 30, TrafficBytes: 800 * gib, MaxDevices: 10, AllowDedicated: true, Active: true, Sort: 3, YearlyEnabled: true, YearlyDurationDays: 365, Version: 1},
		}
		seeded := time.Now().UTC()
		for i := range plans {
			plans[i].CreatedAt, plans[i].UpdatedAt = seeded, seeded
		}
		return tx.Select("*").Clauses(clause.OnConflict{DoNothing: true}).Create(&plans).Error
	})
}

// plansLackColumn reports whether the plans table exists without the named column
// (an upgrade that is about to add it). A fresh install has no table: its seeds already
// carry the right values.
func plansLackColumn(tx *gorm.DB, schemaName, column string) (bool, error) {
	var tables, columns int64
	if err := tx.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'plans'`, schemaName).Scan(&tables).Error; err != nil {
		return false, err
	}
	if err := tx.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = 'plans' AND column_name = ?`, schemaName, column).Scan(&columns).Error; err != nil {
		return false, err
	}
	return tables > 0 && columns == 0, nil
}

// backfillNodeGroups gives every server without a group a one-server group with the
// same id (decision D3), copying its public metadata and plan list. Servers that
// already have a group are skipped, so the step is repeatable; it also adopts servers
// an older binary inserted after a rollback. Server rows keep their version and
// updated_at; subscriptions, sessions and ledgers are not touched.
func backfillNodeGroups(tx *gorm.DB, s string) error {
	if err := tx.Exec(`INSERT INTO ` + s + `node_groups (id, name, region, line_type, rate_permille, plan_ids, enabled, sort, version, created_at, updated_at)
		SELECT n.id, n.name, n.region, n.line_type, n.rate_permille, COALESCE(c.plan_ids, '[]'), n.enabled, 0, n.version, n.created_at, n.updated_at
		FROM ` + s + `nodes AS n LEFT JOIN ` + s + `node_configs AS c ON c.node_id = n.id
		WHERE n.group_id IS NULL
		ON CONFLICT (id) DO NOTHING`).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE ` + s + `nodes SET group_id = id WHERE group_id IS NULL`).Error
}
