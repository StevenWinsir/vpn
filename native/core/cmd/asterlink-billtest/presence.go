package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

// Presence acceptance (handoff_node_use_history_and_load_balancing_c.md, round 5): online
// leases through the production Manager, against the real API. Several devices of the test
// account sign in at once, each with its own device id:
//
//	P01 device limit  the first <limit> devices connect; the next is refused with
//	                  device_limit and the Core's own text; when one disconnects it gets in.
//	P02 group full    on a line whose capacity is below the limit (--full-line), devices are
//	                  admitted until the line answers group_full.
//	P03 reassignment  one device connects; the operator (or a harness) withdraws its server
//	                  in /admin/nodes; the Core must stop, settle and reconnect by itself.
//
// Only the control plane is under test, so by default each device runs a control-plane
// engine (no TUN, no routes, no traffic; zero-byte batches still prove the ledger). --tun
// gives device 1 the production engine instead (privileged), for a reassignment with real
// traffic before and after.

type presenceOptions struct {
	limit           int
	line, fullLine  string
	reassignWait    time.Duration
	reconnectWithin time.Duration
	tun             bool
}

// controlEngine is the no-data-path engine of the presence cases.
type controlEngine struct{}

func (controlEngine) Preflight(context.Context) (network.Interface, error) {
	return network.Interface{}, nil
}
func (controlEngine) Start(context.Context, managed.Profile) error { return nil }
func (controlEngine) Verify(context.Context) error                 { return nil }
func (controlEngine) Totals() (int64, int64, error)                { return 0, 0, nil }
func (controlEngine) Stop() error                                  { return nil }

// countingEngine counts tunnel starts, so a reconnect is seen even when it completes
// between two looks at the snapshot.
type countingEngine struct {
	managed.Engine
	starts atomic.Int32
}

func (e *countingEngine) Start(ctx context.Context, p managed.Profile) error {
	e.starts.Add(1)
	return e.Engine.Start(ctx, p)
}

type presenceDevice struct {
	name   string
	m      *managed.Manager
	engine *countingEngine
}

type presenceRun struct {
	cfg       config
	opts      presenceOptions
	rec       *recorder
	devices   []*presenceDevice
	newEngine func(i int) managed.Engine
	wrap      func(*apiclient.Client)
	// announce is told when P03 waits for the server to be withdrawn.
	announce func(line string)
}

func (p *presenceRun) device(ctx context.Context, i int) (*presenceDevice, error) {
	d := &presenceDevice{name: fmt.Sprintf("device %d", i+1), engine: &countingEngine{Engine: p.newEngine(i)}}
	d.m = managed.New(managed.Options{
		CoreVersion: "billtest", Engine: d.engine, Egress: &network.Egress{},
		NewClient: func(base *url.URL) *apiclient.Client {
			c := apiclient.New(base)
			if p.wrap != nil {
				p.wrap(c)
			}
			return c
		},
	})
	_, err := d.m.Login(ctx, managed.LoginParams{Email: p.cfg.email, Password: p.cfg.password, APIBase: p.cfg.apiBase, Channel: p.cfg.channel,
		DeviceID: deviceFor(p.cfg.deviceID, fmt.Sprintf("presence-%d", i)), AppVersion: "billtest"})
	if err != nil {
		return nil, err
	}
	p.devices = append(p.devices, d)
	return d, nil
}

func (p *presenceRun) closeAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, d := range p.devices {
		d.m.Logout(ctx)
	}
}

// pick finds the line named by want (id or exact name), or the first client-reported one.
func pick(nodes []managed.NodeView, want string) (managed.NodeView, bool) {
	for _, n := range nodes {
		if want != "" && (n.ID == want || n.Name == want) || want == "" && n.MeteringSource != apiclient.MeteringNode {
			return n, true
		}
	}
	return managed.NodeView{}, false
}

func (p *presenceRun) selectLine(ctx context.Context, d *presenceDevice, want string) (managed.NodeView, error) {
	snap, err := d.m.RefreshCatalog(ctx)
	if err != nil {
		return managed.NodeView{}, err
	}
	line, ok := pick(snap.Nodes, want)
	if !ok {
		return line, &apiclient.Error{Code: "node_unavailable"}
	}
	_, err = d.m.SelectNode(ctx, line.ID, snap.CatalogRevision)
	return line, err
}

func lastCode(s managed.Snapshot) string {
	if s.LastError == nil {
		return ""
	}
	return s.LastError.Code
}

func (p *presenceRun) disconnectAll(ctx context.Context) {
	for _, d := range p.devices {
		d.m.Disconnect(ctx)
	}
}

