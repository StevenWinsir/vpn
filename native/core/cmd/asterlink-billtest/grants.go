package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/managed"
)

// Round 7: does the NODE enforce the commercial rules, with the client out of the picture?
// (handoff_app_windows.md section 10 round 7; cases B04-B07.) These scenarios talk to the real API and a
// real managed node. Except for the optional --tun case they change nothing on this machine: the "user" is an
// independent VLESS client holding a grant credential, exactly what someone who extracted it would run.

// grantOptions are the run-time knobs of the round-7 scenarios.
type grantOptions struct {
	tun         bool          // also run B04 through the production Manager and the real TUN (elevated Windows)
	cutWithin   time.Duration // a revoked/ended grant must stop its flows within this long
	holdFor     time.Duration // operator-driven case: how long to wait for flows to be cut
	holdCase    string        // case id for the operator-driven case (B05-expire, B05-quota, B07-outage, ...)
	holdDrive   bool          // keep downloading during the hold, which is how a quota is exhausted
	idleExpiry  bool          // let a grant lapse with no client activity at all
	idleWait    time.Duration // upper bound for the idle-expiry case
	raceN       int           // concurrent sessions for B06 (0 = skip)
	raceAt      time.Time     // synchronised start across machines (zero = now)
	raceMaxByte int64         // B06 is only informative on a small remaining quota
	udpSink     string        // host:port of the sink's UDP echo; empty = TCP flows only
}

type grantRun struct {
	cfg   config
	opt   grantOptions
	rec   *recorder
	api   *apiclient.Client // the auxiliary session: separate device, wrapped to count /traffic
	trace *lossyTransport
	sink  destination
	udp   *destination
	token string // the sink token
	stats func(context.Context) (sinkStats, error)
	h     *harness // set for --tun
	now   func() time.Time
	poll  time.Duration // ledger polling cadence (tests shorten it)
	quiet time.Duration // how long an unmoved ledger is waited on before it counts as "nothing was billed"
}

func (g *grantRun) quietFor() time.Duration {
	if g.quiet > 0 {
		return g.quiet
	}
	return 25 * time.Second // the agent reports every 5 s; five missed reports means it is not billing
}

func (g *grantRun) pollEvery() time.Duration {
	if g.poll > 0 {
		return g.poll
	}
	return 1500 * time.Millisecond
}

func platformName() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "macos"
}

// deviceFor derives a stable UUID per test role from the base device id, so repeated runs reuse the same few
// device slots of the test account instead of exhausting its device limit.
func deviceFor(base, role string) string {
	sum := sha256.Sum256([]byte("asterlink-grants|" + base + "|" + role))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func requestKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "grants-" + hex.EncodeToString(b)
}

func apiCode(err error) string {
	if ae, ok := err.(*apiclient.Error); ok {
		return ae.Code
	}
	return errCode(err)
}

type session struct {
	token string
	state apiclient.Session
}

func (g *grantRun) login(ctx context.Context, role string) (session, error) {
	out, err := g.api.Login(ctx, apiclient.LoginRequest{Email: g.cfg.email, Password: g.cfg.password, DeviceID: deviceFor(g.cfg.deviceID, role), Platform: platformName(), AppVersion: "billtest-grants"})
	return session{token: out.Token, state: out.Session}, err
}

// grant is one minted credential, kept in memory only.
type grant struct {
	id     string
	target nodeTarget
	status apiclient.GrantStatus
}

