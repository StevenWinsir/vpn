//go:build !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metacubex/mihomo/tunnel/statistic"
)

type managedTrafficFixture struct {
	mu                        sync.Mutex
	c                         *managed.Coordinator
	state                     managed.AccountSnapshot
	status                    managed.Status
	profile                   string
	bound, drop, fail, logout bool
	reports                   []managed.Counters
	reportTimes               []time.Time
	port                      int
	target                    string
	transport                 *http.Transport
}

func newManagedTrafficFixture(t *testing.T) *managedTrafficFixture {
	t.Helper()
	f := &managedTrafficFixture{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/hold" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					if _, err := w.Write(bytes.Repeat([]byte("tail"), 256)); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
			}
		}
		_, _ = w.Write(bytes.Repeat([]byte("download"), 4096))
	}))
	t.Cleanup(target.Close)
	f.target = target.URL
	destination := strings.TrimPrefix(target.URL, "http://")
	var nodeSockets sync.Map
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != destination {
			w.WriteHeader(403)
			return
		}
		remote, err := net.DialTimeout("tcp", destination, time.Second)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer remote.Close()
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		nodeSockets.Store(conn, remote)
		defer nodeSockets.Delete(conn)
		defer conn.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, buffer); _ = remote.Close(); close(done) }()
		_, _ = io.Copy(conn, remote)
		_ = conn.Close()
		<-done
	}))
	t.Cleanup(func() {
		nodeSockets.Range(func(key, value any) bool { _ = key.(net.Conn).Close(); _ = value.(net.Conn).Close(); return true })
		node.Close()
	})
	_, nodePort, _ := net.SplitHostPort(strings.TrimPrefix(node.URL, "http://"))
	f.profile = fmt.Sprintf("proxies:\n  - {name: Metered-Node, type: http, server: 127.0.0.1, port: %s}\nproxy-groups:\n  - {name: VIP, type: select, proxies: [Metered-Node]}\nrules: ['DOMAIN,localhost,DIRECT', 'MATCH,VIP']\n", nodePort)
	digest := sha256.Sum256([]byte(f.profile))
	expires := time.Now().Add(time.Hour)
	f.status = managed.Status{SessionID: "22222222-2222-4222-8222-222222222222", ExpiresAt: expires, SubscriptionExpiresAt: &expires,
		PlanName: "Traffic fixture", ProfileVersion: hex.EncodeToString(digest[:]), RemainingBytes: 1 << 28, TotalBytes: 1 << 28,
		SessionIdleTimeout: 180, ReportInterval: 60, LeaseSeconds: 90, MeteringSource: "client_reported", RatePermille: 1000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/login" && r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 43) {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/login":
			f.authorize()
			_ = json.NewEncoder(w).Encode(map[string]any{"token": strings.Repeat("t", 43), "user": managed.User{ID: "11111111-1111-4111-8111-111111111111", Email: "fixture@example.invalid", Name: "Fixture"}, "session": f.status})
		case "/session":
			f.authorize()
			_ = json.NewEncoder(w).Encode(f.status)
		case "/config":
			f.bound = true
			f.authorize()
			_ = json.NewEncoder(w).Encode(managed.Profile{YAML: f.profile, Version: f.status.ProfileVersion, Session: f.status})
		case "/traffic":
			if f.fail {
				f.fail = false
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"code":"heartbeat_unavailable"}}`))
				return
			}
			var batch managed.Counters
			if json.NewDecoder(r.Body).Decode(&batch) != nil {
				w.WriteHeader(400)
				return
			}
			replayed := batch.Sequence == f.status.LastSequence && batch.UploadBytes == f.status.UploadBytes && batch.DownloadBytes == f.status.DownloadBytes
			if !replayed {
				if batch.Sequence != f.status.LastSequence+1 || batch.UploadBytes < f.status.UploadBytes || batch.DownloadBytes < f.status.DownloadBytes {
					w.WriteHeader(409)
					t.Error("non-idempotent cumulative report")
					return
				}
				f.status.RemainingBytes = max(0, f.status.RemainingBytes-(batch.UploadBytes-f.status.UploadBytes)-(batch.DownloadBytes-f.status.DownloadBytes))
				f.status.LastSequence, f.status.UploadBytes, f.status.DownloadBytes = batch.Sequence, batch.UploadBytes, batch.DownloadBytes
			}
			f.reports = append(f.reports, batch)
			f.reportTimes = append(f.reportTimes, time.Now())
			if f.drop {
				f.drop = false
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			f.authorize()
			_ = json.NewEncoder(w).Encode(map[string]any{"session": f.status, "replayed": replayed})
		case "/logout":
			f.logout = true
			_, _ = w.Write([]byte(`{"logged_out":true}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	engine := newManagedTestEngine(t)
	api, _ := url.Parse(server.URL)
	direct := managed.NewTransport(nil)
	t.Cleanup(direct.CloseIdleConnections)
	f.c = managed.NewCoordinator(func() http.RoundTripper {
		return managedRoundTrip(func(r *http.Request) (*http.Response, error) {
			copy := r.Clone(r.Context())
			copy.URL.Scheme, copy.URL.Host = api.Scheme, api.Host
			copy.URL.Path = strings.TrimPrefix(copy.URL.Path, "/api/v1/client")
			return direct.RoundTrip(copy)
		})
	}, func() { handleStopListener(); handleCloseConnections() }, engine, managedTotals)
	previous := managedAccount
	managedAccount = f.c
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = f.c.Reset(ctx)
		managedAccount = previous
	})
	var err error
	f.state, err = f.c.Login(context.Background(), managed.LoginParams{Email: "fixture@example.invalid", Password: "fixture-password", DeviceID: "33333333-3333-4333-8333-333333333333", Platform: "macos", AppVersion: "test"})
	if err != nil || f.state.User == nil {
		t.Fatalf("login: %v / %+v", err, f.state)
	}
	f.state, err = f.c.LoadConfig(context.Background(), f.state.Generation)
	if err != nil || f.state.Configuration == nil {
		t.Fatalf("load: %v / %+v", err, f.state)
	}
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.port = reservation.Addr().(*net.TCPAddr).Port
	_ = reservation.Close()
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", f.port))
	f.transport = &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	t.Cleanup(f.transport.CloseIdleConnections)
	return f
}

