//go:build desktop_acceptance

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vpn/backend/internal/model"
)

func TestDesktopAcceptanceFixture(t *testing.T) {
	dir := os.Getenv("DESKTOP_ACCEPTANCE_DIR")
	if dir == "" {
		t.Skip("only run with the isolated desktop acceptance driver")
	}
	if !strings.HasPrefix(dir, "/tmp/flclash-acceptance-") {
		t.Fatal("private acceptance directory required")
	}
	db, cfg := newIsolatedNativeDatabase(t)
	password, control := uuid.NewString(), uuid.NewString()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cfg.BcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	emails := map[string]string{}
	for _, kind := range []string{"free", "vip", "vip2", "expired", "quota"} {
		u := model.User{ID: uuid.NewString(), Email: kind + "-" + uuid.NewString() + "@example.invalid", Name: "Acceptance", PasswordHash: string(hash), Role: "user", Status: "active"}
		if err = db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		emails[kind] = u.Email
		if kind == "free" {
			continue
		}
		now := time.Now().UTC()
		sub := model.Subscription{ID: uuid.NewString(), UserID: u.ID, PlanID: "pro", PlanName: "Acceptance VIP", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 1048576, MaxDevices: 2}
		if kind == "expired" {
			sub.ExpiresAt = now.Add(-time.Second)
		}
		if kind == "quota" {
			sub.UsedUnits = sub.TrafficLimitBytes * 1000
		}
		if err = db.Create(&sub).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		_, _ = w.Write(bytes.Repeat([]byte("download"), 4096))
	}))
	defer target.Close()
	destination := strings.TrimPrefix(target.URL, "http://")
	var connections sync.Map
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != destination {
			w.WriteHeader(403)
			return
		}
		remote, e := net.DialTimeout("tcp", destination, time.Second)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		defer remote.Close()
		conn, buffer, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer conn.Close()
		connections.Store(conn, remote)
		defer connections.Delete(conn)
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, buffer); _ = remote.Close(); close(done) }()
		_, _ = io.Copy(conn, remote)
		_ = conn.Close()
		<-done
	}))
	defer func() {
		connections.Range(func(k, v any) bool { _ = k.(net.Conn).Close(); _ = v.(net.Conn).Close(); return true })
		node.Close()
	}()
	_, nodePort, _ := net.SplitHostPort(strings.TrimPrefix(node.URL, "http://"))
	profile := fmt.Sprintf("mixed-port: 17995\nexternal-controller: 127.0.0.1:17996\ntun: {enable: true}\nproxies:\n  - {name: Acceptance-Loopback, type: http, server: 127.0.0.1, port: %s}\n  - {name: Acceptance-Alternate, type: http, server: 127.0.0.1, port: %s}\nproxy-groups:\n  - name: VIP\n    type: select\n    proxies: [Acceptance-Loopback, Acceptance-Alternate]\nrules: ['MATCH,VIP']\n", nodePort, nodePort)
	if err = os.WriteFile(filepath.Join(cfg.ClientProfileDir, "pro.yaml"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	router := New(db, cfg)
	var delayNext atomic.Bool
	var delayConfig atomic.Bool
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fixture/") {
			if r.Header.Get("X-Acceptance-Control") != control {
				w.WriteHeader(403)
				return
			}
			switch r.URL.Path {
			case "/fixture/delay":
				delayNext.Store(true)
				w.WriteHeader(204)
			case "/fixture/delay-config":
				delayConfig.Store(true)
				w.WriteHeader(204)
			case "/fixture/config-valid", "/fixture/config-invalid", "/fixture/config-changed", "/fixture/config-missing":
				path := filepath.Join(cfg.ClientProfileDir, "pro.yaml")
				var operationErr error
				if r.URL.Path == "/fixture/config-missing" {
					operationErr = os.Remove(path)
				} else {
					value := profile
					if r.URL.Path == "/fixture/config-invalid" {
						value = strings.ReplaceAll(value, "MATCH,VIP", "MATCH,MISSING")
					}
					if r.URL.Path == "/fixture/config-changed" {
						value = strings.ReplaceAll(value, "Acceptance-Alternate", "Acceptance-Rotated")
					}
					pending := path + ".pending"
					operationErr = os.WriteFile(pending, []byte(value), 0600)
					if operationErr == nil {
						operationErr = os.Rename(pending, path)
					}
					_ = os.Remove(pending)
				}
				if operationErr != nil {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(204)
			case "/fixture/expire":
				if e := db.Model(&model.NativeSession{}).Where("revoked_at IS NULL").Update("expires_at", time.Now().Add(-time.Second)).Error; e != nil {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(204)
			case "/fixture/state":
				var reports, active int64
				var ledger struct{ Upload, Download, Charged int64 }
				if e := db.Model(&model.ClientTrafficReport{}).Select("COALESCE(SUM(upload_delta),0) AS upload, COALESCE(SUM(download_delta),0) AS download, COALESCE(SUM(charged_units),0) AS charged").Scan(&ledger).Error; e != nil {
					w.WriteHeader(500)
					return
				}
				db.Model(&model.ClientTrafficReport{}).Count(&reports)
				db.Model(&model.NativeSession{}).Where("revoked_at IS NULL AND expires_at > ?", time.Now()).Count(&active)
				mu.Lock()
				defer mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"requests": counts, "reports": reports, "active_sessions": active, "upload_bytes": ledger.Upload, "download_bytes": ledger.Download, "charged_units": ledger.Charged})
			default:
				w.WriteHeader(404)
			}
			return
		}
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/api/v1/client/login" && delayNext.Swap(false) {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		if r.URL.Path == "/api/v1/client/config" && delayConfig.Swap(false) {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		router.ServeHTTP(w, r)
	}))
	defer server.Close()
	metadata := map[string]any{"api": server.URL, "password": password, "control": control, "emails": emails, "traffic_target": target.URL}
	data, _ := json.Marshal(metadata)
	if err = os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(25 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("acceptance driver did not stop fixture")
		}
	}
}
