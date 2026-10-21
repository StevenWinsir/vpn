//go:build asterlink_harness

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
)

// deviceID is the one installation identity every sign-in of the run uses (the world allows
// four devices per account and the run signs in one account at a time).
const deviceID = "7f1c2a40-5d3e-4b8a-9c61-0a5e3d2b1f77"

// Run executes the acceptance and returns one Case per check. It never returns an error for a
// failed check: a failure is a Case with status FAIL. ctx bounds the whole run.
func Run(ctx context.Context, cfg Config) []Case {
	r := &runner{cfg: cfg, started: time.Now()}
	defer r.cleanup()
	world, err := newWorldClient(cfg.World.APIBase)
	if err != nil {
		return []Case{{ID: "B.setup", Status: "FAIL", Observed: "bad api_base: " + err.Error()}}
	}
	r.world = world
	if cfg.CacheDir != "" {
		_ = os.Setenv("TMPDIR", cfg.CacheDir)
	}
	r.eng = engine.NewHarness()
	r.eng.Probes = []string{fmt.Sprintf("http://%s:%d/down?t=%s&bytes=0", cfg.Sink.Host, cfg.Sink.Port, cfg.Sink.Token)}
	r.xfer = transfer{dial: r.eng.Dial, host: cfg.Sink.Host, port: cfg.Sink.Port, token: cfg.Sink.Token}
	func() {
		defer func() {
			if p := recover(); p != nil {
				r.record("B.unexpected", "FAIL", fmt.Sprint("panic: ", p))
			}
		}()
		r.runCases(ctx)
	}()
	return r.cases
}

type runner struct {
	cfg     Config
	world   *worldClient
	eng     *engine.Harness
	xfer    transfer
	m       *managed.Manager
	cases   []Case
	started time.Time
}

func (r *runner) record(id, status, observed string) {
	r.cases = append(r.cases, Case{ID: id, Status: status, Observed: observed})
}

func (r *runner) verdict(id string, problems []string, pass string) {
	if len(problems) == 0 {
		r.record(id, "PASS", pass)
		return
	}
	r.record(id, "FAIL", strings.Join(problems, "; "))
}

func (r *runner) cleanup() {
	if r.m != nil {
		stop, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		r.m.Shutdown(stop)
		cancel()
	}
	if r.eng != nil {
		_ = r.eng.Stop()
	}
}

// newManager is the production Manager with the harness engine, as the iOS Core builds it
// (ResumeGate, the Core's own clocks); only the engine differs.
func (r *runner) newManager() *managed.Manager {
	return managed.New(managed.Options{CoreVersion: "harness", Engine: r.eng, ResumePolicy: managed.ResumeGate})
}

// signIn replaces the session with a fresh sign-in as who and waits for the first catalog.
func (r *runner) signIn(ctx context.Context, who string) bool {
	if r.m != nil {
		stop, cancel := context.WithTimeout(ctx, 20*time.Second)
		r.m.Logout(stop)
		cancel()
	} else {
		r.m = r.newManager()
	}
	snap, err := r.m.Login(ctx, managed.LoginParams{
		Email: r.cfg.World.Emails[who], Password: r.cfg.World.Password, APIBase: r.cfg.World.APIBase,
		Channel: "development", DeviceID: deviceID, AppVersion: "0.1.0",
	})
	if err != nil || snap.State == managed.StateSignedOut {
		r.record("B.login."+who, "FAIL", fmt.Sprintf("sign-in failed: %v", err))
		return false
	}
	_, _ = r.m.RefreshCatalog(ctx)
	return r.waitFor(ctx, 10*time.Second, func(s managed.Snapshot) bool { return len(s.Nodes) > 0 }) >= 0
}