func (f *managedTrafficFixture) authorize() {
	f.status.ServerTime = time.Now().UTC()
	f.status.CanConnect = f.bound && f.status.RemainingBytes > 0
	f.status.Reason = ""
	if !f.bound {
		f.status.Reason = "profile_required"
	} else if f.status.RemainingBytes == 0 {
		f.status.Reason = "quota_exhausted"
	}
	f.status.AuthorizationExpiresAt = nil
	if f.status.CanConnect {
		until := f.status.ServerTime.Add(90 * time.Second)
		f.status.AuthorizationExpiresAt = &until
	}
}

func (f *managedTrafficFixture) connect(t *testing.T) {
	t.Helper()
	state, err := f.c.Connect(context.Background(), f.state.Generation, f.port)
	if err != nil || !state.CanConnect || !isRunning.Load() {
		t.Fatalf("connect: %v / %+v", err, state)
	}
	f.state = state
}

func (f *managedTrafficFixture) transfer(t *testing.T, target string) {
	t.Helper()
	client := &http.Client{Transport: f.transport, Timeout: 3 * time.Second}
	response, err := client.Post(target, "application/octet-stream", bytes.NewReader(bytes.Repeat([]byte("upload"), 4096)))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || len(data) != 32768 {
		t.Fatalf("transfer: %v status=%d bytes=%d", err, response.StatusCode, len(data))
	}
}

