// Package agent drives a Forwarder from the control plane's orders and reports what the
// forwarder actually carried (handoff_app.md 7.3, 7.5). It owns three promises:
//
//   - a grant is only ever served between "install" and its absolute deadline;
//   - every byte is reported exactly once, from cumulative counters it measured itself,
//     with at most one unacknowledged report per grant retried verbatim;
//   - a restart never loses the previous epoch's checkpoint and never reuses its
//     sequence numbers: the old epoch is closed with a final report first.
package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"asterlink/nodeagent/control"
	"asterlink/nodeagent/forwarder"
)

type Options struct {
	API       control.API
	Fwd       forwarder.Forwarder
	StatePath string
	Version   string
	Now       func() time.Time
	Logf      func(format string, args ...any)
}

type Agent struct {
	o     Options
	epoch int64

	mu sync.Mutex
	st fileState
}

// New loads the persisted state and opens a new epoch. The epoch is at least the
// previous one plus one and at least the wall clock in seconds, so it still moves
// forward if the state file is lost.
func New(o Options) (*Agent, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	st, err := loadState(o.StatePath)
	if err != nil {
		return nil, err
	}
	epoch := max(st.Epoch+1, o.Now().Unix())
	a := &Agent{o: o, epoch: epoch, st: st}
	a.st.Epoch = epoch
	// Anything from an earlier epoch has no live flows any more. Its last checkpoint is
	// reported as the final word of that epoch before anything else can happen to it.
	for id, gs := range a.st.Grants {
		switch {
		case gs.Epoch < epoch && gs.Done && gs.Announced:
			delete(a.st.Grants, id) // tombstones only protect the epoch they were made in
		case gs.Epoch < epoch && !gs.Done:
			gs.Closing, gs.Dormant = true, true
		}
	}
	if err = a.saveLocked(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Agent) Epoch() int64 { return a.epoch }

func (a *Agent) saveLocked() error { return saveState(a.o.StatePath, a.st) }

func sortedIDs(m map[string]*grantState) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SyncOnce exchanges installed/removed lists for the control plane's orders.
func (a *Agent) SyncOnce(ctx context.Context) error {
	a.mu.Lock()
	usage := a.o.Fwd.Usage()
	installed := make([]string, 0, len(usage))
	for id := range usage {
		installed = append(installed, id)
	}
	sort.Strings(installed)
	var removed []string
	for _, id := range sortedIDs(a.st.Grants) {
		if gs := a.st.Grants[id]; gs.Done && !gs.Announced {
			removed = append(removed, id)
		}
	}
	a.mu.Unlock()

	resp, err := a.o.API.Sync(ctx, control.SyncRequest{Epoch: a.epoch, AgentVersion: a.o.Version, Protocols: a.o.Fwd.Protocols(), Installed: installed, Removed: removed})
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// The control plane has now seen these removals. If it did not accept one it will
	// order the grant revoked again, and the order is answered afresh.
	for _, id := range removed {
		if gs := a.st.Grants[id]; gs != nil && gs.Done {
			gs.Announced = true
		}
	}
	for _, o := range resp.Grants {
		switch o.Action {
		case "install":
			a.installLocked(o)
		case "keep":
			if o.ExpiresAt != nil {
				a.o.Fwd.Update(o.GrantID, *o.ExpiresAt, o.EpochBudgetBytes)
			}
		case "revoke":
			a.revokeLocked(o.GrantID)
		}
	}
	a.expireLocked()
	return a.saveLocked()
}

func (a *Agent) installLocked(o control.Order) {
	gs := a.st.Grants[o.GrantID]
	if gs != nil && (gs.Closing || gs.Done) {
		// This epoch already sent (or owes) the final word for the grant; counters of a
		// closed grant can never resume. It stays unserved until the control plane
		// retires it.
		a.o.Logf("refusing to re-install closed grant %s", o.GrantID)
		return
	}
	if o.ExpiresAt == nil {
		return
	}
	err := a.o.Fwd.Install(forwarder.Grant{ID: o.GrantID, Protocol: o.Protocol, Credential: o.Credential, Deadline: *o.ExpiresAt, EpochBudget: o.EpochBudgetBytes})
	if err != nil {
		a.o.Logf("install %s failed: %v", o.GrantID, err)
		return
	}
	if gs == nil {
		gs = &grantState{Epoch: a.epoch}
		a.st.Grants[o.GrantID] = gs
	}
	gs.Dormant = false
}

// revokeLocked ends a grant for good: flows are killed, the forwarder's final counters
// are folded in, and the next report is the final one.
func (a *Agent) revokeLocked(id string) {
	final, wasInstalled := a.o.Fwd.Remove(id)
	gs := a.st.Grants[id]
	if gs == nil {
		// Never served here: nothing to report, only the removal to announce.
		a.st.Grants[id] = &grantState{Epoch: a.epoch, Done: true}
		return
	}
	if gs.Done {
		gs.Announced = false // the control plane asks again: it did not accept the first announcement
		return
	}
	if wasInstalled {
		gs.CarryUp += final.Upload
		gs.CarryDown += final.Download
		gs.CurUp, gs.CurDown = gs.CarryUp, gs.CarryDown
	}
	gs.Closing, gs.Dormant = true, true
}

// expireLocked suspends grants whose absolute deadline passed. Counters are carried
// (not finalised) so a later renewal continues the same cumulative series.
func (a *Agent) expireLocked() {
	for _, id := range a.o.Fwd.Expired(a.o.Now()) {
		final, ok := a.o.Fwd.Remove(id)
		gs := a.st.Grants[id]
		if !ok || gs == nil {
			continue
		}
		gs.CarryUp += final.Upload
		gs.CarryDown += final.Download
		gs.CurUp, gs.CurDown = gs.CarryUp, gs.CarryDown
		gs.Dormant = true
	}
}

// Tick samples counters, enforces local expiry and persists the checkpoint. It makes no
// network call, so it keeps working while the control plane is unreachable.
func (a *Agent) Tick() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked()
	a.sampleLocked()
	return a.saveLocked()
}

