package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseBase(t *testing.T) {
	good := []struct{ url, channel string }{
		{"https://demo.hyshentou.cn/api/v1/client", "release"},
		{"https://demo.hyshentou.cn/api/v1/client", "development"},
		{"http://127.0.0.1:8080/api/v1/client", "development"},
		{"http://localhost:8080/api/v1/client", "development"},
	}
	for _, c := range good {
		if _, err := ParseBase(c.url, c.channel); err != nil {
			t.Errorf("%s (%s) rejected", c.url, c.channel)
		}
	}
	bad := []struct{ url, channel string }{
		{"http://127.0.0.1:8080/api/v1/client", "release"},
		{"http://demo.hyshentou.cn/api/v1/client", "development"},
		{"https://demo.hyshentou.cn/api/v1/client/", "release"},
		{"https://demo.hyshentou.cn", "release"},
		{"https://demo.hyshentou.cn/api/v1/client?x=1", "release"},
		{"https://demo.hyshentou.cn/api/v1/client#f", "release"},
		{"https://user:pw@demo.hyshentou.cn/api/v1/client", "release"},
		{"ftp://demo.hyshentou.cn/api/v1/client", "release"},
		{"", "release"},
	}
	for _, c := range bad {
		if _, err := ParseBase(c.url, c.channel); err == nil {
			t.Errorf("%q (%s) accepted", c.url, c.channel)
		}
	}
}

func clientFor(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL + apiPath)
	return New(base)
}

func TestErrorEnvelopeUsesOnlyTheCode(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":"device_limit","message":"<script>boom</script>","yaml":"proxies: leaked"}}`))
	})
	_, err := c.Session(context.Background(), "t")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "device_limit" || apiErr.Transient {
		t.Fatalf("%v", err)
	}
	if strings.Contains(apiErr.Error(), "leaked") || strings.Contains(apiErr.Error(), "boom") {
		t.Fatal("server text surfaced in the error")
	}
}

func TestStatusClassification(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		code      string
		transient bool
	}{
		{429, `{}`, "rate_limited", true},
		{503, `<html>`, "client_service_unavailable", true},
		{503, `{"error":{"code":"client_config_unavailable"}}`, "client_config_unavailable", true},
		{401, `{"error":{"code":"native_session_expired"}}`, "native_session_expired", false},
		{400, `not json`, "bad_response", false},
		{409, `{"error":{"code":"has space"}}`, "bad_response", false},
	}
	for _, tc := range cases {
		c := clientFor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) })
		_, err := c.Session(context.Background(), "t")
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.Transient != tc.transient {
			t.Errorf("status %d body %q: got %+v", tc.status, tc.body, err)
		}
	}
}

// A redirect must never carry the bearer token elsewhere.
func TestRedirectIsNotFollowed(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	defer target.Close()
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	_, err := c.Session(context.Background(), "secret-token")
	if err == nil || leaked.Load() {
		t.Fatalf("redirect followed: err=%v leaked=%v", err, leaked.Load())
	}
}

func TestOversizedAndMalformedBodies(t *testing.T) {
	big := clientFor(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("a", maxResponseSize+10))) })
	if _, err := big.Session(context.Background(), "t"); err == nil || err.(*Error).Code != "bad_response" {
		t.Fatalf("oversized body: %v", err)
	}
	bad := clientFor(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"session_id":`)) })
	if _, err := bad.Session(context.Background(), "t"); err == nil || err.(*Error).Code != "bad_response" {
		t.Fatalf("malformed body: %v", err)
	}
}

func TestRequestShape(t *testing.T) {
	var method, path, query, auth, cache string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, query, auth, cache = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Cache-Control")
		w.Write([]byte(`{"yaml":"x","version":"v","session":{},"node_id":"n"}`))
	})
	if _, err := c.Config(context.Background(), "tok", "node-1", 7); err != nil {
		t.Fatal(err)
	}
	if method != "GET" || path != "/api/v1/client/config" || query != "last_sequence=7&node_id=node-1" || auth != "Bearer tok" || cache != "no-store" {
		t.Fatalf("%s %s ?%s %s %s", method, path, query, auth, cache)
	}
}

func TestUnreachableIsTransient(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	base, _ := url.Parse(server.URL + apiPath)
	server.Close()
	_, err := New(base).Session(context.Background(), "t")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "network_unreachable" || !apiErr.Transient {
		t.Fatalf("%v", err)
	}
}

func TestMessagesCoverAllControlCodes(t *testing.T) {
	for _, code := range []string{"invalid_credentials", "native_session_expired", "upgrade_required", "paid_vip_required", "subscription_expired", "quota_exhausted", "profile_required", "profile_changed", "client_config_unavailable", "subscription_changed", "device_limit", "traffic_sequence", "traffic_conflict", "rate_limited"} {
		if _, ok := messages[code]; !ok {
			t.Errorf("no Chinese message for %s", code)
		}
	}
	if Message("totally_unknown") == "" {
		t.Fatal("unknown codes need a generic message")
	}
	if !TerminatesSession("native_session_expired") || TerminatesSession("device_limit") {
		t.Fatal("session termination classification wrong")
	}
}