func TestManagedRealCumulativeTrafficReconnectDisplayResetAndDirect(t *testing.T) {
	f := newManagedTrafficFixture(t)
	baselineUp, baselineDown := statistic.DefaultManager.TotalTraffic(true)
	f.connect(t)
	f.transfer(t, f.target)
	up, down := statistic.DefaultManager.TotalTraffic(true)
	if up-baselineUp < 24576 || down-baselineDown < 32768 {
		t.Fatal("real Mihomo proxy byte counters missing")
	}
	handleResetTraffic()
	if handleGetTotalTraffic(true) != (Traffic{}) {
		t.Fatal("display did not reset")
	}
	actualUp, actualDown := statistic.DefaultManager.TotalTraffic(true)
	if actualUp != up || actualDown != down {
		t.Fatal("display reset corrupted cumulative billing")
	}
	f.transfer(t, strings.Replace(f.target, "127.0.0.1", "localhost", 1))
	actualUp, actualDown = statistic.DefaultManager.TotalTraffic(true)
	if actualUp != up || actualDown != down {
		t.Fatal("DIRECT traffic was billed")
	}
	state, err := f.c.Disconnect(context.Background(), f.state.Generation)
	if err != nil || state.CanConnect || isRunning.Load() {
		t.Fatalf("disconnect: %v / %+v", err, state)
	}
	if state.Metering.AcknowledgedUpload != up-baselineUp || state.Metering.AcknowledgedDownload != down-baselineDown || state.Metering.Pending {
		t.Fatal("tail not acknowledged exactly")
	}
	f.connect(t)
	f.transfer(t, f.target)
	state, err = f.c.LoadConfig(context.Background(), f.state.Generation)
	if err != nil || state.Configuration == nil || state.CanConnect {
		t.Fatalf("reload: %v / %+v", err, state)
	}
	f.connect(t)
	f.transfer(t, f.target)
	state, err = f.c.Logout(context.Background(), f.state.Generation)
	if err != nil || state.User != nil || state.ErrorCode != "" {
		t.Fatalf("logout: %v / %+v", err, state)
	}
	actualUp, actualDown = statistic.DefaultManager.TotalTraffic(true)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.logout || f.status.UploadBytes != actualUp-baselineUp || f.status.DownloadBytes != actualDown-baselineDown {
		t.Fatal("logout lost final cumulative bytes")
	}
	t.Logf("real transfer ledger: up=%d down=%d sequence=%d, display reset and DIRECT excluded", f.status.UploadBytes, f.status.DownloadBytes, f.status.LastSequence)
}

func TestManagedFailureClosesEstablishedTrafficAndRequiresExplicitReconnect(t *testing.T) {
	f := newManagedTrafficFixture(t)
	f.connect(t)
	client := &http.Client{Transport: f.transport, Timeout: 3 * time.Second}
	response, err := client.Get(f.target + "/hold")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err = io.ReadFull(response.Body, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if _, err = f.c.Flush(context.Background(), f.state.Generation); err == nil {
		t.Fatal("failed report accepted")
	}
	if f.c.Snapshot().CanConnect || isRunning.Load() {
		t.Fatal("failed report did not stop listener")
	}
	finished := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, response.Body); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("established connection survived denial")
	}
	if _, err = f.c.Flush(context.Background(), f.state.Generation); err != nil {
		t.Fatal(err)
	}
	if f.c.Snapshot().CanConnect || isRunning.Load() {
		t.Fatal("retry reopened without user intent")
	}
	if handleStartListener() {
		t.Fatal("legacy resume bypassed fresh intent")
	}
	f.connect(t)
	f.transfer(t, f.target)
}

func TestManagedUncertainReportSurvivesReloadWithoutDoubleCharge(t *testing.T) {
	f := newManagedTrafficFixture(t)
	f.connect(t)
	f.transfer(t, f.target)
	f.mu.Lock()
	f.drop = true
	f.mu.Unlock()
	if _, err := f.c.Flush(context.Background(), f.state.Generation); err == nil {
		t.Fatal("lost acknowledgement accepted")
	}
	state, err := f.c.LoadConfig(context.Background(), f.state.Generation)
	if err != nil || state.Configuration == nil || state.CanConnect || state.Metering.Pending {
		t.Fatalf("reload after uncertain commit: %v / %+v", err, state)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reports) < 3 || f.reports[1] != f.reports[2] {
		t.Fatal("pending batch changed during configuration reload")
	}
	if state.Metering.ConfirmedRemaining != f.status.TotalBytes-f.status.UploadBytes-f.status.DownloadBytes {
		t.Fatal("uncertain report charged twice")
	}
}