func (p *presenceRun) run(ctx context.Context) {
	defer p.closeAll()
	limit := p.opts.limit
	for i := 0; i <= limit; i++ {
		if _, err := p.device(ctx, i); err != nil {
			p.rec.record("P-LOGIN", fmt.Sprintf("%d devices of the test account sign in", limit+1), fmt.Sprintf("device %d: %s", i+1, errCode(err)), "blocked")
			return
		}
	}
	p.rec.record("P-LOGIN", fmt.Sprintf("%d devices of the test account sign in", limit+1), fmt.Sprintf("%d signed in; signing in took no slot", limit+1), "pass")
	p.deviceLimit(ctx)
	p.disconnectAll(ctx)
	p.groupFull(ctx)
	p.disconnectAll(ctx)
	p.reassign(ctx)
}

// P01: exactly <limit> devices are online at once; the next is refused until one leaves.
func (p *presenceRun) deviceLimit(ctx context.Context) {
	limit := p.opts.limit
	for i := 0; i <= limit; i++ {
		if _, err := p.selectLine(ctx, p.devices[i], p.opts.line); err != nil {
			p.rec.record("P01-select", "every device selects the line without taking a slot", fmt.Sprintf("%s: %s", p.devices[i].name, errCode(err)), "fail")
			return
		}
	}
	problems := []string{}
	for i := 0; i < limit; i++ {
		s, err := p.devices[i].m.Connect(ctx)
		if err != nil || s.State != managed.StateConnected {
			problems = append(problems, fmt.Sprintf("%s: state=%s error=%s", p.devices[i].name, s.State, errCode(err)))
		}
	}
	p.rec.verdict("P01-admit", fmt.Sprintf("the first %d devices connect", limit), problems, fmt.Sprintf("%d devices online", limit))
	if len(problems) > 0 {
		return
	}
	extra := p.devices[limit]
	s, err := extra.m.Connect(ctx)
	want := apiclient.Message("device_limit")
	p.rec.verdict("P01-refuse", "the next device is refused with device_limit and the Core's text, and nothing starts", problemIf(
		errCode(err) != "device_limit" || s.State == managed.StateConnected || s.LastError == nil || s.LastError.Message != want,
		fmt.Sprintf("state=%s error=%s message=%q", s.State, errCode(err), func() string {
			if s.LastError == nil {
				return ""
			}
			return s.LastError.Message
		}())), fmt.Sprintf("refused: %s %q", errCode(err), want))
	p.devices[0].m.Disconnect(ctx)
	began := time.Now()
	s, err = extra.m.Connect(ctx)
	p.rec.verdict("P01-release", "once a device disconnects, the refused one connects at once", problemIf(err != nil || s.State != managed.StateConnected,
		fmt.Sprintf("state=%s error=%s", s.State, errCode(err))), fmt.Sprintf("connected %v after the release", time.Since(began).Round(time.Millisecond)))
}

// P02: a line with fewer slots than the device limit answers group_full.
func (p *presenceRun) groupFull(ctx context.Context) {
	if p.opts.fullLine == "" {
		p.rec.record("P02-group-full", "a full line refuses with group_full", "pass --full-line with a line whose total capacity is below --device-limit", "blocked")
		return
	}
	admitted := 0
	for i := 0; i < p.opts.limit; i++ {
		d := p.devices[i]
		if _, err := p.selectLine(ctx, d, p.opts.fullLine); err != nil {
			p.rec.record("P02-group-full", "select the full line", fmt.Sprintf("%s: %s", d.name, errCode(err)), "fail")
			return
		}
		s, err := d.m.Connect(ctx)
		if err == nil && s.State == managed.StateConnected {
			admitted++
			continue
		}
		want := apiclient.Message("group_full")
		p.rec.verdict("P02-group-full", "the line admits up to its capacity, then refuses with group_full and the Core's text", problemIf(
			errCode(err) != "group_full" || s.LastError == nil || s.LastError.Message != want || admitted == 0,
			fmt.Sprintf("after %d admitted: state=%s error=%s", admitted, s.State, errCode(err))), fmt.Sprintf("capacity %d, then %s %q", admitted, errCode(err), want))
		return
	}
	p.rec.record("P02-group-full", "the line admits up to its capacity, then refuses with group_full", fmt.Sprintf("all %d devices admitted: the line's capacity is not below the device limit", admitted), "blocked")
}