// mint asks for a grant and waits for the node to confirm it. The credential cannot exist before that.
func (g *grantRun) mint(ctx context.Context, s session, nodeID string, wait time.Duration) (grant, error) {
	status, err := g.api.CreateGrant(ctx, s.token, nodeID, "", requestKey())
	if err != nil {
		return grant{}, err
	}
	deadline := time.Now().Add(wait)
	for {
		if status.State == apiclient.GrantReady {
			break
		}
		if status.State != apiclient.GrantPending && status.State != "" {
			return grant{}, fmt.Errorf("grant ended as %s (%s)", status.State, status.Reason)
		}
		if time.Now().After(deadline) {
			return grant{}, fmt.Errorf("grant_timeout")
		}
		select {
		case <-ctx.Done():
			return grant{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if status, err = g.api.Grant(ctx, s.token, status.GrantID); err != nil {
			return grant{}, err
		}
	}
	profile, err := managed.ParseProfile(nodeID, "", status.Version, status.YAML)
	if err != nil {
		return grant{}, fmt.Errorf("config_rejected")
	}
	target, err := targetFromProfile(profile)
	if err != nil {
		return grant{}, err
	}
	status.YAML = "" // the document is consumed; only the target (in memory) remains
	return grant{id: status.GrantID, target: target, status: status}, nil
}

// release revokes a grant and waits for the node to confirm, because the server only hands the reserved byte budget back
// after that settlement. A test that asked for the next grant at once would see quota_exhausted on a small quota that is
// really just not yet returned.
func (g *grantRun) release(s session, gr grant) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := g.api.RevokeGrant(ctx, s.token, gr.id, ""); err != nil {
		return
	}
	for ctx.Err() == nil {
		status, err := g.api.Grant(ctx, s.token, gr.id)
		if err != nil || status.State == apiclient.GrantRevoked || status.State == apiclient.GrantExpired {
			return
		}
		time.Sleep(g.pollEvery())
	}
}

func (g *grantRun) startProbe(ctx context.Context, gr grant, chunk int64) (*probe, error) {
	return startProbe(ctx, gr.target, g.sink, g.udp, g.token, chunk)
}

// unitsSettled polls the account ledger until it has moved from baseline and then stayed put, because the node
// reports usage on its own cadence. It returns the last session view.
func (g *grantRun) unitsSettled(ctx context.Context, s session, baseline int64, maxWait time.Duration) (apiclient.Session, error) {
	began := time.Now()
	deadline := began.Add(maxWait)
	var last apiclient.Session
	stable, previous := 0, baseline
	for time.Now().Before(deadline) {
		cur, err := g.api.Session(ctx, s.token)
		if err != nil {
			return last, err
		}
		last = cur
		switch {
		case cur.UsedUnits == baseline:
		case cur.UsedUnits == previous:
			stable++
		default:
			stable = 0
		}
		previous = cur.UsedUnits
		if stable >= 3 || cur.UsedUnits == baseline && time.Since(began) >= g.quietFor() {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(g.pollEvery()):
		}
	}
	return last, nil
}

func (g *grantRun) run(ctx context.Context) {
	caps, err := g.api.Capabilities(ctx)
	if err != nil {
		g.rec.record("G-CAPS", "the server answers /capabilities", apiCode(err), "blocked")
		return
	}
	hasV2, hasNode := false, false
	for _, v := range caps.ContractVersions {
		hasV2 = hasV2 || v == 2
	}
	for _, m := range caps.MeteringModes {
		hasNode = hasNode || m == apiclient.MeteringNode
	}
	if !hasV2 || !hasNode {
		g.rec.record("G-CAPS", "the server offers contract 2 and the node_reported metering mode", fmt.Sprintf("contracts=%v modes=%v", caps.ContractVersions, caps.MeteringModes), "blocked")
		return
	}
	g.rec.record("G-CAPS", "the server offers contract 2 and the node_reported metering mode", fmt.Sprintf("contracts=%v modes=%v min_app_version=%q", caps.ContractVersions, caps.MeteringModes, caps.MinAppVersion), "pass")

	aux, err := g.login(ctx, "aux")
	if err != nil {
		g.rec.record("A-LOGIN", "the test account signs in on a second device slot", apiCode(err), "blocked")
		return
	}
	defer func() { _ = g.api.Logout(context.Background(), aux.token) }()
	catalog, err := g.api.Nodes(ctx, aux.token, "")
	if err != nil {
		g.rec.record("G-NODES", "the catalog lists the plan's nodes", apiCode(err), "blocked")
		return
	}
	var managedNodes []apiclient.NodeChoice
	for _, n := range catalog.Nodes {
		if n.MeteringSource != apiclient.MeteringNode {
			continue
		}
		if len(g.cfg.nodes) > 0 && !contains(g.cfg.nodes, n.ID) && !contains(g.cfg.nodes, n.Name) {
			continue
		}
		managedNodes = append(managedNodes, n)
	}
	if len(managedNodes) == 0 {
		g.rec.record("G-NODES", "the plan includes a node governed by a node agent (metering_source=node_reported)",
			fmt.Sprintf("0 of %d visible nodes are node-governed: deploy the agent on a server you manage and register it (docs/macos-stage4.md section 4); until then commercial enforcement cannot be validated", len(catalog.Nodes)), "blocked")
		return
	}
	node := managedNodes[0]
	g.rec.record("G-NODES", "the plan includes a node governed by a node agent", fmt.Sprintf("%d node-governed line(s); testing the first at %d permille", len(managedNodes), node.RatePermille), "pass")

	g.rejectsClientReports(ctx, aux, node)
	g.nodeBillsAlone(ctx, aux, node)
	g.revokeCutsFlows(ctx, aux, node)
	if g.opt.idleExpiry {
		g.idleExpiry(ctx, aux, node)
	}
	if g.opt.holdFor > 0 {
		g.hold(ctx, aux, node)
	}
	if g.opt.raceN > 0 {
		g.race(ctx, aux, node)
	}
	if g.opt.tun && g.h != nil {
		g.tunBilling(ctx, node)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// rejectsClientReports is the second half of B04: once a session holds a grant, the node is the only ledger, so a
// client trying to bill the same bytes again through /traffic (or to fetch the shared credential through
// /config) must be refused, and the ledger must not move.
func (g *grantRun) rejectsClientReports(ctx context.Context, s session, node apiclient.NodeChoice) {
	gr, err := g.mint(ctx, s, node.ID, 30*time.Second)
	if err != nil {
		g.rec.record("B04-grant", "a ready grant is issued for the node-governed line", apiCode(err), "fail")
		return
	}
	defer g.release(s, gr)
	before, err := g.api.Session(ctx, s.token)
	if err != nil {
		g.rec.record("B04-reject", "the session is readable", apiCode(err), "fail")
		return
	}
	_, _, terr := g.api.Traffic(ctx, s.token, apiclient.Counters{Sequence: before.LastSequence + 1, UploadBytes: 1 << 20, DownloadBytes: 1 << 20})
	_, cerr := g.api.Config(ctx, s.token, node.ID, 0)
	after, _ := g.api.Session(ctx, s.token)
	problems := []string{}
	if terr == nil {
		problems = append(problems, "/traffic was ACCEPTED on a node-reported session")
	} else if apiCode(terr) != "metering_source_mismatch" {
		problems = append(problems, fmt.Sprintf("/traffic refused with %q, expected metering_source_mismatch", apiCode(terr)))
	}
	if cerr == nil {
		problems = append(problems, "/config handed out a configuration for a node-governed line")
	}
	if after.UsedUnits != before.UsedUnits {
		problems = append(problems, fmt.Sprintf("the ledger moved by %d units", after.UsedUnits-before.UsedUnits))
	}
	g.rec.verdict("B04-reject", "a node-reported session cannot bill through /traffic or fetch the shared /config; the ledger does not move", problems,
		fmt.Sprintf("/traffic -> %s, /config -> %s, ledger unchanged at %d units", apiCode(terr), apiCode(cerr), after.UsedUnits))
}

// nodeBillProblems judges the node's own accounting of one controlled transfer: the account was charged
// the payload the sink independently saw, at the bound rate, and the remaining balance follows the formula.
// Framing (HTTP) can only add, so the charge is bracketed rather than equal.
func nodeBillProblems(rate, unitsDelta, bodyBytes, allowance, total, usedAfter, remainingAfter int64) []string {
	var problems []string
	low := bodyBytes * rate
	high := (bodyBytes + bodyBytes/50 + allowance) * rate
	switch {
	case unitsDelta == 0:
		problems = append(problems, "the node billed nothing for traffic it carried")
	case unitsDelta < low:
		problems = append(problems, fmt.Sprintf("charged %d units, less than the %d the sink saw at %d permille", unitsDelta, low, rate))
	case unitsDelta > high:
		problems = append(problems, fmt.Sprintf("charged %d units, more than %d (body + 2%% + allowance) at %d permille", unitsDelta, high, rate))
	}
	if want := max(0, (total*1000-usedAfter)/1000); remainingAfter != want {
		problems = append(problems, fmt.Sprintf("remaining %d bytes, formula says %d", remainingAfter, want))
	}
	return problems
}

// nodeBillsAlone is B04: an independent client that sends no usage report at all is still billed by the node.
func (g *grantRun) nodeBillsAlone(ctx context.Context, s session, node apiclient.NodeChoice) {
	up, down := min(g.cfg.upload, 4<<20), min(g.cfg.download, 8<<20)
	before, err := g.api.Session(ctx, s.token)
	if err != nil {
		g.rec.record("B04-node-bills", "session readable", apiCode(err), "fail")
		return
	}
	if need := (up + down) * 11 / 10 * node.RatePermille / 1000; before.RemainingBytes < need {
		g.rec.record("B04-node-bills", "enough quota for the controlled transfer", fmt.Sprintf("remaining %d bytes, up to %d needed", before.RemainingBytes, need), "blocked")
		return
	}
	sinkBefore, err := g.stats(ctx)
	if err != nil {
		g.rec.record("B04-node-bills", "the sink answers", err.Error(), "blocked")
		return
	}
	gr, err := g.mint(ctx, s, node.ID, 30*time.Second)
	if err != nil {
		g.rec.record("B04-node-bills", "a ready grant is issued", apiCode(err), "fail")
		return
	}
	defer g.release(s, gr)
	reportsBefore, _ := g.trace.trafficBodies()
	f, err := openTCP(ctx, gr.target, g.sink, g.token)
	if err != nil {
		g.rec.record("B04-node-bills", "the independent client connects with the grant credential", err.Error(), "fail")
		return
	}
	var moved int64
	for moved < down && err == nil {
		var n int64
		n, err = f.get(min(down-moved, 2<<20), 60*time.Second)
		moved += n
	}
	if err == nil {
		err = f.put(up, 120*time.Second)
	}
	f.close()
	if err != nil {
		g.rec.record("B04-node-bills", "the controlled transfer completes through the node", err.Error(), "fail")
		return
	}
	last, err := g.unitsSettled(ctx, s, before.UsedUnits, 90*time.Second)
	if err != nil {
		g.rec.record("B04-node-bills", "the ledger is readable", apiCode(err), "fail")
		return
	}
	sinkAfter, err := g.stats(ctx)
	if err != nil {
		g.rec.record("B04-node-bills", "the sink answers", err.Error(), "blocked")
		return
	}
	body := (sinkAfter.UpReceived - sinkBefore.UpReceived) + (sinkAfter.DownSent - sinkBefore.DownSent)
	reportsAfter, _ := g.trace.trafficBodies()
	problems := nodeBillProblems(node.RatePermille, last.UsedUnits-before.UsedUnits, body, 2*g.cfg.background, last.TotalBytes, last.UsedUnits, last.RemainingBytes)
	if len(reportsAfter) != len(reportsBefore) {
		problems = append(problems, "this client sent a /traffic report, so the case proves nothing")
	}
	g.rec.verdict("B04-node-bills", "with no client report the node still bills the carried payload at the bound rate", problems,
		fmt.Sprintf("sink saw %d bytes, no /traffic sent, ledger +%d units at %d permille (%.2f%% above the body), remaining %d", body, last.UsedUnits-before.UsedUnits, node.RatePermille,
			100*float64(last.UsedUnits-before.UsedUnits-body*node.RatePermille)/float64(max(1, body*node.RatePermille)), last.RemainingBytes))
}

// revokeCutsFlows is B05 (revocation): the user's own copy of the credential stops working, established TCP and UDP
// flows included, within a stated time; the node does not wait for the client to cooperate.
func (g *grantRun) revokeCutsFlows(ctx context.Context, s session, node apiclient.NodeChoice) {
	gr, err := g.mint(ctx, s, node.ID, 30*time.Second)
	if err != nil {
		g.rec.record("B05-revoke", "a ready grant is issued", apiCode(err), "fail")
		return
	}
	p, err := g.startProbe(ctx, gr, 0)
	if err != nil {
		_, _ = g.api.RevokeGrant(context.Background(), s.token, gr.id, "")
		g.rec.record("B05-revoke", "TCP"+g.udpNote()+" flows are established with the extracted credential", err.Error(), "fail")
		return
	}
	defer p.close()
	time.Sleep(time.Second)
	if !p.tcpAlive() || p.udp != nil && !p.udpAlive() {
		g.rec.record("B05-revoke", "established flows are healthy before the revocation", "a flow failed before revoking", "fail")
		return
	}
	began := time.Now()
	if _, err = g.api.RevokeGrant(ctx, s.token, gr.id, ""); err != nil {
		g.rec.record("B05-revoke", "the revocation is accepted", apiCode(err), "fail")
		return
	}
	cut, ok := g.waitDown(ctx, p, g.opt.cutWithin+10*time.Second)
	problems := []string{}
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("established flow(s) still alive %v after revoking (tcp alive=%v udp alive=%v)", time.Since(began).Round(time.Second), p.tcpAlive(), p.udp != nil && p.udpAlive()))
	case cut.Sub(began) > g.opt.cutWithin:
		problems = append(problems, fmt.Sprintf("cut after %v, allowed %v", cut.Sub(began).Round(100*time.Millisecond), g.opt.cutWithin))
	}
	g.release(s, gr)
	again := !refused(ctx, gr.target, g.sink, g.token)
	if again {
		problems = append(problems, "a NEW connection with the revoked credential was accepted")
	}
	if g.udp != nil {
		if fresh, err := openUDP(ctx, gr.target, *g.udp, g.token); err == nil {
			fresh.close()
			problems = append(problems, "a NEW Mihomo XUDP carrier with the revoked credential was accepted")
		}
	}
	detail := "no flow was cut"
	if ok {
		detail = fmt.Sprintf("TCP%s cut %v after the revocation (sampling %v); a new connection with the same credential is refused", g.udpNote(), cut.Sub(began).Round(100*time.Millisecond), probeInterval)
	}
	g.rec.verdict("B05-revoke", "revocation closes established TCP/UDP flows within the stated time and refuses the credential afterwards", problems, detail)
}

func (g *grantRun) udpNote() string {
	if g.udp == nil {
		return " (UDP not tested: no --udp-sink)"
	}
	return "+UDP(XUDP/pinned Mihomo)"
}

func (g *grantRun) waitDown(ctx context.Context, p *probe, within time.Duration) (time.Time, bool) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if when, ok := p.allDown(); ok {
			return when, true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return time.Time{}, false
}

// idleExpiry is B05 (expiry): the grant lapses on the node's own clock when nothing refreshes it. The client
// makes no API call at all, so a vanished app cannot leave a usable credential behind.
func (g *grantRun) idleExpiry(ctx context.Context, s session, node apiclient.NodeChoice) {
	gr, err := g.mint(ctx, s, node.ID, 30*time.Second)
	if err != nil {
		g.rec.record("B05-idle-expiry", "a ready grant is issued", apiCode(err), "fail")
		return
	}
	defer g.release(s, gr)
	p, err := g.startProbe(ctx, gr, 0)
	if err != nil {
		g.rec.record("B05-idle-expiry", "flows are established", err.Error(), "fail")
		return
	}
	defer p.close()
	idle := time.Duration(s.state.SessionIdleTimeout) * time.Second
	limit := g.opt.idleWait
	if limit <= 0 {
		limit = idle + 90*time.Second
	}
	fmt.Printf("\nHolding flows with NO api traffic for up to %v (session idle timeout %v)...\n", limit, idle)
	began := time.Now()
	cut, ok := g.waitDown(ctx, p, limit)
	if !ok {
		g.rec.record("B05-idle-expiry", "the node ends a grant nothing refreshes", fmt.Sprintf("flows still alive after %v", limit.Round(time.Second)), "fail")
		return
	}
	problems := problemIf(!refused(ctx, gr.target, g.sink, g.token), "a new connection with the lapsed credential was accepted")
	g.rec.verdict("B05-idle-expiry", "a grant nobody refreshes ends on the node's own clock, established flows included", problems,
		fmt.Sprintf("flows cut %v after the last API call (session idle timeout %v)", cut.Sub(began).Round(time.Second), idle))
}

// hold is the operator-driven half of B05/B07: with flows open, YOU change something on the server side
// (end the plan, disable the node, lower the quota, stop the API, kill the agent) and the node must cut the flows.
func (g *grantRun) hold(ctx context.Context, s session, node apiclient.NodeChoice) {
	id := g.opt.holdCase
	gr, err := g.mint(ctx, s, node.ID, 30*time.Second)
	if err != nil {
		g.rec.record(id, "a ready grant is issued", apiCode(err), "fail")
		return
	}
	defer g.release(s, gr)
	chunk := int64(0)
	if g.opt.holdDrive {
		chunk = 1 << 20
	}
	p, err := g.startProbe(ctx, gr, chunk)
	if err != nil {
		g.rec.record(id, "flows are established", err.Error(), "fail")
		return
	}
	defer p.close()
	fmt.Printf("\nFLOWS OPEN (TCP%s). Within %v make the server-side change for %s now.\n\n", g.udpNote(), g.opt.holdFor, id)
	began := time.Now()
	cut, ok := g.waitDown(ctx, p, g.opt.holdFor)
	if !ok {
		g.rec.record(id, "the node closes the established flows after the server-side change",
			fmt.Sprintf("still alive after %v (tcp alive=%v, moved %d bytes)", g.opt.holdFor, p.tcpAlive(), p.moved()), "fail")
		return
	}
	problems := problemIf(!refused(ctx, gr.target, g.sink, g.token), "a NEW connection with the same credential was accepted")
	g.rec.verdict(id, "the node closes the established flows after the server-side change and refuses new ones", problems,
		fmt.Sprintf("flows cut %v after they were opened (moved %d bytes); the credential is refused afterwards", cut.Sub(began).Round(time.Second), p.moved()))
}

type raceOutcome struct {
	device   string
	granted  bool
	code     string
	moved    int64
	cutAfter time.Duration
}

// race is B06 on one machine (run it on Windows and macOS with the same --race-at for the cross-machine case):
// several devices ask for a grant at the same instant and drive the line until it cuts them. The node-held budget
// must never be oversold: the account ends at or below its quota whichever device wins.
func (g *grantRun) race(ctx context.Context, s session, node apiclient.NodeChoice) {
	start, err := g.api.Session(ctx, s.token)
	if err != nil {
		g.rec.record("B06-race", "session readable", apiCode(err), "fail")
		return
	}
	if g.opt.raceMaxByte > 0 && start.RemainingBytes > g.opt.raceMaxByte {
		g.rec.record("B06-race", "a small remaining quota, so the devices actually compete",
			fmt.Sprintf("remaining %d bytes exceeds --race-max-mib; lower the test account's quota so the sessions contend for the last bytes", start.RemainingBytes), "blocked")
		return
	}
	if !g.opt.raceAt.IsZero() {
		fmt.Printf("\nB06: waiting for %s ...\n", g.opt.raceAt.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(g.opt.raceAt)):
		}
	}
	// Enough to exhaust the quota twice over if nothing stopped the devices.
	ceiling := start.RemainingBytes*1000/max(1, node.RatePermille)*2 + (8 << 20)
	var driven atomic.Int64
	results := make([]raceOutcome, g.opt.raceN)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			role := fmt.Sprintf("race-%d", i)
			out := &results[i]
			out.device = role
			sess, err := g.login(ctx, role)
			if err != nil {
				out.code = "login:" + apiCode(err)
				return
			}
			defer func() { _ = g.api.Logout(context.Background(), sess.token) }()
			gr, err := g.mint(ctx, sess, node.ID, 45*time.Second)
			if err != nil {
				out.code = apiCode(err)
				return
			}
			defer g.release(sess, gr)
			out.granted = true
			f, err := openTCP(ctx, gr.target, g.sink, g.token)
			if err != nil {
				out.code = "connect:" + err.Error()
				return
			}
			defer f.close()
			began := time.Now()
			for driven.Load() < ceiling && ctx.Err() == nil {
				n, err := f.get(1<<20, 20*time.Second)
				out.moved += n
				driven.Add(n)
				if err != nil {
					out.cutAfter = time.Since(began)
					out.code = "cut"
					return
				}
			}
		}()
	}
	wg.Wait()
	last, err := g.unitsSettled(ctx, s, start.UsedUnits, 60*time.Second)
	if err != nil {
		g.rec.record("B06-race", "the ledger is readable", apiCode(err), "fail")
		return
	}
	var winners, refusedCount int
	var moved int64
	var summary []string
	for _, r := range results {
		if r.granted {
			winners++
		} else {
			refusedCount++
		}
		moved += r.moved
		summary = append(summary, fmt.Sprintf("%s granted=%v code=%s moved=%d", r.device, r.granted, r.code, r.moved))
	}
	problems := []string{}
	// The node stops forwarding at its budget, but bytes already in flight when it stops are real and are billed, so a
	// few hundred KiB per device over the line is not overselling; anything beyond that is.
	slack := int64(g.opt.raceN) * (256 << 10) * node.RatePermille
	if over := last.UsedUnits - last.TotalBytes*1000; over > slack {
		problems = append(problems, fmt.Sprintf("OVERSOLD: %d units used of %d (%d over, tolerance %d)", last.UsedUnits, last.TotalBytes*1000, over, slack))
	}
	if last.RemainingBytes < 0 {
		problems = append(problems, "negative remaining balance")
	}
	if winners == 0 {
		problems = append(problems, "no device was granted anything")
	}
	g.rec.verdict("B06-race", "competing devices never use more than the account's quota", problems,
		fmt.Sprintf("%d devices: %d granted, %d refused; carried %d bytes; used %d of %d units; remaining %d [%s]", g.opt.raceN, winners, refusedCount, moved, last.UsedUnits, last.TotalBytes*1000, last.RemainingBytes, strings.Join(summary, "; ")))
}