func TestManagedFirstUncertainReportRetainsBatchAndStopRevisionFence(t *testing.T) {
	f := newManagedTrafficFixture(t)
	f.mu.Lock()
	f.drop = true
	f.mu.Unlock()
	state, err := f.c.Connect(context.Background(), f.state.Generation, f.port)
	if err != nil || state.CanConnect || state.Metering == nil || !state.Metering.Pending {
		t.Fatalf("first uncertain batch lost: %v / %+v", err, state)
	}
	f.connect(t)
	f.mu.Lock()
	if len(f.reports) < 2 || f.reports[0] != f.reports[1] {
		t.Fatal("first batch was not retried verbatim")
	}
	f.mu.Unlock()
	stale := f.c.Snapshot()
	f.c.StopNow()
	state, err = f.c.Connect(context.Background(), stale.Generation, f.port, stale.RuntimeRevision)
	if err == nil || state.CanConnect || isRunning.Load() {
		t.Fatal("delayed connect overrode a newer stop")
	}
}

func TestManagedRealQuotaExhaustionClosesExistingStream(t *testing.T) {
	f := newManagedTrafficFixture(t)
	f.mu.Lock()
	f.status.TotalBytes, f.status.RemainingBytes = 4096, 4096
	f.mu.Unlock()
	f.connect(t)
	client := &http.Client{Transport: f.transport, Timeout: 4 * time.Second}
	response, err := client.Get(f.target + "/hold")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	state := f.c.Snapshot()
	if state.CanConnect || isRunning.Load() || state.Metering.EstimatedRemaining != 0 {
		t.Fatalf("quota did not close real transfer: %+v", state)
	}
	_, _ = f.c.Disconnect(context.Background(), f.state.Generation)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status.RemainingBytes != 0 || f.status.DownloadBytes < 4096 {
		t.Fatal("quota final bytes missing")
	}
}

func TestManagedAccountSwitchKeepsUnconfirmedOldMeter(t *testing.T) {
	f := newManagedTrafficFixture(t)
	f.connect(t)
	f.transfer(t, f.target)
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	state, err := f.c.Login(context.Background(), managed.LoginParams{Email: "next@example.invalid", Password: "fixture-password", DeviceID: "33333333-3333-4333-8333-333333333333", Platform: "macos", AppVersion: "test"})
	if err == nil || state.Generation != f.state.Generation || state.User == nil || state.CanConnect || !state.Metering.Pending {
		t.Fatalf("switch dropped old unconfirmed ledger: %v / %+v", err, state)
	}
	if _, err = f.c.Disconnect(context.Background(), state.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestManagedRealMinuteReport(t *testing.T) {
	if os.Getenv("FLCLASH_RUN_MINUTE_TEST") != "1" {
		t.Skip("opt-in real 60-second wall-clock acceptance")
	}
	f := newManagedTrafficFixture(t)
	f.connect(t)
	f.transfer(t, f.target)
	deadline := time.Now().Add(66 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		count := len(f.reports)
		var interval time.Duration
		if count > 1 {
			interval = f.reportTimes[1].Sub(f.reportTimes[0])
		}
		f.mu.Unlock()
		if count > 1 {
			if interval < 59*time.Second || interval > 65*time.Second {
				t.Fatalf("report interval %v", interval)
			}
			state := f.c.Snapshot()
			if !state.CanConnect || state.Metering.AcknowledgedUpload == 0 || state.Metering.AcknowledgedDownload == 0 {
				t.Fatal("minute report missing real bytes or permission")
			}
			t.Logf("actual wall-clock interval=%v, upload=%d, download=%d", interval, state.Metering.AcknowledgedUpload, state.Metering.AcknowledgedDownload)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("minute report did not arrive")
}
