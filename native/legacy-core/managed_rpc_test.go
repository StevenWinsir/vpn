//go:build !cgo

package main

import (
	"context"
	"core/managed"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type managedRoundTrip func(*http.Request) (*http.Response, error)

func (f managedRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var managedTestCall atomic.Uint64

func managedRPC(t *testing.T, method CoreMethod, args string) MethodResponse {
	t.Helper()
	fake := &fakeConn{}
	previous := swapConn(fake)
	defer swapConn(previous)
	id := fmt.Sprintf("managed-%d", managedTestCall.Add(1))
	call := &MethodCall{ID: id, Method: method, Arguments: json.RawMessage(args)}
	handleMethodCall(call, newMethodResponse(id, nil))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, frame := range fake.frames(t) {
			var response MethodResponse
			if json.Unmarshal(frame, &response) == nil && response.ID == id {
				return response
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("managed RPC response timed out")
	return MethodResponse{}
}

func rpcAccount(t *testing.T, response MethodResponse) managed.AccountSnapshot {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("managed RPC failed: %s", response.Error.Code)
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var state managed.AccountSnapshot
	if json.Unmarshal(encoded, &state) != nil {
		t.Fatal("account response is not a structured object")
	}
	for _, forbidden := range []string{"\"token\"", "\"password\"", "\"yaml\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("private field escaped through RPC")
		}
	}
	return state
}

func TestManagedRPCLoginLoadStatusFlushAndLogoutStaySeparate(t *testing.T) {
	data, err := os.ReadFile("../../shared/contracts/native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases map[string]struct {
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var login map[string]json.RawMessage
	_ = json.Unmarshal(fixture.Cases["login"].Response, &login)
	login["session"] = fixture.Cases["vip_unbound"].Response
	var configured managed.Profile
	if json.Unmarshal(fixture.Cases["vip_ready"].Response, &configured) != nil {
		t.Fatal("invalid config fixture")
	}
	configured.YAML = managedFixtureYAML
	digest := sha256.Sum256([]byte(configured.YAML))
	configured.Version = hex.EncodeToString(digest[:])
	configured.Session.ProfileVersion = configured.Version
	engine := newManagedTestEngine(t)
	var reports, logouts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			if r.Header.Get("Authorization") != "" {
				t.Error("anonymous login sent Bearer")
			}
			_ = json.NewEncoder(w).Encode(login)
		case "/config":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Error("config did not use native auth")
			}
			_ = json.NewEncoder(w).Encode(configured)
		case "/session":
			_, _ = w.Write(fixture.Cases["vip_unbound"].Response)
		case "/traffic":
			reports.Add(1)
			w.WriteHeader(500)
		case "/logout":
			logouts.Add(1)
			_, _ = w.Write(fixture.Cases["logout"].Response)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	transport := managed.NewTransport(nil)
	defer transport.CloseIdleConnections()
	previous := managedAccount
	managedAccount = managed.NewCoordinator(func() http.RoundTripper {
		return managedRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Scheme != "https" || r.URL.Host != "demo.hyshentou.cn" || !strings.HasPrefix(r.URL.Path, "/api/v1/client/") {
				t.Error("production API target is not pinned")
			}
			copy := r.Clone(r.Context())
			copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
			copy.URL.Path = strings.TrimPrefix(copy.URL.Path, "/api/v1/client")
			return transport.RoundTrip(copy)
		})
	}, func() { handleStopListener(); handleCloseConnections() }, engine)
	defer func() { _, _ = managedAccount.Reset(context.Background()); managedAccount = previous }()
	if state := rpcAccount(t, managedRPC(t, managedResetMethod, "null")); state.User != nil || state.CanConnect {
		t.Fatal("reset gate open")
	}
	state := rpcAccount(t, managedRPC(t, managedLoginMethod, `{"email":"fixture@example.invalid","password":"fixture-password","device_id":"33333333-3333-4333-8333-333333333333","platform":"macos","app_version":"test"}`))
	if state.Phase != "profile_required" {
		t.Fatal("login not routed to Login")
	}
	args := fmt.Sprintf(`{"generation":%d}`, state.Generation)
	state = rpcAccount(t, managedRPC(t, managedLoadConfigMethod, args))
	if state.Phase != "configuration_applied" || state.CanConnect {
		t.Fatal("load routed incorrectly or prematurely authorized")
	}
	if state = rpcAccount(t, managedRPC(t, managedStatusMethod, "null")); state.Phase != "configuration_applied" {
		t.Fatal("status erased staged configuration")
	}
	flush := managedRPC(t, managedFlushMethod, args)
	if flush.Error == nil || flush.Error.Code != "meter_not_ready" || reports.Load() != 0 || logouts.Load() != 0 {
		t.Fatal("flush misrouted or fabricated traffic")
	}
	if response := managedRPC(t, startListenerMethod, "null"); response.Error != nil || response.Result != false || isRunning.Load() {
		t.Fatal("staged config bypassed listener gate")
	}
	for _, method := range []CoreMethod{setupConfigMethod, updateConfigMethod, validateConfigMethod, getConfigMethod, getProxiesMethod, changeProxyMethod, getExternalProvidersMethod, getExternalProviderMethod, updateExternalProviderMethod, sideLoadExternalProviderMethod, asyncTestDelayMethod, updateGeoDataMethod, clearEffectMethod} {
		response := managedRPC(t, method, `{}`)
		if response.Error == nil || response.Error.Code != "managed_connection_required" {
			t.Fatalf("%s bypassed gate", method)
		}
	}
	state = rpcAccount(t, managedRPC(t, managedLogoutMethod, args))
	if state.User != nil || state.Session != nil || state.ProfileVersion != "" || logouts.Load() != 1 {
		t.Fatal("logout not routed separately")
	}
	stale := managedRPC(t, managedLoadConfigMethod, args)
	if stale.Error == nil || stale.Error.Code != "operation_superseded" {
		t.Fatal("old generation survived logout")
	}
}

func TestManagedRPCArgumentErrorsAreBoundedAndSanitized(t *testing.T) {
	for _, args := range []string{"null", `{"password":"private-fixture","base":"https://other.invalid"}`, `{"email":true,"password":"private-fixture"}`, `{"password":"` + strings.Repeat("private-fixture", 600) + `"}`} {
		response := managedRPC(t, managedLoginMethod, args)
		if response.Error == nil || response.Error.Code != "invalid_arguments" {
			t.Fatal("bad login arguments accepted")
		}
		data, _ := json.Marshal(response)
		if strings.Contains(string(data), "private-fixture") || strings.Contains(string(data), "other.invalid") {
			t.Fatal("request contents escaped in error")
		}
	}
	for _, args := range []string{`{"generation":-1}`, `{"generation":1.5}`, `{"generation":1,"token":"private-fixture"}`} {
		response := managedRPC(t, managedLoadConfigMethod, args)
		if response.Error == nil || response.Error.Code != "invalid_arguments" {
			t.Fatal("malformed generation accepted")
		}
	}
}
