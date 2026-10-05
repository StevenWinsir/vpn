//go:build !cgo

package main

import (
	"bytes"
	"context"
	"core/managed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/mihomo/tunnel/statistic"
)

func TestCatalogBackendMihomoAcceptance(t *testing.T) {
	directory := os.Getenv("CATALOG_ACCEPTANCE_DIR")
	if directory == "" {
		t.Skip("requires scripts/test-node-catalog.py private browser/Gin/PostgreSQL fixture")
	}
	if !strings.HasPrefix(directory, "/tmp/vpn-catalog-") {
		t.Fatal("private fixture directory required")
	}
	data, err := os.ReadFile(filepath.Join(directory, "fixture.json"))
	if err != nil {
		t.Fatal("cannot read private fixture")
	}
	var fixture struct {
		API      string            `json:"api"`
		Password string            `json:"password"`
		Control  string            `json:"control"`
		Emails   map[string]string `json:"emails"`
		Target   string            `json:"traffic_target"`
	}
	if json.Unmarshal(data, &fixture) != nil {
		t.Fatal("invalid fixture metadata")
	}
	apiURL, err := url.Parse(fixture.API)
	if err != nil || apiURL.Scheme != "http" || apiURL.Hostname() != "127.0.0.1" || apiURL.Port() == "" {
		t.Fatal("loopback fixture API required")
	}
	engine := newManagedTestEngine(t)
	direct := managed.NewTransport(nil)
	t.Cleanup(direct.CloseIdleConnections)
	c := managed.NewCoordinator(func() http.RoundTripper {
		return managedRoundTrip(func(r *http.Request) (*http.Response, error) {
			copy := r.Clone(r.Context())
			copy.URL.Scheme, copy.URL.Host = apiURL.Scheme, apiURL.Host
			copy.Host = apiURL.Host
			return direct.RoundTrip(copy)
		})
	}, func() { handleStopListener(); handleCloseConnections() }, engine, managedTotals)
	previous := managedAccount
	managedAccount = c
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.Reset(ctx)
		managedAccount = previous
	})
	state, err := c.Login(context.Background(), managed.LoginParams{Email: fixture.Emails["vip"], Password: fixture.Password, DeviceID: "44444444-4444-4444-8444-444444444444", Platform: "macos", AppVersion: "catalog-acceptance"})
	if err != nil || state.User == nil {
		t.Fatal("official Core login failed")
	}
	state, err = c.LoadConfig(context.Background(), state.Generation)
	if err != nil || state.Configuration == nil || len(state.Configuration.Groups) != 1 || len(state.Configuration.Groups[0].Proxies) != 2 {
		t.Fatalf("database catalog did not reach official Core: %v / %s", err, state.ErrorCode)
	}
	selectNode := func(name string, rate int64) {
		t.Helper()
		state, err = c.SelectProxy(context.Background(), state.Generation, state.Configuration.ID, "VPN", name)
		if err != nil || state.Configuration == nil || state.Configuration.Groups[0].Selected != name || state.Session.RatePermille != rate || state.CanConnect || isRunning.Load() {
			t.Fatalf("node switch did not settle/stop/rebind: %v / %s", err, state.ErrorCode)
		}
	}
	selectNode("Catalog-Half-Edited", 500)
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	_ = reservation.Close()
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	connect := func() {
		t.Helper()
		state, err = c.Connect(context.Background(), state.Generation, port)
		if err != nil || !state.CanConnect || !isRunning.Load() {
			t.Fatalf("official connection denied: %v / %s", err, state.ErrorCode)
		}
	}
	transfer := func() {
		t.Helper()
		payload := strings.Repeat("catalog-traffic-", 100)
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, e := client.Post(fixture.Target, "application/octet-stream", strings.NewReader(payload))
		if e != nil {
			t.Fatal("real proxy transfer failed", e)
		}
		body, e := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if e != nil || response.StatusCode != 200 || !bytes.Equal(body, []byte("loopback-fixture:"+payload)) {
			t.Fatal("proxy transfer payload mismatch")
		}
	}
	control := func(command string) map[string]int64 {
		t.Helper()
		r, _ := http.NewRequest("GET", fixture.API+"/fixture/"+command, nil)
		r.Header.Set("X-Acceptance-Control", fixture.Control)
		response, e := (&http.Client{Transport: direct, Timeout: 5 * time.Second}).Do(r)
		if e != nil {
			t.Fatal("fixture control failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 204 {
			t.Fatal("fixture control denied")
		}
		result := map[string]int64{}
		if response.StatusCode == 200 && json.NewDecoder(response.Body).Decode(&result) != nil {
			t.Fatal("fixture ledger response invalid")
		}
		return result
	}
	baselineUp, baselineDown := statistic.DefaultManager.TotalTraffic(true)
	connect()
	transfer()
	state, err = c.Disconnect(context.Background(), state.Generation)
	if err != nil || state.CanConnect || state.Metering.Pending {
		t.Fatal("first transfer tail did not settle")
	}
	up, down := statistic.DefaultManager.TotalTraffic(true)
	first := control("state")
	if first["upload_bytes"] != up-baselineUp || first["download_bytes"] != down-baselineDown || up-baselineUp < 1600 || down-baselineDown < 1600 || first["charged_units"] != (up+down-baselineUp-baselineDown)*500 || first["used_units"] != first["charged_units"] {
		t.Fatal("real PostgreSQL 0.5x ledger does not match Mihomo cumulative counters")
	}
	selectNode("Catalog-Premium", 1000)
	connect()
	transfer()
	selectNode("Catalog-Half-Edited", 500)
	finalUp, finalDown := statistic.DefaultManager.TotalTraffic(true)
	second := control("state")
	if second["upload_bytes"] != finalUp-baselineUp || second["download_bytes"] != finalDown-baselineDown || second["charged_units"]-first["charged_units"] != (finalUp+finalDown-up-down)*1000 || second["used_units"] != second["charged_units"] {
		t.Fatal("live node switch charged old traffic at the new multiplier")
	}
	connect()
	control("expire")
	state, err = c.Refresh(context.Background(), state.Generation)
	if err != nil || state.CanConnect || isRunning.Load() || state.Session == nil || state.Session.Reason != "subscription_expired" {
		t.Fatal("expired entitlement did not stop the official runtime")
	}
	denied, _ := c.Connect(context.Background(), state.Generation, port)
	if denied.CanConnect || isRunning.Load() {
		t.Fatal("expired subscription reconnected")
	}
	control("renew")
	if output := os.Getenv("CATALOG_OUTPUTS"); output != "" {
		evidence, _ := json.MarshalIndent(map[string]any{"passed": true, "half_rate_ledger": first, "combined_ledger": second, "metering_source": "client_reported", "node_authoritative": false}, "", "  ")
		if os.WriteFile(filepath.Join(output, "traffic-evidence.json"), evidence, 0600) != nil {
			t.Fatal("could not retain safe traffic evidence")
		}
	}
	t.Logf("real Gin/PostgreSQL/Mihomo: half-rate raw=%d bytes charged=%d units; combined raw=%d bytes charged=%d units", first["upload_bytes"]+first["download_bytes"], first["charged_units"], second["upload_bytes"]+second["download_bytes"], second["charged_units"])
}
