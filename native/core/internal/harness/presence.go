//go:build asterlink_harness

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/managed"
)

// Online presence (handoff_node_use_history_and_load_balancing_c.md round 6) against the billing
// world's presence calls (backend presence_world_test.go): the device limit when connecting, the
// automatic reconnect after the administrator withdraws the server the device is on, and the
// release of the online slot on disconnect and on the process-exit path (Shutdown, which is what
// the iOS extension's MobileStop, the macOS Helper's worker and the Windows service run).

type presenceLease struct {
	Lease    string `json:"lease"`
	Device   string `json:"device_id"`
	Holder   bool   `json:"holder"`
	Server   string `json:"server"`
	Label    string `json:"label"`
	Line     string `json:"line"`
	Reason   string `json:"end_reason"`
	EndedAgo int64  `json:"ended_ms_ago"`
}

type presenceState struct {
	Live  []presenceLease `json:"live"`
	Ended []presenceLease `json:"ended"`
}

// own is this device's live lease, if any.
func (p presenceState) own() *presenceLease {
	for i := range p.Live {
		if p.Live[i].Device == deviceID {
			return &p.Live[i]
		}
	}
	return nil
}

func (p presenceState) lastEnded() *presenceLease {
	for i := range p.Ended {
		if p.Ended[i].Device == deviceID {
			return &p.Ended[i]
		}
	}
	return nil
}

