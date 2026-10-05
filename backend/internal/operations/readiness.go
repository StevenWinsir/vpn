package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

type ReadinessReport struct {
	CheckedAt          time.Time `json:"checked_at"`
	DevelopmentReady   bool      `json:"managed_development_ready"`
	ProductionReady    bool      `json:"production_ready"`
	MeteringSource     string    `json:"metering_source"`
	NodeAuthoritative  bool      `json:"node_authoritative"`
	MacOSTransport     string    `json:"macos_managed_transport"`
	MissingTables      []string  `json:"missing_tables"`
	Administrators     int64     `json:"active_administrators"`
	EnabledNodes       int64     `json:"enabled_nodes"`
	EligibleNodes      int       `json:"eligible_nodes_for_account"`
	InvalidNodeConfigs int       `json:"invalid_node_configs"`
	CatalogEnabled     bool      `json:"catalog_enabled"`
	EncryptionKeyValid bool      `json:"node_encryption_key_valid"`
	TestAccessEnabled  bool      `json:"test_client_access_enabled"`
	AccountChecked     string    `json:"account_checked,omitempty"`
	AccountReason      string    `json:"account_reason"`
	Warnings           []string  `json:"warnings"`
}

// Inspect uses a PostgreSQL read-only repeatable-read transaction. It cannot
// migrate tables or alter accounts, orders, sessions, credentials or counters.
func Inspect(ctx context.Context, db *gorm.DB, cfg config.Config, email string) (ReadinessReport, error) {
	report := ReadinessReport{
		CheckedAt: time.Now().UTC(), MeteringSource: "client_reported",
		MacOSTransport: "loopback_mixed_and_system_proxy_not_tun",
		MissingTables:  []string{}, Warnings: []string{"node_side_metering_and_per_user_revocation_not_implemented", "real_payments_and_release_signing_not_verified"},
		CatalogEnabled: cfg.ClientNodeCatalog, EncryptionKeyValid: nodes.ValidateKey(cfg.NodeEncryptionKey),
		TestAccessEnabled: cfg.Env != "production" && cfg.TestPurchase && cfg.ClientAllowTestEntitlements,
		AccountReason:     "account_not_checked",
	}
	if email != "" {
		var valid bool
		report.AccountChecked, valid = NormalizeEmail(email)
		if !valid {
			return report, errors.New("invalid readiness account email")
		}
	}
	if cfg.SSLMode != "verify-full" {
		report.Warnings = append(report.Warnings, "database_tls_not_verified")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var available []string
		if err := tx.Raw("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = ?", cfg.Schema).Scan(&available).Error; err != nil {
			return err
		}
		for _, table := range []string{"users", "sessions", "plans", "subscriptions", "orders", "nodes", "native_sessions", "client_traffic_reports", "node_configs", "admin_audits"} {
			if !slices.Contains(available, table) {
				report.MissingTables = append(report.MissingTables, table)
			}
		}
		if len(report.MissingTables) != 0 {
			return nil
		}
		if err := tx.Model(&model.User{}).Where("role = ? AND status = ?", "admin", "active").Count(&report.Administrators).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Node{}).Where("enabled = ?", true).Count(&report.EnabledNodes).Error; err != nil {
			return err
		}
		if report.AccountChecked == "" {
			return nil
		}
		var user model.User
		err := tx.Select("id", "email", "status").Where("email = ?", report.AccountChecked).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && user.Status != "active" {
			report.AccountReason = "account_missing_or_disabled"
			return nil
		}
		if err != nil {
			return err
		}
		var sub model.Subscription
		err = tx.Where("user_id = ?", user.ID).First(&sub).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			report.AccountReason = "upgrade_required"
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case sub.IsTest && !report.TestAccessEnabled:
			report.AccountReason = "paid_vip_required"
		case report.CheckedAt.Before(sub.StartsAt) || !report.CheckedAt.Before(sub.ExpiresAt):
			report.AccountReason = "subscription_expired"
		case sub.TrafficLimitBytes <= 0 || sub.TrafficLimitBytes > billing.MaxClientCounter || sub.UsedUnits < 0 || sub.TrafficLimitBytes*1000-sub.UsedUnits < 1000:
			report.AccountReason = "quota_exhausted"
		default:
			report.AccountReason = "eligible"
		}
		var rows []struct {
			model.Node `gorm:"embedded"`
			Ciphertext string
			PlanIDs    string
		}
		if err := tx.Table(cfg.Schema+".nodes AS n").Select("n.*, c.ciphertext, c.plan_ids").Joins("LEFT JOIN "+cfg.Schema+".node_configs AS c ON c.node_id = n.id").Where("n.enabled = ?", true).Order("n.id").Limit(nodes.MaxNodes + 1).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var plans []string
			data, decryptErr := nodes.Decrypt(cfg.NodeEncryptionKey, row.ID, row.Ciphertext)
			proxies, parseErr := nodes.Parse(string(data))
			if decryptErr != nil || parseErr != nil || len(proxies) != 1 || proxies[0]["name"] != row.Name || json.Unmarshal([]byte(row.PlanIDs), &plans) != nil || row.RatePermille < 1 || row.RatePermille > 10000 {
				report.InvalidNodeConfigs++
				continue
			}
			if row.LineType == "dedicated" && !sub.AllowDedicated || len(plans) != 0 && !slices.Contains(plans, sub.PlanID) {
				continue
			}
			report.EligibleNodes++
		}
		report.DevelopmentReady = cfg.Env != "production" && report.Administrators > 0 && report.CatalogEnabled && report.EncryptionKeyValid && report.AccountReason == "eligible" && report.EligibleNodes > 0 && report.InvalidNodeConfigs == 0 && report.EnabledNodes <= nodes.MaxNodes
		return nil
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return report, errors.New("read-only readiness inspection failed; database details suppressed")
	}
	return report, nil
}
