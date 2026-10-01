package managed

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetiredRuntimeCallbackCannotStopNewSession(t *testing.T) {
	var stops atomic.Int64
	old := &runtimeLease{stop: func() { stops.Add(1) }}
	old.retire()
	fresh := &runtimeLease{stop: func() { stops.Add(1) }}
	if err := fresh.start(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	before := stops.Load()
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() { defer wg.Done(); old.Stop() }()
	}
	wg.Wait()
	if stops.Load() != before || !fresh.Running() {
		t.Fatal("retired callback stopped new session")
	}
}

type blockedReportTransport struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockedReportTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	b.once.Do(func() { close(b.started) })
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestCloseCancelsActiveReportAndHonorsCallerDeadline(t *testing.T) {
	f := newFixture(t)
	transport := &blockedReportTransport{started: make(chan struct{})}
	f.meter.client.http.Transport = transport
	f.up.Store(42)
	finished := make(chan error, 1)
	go func() { finished <- f.meter.Flush(context.Background()) }()
	<-transport.started
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := f.meter.Close(ctx, true); err == nil {
		t.Fatal("unconfirmed final report accepted")
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatal("close exceeded its deadline")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("old report did not cancel")
	}
	if f.meter.Status().CanConnect {
		t.Fatal("closed meter remained authorized")
	}
}