func (r *runner) presence(ctx context.Context) (presenceState, error) {
	var out presenceState
	raw, err := r.world.post(ctx, "presence", map[string]string{"user": "presence"})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

// waitPresence polls the server's view until ok holds; it returns the wait or -1.
func (r *runner) waitPresence(ctx context.Context, limit time.Duration, ok func(presenceState) bool) (time.Duration, presenceState) {
	start := time.Now()
	var last presenceState
	for time.Since(start) < limit && ctx.Err() == nil {
		if p, err := r.presence(ctx); err == nil {
			last = p
			if ok(p) {
				return time.Since(start), p
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return -1, last
}

func busy(s managed.Snapshot) bool {
	switch s.State {
	case managed.StatePreparing, managed.StateConnecting, managed.StateConnected, managed.StateStopping:
		return true
	}
	return false
}

func (r *runner) presenceCases(ctx context.Context) {
	if r.cfg.World.Nodes["pair"] == "" {
		r.record("P", "BLOCKED", "this billing world has no two-server line (backend older than node groups round 6)")
		return
	}
	defer func() {
		_, _ = r.world.post(ctx, "vacate", map[string]any{"user": "presence"})
		_, _ = r.world.post(ctx, "restore", map[string]any{})
	}()
	if !r.signIn(ctx, "presence") {
		return
	}

	// ---- P01: two other devices of the account are online, so this one is refused at connect.
	if _, err := r.world.post(ctx, "occupy", map[string]any{"user": "presence", "count": 2}); err != nil {
		r.record("P01-device-limit", "FAIL", "occupy: "+err.Error())
		return
	}
	s := r.selectNode(ctx, "pair")
	if !s.ConfigReady {
		r.record("P01-device-limit", "FAIL", fmt.Sprintf("the pair line is not ready after selection: %s %s", s.State, r.lastErrorCode(s)))
		return
	}
	s, err := r.connect(ctx)
	p, _ := r.presence(ctx)
	want := apiclient.Message("device_limit")
	var problems []string
	if err == nil || s.State == managed.StateConnected {
		problems = append(problems, fmt.Sprintf("connected (%s) with two other devices online", s.State))
	}
	if s.LastError == nil || s.LastError.Code != "device_limit" || s.LastError.Message != want {
		problems = append(problems, fmt.Sprintf("last_error %+v, want device_limit %q", s.LastError, want))
	}
	if p.own() != nil || len(p.Live) != 2 {
		problems = append(problems, fmt.Sprintf("server shows %d live leases (own %v)", len(p.Live), p.own() != nil))
	}
	r.verdict("P01-device-limit", problems, fmt.Sprintf("2 of 2 slots held by other devices: Connect refused with device_limit, shown as %q; the server still counts 2 online, none of them this device", want))

	// ---- P01: one of them disconnects; this device is admitted at once.
	if _, err := r.world.post(ctx, "vacate", map[string]any{"user": "presence", "count": 1}); err != nil {
		r.record("P01-after-release", "FAIL", "vacate: "+err.Error())
		return
	}
	began := time.Now()
	s, err = r.connect(ctx)
	took := time.Since(began)
	p, _ = r.presence(ctx)
	problems = nil
	if err != nil || s.State != managed.StateConnected {
		problems = append(problems, fmt.Sprintf("not connected: %s %v %s", s.State, err, r.lastErrorCode(s)))
	}
	if p.own() == nil || p.own().Line != "pair" || len(p.Live) != 2 {
		problems = append(problems, fmt.Sprintf("server: %d live, own %+v", len(p.Live), p.own()))
	}
	r.verdict("P01-after-release", problems, fmt.Sprintf("after another device disconnected this one connected in %d ms (gates, admission, first report, engine, probe); the server counts 2 of 2, this device on %s", took.Milliseconds(), labelOf(p.own())))
	if len(problems) > 0 {
		return
	}
	_, _ = r.world.post(ctx, "vacate", map[string]any{"user": "presence"})

	// ---- P03: the administrator disables the server this device is on: one automatic reconnect
	// to the other server of the line, and the snapshot never shows an idle tunnel meanwhile
	// (the apps would read that as "ended by itself" and tear their tunnel down).
	before := p.own()
	if got, err := r.xfer.download(ctx, 1<<20); err != nil || got != 1<<20 {
		r.record("P03-reassign", "FAIL", fmt.Sprintf("download before: %d %v", got, err))
		return
	}
	var idle []string
	sawMessage := false
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
			snap := r.m.Snapshot()
			if !busy(snap) {
				idle = append(idle, string(snap.State))
			}
			if snap.LastError != nil && snap.LastError.Code == "connection_reassigned" && snap.LastError.Message == apiclient.Message("connection_reassigned") {
				sawMessage = true
			}
		}
	}()
	withdrawn := time.Now()
	if _, err := r.world.post(ctx, "withdraw", map[string]any{"user": "presence"}); err != nil {
		close(stop)
		<-sampled
		r.record("P03-reassign", "FAIL", "withdraw: "+err.Error())
		return
	}
	moved, after := r.waitPresence(ctx, 30*time.Second, func(p presenceState) bool {
		own := p.own()
		return own != nil && own.Server != before.Server
	})
	r.waitFor(ctx, 15*time.Second, func(s managed.Snapshot) bool { return s.State == managed.StateConnected })
	reconnected := time.Since(withdrawn)
	close(stop)
	<-sampled
	got, downErr := r.xfer.download(ctx, 1<<20)
	s = r.m.Snapshot()
	problems = nil
	if moved < 0 {
		problems = append(problems, fmt.Sprintf("this device is not on another server: %+v", after.own()))
	}
	if s.State != managed.StateConnected {
		problems = append(problems, fmt.Sprintf("not connected again: %s %s", s.State, r.lastErrorCode(s)))
	}
	if len(idle) > 0 {
		problems = append(problems, fmt.Sprintf("the snapshot showed an idle tunnel %d times during the reconnect (%s)", len(idle), idle[0]))
	}
	if downErr != nil || got != 1<<20 {
		problems = append(problems, fmt.Sprintf("download after: %d %v", got, downErr))
	}
	var endedOld string
	for _, e := range after.Ended {
		if e.Lease == before.Lease {
			endedOld = e.Reason
		}
	}
	if endedOld != "node_unavailable" {
		problems = append(problems, fmt.Sprintf("the withdrawn lease ended %q", endedOld))
	}
	r.verdict("P03-reassign", problems, fmt.Sprintf("server %s disabled: this device was on %s %d ms later (connected again after %d ms, the reassignment message seen: %v), never idle in between (sampled every 10 ms), "+
		"the old lease ended node_unavailable, 1 MiB downloads before and after", before.Label, labelOf(after.own()), moved.Milliseconds(), reconnected.Milliseconds(), sawMessage))

	// The ledger is continuous across the move: the server's counters equal the Core's confirmed ones.
	settled := r.disconnectAndSettle(ctx)
	l, lerr := r.world.ledger(ctx, "presence")
	problems = nil
	if lerr != nil || settled.Metering == nil || settled.Metering.Pending || l.UploadBytes != settled.Metering.ConfirmedUploadBytes || l.DownloadBytes != settled.Metering.ConfirmedDownloadBytes {
		problems = append(problems, fmt.Sprintf("server %+v (%v) vs Core %+v", l, lerr, settled.Metering))
	}
	if l.DownloadBytes < 2<<20 {
		problems = append(problems, fmt.Sprintf("server counted %d download bytes for 2 MiB through two servers", l.DownloadBytes))
	}
	r.verdict("P03-ledger", problems, fmt.Sprintf("after the move and a disconnect: server up %d / down %d = the Core's confirmed counters, nothing pending", l.UploadBytes, l.DownloadBytes))

	// ---- P04: Disconnect releases the slot at once (not after the lease runs out).
	r.releaseCase(ctx, "P04-disconnect-release", func() { r.m.Disconnect(ctx) })

	// ---- P05: the process-exit path (Shutdown) releases it too.
	r.releaseCase(ctx, "P05-shutdown-release", func() {
		stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		r.m.Shutdown(stopCtx)
		cancel()
		r.m = nil // the next sign-in builds a new Manager, as a restarted process does
	})
}

// releaseCase connects to the pair line, runs end, and expects the server to show the device
// offline with the reason "released", well inside one lease.
func (r *runner) releaseCase(ctx context.Context, id string, end func()) {
	if r.m == nil && !r.signIn(ctx, "presence") {
		return
	}
	if s := r.m.Snapshot(); s.SelectedNodeID != r.cfg.World.Nodes["pair"] {
		r.selectNode(ctx, "pair")
	}
	if s, err := r.connect(ctx); err != nil || s.State != managed.StateConnected {
		r.record(id, "FAIL", fmt.Sprintf("connect: %s %v %s", s.State, err, r.lastErrorCode(s)))
		return
	}
	if _, p := r.waitPresence(ctx, 5*time.Second, func(p presenceState) bool { return p.own() != nil }); p.own() == nil {
		r.record(id, "FAIL", "the server never showed this device online")
		return
	}
	began := time.Now()
	end()
	returned := time.Since(began)
	offline, p := r.waitPresence(ctx, 10*time.Second, func(p presenceState) bool { return p.own() == nil })
	lease := time.Duration(r.cfg.World.LeaseSecs) * time.Second
	var problems []string
	if offline < 0 {
		problems = append(problems, "still online 10 s later")
	}
	last := p.lastEnded()
	if last == nil || last.Reason != "released" {
		problems = append(problems, fmt.Sprintf("ended as %+v, want released", last))
	}
	if lease > 0 && returned+offline >= lease {
		problems = append(problems, fmt.Sprintf("offline only after %d ms, not before the %s lease ran out", (returned+offline).Milliseconds(), lease))
	}
	r.verdict(id, problems, fmt.Sprintf("the call returned in %d ms and the server showed the device offline (released) %d ms after it, inside one lease (%s)", returned.Milliseconds(), offline.Milliseconds(), lease))
}

func labelOf(l *presenceLease) string {
	if l == nil {
		return "nothing"
	}
	return l.Label
}
