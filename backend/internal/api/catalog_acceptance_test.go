package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
)

func TestNodeCatalogAcceptanceFixture(t *testing.T) {
	dir := os.Getenv("CATALOG_ACCEPTANCE_DIR")
	if dir == "" {
		t.Skip("only run with the private node catalog acceptance driver")
	}
	origin, err := url.Parse(os.Getenv("CATALOG_WEB_ORIGIN"))
	if !strings.HasPrefix(dir, "/tmp/vpn-catalog-") || err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.Path != "" {
		t.Fatal("private fixture directory and loopback origin required")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	cfg.ClientNodeCatalog = true
	cfg.NodeEncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{71}, 32))
	cfg.Origins = []string{origin.String()}
	password, control := uuid.NewString(), uuid.NewString()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		t.Fatal(err)
	}
	emails := map[string]string{}
	var vip, lite model.User
	for _, kind := range []string{"admin", "vip", "lite"} {
		role := "user"
		if kind == "admin" {
			role = "admin"
		}
		u := model.User{ID: uuid.NewString(), Email: kind + "-" + uuid.NewString() + "@example.invalid", Name: "Catalog " + kind, PasswordHash: string(hash), Role: role, Status: "active"}
		if err = db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		emails[kind] = u.Email
		if kind == "lite" {
			// A 轻量计划 (starter) customer for the round-4 presence and history pages.
			lite = u
			now := time.Now().UTC()
			sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "starter", PlanName: "轻量计划", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), TrafficLimitBytes: 100 << 30, MaxDevices: 2}
			if err = db.Create(&sub).Error; err != nil {
				t.Fatal(err)
			}
		}
		if kind == "vip" {
			vip = u
			now := time.Now().UTC()
			sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "Catalog VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 1 << 20, MaxDevices: 2, AllowDedicated: true}
			if err = db.Create(&sub).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	loopback := startNativeLoopbackFixture(t, "")
	_, nodePort, _ := net.SplitHostPort(loopback.proxyAddress)
	nodeYAML := fmt.Sprintf("proxies: [{name: Catalog-Half, type: socks5, server: 127.0.0.1, port: %s}]\n", nodePort)
	// Registration runs the real code flow; mail goes to the in-process capture Mailer and
	// codes are read back only through the control-token fixture endpoint below.
	cfg = withTestMail(cfg)
	mail := newMailHarness(db, cfg, nil)
	mail.worker.Poll = 200 * time.Millisecond
	mailCtx, stopMail := context.WithCancel(context.Background())
	mailDone := make(chan struct{})
	go func() { defer close(mailDone); mail.worker.Run(mailCtx) }()
	defer func() { stopMail(); <-mailDone }()
	router := newServer(db, cfg, mail.service)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/fixture/") {
			router.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Acceptance-Control") != control {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/fixture/mail":
			msgs := mail.box.to(r.URL.Query().Get("to"))
			code, ok := "", false
			if len(msgs) > 0 {
				code, ok = codeIn(msgs[len(msgs)-1])
			}
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "count": len(msgs)})
		case "/fixture/reset-link":
			// The newest reset link to an address, as its recipient would read it.
			const marker = "/reset-password#token="
			msgs := mail.box.to(r.URL.Query().Get("to"))
			token := ""
			for i := len(msgs) - 1; i >= 0 && token == ""; i-- {
				if at := strings.Index(msgs[i].Text, marker); at >= 0 && len(msgs[i].Text) >= at+len(marker)+43 {
					token = msgs[i].Text[at+len(marker) : at+len(marker)+43]
				}
			}
			if token == "" {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "count": len(msgs)})
		case "/fixture/historical-user":
			// An account as it existed before mailbox verification: unverified, AuthVersion 1,
			// signing in with the fixture password.
			u := model.User{ID: uuid.NewString(), Email: r.URL.Query().Get("email"), Name: "Reset Fixture", PasswordHash: string(hash), Role: "user", Status: "active"}
			if !strings.HasSuffix(u.Email, ".invalid") || db.Create(&u).Error != nil {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
		case "/fixture/account":
			var users []model.User
			if e := db.Where("email = ?", r.URL.Query().Get("email")).Find(&users).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			out := map[string]any{"count": len(users)}
			if len(users) == 1 {
				out["role"], out["status"], out["verified"] = users[0].Role, users[0].Status, users[0].EmailVerifiedAt != nil
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		case "/fixture/state":
			var totals struct{ Upload, Download, Charged int64 }
			var subscription model.Subscription
			if e := db.Model(&model.ClientTrafficReport{}).Where("user_id = ?", vip.ID).Select("COALESCE(SUM(upload_delta),0) AS upload, COALESCE(SUM(download_delta),0) AS download, COALESCE(SUM(charged_units),0) AS charged").Scan(&totals).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			if e := db.First(&subscription, "user_id = ?", vip.ID).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"upload_bytes": totals.Upload, "download_bytes": totals.Download, "charged_units": totals.Charged, "used_units": subscription.UsedUnits})
		case "/fixture/usage-history":
			// Fixed history for the 轻量计划 customer across 26 hours relative to now: the
			// oldest row is outside the 24-hour window and must never be shown.
			now := time.Now().UTC()
			const mib = 1 << 20
			rows := []struct {
				ago              time.Duration
				upload, download int64
			}{{26 * time.Hour, 900, 900}, {23*time.Hour + 30*time.Minute, 3, 12}, {20 * time.Hour, 40, 200}, {14 * time.Hour, 1, 0},
				{9 * time.Hour, 300, 1500}, {4 * time.Hour, 0, 64}, {10 * time.Minute, 2, 8}, {0, 5, 25}}
			for _, row := range rows {
				bucket := model.UsageBucket{UserID: lite.ID, BucketStart: model.UsageBaseline(now.Add(-row.ago)), UploadBytes: row.upload * mib, DownloadBytes: row.download * mib,
					ChargedUnits: (row.upload + row.download) * mib * 500, UpdatedAt: now}
				if e := db.Omit("User").Save(&bucket).Error; e != nil {
					w.WriteHeader(500)
					return
				}
			}
			w.WriteHeader(204)
		case "/fixture/expire", "/fixture/renew":
			expires := time.Now().Add(time.Hour)
			if r.URL.Path == "/fixture/expire" {
				expires = time.Now().Add(-time.Second)
			}
			if e := db.Model(&model.Subscription{}).Where("user_id = ?", vip.ID).Update("expires_at", expires).Error; e != nil {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	metadata := map[string]any{"api": server.URL, "password": password, "control": control, "emails": emails, "traffic_target": "http://" + loopback.targetAddress + "/fixture", "node_yaml": nodeYAML}
	data, _ := json.Marshal(metadata)
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
			t.Fatal("catalog acceptance driver did not stop fixture")
		}
	}
}
