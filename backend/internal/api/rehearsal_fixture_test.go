package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/config"
	"vpn/backend/internal/store"
)

// legacyBackend answers as the release before the referral programme does, for the mixed
// deployment check (handoff_coupon.md 15.1, R3-02): /meta has no referral_enabled, the
// referral routes do not exist, and a registration that carries referral_code is refused
// as an unknown field. It is an emulation in front of the current API, switched on and off
// by a control-token endpoint; the rehearsal's normal binaries cannot receive a mailbox code.
func legacyBackend(w http.ResponseWriter, r *http.Request, router http.Handler) bool {
	switch {
	case strings.Contains(r.URL.Path, "/referral"):
		http.NotFound(w, r)
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/meta":
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		var meta map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &meta) != nil {
			return false
		}
		delete(meta, "referral_enabled")
		for name, values := range rec.Header() {
			if name != "Content-Length" {
				w.Header()[name] = values
			}
		}
		w.WriteHeader(rec.Code)
		_ = json.NewEncoder(w).Encode(meta)
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/register":
		body, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil {
			if _, known := fields["referral_code"]; known {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_json","message":"请求格式或字段不正确"}}`))
				return true
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	return false
}

// TestReleaseRehearsalFixture serves the current API, loaded from the environment exactly like
// cmd/api, over the release rehearsal's already-migrated private database. The one difference
// from the binary is the mail transport: the real worker delivers into an in-process capture
// Mailer, because a test SMTP server cannot be made trusted for the production TLS check without
// changing the machine's trust store. Codes and reset links are read back only through the
// control-token endpoints below, which exist in this test assembly and nowhere else.
func TestReleaseRehearsalFixture(t *testing.T) {
	dir := os.Getenv("REHEARSAL_FIXTURE_DIR")
	if dir == "" {
		t.Skip("only run with scripts/test-release-rehearsal.py")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, "/tmp/vpn-release-") || cfg.Env != "test" || cfg.Host != "127.0.0.1" || cfg.Schema != "rehearsal" || !cfg.RegistrationOpen() {
		t.Fatal("private rehearsal database, test environment and test mail configuration required")
	}
	db, err := store.Open(cfg)
	if err != nil {
		t.Fatal("rehearsal database connection failed")
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	control := uuid.NewString()
	mail := newMailHarness(db, cfg, nil)
	mail.worker.Poll = 200 * time.Millisecond
	mailCtx, stopMail := context.WithCancel(context.Background())
	mailDone := make(chan struct{})
	go func() { defer close(mailDone); mail.worker.Run(mailCtx) }()
	defer func() { stopMail(); <-mailDone }()
	router := newServer(db, cfg, mail.service)
	var legacy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/fixture/") {
			if legacy.Load() && legacyBackend(w, r, router) {
				return
			}
			router.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Acceptance-Control") != control {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/fixture/legacy-backend" {
			legacy.Store(r.URL.Query().Get("on") == "1")
			w.WriteHeader(204)
			return
		}
		msgs := mail.box.to(r.URL.Query().Get("to"))
		out := map[string]any{"count": len(msgs)}
		switch r.URL.Path {
		case "/fixture/mail":
			if len(msgs) > 0 {
				if code, ok := codeIn(msgs[len(msgs)-1]); ok {
					out["code"] = code
				}
			}
		case "/fixture/reset-link":
			const marker = "/reset-password#token="
			for i := len(msgs) - 1; i >= 0 && out["token"] == nil; i-- {
				if at := strings.Index(msgs[i].Text, marker); at >= 0 && len(msgs[i].Text) >= at+len(marker)+43 {
					out["token"] = msgs[i].Text[at+len(marker) : at+len(marker)+43]
				}
			}
		default:
			w.WriteHeader(404)
			return
		}
		if out["code"] == nil && out["token"] == nil {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	data, _ := json.Marshal(map[string]any{"api": server.URL, "control": control})
	if err = os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(20 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("release rehearsal driver did not stop the fixture")
		}
	}
}
