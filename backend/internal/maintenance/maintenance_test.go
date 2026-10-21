package maintenance

import (
	"context"
	"os"
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
	"vpn/backend/internal/store"
)

type env struct {
	db     *gorm.DB
	schema string
	now    time.Time
	user   model.User
}

// newEnv creates an isolated schema in the private PostgreSQL started by
// scripts/test-native.py. It never reads backend/.env.
func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("NATIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("run scripts/test-native.py for an isolated PostgreSQL instance")
	}
	if !strings.HasPrefix(dsn, "host=/") {
		t.Fatal("database tests require an explicitly supplied private Unix socket")
	}
	cfg := config.Config{Env: "test", Schema: "maint_" + strings.ReplaceAll(uuid.NewString(), "-", ""), MigrationTimeout: time.Minute}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true, Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: cfg.Schema + "."}})
	if err != nil {
		t.Fatal("isolated database connection failed")
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() {
		_ = db.Exec(`DROP SCHEMA IF EXISTS "` + cfg.Schema + `" CASCADE`).Error
		_ = sqlDB.Close()
	})
	if err = store.Migrate(db, cfg); err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, schema: cfg.Schema, now: time.Date(2026, 10, 20, 8, 7, 30, 0, time.UTC)}
	e.user = e.newUser(t)
	return e
}

func (e *env) newUser(t *testing.T) model.User {
	t.Helper()
	u := model.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.invalid", Name: "m", PasswordHash: "x", Role: "user", Status: "active"}
	if err := e.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) sweeper() *Sweeper {
	s := New(e.db, e.schema)
	s.Now = func() time.Time { return e.now }
	s.Logf = func(string, ...any) {}
	return s
}

func (e *env) session(t *testing.T, expires time.Time, revoked bool) model.NativeSession {
	t.Helper()
	sess := model.NativeSession{ID: uuid.NewString(), UserID: e.user.ID, TokenHash: uuid.NewString(), DeviceID: uuid.NewString(), Platform: "android", AppVersion: "1", LastSeenAt: e.now, ExpiresAt: expires}
	if revoked {
		at := e.now.Add(-time.Minute)
		sess.RevokedAt = &at
	}
	if err := e.db.Omit("User").Create(&sess).Error; err != nil {
		t.Fatal(err)
	}
	return sess
}