func (a *Agent) sampleLocked() {
	usage := a.o.Fwd.Usage()
	for id, gs := range a.st.Grants {
		if gs.Dormant || gs.Done {
			continue
		}
		if live, ok := usage[id]; ok {
			gs.CurUp, gs.CurDown = gs.CarryUp+live.Upload, gs.CarryDown+live.Download
		}
	}
}

type outgoing struct {
	id string
	gs *grantState
	r  report
	ep int64
}

// ReportOnce sends at most one report per grant.
func (a *Agent) ReportOnce(ctx context.Context) error {
	a.mu.Lock()
	a.sampleLocked()
	for _, id := range sortedIDs(a.st.Grants) {
		gs := a.st.Grants[id]
		if gs.Pending != nil || gs.Done {
			continue
		}
		switch {
		case gs.Closing && gs.Seq == 0 && gs.CurUp == 0 && gs.CurDown == 0:
			gs.Done = true // never carried a byte: nothing for the ledger to learn
		case gs.Closing:
			gs.Pending = &report{Seq: gs.Seq + 1, Up: gs.CurUp, Down: gs.CurDown, Final: true}
		case gs.CurUp != gs.AckedUp || gs.CurDown != gs.AckedDown:
			gs.Pending = &report{Seq: gs.Seq + 1, Up: gs.CurUp, Down: gs.CurDown}
		}
	}
	// Persist before sending: a crash after the send must still replay the same body.
	if err := a.saveLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	var todo []outgoing
	for _, id := range sortedIDs(a.st.Grants) {
		if gs := a.st.Grants[id]; gs.Pending != nil {
			todo = append(todo, outgoing{id: id, gs: gs, r: *gs.Pending, ep: gs.Epoch})
		}
	}
	a.mu.Unlock()

	var firstErr error
	for _, o := range todo {
		_, err := a.o.API.Usage(ctx, control.UsageRequest{GrantID: o.id, Epoch: o.ep, Sequence: o.r.Seq, UploadBytes: o.r.Up, DownloadBytes: o.r.Down, Final: o.r.Final})
		a.mu.Lock()
		if drop := a.settleLocked(o, err); drop != nil && firstErr == nil {
			firstErr = drop
		}
		a.mu.Unlock()
		if ctx.Err() != nil {
			break
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.saveLocked(); err != nil {
		return err
	}
	return firstErr
}

// settleLocked applies the control plane's verdict on one report. It returns an error
// only for outcomes worth surfacing; every branch keeps the series consistent.
func (a *Agent) settleLocked(o outgoing, err error) error {
	gs := o.gs
	if err == nil {
		a.ackLocked(o)
		return nil
	}
	var ce *control.Error
	if !errors.As(err, &ce) || ce.Transient {
		return err // network / 5xx / 429: retry the identical report later
	}
	switch ce.Code {
	case "usage_closed":
		a.ackLocked(o) // the final word is already on record
	case "grant_not_found":
		a.o.Logf("grant %s is unknown to the control plane; dropping its counters", o.id)
		delete(a.st.Grants, o.id)
	case "usage_epoch_unknown":
		return err // needs a sync first
	case "usage_conflict", "invalid_usage", "usage_overflow":
		// The control plane holds different data under this sequence or cannot take the
		// value. Retrying cannot help; keep the evidence in the log and move on.
		a.o.Logf("report %s seq %d rejected (%s): up=%d down=%d final=%v", o.id, o.r.Seq, ce.Code, o.r.Up, o.r.Down, o.r.Final)
		a.ackLocked(o)
		return fmt.Errorf("report rejected: %s", ce.Code)
	case "agent_epoch_stale":
		a.o.Logf("a newer agent epoch exists; this process must stop reporting grant %s", o.id)
		gs.Pending = nil
		return err
	default:
		return err
	}
	return nil
}

func (a *Agent) ackLocked(o outgoing) {
	gs := o.gs
	if gs.Pending == nil || gs.Pending.Seq != o.r.Seq {
		return
	}
	gs.Seq, gs.AckedUp, gs.AckedDown, gs.Pending = o.r.Seq, o.r.Up, o.r.Down, nil
	if o.r.Final {
		gs.Done = true
	}
}

// Shutdown ends every grant cleanly: flows are closed and each grant's final counters
// are reported, within ctx.
func (a *Agent) Shutdown(ctx context.Context) {
	a.mu.Lock()
	for id := range a.o.Fwd.Usage() {
		a.revokeLocked(id)
	}
	_ = a.saveLocked()
	a.mu.Unlock()
	for range 3 {
		if ctx.Err() != nil || a.ReportOnce(ctx) == nil {
			break
		}
	}
	_ = a.o.Fwd.Close()
}

// Run blocks until ctx ends, then shuts down gracefully (bounded to 10 seconds).
func (a *Agent) Run(ctx context.Context, syncEvery, reportEvery time.Duration) {
	sync := time.NewTicker(syncEvery)
	report := time.NewTicker(reportEvery)
	tick := time.NewTicker(time.Second)
	defer sync.Stop()
	defer report.Stop()
	defer tick.Stop()
	// Close out previous epochs first, then ask for orders.
	if err := a.ReportOnce(ctx); err != nil {
		a.o.Logf("recovery report: %v", err)
	}
	if err := a.SyncOnce(ctx); err != nil {
		a.o.Logf("sync: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			a.Shutdown(stop)
			cancel()
			return
		case <-sync.C:
			if err := a.SyncOnce(ctx); err != nil && ctx.Err() == nil {
				a.o.Logf("sync: %v", err)
			}
		case <-report.C:
			if err := a.ReportOnce(ctx); err != nil && ctx.Err() == nil {
				a.o.Logf("usage: %v", err)
			}
		case <-tick.C:
			if err := a.Tick(); err != nil {
				a.o.Logf("checkpoint: %v", err)
			}
		}
	}
}
