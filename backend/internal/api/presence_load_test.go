package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/config"
	"vpn/backend/internal/maintenance"
	"vpn/backend/internal/model"
)

// TestPresenceLoadPostgres is the round-7 load test of the presence plan
// (handoff_node_use_history_and_load_balancing_c.md 12, round 7): PRESENCE_LOAD_DEVICES
// online devices (two per account, the default cap) report every 60 s of the injected
// clock for PRESENCE_LOAD_MINUTES, through the full router on the private PostgreSQL, with
// the maintenance passes run on their own schedule of that clock. Then 26 hours of history
// are written at one report per account per 5-minute bucket to show that usage_buckets
// stops growing once the 24-hour window is full, and the long poll is measured in real
// time. Opt-in; the numbers go to PRESENCE_LOAD_OUT.
//
// Phases:
//  0. admission: every device connects (POST /connections) through PRESENCE_LOAD_WORKERS
//     concurrent clients, spread over 4 groups of 5 servers;
//  1. steady state: each simulated minute is four 15-second slices; a quarter of the
//     devices reports in each slice, then the lease-expiry pass runs (every 15 s, as in
//     production), the usage pass every 5 minutes and the hourly pass every hour. 1% of
//     the devices miss one report each minute (their leases lapse and the sweeper ends
//     them), then report again, see connection_expired and reconnect, as a Core does;
//  2. history: 26 simulated hours, one report per account per 5 minutes, usage pass after
//     every step; row counts and table size are sampled every simulated hour;
//  3. long poll: PRESENCE_LOAD_POLLERS sessions hold GET /client/nodes for 20 real
//     seconds; process CPU (Go) and PostgreSQL CPU are compared with an idle window, and
//     one minute of reports is timed while they wait.
func TestPresenceLoadPostgres(t *testing.T) {
	devices, _ := strconv.Atoi(os.Getenv("PRESENCE_LOAD_DEVICES"))
	if devices <= 0 {
		t.Skip("set PRESENCE_LOAD_DEVICES (e.g. 2000) to run the presence load test")
	}
	devices -= devices % 2
	minutes := envInt("PRESENCE_LOAD_MINUTES", 30)
	hours := envInt("PRESENCE_LOAD_HISTORY_HOURS", 26)
	workers := envInt("PRESENCE_LOAD_WORKERS", 32)
	pool := envInt("PRESENCE_LOAD_POOL", 10) // DATABASE_MAX_OPEN's default
	pollers := envInt("PRESENCE_LOAD_POLLERS", devices)
	e := newPresenceEnv(t, func(c *config.Config) {
		c.APIRate = 1 << 30 // one client address stands for every device here
		c.MaxOpen = pool
		// Phase 2 reports once per 5 minutes per account; a Core's 60-second /session
		// keep-alive would hold the session open, so the idle window is widened instead.
		c.ClientIdleTimeout = time.Hour
	})
	sqlDB, _ := e.db.DB()
	sqlDB.SetMaxOpenConns(pool)
	sqlDB.SetMaxIdleConns(pool)
	sweeper := maintenance.New(e.db, e.cfg.Schema)
	sweeper.Now, sweeper.Logf = e.now, func(string, ...any) {}
	ctx := context.Background()
	report := map[string]any{"devices": devices, "accounts": devices / 2, "simulated_minutes": minutes, "history_hours": hours,
		"workers": workers, "db_pool": pool, "gomaxprocs": runtime.GOMAXPROCS(0), "lease_seconds": e.cfg.Lease().Seconds()}

	// Fixture: 4 groups x 5 servers x capacity 100 hold 2000 devices; enough for any size.
	capacity := max(100, (devices+19)/20)
	groups := []model.NodeGroup{}
	for g := range 4 {
		group := e.group(fmt.Sprintf("LOAD-%d", g+1), "[]")
		e.servers(group, capacity, capacity, capacity, capacity, capacity)
		groups = append(groups, group)
	}
	type loadDevice struct {
		token, group, lease string
		account             int
		sequence, up, down  int64
		offUntil            int       // simulated minute the device comes back; 0 = online
		renewed             time.Time // clock reading of its last lease renewal
	}
	fleet := make([]*loadDevice, devices)
	users := make([]presenceUser, devices/2)
	for i := range users {
		users[i] = e.user(2)
	}
	for i := range fleet {
		fleet[i] = &loadDevice{token: e.session(users[i/2], uuid.NewString()), group: groups[i%len(groups)].ID, account: i / 2}
	}
	// The run spans more than a day of the injected clock; sessions outlive it.
	if err := e.db.Model(&model.NativeSession{}).Where("1 = 1").Update("expires_at", e.now().Add(30*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	// parallel runs job over items with the worker pool and returns per-call latencies.
	var failures atomic.Int64
	var firstFailure atomic.Value
	fail := func(format string, args ...any) {
		failures.Add(1)
		firstFailure.CompareAndSwap(nil, fmt.Sprintf(format, args...))
	}
	parallel := func(items []*loadDevice, job func(*loadDevice)) []time.Duration {
		var mu sync.Mutex
		out := make([]time.Duration, 0, len(items))
		next := atomic.Int64{}
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				local := []time.Duration{}
				for {
					i := next.Add(1) - 1
					if i >= int64(len(items)) {
						break
					}
					start := time.Now()
					job(items[i])
					local = append(local, time.Since(start))
				}
				mu.Lock()
				out = append(out, local...)
				mu.Unlock()
			}()
		}
		wg.Wait()
		return out
	}
	connect := func(d *loadDevice) {
		w := e.connect(d.token, d.group, "k-"+uuid.NewString())
		if w.Code != 201 {
			fail("connect: %d %s", w.Code, w.Body.String())
			return
		}
		var out connectionResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		d.lease, d.renewed = out.Connection.ID, e.now()
	}
	var readmitted atomic.Int64
	reportOnce := func(d *loadDevice) {
		d.sequence++
		d.up += 40_000
		d.down += 400_000
		w := e.traffic(d.token, d.sequence, d.up, d.down)
		if w.Code != 200 {
			fail("traffic: %d %s", w.Code, w.Body.String())
			return
		}
		if snap := snapshotOf(t, w); snap.ConnectionState != "" && snap.ConnectionState != "active" {
			// A lapsed lease is not revived by a report; the Core connects again.
			readmitted.Add(1)
			connect(d)
			return
		}
		d.renewed = e.now()
	}

	// Phase 0: admission.
	start := time.Now()
	admission := parallel(fleet, connect)
	report["admission"] = summarize(admission, time.Since(start))
	if failures.Load() > 0 {
		t.Fatalf("admission failures: %d, first: %v", failures.Load(), firstFailure.Load())
	}
	var live int64
	(&Server{cfg: e.cfg}).liveLeases(e.db, e.now()).Count(&live)
	if live != int64(devices) {
		t.Fatalf("%d live leases after admitting %d devices", live, devices)
	}

	// Phase 1: steady state.
	var traffic, expirePasses, usagePasses, hourlyPasses []time.Duration
	var expired, churned int64
	runPass := func(into *[]time.Duration, pass func(context.Context) (maintenance.Result, error)) maintenance.Result {
		started := time.Now()
		result, err := pass(ctx)
		if err != nil || result.Skipped {
			t.Fatalf("maintenance pass: %v skipped=%v", err, result.Skipped)
		}
		*into = append(*into, time.Since(started))
		return result
	}
	phase1 := time.Now()
	for minute := 1; minute <= minutes; minute++ {
		// 1% of the online devices miss one report (120 s without renewal > 90 s lease,
		// < 180 s idle window, like a short network loss).
		for i := 0; i < devices/100; i++ {
			d := fleet[(minute*devices/100+i*7919)%devices]
			if d.offUntil == 0 {
				d.offUntil = minute + 1
				churned++
			}
		}
		for slice := range 4 {
			e.advance(15 * time.Second)
			batch := []*loadDevice{}
			for i, d := range fleet {
				if i%4 != slice {
					continue
				}
				if d.offUntil > minute {
					continue
				}
				d.offUntil = 0
				batch = append(batch, d)
			}
			traffic = append(traffic, parallel(batch, reportOnce)...)
			expired += runPass(&expirePasses, sweeper.ExpireLeases).Expired
		}
		if minute%5 == 0 {
			runPass(&usagePasses, sweeper.PruneUsage)
		}
		if minute%60 == 0 {
			runPass(&hourlyPasses, sweeper.PruneHourly)
		}
	}
	if failures.Load() > 0 {
		t.Fatalf("steady-state failures: %d, first: %v", failures.Load(), firstFailure.Load())
	}
	// Devices still off at the end have not reported again yet; every lapse that came back was ended by the sweeper first.
	report["steady_state"] = map[string]any{
		"reports": len(traffic), "traffic": summarize(traffic, time.Since(phase1)),
		"lease_expiry_pass": summarize(expirePasses, 0), "usage_pass": summarize(usagePasses, 0), "hourly_pass": summarize(hourlyPasses, 0),
		"devices_dropped": churned, "leases_expired_by_sweeper": expired, "reconnected_after_expiry": readmitted.Load(),
		"usage_bucket_rows": e.count("usage_buckets"), "usage_buckets_bytes": e.relationSize("usage_buckets"),
		"lease_rows": e.count("connection_leases"), "connection_leases_bytes": e.relationSize("connection_leases"),
	}
	if expired < readmitted.Load() || readmitted.Load() == 0 {
		t.Fatalf("sweeper ended %d leases, %d devices came back to a lapsed lease", expired, readmitted.Load())
	}
	(&Server{cfg: e.cfg}).liveLeases(e.db, e.now()).Count(&live)
	// A device that missed its last report still counts until its lease runs out.
	now := e.now()
	if online := int64(countOf(fleet, func(d *loadDevice) bool { return d.renewed.Add(e.cfg.Lease()).After(now) })); live != online {
		t.Fatalf("%d live leases, %d devices renewed within the lease", live, online)
	}

	// Phase 2: 26 hours of history, one report per account per 5-minute bucket.
	accounts := []*loadDevice{}
	for i := 0; i < devices; i += 2 {
		accounts = append(accounts, fleet[i])
	}
	var historyTraffic, historyPrune []time.Duration
	type sample struct {
		Hour       int   `json:"hour"`
		Rows       int64 `json:"rows"`
		Bytes      int64 `json:"bytes"`
		HeapBytes  int64 `json:"heap_bytes"`
		IndexBytes int64 `json:"index_bytes"`
	}
	samples := []sample{}
	phase2 := time.Now()
	for step := 1; step <= hours*12; step++ {
		e.advance(5 * time.Minute)
		historyTraffic = append(historyTraffic, parallel(accounts, func(d *loadDevice) {
			d.sequence++
			d.up += 10_000
			d.down += 100_000
			if w := e.traffic(d.token, d.sequence, d.up, d.down); w.Code != 200 {
				fail("history traffic: %d %s", w.Code, w.Body.String())
			}
		})...)
		runPass(&historyPrune, sweeper.PruneUsage)
		if step%12 == 0 {
			runPass(&hourlyPasses, sweeper.PruneHourly)
			// Stands in for autovacuum, which at real speed visits the table about 60
			// times per hour; here an hour of the clock passes in seconds.
			e.db.Exec(`VACUUM "` + e.cfg.Schema + `".usage_buckets`)
			var heap, index int64
			e.db.Raw("SELECT pg_relation_size(?::regclass), pg_indexes_size(?::regclass)", `"`+e.cfg.Schema+`".usage_buckets`, `"`+e.cfg.Schema+`".usage_buckets`).Row().Scan(&heap, &index)
			samples = append(samples, sample{Hour: step / 12, Rows: e.count("usage_buckets"), Bytes: e.relationSize("usage_buckets"), HeapBytes: heap, IndexBytes: index})
		}
	}
	if failures.Load() > 0 {
		t.Fatalf("history failures: %d, first: %v", failures.Load(), firstFailure.Load())
	}
	// Every account has one bucket per 5 minutes; the window keeps T-24h ... T: 289 buckets.
	ceiling := int64(len(accounts)) * (24*12 + 1)
	var oldest time.Time
	e.db.Model(&model.UsageBucket{}).Select("min(bucket_start)").Scan(&oldest)
	cutoff := model.UsageCutoff(e.now())
	for _, s := range samples[23:] {
		if s.Rows > ceiling {
			t.Fatalf("usage_buckets kept growing past the window: %+v (ceiling %d)", s, ceiling)
		}
	}
	if oldest.Before(cutoff) {
		t.Fatalf("a bucket older than T-24h survived: %v < %v", oldest, cutoff)
	}
	var stale int64
	e.db.Model(&model.ConnectionLease{}).Where("state = ? AND ended_at < ?", model.LeaseEnded, e.now().Add(-model.EndedLeaseRetention)).Count(&stale)
	if stale != 0 {
		t.Fatalf("%d ended leases older than their retention", stale)
	}
	report["history"] = map[string]any{"reports": len(historyTraffic), "traffic": summarize(historyTraffic, time.Since(phase2)),
		"usage_pass": summarize(historyPrune, 0), "hourly_pass": summarize(hourlyPasses, 0), "hourly_samples": samples,
		"row_ceiling": ceiling, "final_rows": e.count("usage_buckets"), "final_bytes": e.relationSize("usage_buckets"),
		"lease_rows_after": e.count("connection_leases")}

	// Phase 3: long poll in real time. Bring every device back online on the current clock
	// first; the second device of each account sat idle through phase 2 and signs in again.
	for i := 1; i < devices; i += 2 {
		fleet[i].token, fleet[i].sequence, fleet[i].up, fleet[i].down = e.session(users[fleet[i].account], uuid.NewString()), 0, 0, 0
	}
	parallel(fleet, func(d *loadDevice) {
		d.offUntil = 0
		connect(d)
	})
	if failures.Load() > 0 {
		t.Fatalf("reconnect failures: %d, first: %v", failures.Load(), firstFailure.Load())
	}
	window := 20 * time.Second
	idleGo, idlePG := cpuWindow(window, func() {})
	pollers = min(pollers, devices)
	var answered atomic.Int64
	var pollTraffic []time.Duration
	pollCtx, stopPolls := context.WithCancel(ctx)
	var polls sync.WaitGroup
	for _, d := range fleet[:pollers] {
		polls.Add(1)
		go func() {
			defer polls.Done()
			revision := e.catalogRevision(d.token)
			for pollCtx.Err() == nil {
				w := e.do(presenceCall{method: "GET", path: "/client/nodes?revision=" + revision, token: d.token, ctx: pollCtx})
				if w.Code == 200 {
					answered.Add(1)
				}
			}
		}()
	}
	time.Sleep(6 * time.Second) // every poller is inside its wait
	busyGo, busyPG := cpuWindow(window, func() {})
	// One minute's reports from every device while the pollers keep waiting.
	pollTraffic = parallel(fleet, reportOnce)
	stopPolls()
	polls.Wait()
	if failures.Load() > 0 {
		t.Fatalf("long-poll phase failures: %d, first: %v", failures.Load(), firstFailure.Load())
	}
	report["long_poll"] = map[string]any{"pollers": pollers, "window_seconds": window.Seconds(), "node_watch_seconds": e.cfg.NodeWatch().Seconds(),
		"answers": answered.Load(), "idle_go_cpu_seconds": idleGo, "idle_postgres_cpu_seconds": idlePG,
		"busy_go_cpu_seconds": busyGo, "busy_postgres_cpu_seconds": busyPG,
		"go_cpu_per_poller_per_second_ms": 1000 * (busyGo - idleGo) / window.Seconds() / float64(pollers),
		"traffic_while_polling":           summarize(pollTraffic, 0)}

	data, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("presence load:\n%s", data)
	if out := os.Getenv("PRESENCE_LOAD_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}

func countOf[T any](items []T, match func(T) bool) int {
	n := 0
	for _, item := range items {
		if match(item) {
			n++
		}
	}
	return n
}

// summarize reports latency percentiles in microseconds and, given the wall time, the rate.
func summarize(durations []time.Duration, wall time.Duration) map[string]any {
	if len(durations) == 0 {
		return map[string]any{"count": 0}
	}
	sorted := slices.Clone(durations)
	slices.Sort(sorted)
	at := func(p int) int64 { return sorted[min(len(sorted)-1, len(sorted)*p/100)].Microseconds() }
	out := map[string]any{"count": len(sorted), "p50_us": at(50), "p95_us": at(95), "p99_us": at(99), "max_us": sorted[len(sorted)-1].Microseconds()}
	if wall > 0 {
		out["wall_seconds"] = wall.Seconds()
		out["per_second"] = float64(len(sorted)) / wall.Seconds()
	}
	return out
}

func (e *presenceEnv) count(table string) int64 {
	var n int64
	e.db.Table(e.cfg.Schema + "." + table).Count(&n)
	return n
}

// relationSize is the table with its indexes and TOAST.
func (e *presenceEnv) relationSize(table string) int64 {
	var n int64
	e.db.Raw("SELECT pg_total_relation_size(?::regclass)", `"`+e.cfg.Schema+`".`+table).Scan(&n)
	return n
}

func (e *presenceEnv) catalogRevision(token string) string {
	w := e.native("GET", "/nodes", token, nil)
	var out nativeCatalog
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Revision
}

// cpuWindow runs work and waits until window has passed, returning the CPU seconds this
// process and every local postgres process used meanwhile.
func cpuWindow(window time.Duration, work func()) (goSeconds, pgSeconds float64) {
	goBefore, pgBefore := processCPU(), postgresCPU()
	deadline := time.Now().Add(window)
	work()
	time.Sleep(time.Until(deadline))
	return processCPU() - goBefore, postgresCPU() - pgBefore
}

func processCPU() float64 {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()).Seconds()
}

// postgresCPU sums the CPU time of the machine's postgres processes (the private test
// cluster is the only one on CI; elsewhere the figure is an upper bound).
func postgresCPU() float64 {
	out, err := exec.Command("ps", "-A", "-o", "time=", "-o", "comm=").Output()
	if err != nil {
		return 0
	}
	total := 0.0
	for _, line := range strings.Split(string(out), "\n") {
		// Backends retitle themselves ("postgres: user db [local] idle").
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(strings.Join(fields[1:], " "), "postgres") {
			continue
		}
		total += psSeconds(fields[0])
	}
	return total
}

// psSeconds parses ps's [[dd-]hh:]mm:ss[.ss].
func psSeconds(value string) float64 {
	days := 0.0
	if d, rest, ok := strings.Cut(value, "-"); ok {
		days, _ = strconv.ParseFloat(d, 64)
		value = rest
	}
	total := 0.0
	for _, part := range strings.Split(value, ":") {
		f, _ := strconv.ParseFloat(part, 64)
		total = total*60 + f
	}
	return days*86400 + total
}