// tunBilling is B04 over the production path: Manager -> grant -> real TUN -> controlled transfer by an
// ordinary child process -> disconnect. The account ledger (read through the auxiliary session) must show the node's
// charge while the Core never reports a byte.
func (g *grantRun) tunBilling(ctx context.Context, node apiclient.NodeChoice) {
	h := g.h
	snap, ok := h.login(ctx)
	if !ok {
		return
	}
	defer func() { h.m.Logout(context.Background()) }()
	target := managed.NodeView{}
	for _, n := range snap.Nodes {
		if n.ID == node.ID {
			target = n
		}
	}
	if target.ID == "" {
		g.rec.record("B04-tun", "the Manager sees the node-governed line", "line not in the Manager's catalog", "fail")
		return
	}
	if target.MeteringSource != apiclient.MeteringNode {
		g.rec.record("B04-tun", "the Manager marks the line node-metered", "metering_source="+target.MeteringSource, "fail")
		return
	}
	up, down := min(g.cfg.upload, 8<<20), min(g.cfg.download, 16<<20)
	selected, err := h.selectNode(ctx, target)
	if err != nil || !selected.ConfigReady {
		g.rec.record("B04-tun-select", "a node-governed line can be selected without fetching a document", fmt.Sprintf("ready=%v error=%s", selected.ConfigReady, errCode(err)), "fail")
		return
	}
	aux, err := g.login(ctx, "aux-tun")
	if err != nil {
		g.rec.record("B04-tun", "a ledger-reading session", apiCode(err), "blocked")
		return
	}
	defer func() { _ = g.api.Logout(context.Background(), aux.token) }()
	before, err := g.api.Session(ctx, aux.token)
	if err != nil {
		g.rec.record("B04-tun", "ledger readable", apiCode(err), "fail")
		return
	}
	if need := (up + down) * 12 / 10 * target.RatePermille / 1000; before.RemainingBytes < need {
		g.rec.record("B04-tun", "enough quota", fmt.Sprintf("remaining %d, up to %d needed", before.RemainingBytes, need), "blocked")
		return
	}
	sinkBefore, err := h.stats(ctx)
	if err != nil {
		g.rec.record("B04-tun", "the sink answers while no tunnel exists", err.Error(), "blocked")
		return
	}
	began := time.Now()
	connected, err := h.m.Connect(ctx)
	if err != nil || connected.State != managed.StateConnected {
		verdict := "fail"
		switch errCode(err) {
		case "vpn_conflict", "no_network", "tun_unavailable":
			verdict = "blocked"
		}
		g.rec.record("B04-tun-connect", "Connect obtains a node-confirmed grant, brings the TUN up and passes a forwarding probe", fmt.Sprintf("state=%s error=%s", connected.State, errCode(err)), verdict)
		_ = h.m.Disconnect(ctx)
		return
	}
	problems := []string{}
	if connected.Metering == nil || connected.Metering.Source != apiclient.MeteringNode {
		problems = append(problems, "the connected snapshot does not say node_reported")
	}
	g.rec.verdict("B04-tun-connect", "Connect obtains a node-confirmed grant, brings the TUN up and passes a forwarding probe", problems,
		fmt.Sprintf("connected in %v via a grant, source=node_reported", time.Since(began).Round(time.Millisecond)))
	if len(problems) > 0 {
		_ = h.m.Disconnect(ctx)
		return
	}
	result, xerr := h.xfer(ctx, up, down)
	time.Sleep(h.cfg.settleWait)
	live := h.m.Snapshot()
	disconnected := h.m.Disconnect(ctx)
	if xerr != nil {
		g.rec.record("B04-tun", "the transfer completes through the tunnel", xerr.Error(), "fail")
		return
	}
	last, err := g.unitsSettled(ctx, aux, before.UsedUnits, 90*time.Second)
	if err != nil {
		g.rec.record("B04-tun", "ledger readable", apiCode(err), "fail")
		return
	}
	sinkAfter, err := h.stats(ctx)
	if err != nil {
		g.rec.record("B04-tun", "the sink answers", err.Error(), "blocked")
		return
	}
	body := (sinkAfter.UpReceived - sinkBefore.UpReceived) + (sinkAfter.DownSent - sinkBefore.DownSent)
	issues := nodeBillProblems(target.RatePermille, last.UsedUnits-before.UsedUnits, body, 2*h.cfg.background, last.TotalBytes, last.UsedUnits, last.RemainingBytes)
	if bodies, _ := h.lossy.trafficBodies(); len(bodies) != 0 {
		issues = append(issues, fmt.Sprintf("the Core sent %d /traffic report(s) on a node-metered line", len(bodies)))
	}
	if disconnected.State == managed.StateConnected {
		issues = append(issues, "still connected after Disconnect")
	}
	if live.Metering != nil && live.Metering.Source == apiclient.MeteringNode {
		if live.Metering.ConfirmedUploadBytes != 0 || live.Metering.ConfirmedDownloadBytes != 0 || live.Metering.Pending {
			issues = append(issues, "the snapshot shows client-side confirmation or a pending batch on a node-metered line")
		}
		if live.Metering.UploadBytes+live.Metering.DownloadBytes < up+down {
			issues = append(issues, fmt.Sprintf("the window's live usage %d is below the %d transferred", live.Metering.UploadBytes+live.Metering.DownloadBytes, up+down))
		}
	} else {
		issues = append(issues, "no node-reported metering view while connected")
	}
	g.rec.verdict("B04-tun", "over the real TUN the node bills the traffic, the Core reports nothing and the window shows local usage beside the server balance", issues,
		fmt.Sprintf("body up %d + down %d (sink saw %d), ledger +%d units at %d permille, remaining %d, no /traffic report", result.Upload, result.Download, body, last.UsedUnits-before.UsedUnits, target.RatePermille, last.RemainingBytes))
}
