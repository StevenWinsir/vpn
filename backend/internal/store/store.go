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
		if err := tx.AutoMigrate(&model.User{}, &model.Plan{}, &model.Session{}, &model.Subscription{}, &model.Order{}, &model.Node{}, &model.NativeSession{}, &model.ClientTrafficReport{}); err != nil {
			return err
		}
		const gib = int64(1024 * 1024 * 1024)
		plans := []model.Plan{
			{ID: "starter", Name: "轻量计划", Description: "适合轻量浏览与日常连接", PriceCents: 1990, DurationDays: 30, TrafficBytes: 100 * gib, MaxDevices: 2, AllowDedicated: false, Active: true, Sort: 1},
			{ID: "pro", Name: "进阶计划", Description: "更充裕的流量，支持专线权益", PriceCents: 3990, DurationDays: 30, TrafficBytes: 300 * gib, MaxDevices: 5, AllowDedicated: true, Active: true, Sort: 2},
			{ID: "max", Name: "旗舰计划", Description: "面向多设备和高流量需求", PriceCents: 7990, DurationDays: 30, TrafficBytes: 800 * gib, MaxDevices: 10, AllowDedicated: true, Active: true, Sort: 3},
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&plans).Error
	})
}