func (e *env) lease(t *testing.T, sess model.NativeSession, state string, expires time.Time, ended *time.Time) string {
	t.Helper()
	row := model.ConnectionLease{ID: uuid.NewString(), UserID: e.user.ID, SessionID: sess.ID, DeviceID: sess.DeviceID, Platform: sess.Platform, AppVersion: sess.AppVersion, GroupID: uuid.NewString(), NodeID: uuid.NewString(), Method: model.LeaseByConnections, State: state, AcquiredAt: e.now.Add(-time.Hour), RenewedAt: e.now.Add(-time.Minute), ExpiresAt: expires, EndedAt: ended}
	if ended != nil {
		row.EndReason = model.LeaseReleased
	}
	if err := e.db.Omit("User", "Session").Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func (e *env) leaseState(t *testing.T, id string) model.ConnectionLease {
	t.Helper()
	var row model.ConnectionLease
	if err := e.db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

// T = 08:05 (now 08:07:30 rounded down to 5 minutes); T-24h = 08:05 the day before.
// Rows strictly older than T-24h go; the row at exactly T-24h stays.
func TestPruneUsageWindowBoundary(t *testing.T) {
	e := newEnv(t)
	other := e.newUser(t)
	cutoff := time.Date(2026, 10, 19, 8, 5, 0, 0, time.UTC)
	if got := model.UsageCutoff(e.now); !got.Equal(cutoff) {
		t.Fatalf("cutoff %v want %v", got, cutoff)
	}
	keep := []time.Time{cutoff, cutoff.Add(5 * time.Minute), time.Date(2026, 10, 20, 8, 5, 0, 0, time.UTC)}
	drop := []time.Time{cutoff.Add(-5 * time.Minute), cutoff.Add(-10 * time.Minute), cutoff.Add(-3 * time.Hour), cutoff.Add(-30 * time.Hour), cutoff.Add(-30 * 24 * time.Hour)}
	for _, u := range []model.User{e.user, other} {
		for _, start := range append(append([]time.Time{}, keep...), drop...) {
			if err := e.db.Omit("User").Create(&model.UsageBucket{UserID: u.ID, BucketStart: start, UploadBytes: 1, DownloadBytes: 2, ChargedUnits: 3000, UpdatedAt: start}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	s := e.sweeper()
	s.Batch = 3 // ten old rows: forces several bounded batches in one pass
	result, err := s.PruneUsage(context.Background())
	if err != nil || result.Skipped || result.UsageBuckets != int64(2*len(drop)) {
		t.Fatalf("prune: %+v %v", result, err)
	}
	var left []time.Time
	e.db.Model(&model.UsageBucket{}).Where("user_id = ?", e.user.ID).Order("bucket_start").Pluck("bucket_start", &left)
	if len(left) != len(keep) || !left[0].Equal(cutoff) {
		t.Fatalf("kept %v, want %v", left, keep)
	}
	var older int64
	e.db.Model(&model.UsageBucket{}).Where("bucket_start < ?", cutoff).Count(&older)
	if older != 0 {
		t.Fatal("rows older than the 24-hour window survived")
	}
	// Five minutes later the boundary row is the oldest one out.
	e.now = e.now.Add(5 * time.Minute)
	if result, err = s.PruneUsage(context.Background()); err != nil || result.UsageBuckets != 2 {
		t.Fatalf("next window: %+v %v", result, err)
	}
	if result, err = s.PruneUsage(context.Background()); err != nil || result.UsageBuckets != 0 {
		t.Fatalf("repeat pass changed rows: %+v %v", result, err)
	}
}

func TestExpireLeasesRecordsReasons(t *testing.T) {
	e := newEnv(t)
	live := e.session(t, e.now.Add(time.Hour), false)
	revoked := e.session(t, e.now.Add(time.Hour), true)
	ended := e.session(t, e.now, false) // session expiry is exclusive: expires_at == now is over
	lapsed := e.lease(t, live, model.LeaseActive, e.now, nil)
	sessionRevoked := e.lease(t, revoked, model.LeaseActive, e.now.Add(time.Minute), nil)
	sessionEnded := e.lease(t, ended, model.LeaseActive, e.now.Add(time.Minute), nil)
	other := e.session(t, e.now.Add(time.Hour), false)
	valid := e.lease(t, other, model.LeaseActive, e.now.Add(time.Second), nil)
	earlier := e.now.Add(-time.Hour)
	history := e.lease(t, live, model.LeaseEnded, e.now.Add(-time.Hour), &earlier)

	s := e.sweeper()
	s.Batch = 2
	result, err := s.ExpireLeases(context.Background())
	if err != nil || result.Skipped || result.Expired != 2 || result.SessionRevoked != 1 {
		t.Fatalf("expire: %+v %v", result, err)
	}
	for id, reason := range map[string]string{lapsed: model.LeaseExpired, sessionRevoked: model.LeaseSessionRevoked, sessionEnded: model.LeaseExpired} {
		row := e.leaseState(t, id)
		if row.State != model.LeaseEnded || row.EndReason != reason || row.EndedAt == nil || !row.EndedAt.Equal(e.now) {
			t.Fatalf("lease %s: %+v, want ended/%s at the injected time", id, row, reason)
		}
	}
	if row := e.leaseState(t, valid); row.State != model.LeaseActive || row.EndedAt != nil || row.EndReason != "" {
		t.Fatalf("unexpired lease ended: %+v", row)
	}
	if row := e.leaseState(t, history); row.EndReason != model.LeaseReleased || !row.EndedAt.Equal(earlier) {
		t.Fatalf("ended lease rewritten: %+v", row)
	}
	if result, err = s.ExpireLeases(context.Background()); err != nil || result.Expired+result.SessionRevoked != 0 {
		t.Fatalf("repeat pass changed rows: %+v %v", result, err)
	}
	// One second later the last lease lapses too.
	e.now = e.now.Add(time.Second)
	if result, err = s.ExpireLeases(context.Background()); err != nil || result.Expired != 1 {
		t.Fatalf("boundary: %+v %v", result, err)
	}
}

func TestPruneHourlyRetention(t *testing.T) {
	e := newEnv(t)
	node := model.Node{ID: uuid.NewString(), Name: "JP1", Region: "JP", LineType: "direct", RatePermille: 1000, Enabled: true, Version: 1}
	group := model.NodeGroup{ID: node.ID, Name: node.Name, Region: node.Region, LineType: node.LineType, RatePermille: node.RatePermille, PlanIDs: "[]", Enabled: true, Version: 1}
	node.GroupID = &group.ID
	if err := e.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	event := func(at time.Time) string {
		row := model.NodeHealthEvent{ID: uuid.NewString(), NodeID: node.ID, UserID: e.user.ID, Reason: model.LeaseTunnelFailed, OccurredAt: at}
		if err := e.db.Omit("Node", "User").Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	keepEvent, dropEvent := event(e.now.Add(-time.Hour)), event(e.now.Add(-time.Hour-time.Second))
	sess := e.session(t, e.now.Add(time.Hour), false)
	keepAt, dropAt := e.now.Add(-24*time.Hour), e.now.Add(-24*time.Hour-time.Second)
	keepLease, dropLease := e.lease(t, sess, model.LeaseEnded, keepAt, &keepAt), e.lease(t, sess, model.LeaseEnded, dropAt, &dropAt)
	activeLease := e.lease(t, sess, model.LeaseActive, e.now.Add(time.Minute), nil)
	assign := func(u model.User, used time.Time) {
		if err := e.db.Omit("User", "Group", "Node").Create(&model.NodeAssignment{UserID: u.ID, GroupID: group.ID, NodeID: node.ID, AssignedAt: used, LastUsedAt: used}).Error; err != nil {
			t.Fatal(err)
		}
	}
	stale := e.newUser(t)
	assign(e.user, e.now.Add(-30*24*time.Hour))
	assign(stale, e.now.Add(-30*24*time.Hour-time.Second))

	result, err := e.sweeper().PruneHourly(context.Background())
	if err != nil || result.Skipped || result.HealthEvents != 1 || result.EndedLeases != 1 || result.Assignments != 1 {
		t.Fatalf("hourly: %+v %v", result, err)
	}
	exists := func(m any, query string, args ...any) bool {
		var n int64
		e.db.Model(m).Where(query, args...).Count(&n)
		return n == 1
	}
	if !exists(&model.NodeHealthEvent{}, "id = ?", keepEvent) || exists(&model.NodeHealthEvent{}, "id = ?", dropEvent) {
		t.Fatal("health event retention boundary wrong")
	}
	if !exists(&model.ConnectionLease{}, "id = ?", keepLease) || exists(&model.ConnectionLease{}, "id = ?", dropLease) || !exists(&model.ConnectionLease{}, "id = ?", activeLease) {
		t.Fatal("ended lease retention boundary wrong")
	}
	if !exists(&model.NodeAssignment{}, "user_id = ?", e.user.ID) || exists(&model.NodeAssignment{}, "user_id = ?", stale.ID) {
		t.Fatal("assignment retention boundary wrong")
	}
}

type pauseKey struct{}

// Only one API instance sweeps at a time: while one pass runs, another instance's
// pass is skipped without touching rows, and an outside holder of the lock blocks
// every pass.
func TestSweepRunsOnOneInstanceAtATime(t *testing.T) {
	e := newEnv(t)
	for i := range 4 {
		start := model.UsageCutoff(e.now).Add(-time.Duration(i+1) * model.UsageGranularity)
		if err := e.db.Omit("User").Create(&model.UsageBucket{UserID: e.user.ID, BucketStart: start, UpdatedAt: start}).Error; err != nil {
			t.Fatal(err)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	if err := e.db.Callback().Raw().Before("gorm:raw").Register("test:pause", func(tx *gorm.DB) {
		if tx.Statement.Context.Value(pauseKey{}) != nil && strings.HasPrefix(tx.Statement.SQL.String(), "DELETE") {
			once.Do(func() { close(entered); <-release })
		}
	}); err != nil {
		t.Fatal(err)
	}
	first := make(chan Result, 1)
	go func() {
		result, err := e.sweeper().PruneUsage(context.WithValue(context.Background(), pauseKey{}, true))
		if err != nil {
			t.Error(err)
		}
		first <- result
	}()
	<-entered
	second, err := e.sweeper().PruneUsage(context.Background())
	if err != nil || !second.Skipped || second.UsageBuckets != 0 {
		t.Fatalf("second instance ran during the first pass: %+v %v", second, err)
	}
	close(release)
	if result := <-first; result.Skipped || result.UsageBuckets != 4 {
		t.Fatalf("first instance: %+v", result)
	}

	// An outside holder of the lock (another instance mid-pass) blocks every pass.
	for i := range 2 {
		start := model.UsageCutoff(e.now).Add(-time.Duration(i+1) * model.UsageGranularity)
		if err := e.db.Omit("User").Create(&model.UsageBucket{UserID: e.user.ID, BucketStart: start, UpdatedAt: start}).Error; err != nil {
			t.Fatal(err)
		}
	}
	holder := e.db.Begin()
	if err := holder.Exec("SELECT pg_advisory_xact_lock(?)", LockKey).Error; err != nil {
		t.Fatal(err)
	}
	s := e.sweeper()
	for _, pass := range []func(context.Context) (Result, error){s.ExpireLeases, s.PruneUsage, s.PruneHourly} {
		if result, err := pass(context.Background()); err != nil || !result.Skipped {
			t.Fatalf("pass ran while another instance held the lock: %+v %v", result, err)
		}
	}
	holder.Rollback()
	if result, err := s.PruneUsage(context.Background()); err != nil || result.Skipped || result.UsageBuckets != 2 {
		t.Fatalf("after release: %+v %v", result, err)
	}
}

// Run sweeps once at start and stops with its context.
func TestRunStopsWithContext(t *testing.T) {
	e := newEnv(t)
	start := model.UsageCutoff(e.now).Add(-model.UsageGranularity)
	if err := e.db.Omit("User").Create(&model.UsageBucket{UserID: e.user.ID, BucketStart: start, UpdatedAt: start}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.sweeper().Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int64
		e.db.Model(&model.UsageBucket{}).Count(&n)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial pass did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}
