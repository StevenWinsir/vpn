package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"asterlink/core/internal/managed"
	"asterlink/core/internal/meter"
)

type caseResult struct {
	ID       string `json:"case_id"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	Result   string `json:"result"` // pass | fail | blocked
}

type recorder struct{ cases []caseResult }

func (r *recorder) record(id, expected, observed, result string) {
	r.cases = append(r.cases, caseResult{id, expected, observed, result})
	fmt.Printf("%-18s %-7s %s\n", id, strings.ToUpper(result), observed)
}

func (r *recorder) verdict(id, expected string, problems []string, passText string) {
	if len(problems) == 0 {
		r.record(id, expected, passText, "pass")
		return
	}
	r.record(id, expected, strings.Join(problems, "; "), "fail")
}

func (r *recorder) counts() (failed, blocked int) {
	for _, c := range r.cases {
		switch c.Result {
		case "fail":
			failed++
		case "blocked":
			blocked++
		}
	}
	return
}

type config struct {
	apiBase, channel, email, password, deviceID string
	sinkBase                                    string
	upload, download                            int64
	nodes                                       []string // ids or exact names; empty = one node per distinct rate
	background                                  int64    // tolerated foreign bytes per direction
	ackLoss                                     bool
	watchStop                                   time.Duration
	watchDrive                                  bool
	expectStop                                  string
	settleWait                                  time.Duration
}

type harness struct {
	cfg   config
	m     *managed.Manager
	xfer  transferFunc
	stats func(context.Context) (sinkStats, error)
	lossy *lossyTransport
	rec   *recorder
}

func (h *harness) login(ctx context.Context) (managed.Snapshot, bool) {
	snap, err := h.m.Login(ctx, managed.LoginParams{Email: h.cfg.email, Password: h.cfg.password, APIBase: h.cfg.apiBase, Channel: h.cfg.channel, DeviceID: h.cfg.deviceID, AppVersion: "billtest"})
	if err != nil {
		h.rec.record("A-LOGIN", "the test account signs in against the real API", "login failed: "+errCode(err), "blocked")
		return snap, false
	}
	deadline := time.Now().Add(20 * time.Second)
	for len(snap.Nodes) == 0 && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		snap = h.m.Snapshot()
	}
	e := snap.Entitlement
	if e == nil || !e.AccountAuthorized || len(snap.Nodes) == 0 {
		reason := "no entitlement view"
		if e != nil {
			reason = fmt.Sprintf("account_authorized=%v reason=%q nodes=%d", e.AccountAuthorized, e.Reason, len(snap.Nodes))
		}
		h.rec.record("A-LOGIN", "the account has an active plan and at least one node", reason, "blocked")
		return snap, false
	}
	h.rec.record("A-LOGIN", "the account signs in and receives plan + nodes", fmt.Sprintf("plan=%q nodes=%d remaining=%d of %d bytes", e.PlanName, len(snap.Nodes), e.RemainingBytes, e.TotalBytes), "pass")
	return snap, true
}

func errCode(err error) string {
	if err == nil {
		return ""
	}
	if code := meter.ErrorCode(err); code != "" {
		return code
	}
	return err.Error()
}

// chooseNodes applies --nodes, or takes the first node of each distinct rate (at most two),
// so one run covers both a dedicated and an ordinary line when the plan has them.
func chooseNodes(nodes []managed.NodeView, wanted []string) []managed.NodeView {
	var out []managed.NodeView
	if len(wanted) > 0 {
		for _, w := range wanted {
			for _, n := range nodes {
				if n.ID == w || n.Name == w {
					out = append(out, n)
					break
				}
			}
		}
		return out
	}
	sorted := append([]managed.NodeView(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].RatePermille > sorted[j].RatePermille })
	seen := map[int64]bool{}
	for _, n := range sorted {
		if !seen[n.RatePermille] && len(out) < 2 {
			seen[n.RatePermille] = true
			out = append(out, n)
		}
	}
	return out
}

func (h *harness) selectNode(ctx context.Context, node managed.NodeView) (managed.Snapshot, error) {
	snap, err := h.m.RefreshCatalog(ctx)
	if err != nil {
		return snap, err
	}
	return h.m.SelectNode(ctx, node.ID, snap.CatalogRevision)
}

func confirmed(s managed.Snapshot) (up, down, units, total, remaining int64, ok bool) {
	if s.Metering == nil || s.Entitlement == nil {
		return 0, 0, 0, 0, 0, false
	}
	return s.Metering.ConfirmedUploadBytes, s.Metering.ConfirmedDownloadBytes, s.Entitlement.UsedUnits, s.Entitlement.TotalBytes, s.Entitlement.RemainingBytes, true
}

// measure runs one full connect -> controlled transfer -> disconnect cycle and returns the
// numbers. A false return means a case has already been recorded.
func (h *harness) measure(ctx context.Context, tag string, node managed.NodeView, up, down int64, afterConnect func()) (segment, managed.Snapshot, bool) {
	var seg segment
	before, err := h.selectNode(ctx, node)
	if err != nil {
		h.rec.record(tag+"-select", "the node can be selected and its config verified", errCode(err), "fail")
		return seg, before, false
	}
	b := func() (ok bool) {
		seg.UploadBefore, seg.DownloadBefore, seg.UnitsBefore, seg.TotalBytes, _, ok = confirmed(before)
		return
	}
	if !b() {
		h.rec.record(tag+"-select", "a snapshot with plan and metering", "metering view missing", "fail")
		return seg, before, false
	}
	seg.RatePermille = node.RatePermille
	// Worst case counted: body + 10% + background, charged at the node's rate.
	worst := (up + down) + (up+down)/10 + 2*h.cfg.background
	if need := worst * node.RatePermille / 1000; before.Entitlement.RemainingBytes < need {
		h.rec.record(tag+"-quota", "enough quota for the controlled transfer", fmt.Sprintf("remaining %d bytes, up to %d needed: raise the plan quota or lower --upload-mib/--download-mib", before.Entitlement.RemainingBytes, need), "blocked")
		return seg, before, false
	}
	sinkBefore, err := h.stats(ctx)
	if err != nil {
		h.rec.record(tag+"-sink", "the controlled sink answers while no tunnel exists", err.Error(), "blocked")
		return seg, before, false
	}
	started := time.Now()
	connected, err := h.m.Connect(ctx)
	if err != nil || connected.State != managed.StateConnected {
		verdict := "fail"
		switch errCode(err) {
		case "vpn_conflict", "no_network", "tun_unavailable":
			verdict = "blocked" // the machine, not the product, prevented the test (e.g. a company VPN is up)
		}
		h.rec.record(tag+"-connect", "Connect reaches connected after the first metering ack and a real forwarding probe", fmt.Sprintf("state=%s error=%s", connected.State, errCode(err)), verdict)
		_ = h.m.Disconnect(ctx)
		return seg, before, false
	}
	h.rec.record(tag+"-connect", "Connect reaches connected after the first metering ack and a real forwarding probe", fmt.Sprintf("connected in %v, health=%s, rate=%d permille", time.Since(started).Round(time.Millisecond), connected.TunnelHealth, node.RatePermille), "pass")

	if afterConnect != nil {
		afterConnect()
	}
	result, xerr := h.xfer(ctx, up, down)
	time.Sleep(h.cfg.settleWait) // the monitor samples once a second
	live := h.m.Snapshot()
	after := h.m.Disconnect(ctx)
	for try := 0; try < 3 && after.Metering != nil && after.Metering.Pending; try++ {
		after = h.m.Disconnect(ctx)
	}
	if xerr != nil {
		h.rec.record(tag+"-transfer", "the controlled transfer completes through the tunnel", xerr.Error(), "fail")
		return seg, after, false
	}
	seg.UploadBody, seg.DownloadBody = result.Upload, result.Download
	if live.Metering != nil && before.Metering != nil {
		movedUp := live.Metering.UploadBytes - before.Metering.UploadBytes
		movedDown := live.Metering.DownloadBytes - before.Metering.DownloadBytes
		h.rec.verdict(tag+"-live", "the live usage the window shows already includes the transfer",
			problemIf(movedUp < up || movedDown < down, fmt.Sprintf("live up %d (body %d), down %d (body %d)", movedUp, up, movedDown, down)),
			fmt.Sprintf("live up %d, down %d, history %d samples", movedUp, movedDown, len(live.Metering.History)))
	}
	u, d, units, _, remaining, ok := confirmed(after)
	if !ok || after.Metering.Pending {
		h.rec.record(tag+"-settle", "Disconnect leaves no unacknowledged batch", fmt.Sprintf("pending=%v error=%v", after.Metering != nil && after.Metering.Pending, after.LastError), "fail")
		return seg, after, false
	}
	seg.UploadAfter, seg.DownloadAfter, seg.UnitsAfter, seg.RemainingAfter = u, d, units, remaining
	sinkAfter, err := h.stats(ctx)
	if err != nil {
		h.rec.record(tag+"-sink", "the sink answers after disconnect", err.Error(), "blocked")
		return seg, after, false
	}
	h.rec.verdict(tag+"-sink", "the sink independently saw exactly the bytes sent and received",
		problemIf(sinkAfter.UpReceived-sinkBefore.UpReceived != up || sinkAfter.DownSent-sinkBefore.DownSent != down,
			fmt.Sprintf("sink up %d (sent %d), down %d (wanted %d)", sinkAfter.UpReceived-sinkBefore.UpReceived, up, sinkAfter.DownSent-sinkBefore.DownSent, down)),
		fmt.Sprintf("sink up %d, down %d", up, down))
	return seg, after, true
}

func problemIf(bad bool, text string) []string {
	if bad {
		return []string{text}
	}
	return nil
}

// billing is B01: the same controlled bytes charged at each distinct rate, to the unit.
func (h *harness) billing(ctx context.Context, snap managed.Snapshot) {
	nodes := chooseNodes(snap.Nodes, h.cfg.nodes)
	if len(nodes) == 0 {
		h.rec.record("B01", "a node to measure", "no node matches --nodes", "blocked")
		return
	}
	var perByte []string
	for i, node := range nodes {
		tag := fmt.Sprintf("B01-n%d", i+1)
		seg, _, ok := h.measure(ctx, tag, node, h.cfg.upload, h.cfg.download, nil)
		if !ok {
			continue
		}
		h.rec.verdict(tag+"-ledger", "charged units = (up delta + down delta) x rate, remaining follows the formula", ledgerProblems(seg),
			fmt.Sprintf("rate %d: counted up %d + down %d = %d bytes -> %d units (%d bytes of quota); remaining %d", seg.RatePermille, seg.deltaUp(), seg.deltaDown(), seg.deltaUp()+seg.deltaDown(), seg.deltaUnits(), quotaBytes(seg.deltaUnits()), seg.RemainingAfter))
		h.rec.verdict(tag+"-body", "counters cover the body with only protocol framing and bounded background traffic", bodyProblems(seg, h.cfg.background),
			fmt.Sprintf("upload body %d counted %d (%+.2f%%); download body %d counted %d (%+.2f%%)", seg.UploadBody, seg.deltaUp(), 100*overhead(seg.deltaUp(), seg.UploadBody), seg.DownloadBody, seg.deltaDown(), 100*overhead(seg.deltaDown(), seg.DownloadBody)))
		perByte = append(perByte, fmt.Sprintf("%d permille: %d units", seg.RatePermille, seg.deltaUnits()))
	}
	if len(perByte) > 1 {
		h.rec.record("B01-rates", "different rates were measured with the same procedure", strings.Join(perByte, " | "), "pass")
	}
}

// ackLoss is B02 on the real server: one acknowledgement is lost, the Core must resend the
// identical batch, and the server must charge it exactly once.
func (h *harness) ackLoss(ctx context.Context, snap managed.Snapshot) {
	nodes := chooseNodes(snap.Nodes, h.cfg.nodes)
	if len(nodes) == 0 {
		h.rec.record("B02", "a node to measure", "no node matches --nodes", "blocked")
		return
	}
	node := nodes[0]
	up, down := min(h.cfg.upload, 2<<20), min(h.cfg.download, 4<<20)
	seg, _, ok := h.measure(ctx, "B02", node, up, down, nil)
	if !ok {
		return
	}
	// The loss is injected into the second cycle, after Connect's own first acknowledgement.
	h.rec.verdict("B02-baseline", "an ordinary cycle on this node is exact before loss is injected", ledgerProblems(seg), fmt.Sprintf("%d units", seg.deltaUnits()))

	lost, _, ok := h.measure(ctx, "B02-lost", node, up, down, h.lossy.dropNext)
	bodies, dropped := h.lossy.trafficBodies()
	switch {
	case dropped < 0:
		h.rec.record("B02-injected", "one real /traffic reply was discarded", "no /traffic request happened after arming", "fail")
		return
	case dropped+1 >= len(bodies):
		h.rec.record("B02-retry", "the discarded batch was sent again", fmt.Sprintf("only %d traffic requests, dropped #%d", len(bodies), dropped), "fail")
	default:
		h.rec.verdict("B02-retry", "the retry carries the original sequence and the original cumulative counters",
			problemIf(bodies[dropped] != bodies[dropped+1], fmt.Sprintf("dropped %s, next %s", bodies[dropped], bodies[dropped+1])),
			fmt.Sprintf("batch %s was sent again unchanged", bodies[dropped]))
	}
	if ok {
		h.rec.verdict("B02-once", "the batch whose reply was lost is charged exactly once", ledgerProblems(lost), fmt.Sprintf("%d units charged for %d counted bytes", lost.deltaUnits(), lost.deltaUp()+lost.deltaDown()))
	}
}

// watchStop is A04/B03 on the live system: the operator changes something in the admin
// panel (disables or deletes the node, edits its rate, ends the plan, lowers the quota) and
// the tunnel must close by itself. With --watch-drive the harness keeps moving data, which is
// how quota exhaustion is provoked.
func (h *harness) watchStop(ctx context.Context, snap managed.Snapshot) {
	nodes := chooseNodes(snap.Nodes, h.cfg.nodes)
	if len(nodes) == 0 {
		h.rec.record("A04-watch", "a node to hold open", "no node matches --nodes", "blocked")
		return
	}
	if _, err := h.selectNode(ctx, nodes[0]); err != nil {
		h.rec.record("A04-watch", "select a node", errCode(err), "fail")
		return
	}
	connected, err := h.m.Connect(ctx)
	if err != nil || connected.State != managed.StateConnected {
		h.rec.record("A04-watch", "connect", fmt.Sprintf("state=%s error=%s", connected.State, errCode(err)), "fail")
		return
	}
	fmt.Printf("\nCONNECTED. Within %v change the node, plan or quota in the admin panel (expected stop: %q).\n\n", h.cfg.watchStop, h.cfg.expectStop)
	began := time.Now()
	deadline := began.Add(h.cfg.watchStop)
	var stopped managed.Snapshot
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if h.cfg.watchDrive {
			_, _ = h.xfer(ctx, 0, 8<<20)
		}
		snap := h.m.Snapshot()
		if snap.State != managed.StateConnected {
			stopped = snap
			break
		}
		time.Sleep(time.Second)
	}
	if stopped.State == "" {
		_ = h.m.Disconnect(ctx)
		h.rec.record("A04-watch", "the tunnel closes by itself after the server-side change", fmt.Sprintf("still connected after %v", h.cfg.watchStop), "fail")
		return
	}
	// Wait for the settlement the stop path performs, then read the final state.
	for try := 0; try < 20; try++ {
		stopped = h.m.Snapshot()
		if stopped.State != managed.StateStopping && (stopped.Metering == nil || !stopped.Metering.Pending) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	code := ""
	if stopped.LastError != nil {
		code = stopped.LastError.Code
	}
	problems := []string{}
	if code == "" {
		problems = append(problems, "stopped without a reason")
	}
	if h.cfg.expectStop != "" && !strings.Contains(","+h.cfg.expectStop+",", ","+code+",") {
		problems = append(problems, fmt.Sprintf("reason %q, expected one of %q", code, h.cfg.expectStop))
	}
	if stopped.TunnelHealth == "up" {
		problems = append(problems, "tunnel health still up")
	}
	h.rec.verdict("A04-watch", "the tunnel closes on its own with a stable reason code and the tail is settled", problems,
		fmt.Sprintf("closed %v after the watch began, reason=%q, state=%s, pending=%v", time.Since(began).Round(time.Second), code, stopped.State, stopped.Metering != nil && stopped.Metering.Pending))
	again, err := h.m.Connect(ctx)
	connectedAgain := err == nil && again.State == managed.StateConnected
	if code == "profile_changed" {
		// An edited line is not a refusal: the next Connect fetches the new config and binds
		// the new rate. The old segment was settled at the old rate above.
		rate := int64(-1)
		for _, n := range again.Nodes {
			if n.ID == again.SelectedNodeID {
				rate = n.RatePermille
			}
		}
		bound := again.Metering != nil && again.Metering.RatePermille == rate
		if connectedAgain {
			_ = h.m.Disconnect(ctx)
		}
		h.rec.verdict("A04-rebind", "after an edit the next connection binds the new rate", problemIf(!connectedAgain || !bound,
			fmt.Sprintf("connected=%v error=%s rate bound=%v", connectedAgain, errCode(err), bound)), fmt.Sprintf("reconnected at the edited rate %d permille", rate))
		return
	}
	if connectedAgain {
		_ = h.m.Disconnect(ctx)
		h.rec.record("A04-reconnect", "a new connection is refused while the cause persists", "Connect succeeded again (undo the admin change if this was intended)", "fail")
		return
	}
	h.rec.record("A04-reconnect", "a new connection is refused while the cause persists", "refused: "+errCode(err), "pass")
}

func (h *harness) run(ctx context.Context) {
	snap, ok := h.login(ctx)
	if !ok {
		return
	}
	defer func() { h.m.Logout(context.Background()) }()
	if h.cfg.watchStop > 0 {
		h.watchStop(ctx, snap)
		return
	}
	h.billing(ctx, snap)
	if h.cfg.ackLoss {
		h.ackLoss(ctx, snap)
	}
}
