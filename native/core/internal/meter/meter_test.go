package meter

import (
	"context"
	"errors"
	"testing"
	"time"

	"asterlink/core/internal/apiclient"
)

// ledger is a faithful miniature of the server's nativeTraffic handler: strict
// sequence, cumulative counters, integer units, replay of an identical batch.
type ledger struct {
	session  apiclient.Session
	reports  map[int64]apiclient.Counters
	losses   int // number of upcoming replies to drop after applying the batch
	rejected int
	calls    int
	bodies   []apiclient.Counters
}

func newLedger(total int64, rate int64) *ledger {
	return &ledger{
		reports: map[int64]apiclient.Counters{},
		session: apiclient.Session{SessionID: "s1", TotalBytes: total, RemainingBytes: total, RatePermille: rate, CanConnect: true},
	}
}

func (l *ledger) report(_ context.Context, c apiclient.Counters) (apiclient.Session, error) {
	l.calls++
	l.bodies = append(l.bodies, c)
	s := &l.session
	if c.Sequence <= s.LastSequence {
		if prev := l.reports[c.Sequence]; prev != c {
			l.rejected++
			return apiclient.Session{}, &apiclient.Error{Status: 409, Code: "traffic_conflict"}
		}
		return *s, nil // replayed: no second charge
	}
	if c.Sequence != s.LastSequence+1 || c.UploadBytes < s.UploadBytes || c.DownloadBytes < s.DownloadBytes {
		l.rejected++
		return apiclient.Session{}, &apiclient.Error{Status: 409, Code: "traffic_sequence"}
	}
	delta := (c.UploadBytes - s.UploadBytes) + (c.DownloadBytes - s.DownloadBytes)
	s.UsedUnits += delta * s.RatePermille
	s.UploadBytes, s.DownloadBytes, s.LastSequence = c.UploadBytes, c.DownloadBytes, c.Sequence
	s.RemainingBytes = max(0, (s.TotalBytes*1000-s.UsedUnits)/1000)
	l.reports[c.Sequence] = c
	if l.losses > 0 {
		l.losses--
		return apiclient.Session{}, &apiclient.Error{Code: "network_unreachable", Transient: true}
	}
	return *s, nil
}

type engine struct{ up, down int64 }

func (e *engine) totals() (int64, int64, error) { return e.up, e.down, nil }

func start(t *testing.T, l *ledger, e *engine) *Meter {
	t.Helper()
	clock := time.Unix(1_700_000_000, 0)
	m := New(Config{Base: l.session, Totals: e.totals, Report: l.report, Now: func() time.Time { clock = clock.Add(time.Second); return clock }})
	if err := m.Begin(l.session); err != nil {
		t.Fatal(err)
	}
	return m
}

const mib = 1 << 20

// B01: identical bidirectional bytes at 500‰ and 1000‰ charge exactly half and full.
func TestExactIntegerChargeAtBothRates(t *testing.T) {
	for _, tc := range []struct {
		rate, wantUnits, wantRemaining int64
	}{
		{1000, 104_857_600_000, 1024*mib - 100*mib},
		{500, 52_428_800_000, 1024*mib - 50*mib},
	} {
		l := newLedger(1024*mib, tc.rate)
		e := &engine{}
		m := start(t, l, e)
		e.up, e.down = 20*mib, 80*mib
		if _, _, err := m.Settle(context.Background()); err != nil {
			t.Fatal(err)
		}
		if l.session.UsedUnits != tc.wantUnits || l.session.RemainingBytes != tc.wantRemaining {
			t.Fatalf("rate %d: units=%d remaining=%d", tc.rate, l.session.UsedUnits, l.session.RemainingBytes)
		}
		if view := m.View(); view.UploadBytes != 20*mib || view.DownloadBytes != 80*mib || view.Pending {
			t.Fatalf("view: %+v", view)
		}
	}
}

// B02: a lost acknowledgement is retried with the identical batch even though more
// bytes have flowed meanwhile; the server charges once and the tail goes in the next report.
func TestLostAckRetriesOriginalBatchAndNeverDoubleCharges(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	e.up, e.down = 1*mib, 2*mib
	l.losses = 1
	if _, _, err := m.Flush(context.Background()); err == nil {
		t.Fatal("the lost reply must surface as an error")
	}
	if !m.Pending() {
		t.Fatal("batch must stay pending")
	}
	e.down += 5 * mib // traffic keeps flowing while the report is unconfirmed
	if _, _, err := m.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l.bodies[0] != l.bodies[1] {
		t.Fatalf("retry changed the pending batch: %+v vs %+v", l.bodies[0], l.bodies[1])
	}
	if l.session.UsedUnits != 3*mib*1000 {
		t.Fatalf("charged %d units after a replayed batch", l.session.UsedUnits)
	}
	if _, _, err := m.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l.session.UsedUnits != 8*mib*1000 || !m.Settled() {
		t.Fatalf("tail not settled: units=%d", l.session.UsedUnits)
	}
}