// waitFor polls the snapshot until the predicate holds; it returns the wait or -1 on timeout.
func (r *runner) waitFor(ctx context.Context, limit time.Duration, ok func(managed.Snapshot) bool) time.Duration {
	start := time.Now()
	for time.Since(start) < limit && ctx.Err() == nil {
		if ok(r.m.Snapshot()) {
			return time.Since(start)
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return -1
}

func (r *runner) selectNode(ctx context.Context, key string) managed.Snapshot {
	snap := r.m.Snapshot()
	out, _ := r.m.SelectNode(ctx, r.cfg.World.Nodes[key], snap.CatalogRevision)
	return out
}

// connect connects and reports whether the Core says Connected.
func (r *runner) connect(ctx context.Context) (managed.Snapshot, error) {
	return r.m.Connect(ctx)
}

func (r *runner) disconnectAndSettle(ctx context.Context) managed.Snapshot {
	r.m.Disconnect(ctx)
	r.waitFor(ctx, 15*time.Second, func(s managed.Snapshot) bool { return !tunnelBusy(s) && (s.Metering == nil || !s.Metering.Pending) })
	return r.m.Snapshot()
}

func tunnelBusy(s managed.Snapshot) bool {
	switch s.State {
	case managed.StatePreparing, managed.StateConnecting, managed.StateConnected, managed.StateStopping:
		return true
	}
	return false
}

func (r *runner) lastErrorCode(s managed.Snapshot) string {
	if s.LastError == nil {
		return ""
	}
	return s.LastError.Code
}

type measured struct {
	before, after        ledger
	clientUp, clientDown int64
	sinkUp, sinkDown     int64
	live                 *usage
	snap                 managed.Snapshot
}

func (m measured) serverUp() int64   { return m.after.UploadBytes - m.before.UploadBytes }
func (m measured) serverDown() int64 { return m.after.DownloadBytes - m.before.DownloadBytes }
func (m measured) units() int64      { return m.after.UsedUnits - m.before.UsedUnits }

// usage is the connect page's per-connection figure (AsterLinkKit ConnectionUsage): the Core's
// physical counters minus the baseline taken when the tunnel came up, at the bound rate.
type usage struct {
	physical, billed, rate int64
}

func billedBytes(physical, rate int64) int64 { return physical/1000*rate + physical%1000*rate/1000 }

// measure: select, connect, controlled transfer through the node, disconnect (tail settled),
// with the server's ledger and the sink read before and after. Nil when a case failed.
func (r *runner) measure(ctx context.Context, tag, who, node string, up, down int64, during func()) *measured {
	if s := r.selectNode(ctx, node); !s.ConfigReady {
		r.record(tag+"-select", "FAIL", fmt.Sprintf("line not ready: %s", r.lastErrorCode(s)))
		return nil
	}
	before, err := r.world.ledger(ctx, who)
	if err != nil {
		r.record(tag+"-ledger", "FAIL", err.Error())
		return nil
	}
	sinkUp0, sinkDown0, _ := r.world.sink(ctx)
	pre := r.m.Snapshot().Metering
	began := time.Now()
	s, err := r.connect(ctx)
	if err != nil || s.State != managed.StateConnected {
		r.record(tag+"-connect", "FAIL", fmt.Sprintf("state %s, error %v, last_error %s", s.State, err, r.lastErrorCode(s)))
		return nil
	}
	rate := int64(0)
	var base struct{ up, down int64 }
	if s.Metering != nil {
		rate = s.Metering.RatePermille
		base.up, base.down = s.Metering.UploadBytes, s.Metering.DownloadBytes
	}
	r.record(tag+"-connect", "PASS", fmt.Sprintf("connected in %d ms after the Core's gates, first ACK, engine and Verify; health %s, bound rate %d permille, source %s",
		time.Since(began).Milliseconds(), s.TunnelHealth, rate, meteringSource(s)))
	gotUp, upErr := r.xfer.upload(ctx, up)
	gotDown, downErr := r.xfer.download(ctx, down)
	if during != nil {
		during()
	}
	time.Sleep(2500 * time.Millisecond) // the meter samples once a second
	var live *usage
	if now := r.m.Snapshot().Metering; now != nil {
		physical := (now.UploadBytes - base.up) + (now.DownloadBytes - base.down)
		live = &usage{physical: physical, billed: billedBytes(physical, now.RatePermille), rate: now.RatePermille}
	}
	end := r.disconnectAndSettle(ctx)
	if gotUp != up || gotDown != down {
		r.record(tag+"-transfer", "FAIL", fmt.Sprintf("upload %d (%v), download %d (%v)", gotUp, upErr, gotDown, downErr))
		return nil
	}
	after := end.Metering
	if after == nil || after.Pending || pre == nil {
		r.record(tag+"-settle", "FAIL", fmt.Sprintf("after Disconnect: metering %v, last_error %s", after != nil, r.lastErrorCode(end)))
		return nil
	}
	sinkUp1, sinkDown1, _ := r.world.sink(ctx)
	ledgerAfter, _ := r.world.ledger(ctx, who)
	return &measured{
		before: before, after: ledgerAfter,
		clientUp: after.ConfirmedUploadBytes - pre.ConfirmedUploadBytes, clientDown: after.ConfirmedDownloadBytes - pre.ConfirmedDownloadBytes,
		sinkUp: sinkUp1 - sinkUp0, sinkDown: sinkDown1 - sinkDown0, live: live, snap: end,
	}
}

func meteringSource(s managed.Snapshot) string {
	if s.Metering == nil {
		return ""
	}
	return s.Metering.Source
}

// ledgerProblems are the ledger rules of handoff 9 from the server's own rows and the Core's
// confirmed counters.
func ledgerProblems(m *measured, rate int64) []string {
	var problems []string
	counted := m.serverUp() + m.serverDown()
	if m.units() != counted*rate {
		problems = append(problems, fmt.Sprintf("charged %d units, (up %d + down %d) x %d = %d", m.units(), m.serverUp(), m.serverDown(), rate, counted*rate))
	}
	if m.serverUp() != m.clientUp || m.serverDown() != m.clientDown {
		problems = append(problems, fmt.Sprintf("server counted up %d down %d, the Core's confirmed counters moved up %d down %d", m.serverUp(), m.serverDown(), m.clientUp, m.clientDown))
	}
	if formula := max(0, (m.after.TotalBytes*1000-m.after.UsedUnits)/1000); m.after.RemainingBytes != formula {
		problems = append(problems, fmt.Sprintf("remaining %d, formula %d", m.after.RemainingBytes, formula))
	}
	if shown := m.snap.Entitlement; shown == nil || shown.UsedUnits != m.after.UsedUnits || shown.RemainingBytes != m.after.RemainingBytes {
		problems = append(problems, fmt.Sprintf("the Core shows %v, the server has used %d / remaining %d", shown, m.after.UsedUnits, m.after.RemainingBytes))
	}
	return problems
}

func (r *runner) bodyProblems(m *measured, up, down int64) []string {
	var problems []string
	if m.sinkUp != up || m.sinkDown != down {
		problems = append(problems, fmt.Sprintf("the sink saw up %d (sent %d), down %d (wanted %d)", m.sinkUp, up, m.sinkDown, down))
	}
	check := func(name string, counted, body int64) {
		limit := body + body/10 + r.cfg.BackgroundBytes
		if counted < body {
			problems = append(problems, fmt.Sprintf("%s counted %d is below the %d byte body", name, counted, body))
		}
		if counted > limit {
			problems = append(problems, fmt.Sprintf("%s counted %d exceeds the body %d by more than 10%% + %d", name, counted, body, r.cfg.BackgroundBytes))
		}
	}
	check("upload", m.serverUp(), up)
	check("download", m.serverDown(), down)
	return problems
}

func pct(counted, body int64) string {
	if body == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%+.2f%%", 100*float64(counted-body)/float64(body))
}

func (r *runner) runCases(ctx context.Context) {
	if r.cfg.Only == "presence" {
		r.presenceCases(ctx)
		return
	}
	up, down := r.cfg.UploadMiB<<20, r.cfg.DownloadMiB<<20

	// ---- B01: the same controlled bytes at 1x (dedicated, Shadowsocks) and 0.5x (ordinary, VLESS).
	if !r.signIn(ctx, "meter") {
		return
	}
	var perRate []string
	for index, node := range []string{"dedicated", "ordinary"} {
		tag := fmt.Sprintf("B01-n%d", index+1)
		rate := int64(0)
		for _, n := range r.m.Snapshot().Nodes {
			if n.ID == r.cfg.World.Nodes[node] {
				rate = n.RatePermille
			}
		}
		m := r.measure(ctx, tag, "meter", node, up, down, nil)
		if m == nil {
			continue
		}
		r.verdict(tag+"-sink-and-body", r.bodyProblems(m, up, down),
			fmt.Sprintf("the sink saw exactly up %d / down %d; counted up %d (%s), down %d (%s) incl. HTTP framing and Verify",
				up, down, m.serverUp(), pct(m.serverUp(), up), m.serverDown(), pct(m.serverDown(), down)))
		r.verdict(tag+"-ledger", ledgerProblems(m, rate),
			fmt.Sprintf("rate %d permille: (%d + %d) bytes -> %d units (%d bytes of quota); server, Core counters and the Core's own figures agree; remaining %d",
				rate, m.serverUp(), m.serverDown(), m.units(), m.units()/1000, m.after.RemainingBytes))
		var liveProblems []string
		if m.live == nil {
			liveProblems = append(liveProblems, "no per-connection usage while connected")
		} else {
			if m.live.physical < up+down {
				liveProblems = append(liveProblems, fmt.Sprintf("the connect page would show %d bytes, below the %d byte body", m.live.physical, up+down))
			}
			if m.live.rate != rate {
				liveProblems = append(liveProblems, fmt.Sprintf("shown at %d permille, the line is %d", m.live.rate, rate))
			}
		}
		liveText := "n/a"
		if m.live != nil {
			liveText = fmt.Sprintf("%d bytes, billed %d at %d permille", m.live.physical, m.live.billed, m.live.rate)
		}
		r.verdict(tag+"-ui", liveProblems, "this connection's per-connection usage read "+liveText+" (before the last report settled)")
		perRate = append(perRate, fmt.Sprintf("%d permille: %d units for %d bytes", rate, m.units(), m.serverUp()+m.serverDown()))
	}
	if len(perRate) == 2 {
		r.record("B01-rates", "PASS", strings.Join(perRate, " | "))
	}

	// ---- C01: Connect right after Disconnect, five times; the old teardown never ends the new engine.
	r.reconnectCase(ctx)

	// ---- B02: one real acknowledgement is lost; the identical batch is resent and charged once.
	r.ackLossCase(ctx)

	// ---- A04: an administrator re-rates the connected line.
	if r.signIn(ctx, "change") {
		r.stopCase(ctx, "A04-rate", "change", "changing", 1000, []string{"profile_changed"}, false, func() {
			_, _ = r.world.post(ctx, "edit", map[string]any{"node": "changing", "rate_permille": 2000})
		})
		rated := r.waitFor(ctx, 10*time.Second, func(s managed.Snapshot) bool {
			return slices.ContainsFunc(s.Nodes, func(n managed.NodeView) bool { return n.ID == r.cfg.World.Nodes["changing"] && n.RatePermille == 2000 })
		})
		r.selectNode(ctx, "changing")
		s, _ := r.connect(ctx)
		var bound int64
		if s.Metering != nil {
			bound = s.Metering.RatePermille
		}
		r.disconnectAndSettle(ctx)
		status := "FAIL"
		if rated >= 0 && s.State == managed.StateConnected && bound == 2000 {
			status = "PASS"
		}
		r.record("A04-rebind", status, fmt.Sprintf("new rate listed after %d ms; the next connection %s bound at %d permille", rated.Milliseconds(), s.State, bound))

		// ---- A04: the line is disabled while connected.
		r.stopCase(ctx, "A04-disable", "change", "changing", 2000, []string{"client_config_unavailable", "node_unavailable"}, false, func() {
			_, _ = r.world.post(ctx, "edit", map[string]any{"node": "changing", "enabled": false})
		})
		gone := r.waitFor(ctx, 10*time.Second, func(s managed.Snapshot) bool {
			return !slices.ContainsFunc(s.Nodes, func(n managed.NodeView) bool { return n.ID == r.cfg.World.Nodes["changing"] })
		})
		now := r.m.Snapshot()
		canConnect := now.Entitlement != nil && now.Entitlement.CanConnect
		status = "FAIL"
		if gone >= 0 && !canConnect {
			status = "PASS"
		}
		r.record("A04-disable-gone", status, fmt.Sprintf("the disabled line left the list after %d ms; connect allowed %v", gone.Milliseconds(), canConnect))
	}

	// ---- A04: the plan ends while connected.
	if r.signIn(ctx, "expire") {
		r.stopCase(ctx, "A04-expire", "expire", "dedicated", 1000, []string{"subscription_expired"}, false, func() {
			_, _ = r.world.post(ctx, "expire", map[string]string{"user": "expire"})
		})
		s := r.m.Snapshot()
		authorized := s.Entitlement != nil && s.Entitlement.AccountAuthorized
		status := "FAIL"
		if !authorized {
			status = "PASS"
		}
		reason := ""
		if s.Entitlement != nil {
			reason = s.Entitlement.Reason
		}
		r.record("A04-expire-blocked", status, fmt.Sprintf("after the plan ended: account authorized %v, reason %s", authorized, reason))
	}

	// ---- A04: the data runs out while connected (the client keeps downloading).
	_, _ = r.world.post(ctx, "quota", map[string]any{"user": "quota", "remaining_bytes": 12 << 20})
	if r.signIn(ctx, "quota") {
		r.stopCase(ctx, "A04-quota", "quota", "dedicated", 1000, []string{"quota_exhausted"}, true, func() {})
		l, _ := r.world.ledger(ctx, "quota")
		status := "FAIL"
		if l.RemainingBytes == 0 {
			status = "PASS"
		}
		r.record("A04-quota-blocked", status, fmt.Sprintf("server remaining %d of %d bytes (used %d bytes: overshoot %d bytes, the client-reported meter's last interval)",
			l.RemainingBytes, l.TotalBytes, l.UsedUnits/1000, l.UsedUnits/1000-l.TotalBytes))
	}

	// ---- B04: a node-reported line, through the real node agent.
	r.nodeReported(ctx)

	// ---- P01-P05: online presence (node groups round 6), last: it ends with a Shutdown.
	r.presenceCases(ctx)
}

func (r *runner) reconnectCase(ctx context.Context) {
	r.selectNode(ctx, "dedicated")
	var seen []string
	if s, err := r.connect(ctx); err != nil || s.State != managed.StateConnected {
		r.record("C01-reconnect-at-once", "FAIL", fmt.Sprintf("first connect: %s %v", s.State, err))
		return
	}
	for range 5 {
		r.m.Disconnect(ctx)
		began := time.Now()
		s, err := r.connect(ctx)
		connected := err == nil && s.State == managed.StateConnected
		took := time.Since(began)
		// Still connected a moment later: no late teardown of the previous engine took it down.
		time.Sleep(1500 * time.Millisecond)
		held := r.m.Snapshot().State == managed.StateConnected
		if connected && held {
			seen = append(seen, fmt.Sprintf("%d ms", took.Milliseconds()))
		} else {
			seen = append(seen, fmt.Sprintf("%s/%v", r.m.Snapshot().State, err))
		}
	}
	r.disconnectAndSettle(ctx)
	status := "PASS"
	for _, s := range seen {
		if !strings.HasSuffix(s, " ms") {
			status = "FAIL"
		}
	}
	r.record("C01-reconnect-at-once", status, "Connect issued the moment Disconnect returned, 5 times: "+strings.Join(seen, ", "))
}

func (r *runner) ackLossCase(ctx context.Context) {
	seen := 0
	if reports, err := r.world.reports(ctx); err == nil {
		seen = len(reports)
	}
	m := r.measure(ctx, "B02", "meter", "dedicated", 1<<20, 4<<20, func() {
		_, _ = r.world.post(ctx, "drop_ack", map[string]string{})
		_, _ = r.xfer.download(ctx, 2<<20)
		// The next report (every report interval) is the one whose reply is lost; wait for the resend.
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			reports, _ := r.world.reports(ctx)
			if len(reports) > seen {
				tail := reports[seen:]
				if at := slices.IndexFunc(tail, func(t trafficReport) bool { return t.Dropped }); at >= 0 &&
					slices.ContainsFunc(tail[at+1:], func(t trafficReport) bool { return t.Token == tail[at].Token }) {
					return
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	if m == nil {
		return
	}
	reports, _ := r.world.reports(ctx)
	dropped := -1
	for i, t := range reports {
		if t.Dropped {
			dropped = i
		}
	}
	switch {
	case dropped < 0:
		r.record("B02-retry", "FAIL", "no /traffic report was dropped")
	default:
		next := -1
		for i := dropped + 1; i < len(reports); i++ {
			if reports[i].Token == reports[dropped].Token {
				next = i
				break
			}
		}
		if next < 0 {
			r.record("B02-retry", "FAIL", "the dropped batch was never sent again")
		} else {
			status := "FAIL"
			if reports[next].Body == reports[dropped].Body {
				status = "PASS"
			}
			r.record("B02-retry", status, fmt.Sprintf("dropped %s (committed with %d); next %s -> %d", reports[dropped].Body, reports[dropped].Status, reports[next].Body, reports[next].Status))
		}
	}
	r.verdict("B02-once", ledgerProblems(m, 1000), fmt.Sprintf("the batch whose reply was lost is charged once: %d units for %d bytes, server = Core counters", m.units(), m.serverUp()+m.serverDown()))
}

// stopCase: connect on node, move a little data, then trigger a server-side change (or, with
// drive, keep downloading until the quota is gone) and require the engine to stop by itself with
// one of reasons, the tail to settle and the whole connection to be billed at its own rate.
func (r *runner) stopCase(ctx context.Context, id, who, node string, rate int64, reasons []string, drive bool, trigger func()) {
	r.selectNode(ctx, node)
	before, _ := r.world.ledger(ctx, who)
	if s, err := r.connect(ctx); err != nil || s.State != managed.StateConnected {
		r.record(id, "FAIL", fmt.Sprintf("did not connect: %v %s", err, r.lastErrorCode(s)))
		return
	}
	_, _ = r.xfer.download(ctx, 1<<20)
	trigger()
	began := time.Now()
	stopped := time.Duration(-1)
	for time.Since(began) < 90*time.Second && ctx.Err() == nil {
		if r.m.Snapshot().State != managed.StateConnected {
			stopped = time.Since(began)
			break
		}
		if drive {
			_, _ = r.xfer.download(ctx, 2<<20)
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if stopped < 0 {
		r.disconnectAndSettle(ctx)
		r.record(id, "FAIL", "still connected 90 s after the change")
		return
	}
	settled := r.waitFor(ctx, 20*time.Second, func(s managed.Snapshot) bool { return !tunnelBusy(s) && (s.Metering == nil || !s.Metering.Pending) })
	engineStopped := r.eng.ActiveConnections() == 0
	s := r.m.Snapshot()
	code := r.lastErrorCode(s)
	after, _ := r.world.ledger(ctx, who)
	counted := (after.UploadBytes - before.UploadBytes) + (after.DownloadBytes - before.DownloadBytes)
	units := after.UsedUnits - before.UsedUnits
	var problems []string
	if !slices.Contains(reasons, code) {
		problems = append(problems, fmt.Sprintf("stopped with %q, expected one of %v", code, reasons))
	}
	if settled < 0 {
		problems = append(problems, "the tail never settled")
	}
	if !engineStopped {
		problems = append(problems, fmt.Sprintf("%d proxy flows still tracked after the stop", r.eng.ActiveConnections()))
	}
	if units != counted*rate {
		problems = append(problems, fmt.Sprintf("charged %d units for %d bytes, expected x %d", units, counted, rate))
	}
	message := ""
	if s.LastError != nil {
		message = s.LastError.Message
	}
	r.verdict(id, problems, fmt.Sprintf("stopped by itself %d ms after the change with %q (%s); tail settled, no flow left; the segment's %d bytes charged %d units at its own %d permille",
		stopped.Milliseconds(), code, message, counted, units, rate))
}

func (r *runner) nodeReported(ctx context.Context) {
	if !r.cfg.World.NodeReported {
		r.record("B04", "BLOCKED", "no node agent in this world (the suite builds native/nodeagent)")
		return
	}
	target := r.cfg.World.AgentTarget
	if !r.cfg.World.AgentResolve {
		r.record("B04", "BLOCKED", fmt.Sprintf("this host's resolver does not map %s to loopback (RFC 6761), so the node cannot reach the sink by name", target))
		return
	}
	if !r.signIn(ctx, "grant") {
		return
	}
	agent := r.cfg.World.Nodes["agent"]
	listed := r.waitFor(ctx, 10*time.Second, func(s managed.Snapshot) bool {
		return slices.ContainsFunc(s.Nodes, func(n managed.NodeView) bool { return n.ID == agent && n.MeteringSource == "node_reported" })
	})
	if listed < 0 {
		r.record("B04-connect", "FAIL", "the node-reported line is not listed")
		return
	}
	r.selectNode(ctx, "agent")
	before, _ := r.world.ledger(ctx, "grant")
	sinkUp0, sinkDown0, _ := r.world.sink(ctx)
	reportsBefore := 0
	if reports, err := r.world.reports(ctx); err == nil {
		reportsBefore = len(reports)
	}
	s, err := r.connect(ctx)
	if err != nil || s.State != managed.StateConnected {
		r.record("B04-connect", "FAIL", fmt.Sprintf("state %s: %v %s", s.State, err, r.lastErrorCode(s)))
		return
	}
	rate := int64(0)
	if s.Metering != nil {
		rate = s.Metering.RatePermille
	}
	status := "FAIL"
	if meteringSource(s) == "node_reported" {
		status = "PASS"
	}
	r.record("B04-connect", status, fmt.Sprintf("connected through a grant on the agent's forwarder; metering source %s, rate %d permille", meteringSource(s), rate))

	out, err := r.world.post(ctx, "reject_probe", map[string]string{})
	var probe struct {
		Status int    `json:"status"`
		Code   string `json:"code"`
		Moved  bool   `json:"ledger_moved"`
	}
	if err != nil || json.Unmarshal(out, &probe) != nil {
		r.record("B04-reject", "FAIL", fmt.Sprintf("reject probe: %v", err))
	} else {
		status = "FAIL"
		if probe.Status == 409 && probe.Code == "metering_source_mismatch" && !probe.Moved {
			status = "PASS"
		}
		r.record("B04-reject", status, fmt.Sprintf("a client report on this node-reported session -> %d %s; ledger moved %v", probe.Status, probe.Code, probe.Moved))
	}

	up, down := int64(2<<20), int64(8<<20)
	byName := transfer{dial: r.eng.Dial, host: target, port: r.cfg.Sink.Port, token: r.cfg.Sink.Token}
	gotUp, upErr := byName.upload(ctx, up)
	gotDown, downErr := byName.download(ctx, down)
	time.Sleep(2 * time.Second)
	r.m.Disconnect(ctx)
	r.waitFor(ctx, 15*time.Second, func(s managed.Snapshot) bool { return !tunnelBusy(s) })
	// The agent reports usage every second; wait for the ledger to stop moving.
	after, _ := r.world.ledger(ctx, "grant")
	stable := 0
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && stable < 3; {
		time.Sleep(time.Second)
		now, _ := r.world.ledger(ctx, "grant")
		if now.UsedUnits == after.UsedUnits && now.UsedUnits > before.UsedUnits {
			stable++
		} else {
			stable = 0
		}
		after = now
	}
	sinkUp1, sinkDown1, _ := r.world.sink(ctx)
	reports, _ := r.world.reports(ctx)
	extra := 0
	if len(reports) > reportsBefore {
		extra = len(reports) - reportsBefore
	}
	units := after.UsedUnits - before.UsedUnits
	body := up + down
	low := body * rate
	high := (body + body/50 + r.cfg.BackgroundBytes) * rate
	status = "FAIL"
	if extra == 0 {
		status = "PASS"
	}
	r.record("B04-no-report", status, fmt.Sprintf("client /traffic reports during the node-reported connection: %d", extra))
	var problems []string
	if gotUp != up || gotDown != down {
		problems = append(problems, fmt.Sprintf("transfer: upload %d (%v), download %d (%v)", gotUp, upErr, gotDown, downErr))
	}
	if sinkUp1-sinkUp0 != up || sinkDown1-sinkDown0 != down {
		problems = append(problems, fmt.Sprintf("the sink saw up %d down %d", sinkUp1-sinkUp0, sinkDown1-sinkDown0))
	}
	if units < low || units > high {
		problems = append(problems, fmt.Sprintf("the node billed %d units, expected %d..%d (body x %d, + 2%% + %d B)", units, low, high, rate, r.cfg.BackgroundBytes))
	}
	r.verdict("B04-node-bills", problems, fmt.Sprintf("the node billed %d units for a %d byte body at %d permille, with no client report", units, body, rate))
}
