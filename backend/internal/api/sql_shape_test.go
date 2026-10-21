package api

import (
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// endLeases combines its fixed state filter with caller conditions that contain OR;
// the caller's condition must stay grouped so already-ended rows are never rewritten.
func TestEndLeasesGroupsCallerCondition(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=/nonexistent"}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return leaseEndQuery(tx, "session_id = ? OR (user_id = ? AND device_id = ?)", "s", "u", "d").
			Updates(map[string]any{"state": model.LeaseEnded, "ended_at": time.Unix(0, 0), "end_reason": model.LeaseReplaced})
	})
	if !strings.Contains(sql, `WHERE state = 'active' AND (session_id = 's' OR (user_id = 'u' AND device_id = 'd'))`) {
		t.Fatalf("caller condition not grouped: %s", sql)
	}
}