func TestCounterRegressionIsFatalAndNothingIsReported(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	e.up, e.down = 10, 10
	if err := m.Sample(); err != nil {
		t.Fatal(err)
	}
	e.down = 5
	if err := m.Sample(); ErrorCode(err) != CodeInvalidCounters {
		t.Fatalf("want invalid_core_counters, got %v", err)
	}
	calls := l.calls
	if _, _, err := m.Flush(context.Background()); ErrorCode(err) != CodeInvalidCounters || l.calls != calls {
		t.Fatalf("a fatal ledger must not report: %v calls %d->%d", err, calls, l.calls)
	}
	if err := m.Begin(l.session); ErrorCode(err) != CodeInvalidCounters {
		t.Fatalf("a fatal ledger must not start a segment: %v", err)
	}
}

// Section 7.2: the engine's counters may restart between segments, but only after
// the previous segment is fully confirmed.
func TestRebaseAcrossSettledSegments(t *testing.T) {
	l := newLedger(1024*mib, 500)
	e := &engine{}
	m := start(t, l, e)
	e.up, e.down = 1*mib, 1*mib
	if _, _, err := m.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.up, e.down = 0, 0 // engine restarted: counters begin again at zero
	if err := m.Begin(l.session); err != nil {
		t.Fatalf("settled ledger must accept a re-baselined engine: %v", err)
	}
	e.up, e.down = 3*mib, 0
	if _, _, err := m.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l.session.UploadBytes != 4*mib || l.session.DownloadBytes != 1*mib || l.session.UsedUnits != 5*mib*500 {
		t.Fatalf("cumulative did not continue across segments: %+v", l.session)
	}
}

func TestBeginRefusesUnsettledOrDisagreeingLedger(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	e.up = 100
	if err := m.Sample(); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(l.session); ErrorCode(err) != CodeUnconfirmed {
		t.Fatalf("unreported bytes must block a new segment: %v", err)
	}
	if _, _, err := m.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := l.session
	other.LastSequence += 7 // the server moved without us (another writer)
	if err := m.Begin(other); ErrorCode(err) != CodeConflict {
		t.Fatalf("a diverged server must force re-login: %v", err)
	}
}

func TestServerConflictOnSequenceIsFatal(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	e.up = 10
	l.session.LastSequence = 5 // someone else advanced the sequence
	_, _, err := m.Flush(context.Background())
	if ErrorCode(err) != CodeConflict || m.Fatal() != CodeConflict {
		t.Fatalf("want traffic_conflict, got %v / %q", err, m.Fatal())
	}
}

func TestRateChangeIsReportedAfterBytesAreSettledAtTheBoundRate(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	e.up = 2 * mib
	if _, _, err := m.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.session.RatePermille = 1500 // admin changed the multiplier
	e.up += 1 * mib
	_, _, err := m.Flush(context.Background())
	if ErrorCode(err) != CodeRateChanged {
		t.Fatalf("rate change must stop the segment: %v", err)
	}
	// Bytes counted before the ack were charged by the server at the rate it held;
	// the next segment (after a rebind) starts from the acknowledged state.
	if l.session.UsedUnits != 2*mib*1000+1*mib*1500 {
		t.Fatalf("units %d", l.session.UsedUnits)
	}
}

func TestEstimatedRemainingUsesIntegerUnitsAndFloorsAtZero(t *testing.T) {
	l := newLedger(1000, 500) // 1000 bytes total, 0.5x
	e := &engine{}
	m := start(t, l, e)
	e.up = 1001
	if err := m.Sample(); err != nil {
		t.Fatal(err)
	}
	// 1001 bytes * 500 = 500500 units; total 1,000,000 units -> 499,500 left -> floor 499 bytes
	if got := m.EstimatedRemaining(); got != 499 {
		t.Fatalf("estimate %d", got)
	}
	e.up = 5000
	_ = m.Sample()
	if got := m.EstimatedRemaining(); got != 0 {
		t.Fatalf("estimate must floor at zero, got %d", got)
	}
	if view := m.View(); view.ConfirmedRemaining != 1000 {
		t.Fatalf("confirmed balance must not move before an ack: %d", view.ConfirmedRemaining)
	}
}

func TestRatesAndBoundedHistory(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	for i := 1; i <= HistoryLen+10; i++ {
		e.down += 1000
		e.up += 100
		if err := m.Sample(); err != nil {
			t.Fatal(err)
		}
	}
	view := m.View()
	if view.DownloadRate != 1000 || view.UploadRate != 100 {
		t.Fatalf("rates: %+v", view)
	}
	if len(view.History) != HistoryLen {
		t.Fatalf("history unbounded: %d", len(view.History))
	}
	m.Idle()
	if v := m.View(); v.DownloadRate != 0 {
		t.Fatal("idle must zero the rate")
	}
}

func TestSettleGivesUpWithoutLosingPending(t *testing.T) {
	l := newLedger(1024*mib, 1000)
	e := &engine{}
	m := start(t, l, e)
	e.up = 42
	l.losses = 100
	_, _, err := m.Settle(context.Background())
	var api *apiclient.Error
	if !errors.As(err, &api) || !m.Pending() {
		t.Fatalf("unconfirmed settle must keep the batch: %v pending=%v", err, m.Pending())
	}
	l.losses = 0
	if _, _, err = m.Settle(context.Background()); err != nil || !m.Settled() {
		t.Fatalf("later settle must succeed: %v", err)
	}
	if l.session.UploadBytes != 42 {
		t.Fatal("bytes lost")
	}
}
