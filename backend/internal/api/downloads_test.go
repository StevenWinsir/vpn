package api

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// Client downloads, round 1 of handoff_download.md (DL1-01 … DL1-10).

const (
	dlSHA  = "BB155B2FE7F6651C71976E7CD867FCA7FEBD4E0C514D694C21C4D7E9568393A6"
	dlCert = "f5:eb:55:6c:08:7d:14:be:e4:51:9a:03:ae:68:a2:6d:ff:11:50:23:69:c1:64:54:d7:47:bb:48:cd:ed:7a:d4"
)

func dlBody(platform, arch, format, url string, extra map[string]any) map[string]any {
	body := map[string]any{"platform": platform, "arch": arch, "format": format, "app_version": "1.2.3", "url": url}
	if _, store := downloadStoreHosts[format]; !store {
		body["sha256"] = dlSHA
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

// The website reads the same catalogue from testdata (round 3); the two must not drift.
func TestClientDownloadCatalogSnapshot(t *testing.T) {
	data, err := os.ReadFile("testdata/client_downloads_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err = json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(catalogView())
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("testdata/client_downloads_catalog.json is stale:\n%s", encoded)
	}
	for _, p := range downloadCatalog {
		for _, f := range p.Formats {
			_, store := downloadStoreHosts[f]
			if _, file := downloadSuffixes[f]; file == store {
				t.Fatalf("format %s must be exactly one of file or store", f)
			}
		}
	}
}

// DL1-02 … DL1-04 without a database: every legal variant, and every rule of D7.
func TestValidateClientDownload(t *testing.T) {
	for _, p := range downloadCatalog {
		for _, arch := range p.Archs {
			for _, format := range p.Formats {
				url := "https://dl.example.com/AsterLink-1.2.3" + downloadSuffixes[format]
				extra := map[string]any{}
				if host, store := downloadStoreHosts[format]; store {
					url = "https://" + host + "/app/asterlink/id1"
				}
				if format == "ipa" {
					extra["bundle_id"] = "com.example.asterlink"
				}
				in := toInput(t, dlBody(p.Platform, arch, format, url, extra))
				if err := validateDownload(&in, true); err != nil {
					t.Errorf("%s/%s/%s refused: %v", p.Platform, arch, format, err)
				}
			}
		}
	}
	// Normalisation: upper case, colons and spaces are what the packaging tools print.
	in := toInput(t, dlBody(" android ", "arm64-v8a", "apk", " https://dl.example.com/a/AsterLink.APK?token=x ", map[string]any{"sha256": " " + dlSHA + " ", "signing_cert_sha256": strings.ToUpper(dlCert), "app_version": " 1.2.3-beta.1 "}))
	if err := validateDownload(&in, true); err != nil {
		t.Fatal(err)
	}
	if in.Platform != "android" || in.SHA256 != strings.ToLower(dlSHA) || in.SigningCertSHA256 != "f5eb556c087d14bee4519a03ae68a26dff11502369c16454d747bb48cded7ad4" || in.AppVersion != "1.2.3-beta.1" || strings.HasPrefix(in.URL, " ") {
		t.Fatalf("normalised %+v", in)
	}
	appimage := toInput(t, dlBody("linux", "x64", "appimage", "https://dl.example.com/AsterLink-x86_64.AppImage", nil))
	if err := validateDownload(&appimage, true); err != nil {
		t.Fatalf("suffix must be case-insensitive: %v", err)
	}
	loopback := toInput(t, dlBody("windows", "x64", "exe", "http://127.0.0.1:9000/AsterLink-Setup.exe", nil))
	if err := validateDownload(&loopback, false); err != nil {
		t.Fatalf("loopback http outside production: %v", err)
	}

	win := "https://dl.example.com/AsterLink-Setup-x64-1.2.3.exe"
	for _, tc := range []struct {
		name, field string
		body        map[string]any
		production  bool
	}{
		{"unknown platform", "platform", dlBody("beos", "x64", "exe", win, nil), true},
		{"arch of another platform", "arch", dlBody("windows", "arm64-v8a", "exe", win, nil), true},
		{"format of another platform", "format", dlBody("windows", "x64", "dmg", "https://dl.example.com/a.dmg", nil), true},
		{"version word", "app_version", dlBody("windows", "x64", "exe", win, map[string]any{"app_version": "latest"}), true},
		{"version too long", "app_version", dlBody("windows", "x64", "exe", win, map[string]any{"app_version": "1.2.3-" + strings.Repeat("a", 30)}), true},
		{"no url", "url", dlBody("windows", "x64", "exe", "", nil), true},
		{"plain http", "url", dlBody("windows", "x64", "exe", "http://dl.example.com/a.exe", nil), true},
		{"loopback http in production", "url", dlBody("windows", "x64", "exe", "http://127.0.0.1/a.exe", nil), true},
		{"plain http elsewhere outside production", "url", dlBody("windows", "x64", "exe", "http://dl.example.com/a.exe", nil), false},
		{"credentials", "url", dlBody("windows", "x64", "exe", "https://u:p@dl.example.com/a.exe", nil), true},
		{"javascript", "url", dlBody("windows", "x64", "exe", "javascript:alert(1)//a.exe", nil), true},
		{"fragment", "url", dlBody("windows", "x64", "exe", win+"#x", nil), true},
		{"wrong suffix", "url", dlBody("windows", "x64", "exe", "https://dl.example.com/a.msi", nil), true},
		{"suffix only in query", "url", dlBody("windows", "x64", "exe", "https://dl.example.com/get?f=a.exe", nil), true},
		{"too long", "url", dlBody("windows", "x64", "exe", "https://dl.example.com/"+strings.Repeat("a", 1020)+".exe", nil), true},
		{"store elsewhere", "url", dlBody("ios", "arm64", "app_store", "https://evil.example/app", nil), true},
		{"store over http", "url", dlBody("ios", "arm64", "testflight", "http://testflight.apple.com/join/x", nil), true},
		{"store with hash", "sha256", dlBody("ios", "arm64", "app_store", "https://apps.apple.com/app/id1", map[string]any{"sha256": dlSHA}), true},
		{"store with size", "size_bytes", dlBody("ios", "arm64", "app_store", "https://apps.apple.com/app/id1", map[string]any{"size_bytes": 10}), true},
		{"file without hash", "sha256", dlBody("windows", "x64", "exe", win, map[string]any{"sha256": nil}), true},
		{"short hash", "sha256", dlBody("windows", "x64", "exe", win, map[string]any{"sha256": "abc"}), true},
		{"zero size", "size_bytes", dlBody("windows", "x64", "exe", win, map[string]any{"size_bytes": 0}), true},
		{"huge size", "size_bytes", dlBody("windows", "x64", "exe", win, map[string]any{"size_bytes": int64(9) << 30}), true},
		{"cert on windows", "signing_cert_sha256", dlBody("windows", "x64", "exe", win, map[string]any{"signing_cert_sha256": dlCert}), true},
		{"bad cert", "signing_cert_sha256", dlBody("android", "arm64-v8a", "apk", "https://dl.example.com/a.apk", map[string]any{"signing_cert_sha256": "nope"}), true},
		{"ipa without bundle", "bundle_id", dlBody("ios", "arm64", "ipa", "https://dl.example.com/a.ipa", nil), true},
		{"bad bundle", "bundle_id", dlBody("ios", "arm64", "ipa", "https://dl.example.com/a.ipa", map[string]any{"bundle_id": "nodots"}), true},
		{"bundle on android", "bundle_id", dlBody("android", "arm64-v8a", "apk", "https://dl.example.com/a.apk", map[string]any{"bundle_id": "com.example.a"}), true},
	} {
		in := toInput(t, tc.body)
		err := validateDownload(&in, tc.production)
		detailed, ok := err.(*detailedError)
		if !ok || detailed.Extra["field"] != tc.field {
			t.Errorf("%s: got %v, want a %s error", tc.name, err, tc.field)
		}
	}
}

func toInput(t *testing.T, body map[string]any) downloadInput {
	t.Helper()
	data, _ := json.Marshal(body)
	var in downloadInput
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

type savedDownload struct {
	Download model.ClientDownload `json:"download"`
}

// DL1-01, DL1-05 … DL1-10 against the real router and PostgreSQL.
func TestClientDownloadsConsoleAndPublic(t *testing.T) {
	e := newConsoleEnv(t, nil)
	user := e.user("download-user", "user")
	for _, call := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/console/downloads"},
		{"POST", "/api/v1/admin/console/downloads"},
		{"POST", "/api/v1/admin/console/downloads/" + uuid.NewString()},
		{"DELETE", "/api/v1/admin/console/downloads/" + uuid.NewString()},
	} {
		var body any = map[string]any{} // like the route matrix: every write sends JSON
		if call.method == "POST" {
			body = dlBody("windows", "x64", "exe", "https://dl.example.com/a.exe", nil)
		} else if call.method == "GET" {
			body = nil
		}
		e.want(e.call(nil, call.method, call.path, body, ""), 401)
		e.code(e.call(user.cookies, call.method, call.path, body, ""), 403, "admin_required")
	}

	var listed struct {
		Downloads []model.ClientDownload `json:"downloads"`
		Catalog   downloadCatalogView    `json:"catalog"`
	}
	e.decode(e.get("/downloads"), 200, &listed)
	if len(listed.Downloads) != 0 || len(listed.Catalog.Platforms) != 5 || listed.Catalog.Platforms[0].Platform != "windows" || listed.Catalog.Limit != maxClientDownloads {
		t.Fatalf("empty list %+v", listed)
	}

	create := func(body map[string]any, status int) model.ClientDownload {
		t.Helper()
		var out savedDownload
		e.decode(e.post("/downloads", body), status, &out)
		return out.Download
	}
	start := e.clock.Now()
	// Created out of page order on purpose.
	linux := create(dlBody("linux", "x64", "appimage", "https://dl.example.com/AsterLink-1.2.3-x86_64.AppImage", nil), 201)
	win := create(dlBody("windows", "x64", "exe", "https://dl.example.com/AsterLink-Setup-x64-1.2.3.exe?token=secret-token", map[string]any{"size_bytes": 52428800}), 201)
	android := create(dlBody("android", "arm64-v8a", "apk", "https://dl.example.com/AsterLink-1.2.3-android-arm64-v8a.apk", map[string]any{"signing_cert_sha256": dlCert, "published": true}), 201)
	mac := create(dlBody("macos", "arm64", "dmg", "https://dl.example.com/AsterLink-1.2.3-arm64.dmg", nil), 201)
	ipa := create(dlBody("ios", "arm64", "ipa", "https://dl.example.com/AsterLink-1.2.3-ios.ipa", map[string]any{"bundle_id": "com.example.asterlink", "published": true}), 201)
	store := create(dlBody("ios", "arm64", "app_store", "https://apps.apple.com/app/asterlink/id123", nil), 201)
	if win.Published || win.Version != 1 || *win.SHA256 != strings.ToLower(dlSHA) || *win.SizeBytes != 52428800 || !win.ReleasedAt.Equal(start) {
		t.Fatalf("created %+v", win)
	}
	if !android.Published || *android.SigningCertSHA256 != "f5eb556c087d14bee4519a03ae68a26dff11502369c16454d747bb48cded7ad4" || store.SHA256 != nil || *ipa.BundleID != "com.example.asterlink" {
		t.Fatalf("created %+v %+v %+v", android, store, ipa)
	}
	e.code(e.post("/downloads", dlBody("windows", "x64", "exe", "https://dl.example.com/other.exe", nil)), 409, "duplicate")
	e.code(e.post("/downloads", dlBody("windows", "x64", "dmg", "https://dl.example.com/a.dmg", nil)), 400, "invalid_download")
	e.code(e.post("/downloads", map[string]any{"platform": "windows", "url": "x", "unknown": 1}), 400, "invalid_json")

	e.decode(e.get("/downloads"), 200, &listed)
	var order []string
	for _, row := range listed.Downloads {
		order = append(order, row.Platform+"/"+row.Format)
	}
	if strings.Join(order, ",") != "windows/exe,macos/dmg,android/apk,ios/ipa,ios/app_store,linux/appimage" {
		t.Fatalf("console order %v", order)
	}
	if listed.Downloads[0].URL == "" {
		t.Fatal("the console must show the address")
	}

	edit := func(row model.ClientDownload, change map[string]any, status int) model.ClientDownload {
		t.Helper()
		body := map[string]any{"platform": row.Platform, "arch": row.Arch, "format": row.Format, "app_version": row.AppVersion, "url": row.URL,
			"sha256": derefString(row.SHA256), "signing_cert_sha256": derefString(row.SigningCertSHA256), "bundle_id": derefString(row.BundleID),
			"published": row.Published, "version": row.Version}
		if row.SizeBytes != nil {
			body["size_bytes"] = *row.SizeBytes
		}
		for k, v := range change {
			body[k] = v
		}
		var out savedDownload
		e.decode(e.post("/downloads/"+row.ID, body), status, &out)
		return out.Download
	}
	// Publishing does not move the release date (DL1-06).
	e.clock.Advance(time.Hour)
	published := edit(win, map[string]any{"published": true}, 200)
	if !published.Published || published.Version != 2 || !published.ReleasedAt.Equal(start) {
		t.Fatalf("published %+v", published)
	}
	e.code(e.post("/downloads/"+win.ID, map[string]any{"platform": "windows", "arch": "x64", "format": "exe", "app_version": "1.2.3", "url": win.URL, "sha256": dlSHA, "version": 1}), 409, "version_conflict")
	e.code(e.post("/downloads/"+win.ID, map[string]any{"platform": "windows", "arch": "x64", "format": "exe", "app_version": "1.2.3", "url": win.URL, "sha256": dlSHA, "size_bytes": 52428800, "published": true, "version": 2}), 400, "no_change")
	e.code(e.post("/downloads/"+win.ID, map[string]any{"platform": "macos", "arch": "arm64", "format": "pkg", "app_version": "1.2.3", "url": "https://dl.example.com/a.pkg", "sha256": dlSHA, "version": 2}), 400, "invalid_download")
	e.code(e.post("/downloads/"+uuid.NewString(), dlBody("windows", "x64", "exe", win.URL, map[string]any{"version": 1})), 404, "download_not_found")
	e.code(e.post("/downloads/not-a-uuid", dlBody("windows", "x64", "exe", win.URL, map[string]any{"version": 1})), 404, "download_not_found")
	// Moving an entry onto a variant that exists is a conflict, not a second row.
	arm := create(dlBody("windows", "arm64", "exe", "https://dl.example.com/AsterLink-Setup-arm64-1.2.3.exe", nil), 201)
	e.code(e.post("/downloads/"+arm.ID, dlBody("windows", "x64", "exe", arm.URL, map[string]any{"version": 1})), 409, "duplicate")
	// A new version moves the release date.
	e.clock.Advance(time.Hour)
	upgraded := edit(published, map[string]any{"app_version": "1.3.0", "url": "https://dl.example.com/AsterLink-Setup-x64-1.3.0.exe?token=new-token"}, 200)
	if upgraded.Version != 3 || !upgraded.ReleasedAt.Equal(e.clock.Now()) || upgraded.AppVersion != "1.3.0" {
		t.Fatalf("upgraded %+v", upgraded)
	}

	// The public list: published rows only, page order, no address and no id (DL1-09).
	w := e.call(nil, "GET", "/api/v1/downloads", nil, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("public list %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if body := w.Body.String(); strings.Contains(body, "dl.example.com") || strings.Contains(body, "token") {
		t.Fatalf("public list leaks an address: %s", body)
	}
	var public struct {
		Downloads []map[string]any `json:"downloads"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &public)
	order = order[:0]
	for _, row := range public.Downloads {
		if _, ok := row["url"]; ok {
			t.Fatal("url key in the public list")
		}
		if _, ok := row["id"]; ok {
			t.Fatal("id key in the public list")
		}
		order = append(order, row["platform"].(string)+"/"+row["format"].(string)+"/"+row["app_version"].(string))
	}
	if strings.Join(order, ",") != "windows/exe/1.3.0,android/apk/1.2.3,ios/ipa/1.2.3" {
		t.Fatalf("public order %v", order)
	}
	if public.Downloads[2]["bundle_id"] != "com.example.asterlink" || public.Downloads[1]["signing_cert_sha256"] == nil {
		t.Fatalf("public facts %v", public.Downloads)
	}

	// The redirect (DL1-10).
	w = e.call(nil, "GET", "/api/v1/downloads/windows/x64/exe", nil, "")
	if w.Code != 302 || w.Header().Get("Location") != upgraded.URL || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("redirect %d %v", w.Code, w.Header())
	}
	if strings.Contains(w.Body.String(), "android") {
		t.Fatal("redirect body names other entries")
	}
	for _, path := range []string{"/api/v1/downloads/macos/arm64/dmg", "/api/v1/downloads/windows/x64/dmg", "/api/v1/downloads/beos/x64/exe", "/api/v1/downloads/windows/arm64/exe"} {
		w = e.call(nil, "GET", path, nil, "")
		if w.Code != 404 || payErrorCode(e.want(w, 404)) != "not_found" || w.Header().Get("Location") != "" {
			t.Errorf("%s: %d %v", path, w.Code, w.Header())
		}
	}
	// A row edited by hand into a script address is never followed.
	if err := e.db.Model(&model.ClientDownload{}).Where("id = ?", android.ID).Update("url", "javascript:alert(1)//a.apk").Error; err != nil {
		t.Fatal(err)
	}
	e.want(e.call(nil, "GET", "/api/v1/downloads/android/arm64-v8a/apk", nil, ""), 404)

	// Delete; the redirect is gone, the second delete finds nothing.
	e.decode(e.call(e.admin.cookies, "DELETE", "/api/v1/admin/console/downloads/"+upgraded.ID, map[string]any{}, ""), 200, nil)
	e.want(e.call(nil, "GET", "/api/v1/downloads/windows/x64/exe", nil, ""), 404)
	e.code(e.call(e.admin.cookies, "DELETE", "/api/v1/admin/console/downloads/"+upgraded.ID, map[string]any{}, ""), 404, "download_not_found")

	// One audit row per write; the detail names the file but never the query (DL1-07).
	for _, tc := range []struct {
		action, target string
		want           int64
	}{
		{AuditDownloadCreate, win.ID, 1}, {AuditDownloadUpdate, win.ID, 2}, {AuditDownloadDelete, win.ID, 1},
		{AuditDownloadCreate, linux.ID, 1}, {AuditDownloadCreate, mac.ID, 1}, {AuditDownloadUpdate, arm.ID, 0},
	} {
		if got := e.countAudit(tc.action, e.admin.ID, tc.target); got != tc.want {
			t.Errorf("%s on %s: %d audit rows, want %d", tc.action, tc.target, got, tc.want)
		}
	}
	var audits []model.AdminAudit
	e.db.Where("target_id = ?", win.ID).Order("created_at, version").Find(&audits)
	for _, a := range audits {
		if strings.Contains(a.Detail, "token") || strings.Contains(a.Detail, "https://") {
			t.Errorf("audit %s leaks the address: %s", a.Action, a.Detail)
		}
	}
	if len(audits) != 4 || !strings.Contains(audits[2].Detail, `"app_version":{"from":"1.2.3","to":"1.3.0"}`) || !strings.Contains(audits[2].Detail, "AsterLink-Setup-x64-1.3.0.exe") {
		t.Fatalf("audit trail %+v", audits)
	}
}

// The cap holds even if the catalogue grows, and a row the catalogue no longer knows is
// never offered (DL1-05, DL1-09).
func TestClientDownloadsCapAndRetiredVariants(t *testing.T) {
	e := newConsoleEnv(t, nil)
	now := e.clock.Now()
	rows := make([]model.ClientDownload, 0, maxClientDownloads)
	for i := 0; i < maxClientDownloads; i++ {
		rows = append(rows, model.ClientDownload{ID: uuid.NewString(), Platform: "retired", Arch: "a" + string(rune('A'+i%26)) + string(rune('A'+i/26)), Format: "exe",
			AppVersion: "1.0.0", URL: "https://dl.example.com/old.exe", Published: true, ReleasedAt: now, Version: 1, CreatedAt: now, UpdatedAt: now})
	}
	if err := e.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	e.code(e.post("/downloads", dlBody("windows", "x64", "exe", "https://dl.example.com/a.exe", nil)), 400, "too_many")
	var public struct {
		Downloads []map[string]any `json:"downloads"`
	}
	e.decode(e.call(nil, "GET", "/api/v1/downloads", nil, ""), 200, &public)
	if len(public.Downloads) != 0 {
		t.Fatalf("retired variants offered: %v", public.Downloads)
	}
	e.want(e.call(nil, "GET", "/api/v1/downloads/retired/aAA/exe", nil, ""), 404)
}

// Production refuses plain http even on loopback (D7), through the real handler.
func TestClientDownloadsProductionURL(t *testing.T) {
	in := toInput(t, dlBody("windows", "x64", "exe", "http://localhost:3000/a.exe", nil))
	if err := validateDownload(&in, true); err == nil {
		t.Fatal("production accepted plain http")
	}
	if err := checkDownloadURL("http://[::1]:3000/a.exe", "exe", false); err != nil {
		t.Fatalf("IPv6 loopback outside production: %v", err)
	}
}