// P03: the operator withdraws the server; the Core stops, settles and reconnects by itself.
func (p *presenceRun) reassign(ctx context.Context) {
	if p.opts.reassignWait <= 0 {
		p.rec.record("P03-reassign", "withdrawing the server moves the device to another one by itself", "pass --reassign-wait-seconds and withdraw the server in /admin/nodes when asked", "blocked")
		return
	}
	d := p.devices[0]
	line, err := p.selectLine(ctx, d, p.opts.line)
	if err != nil {
		p.rec.record("P03-reassign", "select the line", errCode(err), "fail")
		return
	}
	connected, err := d.m.Connect(ctx)
	if err != nil || connected.State != managed.StateConnected {
		p.rec.record("P03-reassign", "connect before the withdrawal", fmt.Sprintf("state=%s error=%s", connected.State, errCode(err)), "fail")
		return
	}
	before, starts := connected.Metering.LastSequence, d.engine.starts.Load()
	if p.announce != nil {
		p.announce(line.ID)
	}
	fmt.Printf("\nPRESENCE-REASSIGN-READY line=%s. Within %v disable the server this device is on (group %q) in /admin/nodes; keep another server of the group enabled.\n\n", shortHash(line.ID), p.opts.reassignWait, line.Name)
	deadline := time.Now().Add(p.opts.reassignWait)
	var stoppedAt, reconnectedAt time.Time
	message := ""
	for time.Now().Before(deadline) && ctx.Err() == nil {
		s := d.m.Snapshot()
		if s.LastError != nil && s.LastError.Code == "connection_reassigned" && message == "" {
			message = s.LastError.Message
			if stoppedAt.IsZero() {
				stoppedAt = time.Now()
			}
		}
		moved := d.engine.starts.Load() > starts
		if moved && stoppedAt.IsZero() {
			stoppedAt = time.Now() // the whole stop/reconnect happened between two looks
		}
		if moved && s.State == managed.StateConnected {
			reconnectedAt = time.Now()
			break
		}
		if s.State != managed.StateConnected && s.State != managed.StateStopping && s.State != managed.StatePreparing && s.State != managed.StateConnecting && stoppedAt.IsZero() {
			stoppedAt = time.Now()
		}
		if !stoppedAt.IsZero() && time.Since(stoppedAt) > p.opts.reconnectWithin {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stoppedAt.IsZero() {
		p.rec.record("P03-reassign", "the device notices the withdrawal", fmt.Sprintf("still on the same segment after %v", p.opts.reassignWait), "blocked")
		return
	}
	after := d.m.Snapshot()
	off := d.m.Disconnect(ctx)
	problems := []string{}
	if reconnectedAt.IsZero() {
		problems = append(problems, fmt.Sprintf("not reconnected within %v: state=%s error=%s", p.opts.reconnectWithin, after.State, lastCode(after)))
	}
	if n := d.engine.starts.Load() - starts; n != 1 {
		problems = append(problems, fmt.Sprintf("%d tunnel starts after the withdrawal, want exactly 1", n))
	}
	if message != "" && message != apiclient.Message("connection_reassigned") {
		problems = append(problems, fmt.Sprintf("message %q", message))
	}
	if off.Metering == nil || off.Metering.Pending || off.Metering.LastSequence <= before || lastCode(off) != "" {
		problems = append(problems, fmt.Sprintf("ledger after the move: pending=%v sequence %d -> %d error=%s", off.Metering != nil && off.Metering.Pending, before, func() int64 {
			if off.Metering == nil {
				return -1
			}
			return off.Metering.LastSequence
		}(), lastCode(off)))
	}
	seen := "not observed (the reconnect finished between two looks)"
	if message != "" {
		seen = fmt.Sprintf("%q", message)
	}
	p.rec.verdict("P03-reassign", "the Core stops, settles and reconnects once to another server of the same line, with a continuous ledger", problems,
		fmt.Sprintf("reconnected %v after the stop; message %s; ledger sequence %d -> %d, nothing pending", reconnectedAt.Sub(stoppedAt).Round(time.Millisecond), seen, before, off.Metering.LastSequence))
}

// presenceSummary lists the cases for the report header.
func presenceSummary(o presenceOptions) string {
	parts := []string{fmt.Sprintf("limit=%d", o.limit)}
	if o.fullLine != "" {
		parts = append(parts, "full-line=set")
	}
	if o.reassignWait > 0 {
		parts = append(parts, fmt.Sprintf("reassign-wait=%v", o.reassignWait))
	}
	if o.tun {
		parts = append(parts, "device1=production engine")
	}
	return strings.Join(parts, " ")
}
