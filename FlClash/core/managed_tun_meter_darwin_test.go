//go:build darwin && !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/mihomo/listener"
)

func TestManagedMacOSTUNTCPMeterAndReconnect(t *testing.T) {
	if os.Getenv("RUN_MANAGED_TUN_FULL_ROUTE_TEST") != "1" {
		t.Skip("requires an isolated privileged macOS runner; captures routed traffic")
	}
	if os.Geteuid() != 0 {
		t.Fatal("real TUN metering requires root")
	}
	upload := strings.Repeat("managed-upload-", 128)
	download := strings.Repeat("managed-download-", 256)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "198.19.253.2:80" {
			t.Errorf("request did not reach the configured proxy: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		request, err := http.ReadRequest(buffer.Reader)
		if err != nil {
			t.Error(err)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, 8192))
		_ = request.Body.Close()
		if err != nil || request.Method != http.MethodPost || !bytes.Equal(body, []byte(upload)) {
			t.Errorf("TUN upload payload mismatch: %v", err)
			return
		}
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(download), download)
		_ = buffer.Flush()
	}))
	defer upstream.Close()
	nodePort := upstream.Listener.Addr().(*net.TCPAddr).Port
	engine := newManagedTestEngine(t)
	engine.network = macOSManagedNetwork{}
	yaml := fmt.Sprintf("proxies: [{name: TUN-Node, type: http, server: 127.0.0.1, port: %d}]\nproxy-groups: [{name: VIP, type: select, proxies: [TUN-Node]}]\nrules: ['MATCH,VIP']\n", nodePort)
	prepared, err := engine.Prepare(context.Background(), managedProfileFixture(yaml))
	if err != nil {
		t.Fatal(err)
	}
	view, err := prepared.Apply(context.Background(), managed.ConfigurationOwner{Generation: 1, UserID: "tun-meter-user", SessionID: "tun-meter-session"})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		beforeUp, beforeDown, err := managedTotals(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.Start(context.Background(), view.Owner, view.ID, managedTestPort(t)); err != nil {
			t.Fatal(err)
		}
		if listener.GetPorts().MixedPort != 0 || managedNetworkCloser == nil || !currentConfig.General.Tun.Enable {
			t.Fatal("runtime is not TUN-only")
		}
		transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := client.Post("http://198.19.253.2/probe", "application/octet-stream", strings.NewReader(upload))
		if err != nil {
			transport.CloseIdleConnections()
			t.Fatalf("unproxied OS TCP request did not traverse TUN and the node: %v", err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 16384))
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != download {
			t.Fatalf("TUN download payload mismatch: %v", err)
		}
		if !handleStopListener() || managedNetworkCloser != nil || isRunning.Load() {
			t.Fatal("TUN disconnect did not release runtime ownership")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = engine.Drain(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		afterUp, afterDown, err := managedTotals(context.Background())
		if err != nil || afterUp-beforeUp < int64(len(upload)) || afterDown-beforeDown < int64(len(download)) {
			t.Fatalf("TUN payload was not sampled by the managed meter: up=%d down=%d err=%v", afterUp-beforeUp, afterDown-beforeDown, err)
		}
		t.Logf("attempt %d: real TUN -> configured HTTP node, settled upload=%d download=%d, mixed=0", attempt, afterUp-beforeUp, afterDown-beforeDown)
	}
}
