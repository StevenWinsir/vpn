package api

import (
	"bytes"
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
	var vip model.User
	for _, kind := range []string{"admin", "vip"} {
		role := "user"
		if kind == "admin" {
			role = "admin"
		}
		u := model.User{ID: uuid.NewString(), Email: kind + "-" + uuid.NewString() + "@example.invalid", Name: "Catalog " + kind, PasswordHash: string(hash), Role: role, Status: "active"}
		if err = db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		emails[kind] = u.Email
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
	router := New(db, cfg)
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
