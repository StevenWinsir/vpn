package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type options struct {
	URL           string
	RTTURL        string
	RTTEvery      time.Duration // probe interval; public test sites may block an exit IP that asks every second
	Streams       int
	Duration      time.Duration
	Warmup        time.Duration
	Window        time.Duration
	ContentionAt  time.Duration // 0 with ContentionFor 0: no contention phase
	ContentionFor time.Duration
	Burners       int
	CoreName      string
	CorePID       int
	StallAfter    time.Duration
	LimitMbps     float64 // 0: as fast as the path goes; >0: read at most this rate in total (like one video stream)
	Thresholds    thresholds
}

// run holds the live counters of one measurement.
type run struct {
	o        options
	client   *http.Client
	limit    *pacer
	total    atomic.Int64
	requests atomic.Int64
	errors   atomic.Int64
	lastErr  atomic.Value // string: the latest download error, so a window full of errors says why
	stalls   atomic.Int64

	mu         sync.Mutex
	ttfb       []float64 // milliseconds, since the last window was cut
	probeFails int
	core       coreSample
}

// row is one CSV line and one judged window.
type row struct {
	window
	Phase      string
	TTFBP50    float64
	TTFBMax    float64
	ProbeFails int
	Requests   int64
	Errors     int64
	Stalls     int64
	LastError  string // set only when the window had download errors
	Core       coreSample
}

type result struct {
	Rows          []row
	PerSecond     []int64
	ContentionEnd time.Duration
	Cores         []coreSample
}

// userAgent names the tool. Some public test sites close the connection on Go's default
// "Go-http-client/1.1" without answering (seen at Hetzner, 2026-11-05: every request ended EOF).
const userAgent = "asterlink-throughput/1"

func newClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil, // measure the tunnel, never an environment proxy
		DisableCompression:    true,
		MaxIdleConnsPerHost:   16,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// progressReader counts bytes into the run and stamps the time of the last byte, so a stream
// that stops moving can be cut and retried (a stall is recorded, the window shows the gap).
type progressReader struct {
	r    io.Reader
	run  *run
	last *atomic.Int64
}

func (p progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.run.total.Add(int64(n))
		p.last.Store(time.Now().UnixNano())
		p.run.limit.wait(n) // back-pressure on the TCP stream, the way a player stops reading when its buffer is full
		p.last.Store(time.Now().UnixNano())
	}
	return n, err
}

// retryDelay backs off after consecutive failed requests: 2 s, 4 s, ... up to 60 s. Public test
// sites limit connections per IP (Vultr answers 503 beyond two) and block an IP that keeps
// asking; retrying every second got the node's exit blocked at Vultr and OVH (2026-11-05).
func retryDelay(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	d := time.Second << min(failures, 6)
	return min(d, time.Minute)
}

func (r *run) stream(ctx context.Context) {
	buf := make([]byte, 256<<10)
	failures := 0
	for ctx.Err() == nil {
		reqCtx, cancel := context.WithCancel(ctx)
		var last atomic.Int64
		last.Store(time.Now().UnixNano())
		stalled := make(chan struct{})
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-reqCtx.Done():
					return
				case <-tick.C:
					if time.Since(time.Unix(0, last.Load())) > r.o.StallAfter {
						close(stalled)
						cancel()
						return
					}
				}
			}
		}()
		err := r.fetch(reqCtx, buf, &last)
		cancel()
		select {
		case <-stalled:
			r.stalls.Add(1)
		default:
			if err != nil && ctx.Err() == nil {
				r.errors.Add(1)
				r.lastErr.Store(redact(err.Error()))
				failures++
				select {
				case <-ctx.Done():
				case <-time.After(retryDelay(failures)):
				}
			} else if err == nil {
				failures = 0
			}
		}
	}
}

func (r *run) fetch(ctx context.Context, buf []byte, last *atomic.Int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.o.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-store")
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r.requests.Add(1)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	_, err = io.CopyBuffer(io.Discard, progressReader{resp.Body, r, last}, buf)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// probeTTFB times one small request (Range 0-0) to the first response byte: the round trip of
// the whole path, which a TUN-local TCP handshake would not show.
func (r *run) probeTTFB(ctx context.Context) {
	url := r.o.RTTURL
	if url == "" {
		url = r.o.URL
	}
	client := newClient()
	every := r.o.RTTEvery
	if every <= 0 {
		every = time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		req.Header.Set("Range", "bytes=0-0")
		req.Header.Set("Cache-Control", "no-store")
		req.Header.Set("User-Agent", userAgent)
		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
		}
		cancel()
		r.mu.Lock()
		if err != nil || resp.StatusCode/100 != 2 {
			if ctx.Err() == nil {
				r.probeFails++
			}
		} else {
			r.ttfb = append(r.ttfb, float64(elapsed.Microseconds())/1000)
		}
		r.mu.Unlock()
	}
}

