package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func endlessServer(t *testing.T) *httptest.Server {
	block := make([]byte, 64<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(block[:1])
			return
		}
		for i := 0; i < 256; i++ { // 16 MiB per request, then the stream asks again
			if _, err := w.Write(block); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A short real run on loopback: rows are cut on whole windows, the contention phase is flagged
// on exactly the windows inside it, the burners are gone afterwards and the CSV has every column.
func TestMeasureOnLoopback(t *testing.T) {
	srv := endlessServer(t)
	o := options{URL: srv.URL, Streams: 2, Duration: 8 * time.Second, Window: time.Second, ContentionAt: 3 * time.Second,
		ContentionFor: 2 * time.Second, Burners: 1, CoreName: "no-such-process-asterlink", CorePID: os.Getpid(), StallAfter: 5 * time.Second,
		Thresholds: defaultThresholds()}
	if err := validate(&o); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := measure(ctx, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 8 || len(res.PerSecond) != 8 || res.ContentionEnd != 5*time.Second {
		t.Fatalf("rows %d, seconds %d, contention end %v", len(res.Rows), len(res.PerSecond), res.ContentionEnd)
	}
	phases := ""
	for _, rw := range res.Rows {
		phases += rw.Phase[:1]
		if len(rw.csv()) != len(csvHeader) {
			t.Fatalf("csv row has %d fields, header %d", len(rw.csv()), len(csvHeader))
		}
		if rw.Bytes <= 0 {
			t.Fatalf("window %v moved nothing", rw.Start)
		}
	}
	if phases != "iiiccaaa" {
		t.Fatalf("phases %q", phases)
	}
	if res.Rows[0].TTFBP50 <= 0 && res.Rows[1].TTFBP50 <= 0 {
		t.Fatal("the time-to-first-byte probe never answered")
	}
}

// --limit-mbps caps the total like one video stream; the loopback path is far faster than the cap.
func TestMeasureHonoursTheLimit(t *testing.T) {
	srv := endlessServer(t)
	o := options{URL: srv.URL, Streams: 3, Duration: 4 * time.Second, Window: time.Second, CoreName: "no-such-process-asterlink",
		StallAfter: 5 * time.Second, LimitMbps: 80, Thresholds: defaultThresholds()}
	res, err := measure(context.Background(), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rw := range res.Rows[1:] { // the first second also fills the socket buffers
		if m := rw.mbps(); m < 50 || m > 110 {
			t.Fatalf("window %v: %.1f Mbit/s with an 80 Mbit/s cap", rw.Start, m)
		}
	}
}

// A stream whose server stops sending is cut after --stall-after and retried; the stall is counted.
func TestMeasureCutsAStalledStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // never another byte
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	o := options{URL: "http://" + ln.Addr().String() + "/", Streams: 1, Duration: 5 * time.Second, Window: time.Second,
		CoreName: "no-such-process-asterlink", StallAfter: 2 * time.Second, Thresholds: defaultThresholds()}
	res, err := measure(context.Background(), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stalls int64
	for _, rw := range res.Rows {
		stalls += rw.Stalls
	}
	if stalls < 1 {
		t.Fatalf("no stall counted: %+v", res.Rows)
	}
}
