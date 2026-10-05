package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
	"vpn/backend/internal/operations"
)

func TestConsoleReadinessAndAdministratorPostgres(t *testing.T) {
	db, cfg := newIsolatedNativeDatabase(t)
	ctx := context.Background()
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32))
	cfg.TestPurchase, cfg.ClientAllowTestEntitlements = true, true
	email, password := "console-admin@example.invalid", "Console-Fixture-Password-Only"
	if err := operations.CreateAdmin(ctx, db, email, password, 10); err != nil {
		t.Fatal(err)
	}
	if err := operations.CreateAdmin(ctx, db, email, "Must-not-overwrite-password", 10); err == nil {
		t.Fatal("administrator creation overwrote an existing account")
	}
	var admin model.User
	if err := db.Where("email = ?", email).First(&admin).Error; err != nil || bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)) != nil {
		t.Fatal("original administrator credentials changed")
	}
	var audits int64
	db.Model(&model.AdminAudit{}).Count(&audits)
	if audits != 1 {
		t.Fatal("administrator bootstrap was not atomically audited")
	}
	if err := operations.GrantAdmin(ctx, db, email); err != nil {
		t.Fatal(err)
	}
	if err := operations.GrantAdmin(ctx, db, "missing@example.invalid"); err == nil {
		t.Fatal("grant created a missing account")
	}
	if err := operations.CreateAdmin(ctx, db, "invalid", "short", 10); err == nil {
		t.Fatal("invalid bootstrap accepted")
	}
	sub := model.Subscription{ID: uuid.NewString(), UserID: admin.ID, PlanID: "starter", PlanName: "Starter", StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), TrafficLimitBytes: 1 << 30, MaxDevices: 2, IsTest: true}
	if err := db.Omit("User", "Plan").Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	node := model.Node{ID: uuid.NewString(), Name: "Fixture", Region: "HK", LineType: "direct", RatePermille: 500, Enabled: true, Version: 1}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	secret := "proxies: [{name: Fixture, type: ss, server: 127.0.0.1, port: 18443, cipher: aes-256-gcm, password: readiness-private-fixture}]"
	cipher, err := nodes.Encrypt(cfg.NodeEncryptionKey, node.ID, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	record := model.NodeConfig{NodeID: node.ID, Ciphertext: cipher, PlanIDs: "[]"}
	if err := db.Omit("Node").Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	// Check transaction_read_only from inside Inspect's first database query.
	checkedReadOnly := false
	const callback = "operations-test-readonly"
	if err := db.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
		if !strings.Contains(tx.Statement.SQL.String(), "pg_catalog.pg_tables") {
			return
		}
		var setting string
		if err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SHOW transaction_read_only").Scan(&setting); err != nil || setting != "on" {
			t.Error("readiness did not use a read-only PostgreSQL transaction")
		}
		checkedReadOnly = true
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Row().Remove(callback)
	inspect := func() operations.ReadinessReport {
		t.Helper()
		report, err := operations.Inspect(ctx, db, cfg, email)
		if err != nil {
			t.Fatal(err)
		}
		serialized, _ := json.Marshal(report)
		if bytes.Contains(serialized, []byte(password)) || bytes.Contains(serialized, []byte("readiness-private-fixture")) || bytes.Contains(serialized, []byte(cipher)) {
			t.Fatal("readiness exposed private data")
		}
		if report.ProductionReady || report.NodeAuthoritative {
			t.Fatal("development report claims production readiness")
		}
		return report
	}
	report := inspect()
	if !report.DevelopmentReady || report.EligibleNodes != 1 || report.Administrators != 1 || report.AccountReason != "eligible" {
		t.Fatalf("unexpected readiness: %+v", report)
	}
	if !checkedReadOnly {
		t.Fatal("read-only transaction assertion did not execute")
	}
	cfg.ClientAllowTestEntitlements = false
	if report = inspect(); report.DevelopmentReady || report.AccountReason != "paid_vip_required" {
		t.Fatal("test entitlement bypass")
	}
	cfg.ClientAllowTestEntitlements = true
	if err := db.Model(&node).Update("line_type", "dedicated").Error; err != nil {
		t.Fatal(err)
	}
	if report = inspect(); report.DevelopmentReady || report.EligibleNodes != 0 {
		t.Fatal("starter admitted to dedicated node")
	}
	if err := db.Model(&node).Update("line_type", "direct").Error; err != nil {
		t.Fatal(err)
	}
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{32}, 32))
	if report = inspect(); report.DevelopmentReady || report.InvalidNodeConfigs != 1 {
		t.Fatal("incorrect encryption key ignored")
	}
	if err := db.Model(&sub).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if report = inspect(); report.AccountReason != "subscription_expired" {
		t.Fatal("expired subscription accepted")
	}
	var after model.User
	db.First(&after, "id = ?", admin.ID)
	if after.PasswordHash != admin.PasswordHash || after.Role != "admin" {
		t.Fatal("readiness modified the account")
	}
	if err := db.Exec("DROP TABLE " + cfg.Schema + ".admin_audits").Error; err != nil {
		t.Fatal(err)
	}
	if report = inspect(); len(report.MissingTables) != 1 || report.MissingTables[0] != "admin_audits" || report.DevelopmentReady {
		t.Fatal("missing migration was not diagnosed")
	}
	if err := operations.CreateAdmin(ctx, db, "rollback@example.invalid", password, 10); err == nil {
		t.Fatal("admin created without audit table")
	}
	var count int64
	db.Model(&model.User{}).Where("email = ?", "rollback@example.invalid").Count(&count)
	if count != 0 {
		t.Fatal("admin write was not rolled back when audit failed")
	}
}