func (r *run) watchCore(ctx context.Context, out chan<- coreSample) {
	tick := time.NewTicker(r.o.Window)
	defer tick.Stop()
	for {
		s := probeCore(r.o.CoreName, r.o.CorePID)
		r.mu.Lock()
		r.core = s
		r.mu.Unlock()
		out <- s
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// burners starts the contention processes: copies of this executable in "burn" mode, at the
// same (normal) priority as any app, one busy thread each. They also stop by themselves.
func startBurners(n int, d time.Duration) []*exec.Cmd {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	var cmds []*exec.Cmd
	for i := 0; i < n; i++ {
		cmd := exec.Command(self, "burn", "--for", (d + 5*time.Second).String())
		if cmd.Start() == nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func stopBurners(cmds []*exec.Cmd) {
	for _, c := range cmds {
		_ = c.Process.Kill()
		_ = c.Wait()
	}
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[int(p*float64(len(sorted)-1)+0.5)]
}

func measure(ctx context.Context, o options, onRow func(row)) (result, error) {
	r := &run{o: o, client: newClient()}
	if o.LimitMbps > 0 {
		r.limit = &pacer{bytesPerSecond: o.LimitMbps * 1e6 / 8}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cores := make(chan coreSample, 1024)
	go r.watchCore(ctx, cores)
	go r.probeTTFB(ctx)
	for i := 0; i < o.Streams; i++ {
		go r.stream(ctx)
	}

	perWindow := int(o.Window / time.Second)
	cStart, cEnd := int(o.ContentionAt/time.Second), int((o.ContentionAt+o.ContentionFor)/time.Second)
	planned := o.ContentionFor > 0
	var res result
	if planned {
		res.ContentionEnd = time.Duration(cEnd) * time.Second
	}
	var burning []*exec.Cmd
	defer func() { stopBurners(burning) }()
	var lastTotal, lastReq, lastErr, lastStall int64
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	seconds := int(o.Duration / time.Second)
	for s := 0; s < seconds; s++ {
		if planned && s == cStart {
			burning = startBurners(o.Burners, o.ContentionFor)
		}
		if planned && s == cEnd {
			stopBurners(burning)
			burning = nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-tick.C:
		}
		total := r.total.Load()
		res.PerSecond = append(res.PerSecond, total-lastTotal)
		lastTotal = total
		if (s+1)%perWindow != 0 {
			continue
		}
		from := s + 1 - perWindow
		var bytes int64
		for _, b := range res.PerSecond[from:] {
			bytes += b
		}
		w := window{Start: time.Duration(from) * time.Second, Length: o.Window, Bytes: bytes,
			Contention: planned && from >= cStart && s+1 <= cEnd, Warmup: time.Duration(from)*time.Second < o.Warmup}
		r.mu.Lock()
		rw := row{window: w, TTFBP50: percentile(r.ttfb, 0.5), TTFBMax: percentile(r.ttfb, 1), ProbeFails: r.probeFails, Core: r.core}
		r.ttfb, r.probeFails = nil, 0
		r.mu.Unlock()
		req, errs, stalls := r.requests.Load(), r.errors.Load(), r.stalls.Load()
		rw.Requests, rw.Errors, rw.Stalls = req-lastReq, errs-lastErr, stalls-lastStall
		if rw.Errors > 0 {
			rw.LastError, _ = r.lastErr.Load().(string)
		}
		lastReq, lastErr, lastStall = req, errs, stalls
		switch {
		case w.Warmup:
			rw.Phase = "warmup"
		case w.Contention:
			rw.Phase = "contention"
		case planned && from >= cEnd:
			rw.Phase = "after"
		default:
			rw.Phase = "idle"
		}
		res.Rows = append(res.Rows, rw)
		if onRow != nil {
			onRow(rw)
		}
	}
	cancel()
	for {
		select {
		case c := <-cores:
			res.Cores = append(res.Cores, c)
		default:
			return res, nil
		}
	}
}

var csvHeader = append([]string{"from_s", "to_s", "phase", "mbit_s", "ttfb_p50_ms", "ttfb_max_ms", "ttfb_failures", "requests", "errors", "stalls", "last_error"}, coreCSVHeader...)

func (rw row) csv() []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	return append([]string{
		strconv.Itoa(int(rw.Start.Seconds())), strconv.Itoa(int((rw.Start + rw.Length).Seconds())), rw.Phase,
		f(rw.mbps()), f(rw.TTFBP50), f(rw.TTFBMax), strconv.Itoa(rw.ProbeFails),
		strconv.FormatInt(rw.Requests, 10), strconv.FormatInt(rw.Errors, 10), strconv.FormatInt(rw.Stalls, 10), rw.LastError,
	}, rw.Core.csvFields()...)
}
