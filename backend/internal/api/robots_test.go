package api

import (
	"net/http/httptest"
	"testing"
)

// handoff_seo.md S1-10: the API host has a robots.txt that turns every crawler away, and every
// answer (success, client error, missing route, refused origin) carries X-Robots-Tag.
func TestAPIHostIsNeverIndexed(t *testing.T) {
	engine := New(nil, metaConfig())
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("GET", "/robots.txt", nil))
	if w.Code != 200 || w.Body.String() != "User-agent: *\nDisallow: /\n" {
		t.Fatalf("robots.txt: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("robots.txt content type %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("robots.txt cache %q", got)
	}
	for _, c := range []struct {
		method, path, origin string
		status               int
	}{
		{"GET", "/robots.txt", "", 200},
		{"GET", "/healthz", "", 200},
		{"GET", "/api/v1/meta", "", 200},
		{"GET", "/api/v1/auth/me", "", 401},
		{"GET", "/api/v1/no-such-route", "", 404},
		{"GET", "/no-such-page", "", 404},
		{"POST", "/api/v1/auth/login", "https://elsewhere.example", 403},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(c.method, c.path, nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		engine.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Errorf("%s %s: status %d, want %d", c.method, c.path, w.Code, c.status)
		}
		if got := w.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
			t.Errorf("%s %s: X-Robots-Tag %q", c.method, c.path, got)
		}
	}
}
