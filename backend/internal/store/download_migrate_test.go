package store

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// DL1-01 (handoff_download.md 4.1): client_downloads arrives additively over a database
// from before it. Repeating the migration changes nothing that existed and keeps the
// administrator's rows; the variant is unique and nothing is published by default.
func TestClientDownloadMigrationOverExistingData(t *testing.T) {
	db, cfg := newIsolatedSchema(t)
	s := cfg.Schema
	seedLegacy(t, db, s)
	if err := Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `DROP TABLE "`+s+`".client_downloads`)
	columns := legacyColumns(t, db, s)
	delete(columns, "client_downloads")
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

	var indexes []string
	if err := db.Raw(`SELECT regexp_replace(indexdef, ' ON [^(]*\(', ' (') FROM pg_indexes WHERE schemaname = ? AND tablename = 'client_downloads'`, s).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	if defs := strings.Join(indexes, "\n"); !strings.Contains(defs, "UNIQUE INDEX idx_client_download_variant (platform, arch, format)") {
		t.Fatalf("variant index missing from\n%s", defs)
	}

	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	row := func() map[string]any {
		return map[string]any{"id": uuid.NewString(), "platform": "windows", "arch": "x64", "format": "exe", "app_version": "1.0.0",
			"url": "https://dl.example.com/a.exe", "released_at": now, "created_at": now, "updated_at": now}
	}
	first := row()
	if err := db.Table(s + ".client_downloads").Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table(s + ".client_downloads").Create(row()).Error; err == nil {
		t.Fatal("a second row for the same variant was accepted")
	}
	var stored model.ClientDownload
	if err := db.First(&stored, "id = ?", first["id"]).Error; err != nil || stored.Published || stored.Version != 1 {
		t.Fatalf("defaults %+v %v", stored, err)
	}
	// A repeated migration keeps the administrator's rows.
	if err := Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.ClientDownload{}).Count(&count)
	if count != 1 {
		t.Fatalf("rows after a repeated migration: %d", count)
	}
}
