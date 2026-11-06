package api

import (
	"errors"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Client downloads (handoff_download.md 4–5): the administrator registers where each
// installer lives; the public list never carries that address, and the redirect is the
// only way to it (D4).

const (
	AuditDownloadCreate = "admin.download_create"
	AuditDownloadUpdate = "admin.download_update"
	AuditDownloadDelete = "admin.download_delete"

	maxClientDownloads = 60
	maxDownloadBytes   = int64(8) << 30
	maxDownloadURL     = 1024
)

// downloadPlatform is one entry of the fixed catalogue (D3): the order of platforms,
// architectures and formats is the order of the page. The first architecture is the
// recommended one. testdata/client_downloads_catalog.json is the same catalogue for the
// website; TestClientDownloadCatalogSnapshot keeps the two equal.
type downloadPlatform struct {
	Platform string   `json:"platform"`
	Archs    []string `json:"archs"`
	Formats  []string `json:"formats"`
}

var downloadCatalog = []downloadPlatform{
	{"windows", []string{"x64", "arm64"}, []string{"exe", "msi", "zip"}},
	{"macos", []string{"arm64", "x64", "universal"}, []string{"dmg", "pkg"}},
	{"android", []string{"arm64-v8a", "armeabi-v7a", "x86_64", "universal"}, []string{"apk"}},
	{"ios", []string{"arm64"}, []string{"ipa", "app_store", "testflight"}},
	{"linux", []string{"x64", "arm64"}, []string{"appimage", "deb", "rpm", "tar.gz"}},
}

// downloadSuffixes: the file a format's address must end with (case-insensitive, D7).
var downloadSuffixes = map[string]string{
	"exe": ".exe", "msi": ".msi", "zip": ".zip", "dmg": ".dmg", "pkg": ".pkg", "apk": ".apk",
	"ipa": ".ipa", "appimage": ".appimage", "deb": ".deb", "rpm": ".rpm", "tar.gz": ".tar.gz",
}

// downloadStoreHosts: store formats link to a store page on exactly this host and carry
// no file facts.
var downloadStoreHosts = map[string]string{"app_store": "apps.apple.com", "testflight": "testflight.apple.com"}

var (
	downloadVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$`)
	hex64Pattern           = regexp.MustCompile(`^[0-9a-f]{64}$`)
	bundleIDPattern        = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
)

func catalogPlatform(platform string) (downloadPlatform, bool) {
	for _, p := range downloadCatalog {
		if p.Platform == platform {
			return p, true
		}
	}
	return downloadPlatform{}, false
}

func validVariant(platform, arch, format string) bool {
	p, ok := catalogPlatform(platform)
	return ok && slices.Contains(p.Archs, arch) && slices.Contains(p.Formats, format)
}

// downloadRank orders rows like the page: platform, then architecture, then format.
func downloadRank(row model.ClientDownload) [3]int {
	for i, p := range downloadCatalog {
		if p.Platform == row.Platform {
			return [3]int{i, slices.Index(p.Archs, row.Arch), slices.Index(p.Formats, row.Format)}
		}
	}
	return [3]int{len(downloadCatalog), 0, 0}
}

func sortDownloads(rows []model.ClientDownload) {
	slices.SortFunc(rows, func(a, b model.ClientDownload) int {
		ra, rb := downloadRank(a), downloadRank(b)
		for i := range ra {
			if ra[i] != rb[i] {
				return ra[i] - rb[i]
			}
		}
		return 0
	})
}

func downloadField(field, message string) error {
	return &detailedError{apiError{400, "invalid_download", message}, gin.H{"field": field}}
}

// normalizeHex accepts the forms the packaging tools print: upper case, colons, spaces.
func normalizeHex(value string) string {
	return strings.NewReplacer(":", "", " ", "", "\t", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}

// checkDownloadURL applies D7. Outside production a plain-http loopback address is
// accepted, so the browser fixtures need no certificate.
func checkDownloadURL(raw, format string, production bool) error {
	bad := func(message string) error { return downloadField("url", message) }
	if raw == "" {
		return bad("请填写下载地址")
	}
	if len(raw) > maxDownloadURL {
		return bad("下载地址最多 1024 个字符")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return bad("下载地址格式不正确")
	}
	if u.User != nil {
		return bad("下载地址不能包含账号或密码")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return bad("下载地址不能包含 # 片段")
	}
	host := strings.ToLower(u.Hostname())
	if store, ok := downloadStoreHosts[format]; ok {
		if u.Scheme != "https" || host != store || u.Port() != "" {
			return bad("该格式只能填写 https://" + store + " 的页面地址")
		}
		return nil
	}
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback && !production) {
		return bad("下载地址必须以 https:// 开头")
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), downloadSuffixes[format]) {
		return bad("下载地址的文件名须以 " + downloadSuffixes[format] + " 结尾，与所选格式一致")
	}
	return nil
}

type downloadInput struct {
	Platform          string `json:"platform"`
	Arch              string `json:"arch"`
	Format            string `json:"format"`
	AppVersion        string `json:"app_version"`
	URL               string `json:"url"`
	SHA256            string `json:"sha256"`
	SizeBytes         *int64 `json:"size_bytes"`
	SigningCertSHA256 string `json:"signing_cert_sha256"`
	BundleID          string `json:"bundle_id"`
	Published         bool   `json:"published"`
	// Version is required when editing (optimistic concurrency between administrators).
	Version int64 `json:"version"`
}

// validateDownload checks and normalises the input in place.
func validateDownload(in *downloadInput, production bool) error {
	in.Platform, in.Arch, in.Format = strings.TrimSpace(in.Platform), strings.TrimSpace(in.Arch), strings.TrimSpace(in.Format)
	p, ok := catalogPlatform(in.Platform)
	if !ok {
		return downloadField("platform", "请选择平台")
	}
	if !slices.Contains(p.Archs, in.Arch) {
		return downloadField("arch", "请选择该平台支持的架构")
	}
	if !slices.Contains(p.Formats, in.Format) {
		return downloadField("format", "请选择该平台支持的格式")
	}
	in.AppVersion = strings.TrimSpace(in.AppVersion)
	if len(in.AppVersion) > 32 || !downloadVersionPattern.MatchString(in.AppVersion) {
		return downloadField("app_version", "版本号格式应为 1.2.3（可带 -beta.1 之类的后缀），最多 32 个字符")
	}
	in.URL = strings.TrimSpace(in.URL)
	if err := checkDownloadURL(in.URL, in.Format, production); err != nil {
		return err
	}
	in.SHA256, in.SigningCertSHA256, in.BundleID = normalizeHex(in.SHA256), normalizeHex(in.SigningCertSHA256), strings.TrimSpace(in.BundleID)
	_, store := downloadStoreHosts[in.Format]
	switch {
	case store && in.SHA256 != "":
		return downloadField("sha256", "商店链接不需要填写 SHA-256")
	case store && in.SizeBytes != nil:
		return downloadField("size_bytes", "商店链接不需要填写文件大小")
	case !store && !hex64Pattern.MatchString(in.SHA256):
		return downloadField("sha256", "SHA-256 须为 64 位十六进制")
	case in.SizeBytes != nil && (*in.SizeBytes < 1 || *in.SizeBytes > maxDownloadBytes):
		return downloadField("size_bytes", "文件大小须为 1 字节到 8 GiB")
	}
	if in.SigningCertSHA256 != "" {
		if in.Format != "apk" {
			return downloadField("signing_cert_sha256", "只有 Android 安装包需要填写签名证书指纹")
		}
		if !hex64Pattern.MatchString(in.SigningCertSHA256) {
			return downloadField("signing_cert_sha256", "签名证书指纹须为 64 位十六进制")
		}
	}
	if in.Format == "ipa" {
		if len(in.BundleID) > 155 || !bundleIDPattern.MatchString(in.BundleID) {
			return downloadField("bundle_id", "请填写 Bundle ID，例如 com.example.app")
		}
	} else if in.BundleID != "" {
		return downloadField("bundle_id", "只有 iOS 网页安装需要填写 Bundle ID")
	}
	return nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// downloadFile is the short audit form of an address: host and file name, never the
// query (a signed CDN address may carry a token there).
func downloadFile(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	file := u.Hostname() + "/" + path.Base(u.Path)
	if len(file) > 120 {
		file = file[:120]
	}
	return file
}

type downloadCatalogView struct {
	Platforms  []downloadPlatform `json:"platforms"`
	Suffixes   map[string]string  `json:"suffixes"`
	StoreHosts map[string]string  `json:"store_hosts"`
	Limit      int                `json:"limit"`
}

func catalogView() downloadCatalogView {
	return downloadCatalogView{Platforms: downloadCatalog, Suffixes: downloadSuffixes, StoreHosts: downloadStoreHosts, Limit: maxClientDownloads}
}

func (s *Server) consoleDownloadList(c *gin.Context) {
	rows := []model.ClientDownload{}
	if s.database(c).Limit(maxClientDownloads*2).Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "下载条目暂时不可用")
		return
	}
	sortDownloads(rows)
	c.JSON(200, gin.H{"downloads": rows, "catalog": catalogView()})
}

func (s *Server) consoleDownloadSave(c *gin.Context) {
	var in downloadInput
	if !decode(c, &in) {
		return
	}
	id := c.Param("id")
	creating := id == ""
	if !creating {
		if _, err := uuid.Parse(id); err != nil {
			fail(c, 404, "download_not_found", "下载条目不存在")
			return
		}
	}
	if err := validateDownload(&in, s.cfg.Env == "production"); planFailure(c, err) {
		return
	}
	now, actor := s.now(), currentUser(c).ID
	var saved model.ClientDownload
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		// The unique index is the last word; this answers the common case with a message.
		clash := func() error {
			var n int64
			if err := tx.Model(&model.ClientDownload{}).Where("platform = ? AND arch = ? AND format = ? AND id <> ?", in.Platform, in.Arch, in.Format, saved.ID).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return &apiError{409, "duplicate", "该平台的此架构与格式已存在，请直接编辑"}
			}
			return nil
		}
		if creating {
			saved.ID = uuid.NewString()
			if err := clash(); err != nil {
				return err
			}
			var total int64
			if err := tx.Model(&model.ClientDownload{}).Count(&total).Error; err != nil {
				return err
			}
			if total >= maxClientDownloads {
				return &apiError{400, "too_many", "下载条目过多，请先删除不用的条目"}
			}
			saved = model.ClientDownload{ID: saved.ID, Platform: in.Platform, Arch: in.Arch, Format: in.Format, AppVersion: in.AppVersion, URL: in.URL,
				SHA256: optionalString(in.SHA256), SizeBytes: in.SizeBytes, SigningCertSHA256: optionalString(in.SigningCertSHA256), BundleID: optionalString(in.BundleID),
				Published: in.Published, ReleasedAt: now, Version: 1, UpdatedBy: &actor, CreatedAt: now, UpdatedAt: now}
			// Select("*"): GORM would replace a false Published by the column default.
			if err := tx.Select("*").Create(&saved).Error; err != nil {
				return err
			}
			return consoleAudit(tx, actor, AuditDownloadCreate, saved.ID, saved.Version, gin.H{"platform": saved.Platform, "arch": saved.Arch, "format": saved.Format,
				"app_version": saved.AppVersion, "file": downloadFile(saved.URL), "published": saved.Published}, now)
		}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&saved, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &apiError{404, "download_not_found", "下载条目不存在"}
		}
		if err != nil {
			return err
		}
		if in.Version != saved.Version {
			return &apiError{409, "version_conflict", "下载条目已被其他管理员修改，请刷新后重试"}
		}
		if in.Platform != saved.Platform {
			return downloadField("platform", "平台创建后不能修改，请删除后重新添加")
		}
		if err := clash(); err != nil {
			return err
		}
		before := saved
		saved.Arch, saved.Format, saved.AppVersion, saved.URL = in.Arch, in.Format, in.AppVersion, in.URL
		saved.SHA256, saved.SizeBytes, saved.SigningCertSHA256, saved.BundleID = optionalString(in.SHA256), in.SizeBytes, optionalString(in.SigningCertSHA256), optionalString(in.BundleID)
		saved.Published = in.Published
		detail := gin.H{"platform": saved.Platform}
		pair := func(key string, a, b any) {
			if a != b {
				detail[key] = gin.H{"from": a, "to": b}
			}
		}
		pair("arch", before.Arch, saved.Arch)
		pair("format", before.Format, saved.Format)
		pair("app_version", before.AppVersion, saved.AppVersion)
		if before.URL != saved.URL {
			// Two addresses can share host and name (a new query token): say so anyway.
			detail["file"] = gin.H{"from": downloadFile(before.URL), "to": downloadFile(saved.URL), "changed": true}
		}
		pair("sha256", derefString(before.SHA256), derefString(saved.SHA256))
		pair("size_bytes", derefInt(before.SizeBytes), derefInt(saved.SizeBytes))
		pair("signing_cert_sha256", derefString(before.SigningCertSHA256), derefString(saved.SigningCertSHA256))
		pair("bundle_id", derefString(before.BundleID), derefString(saved.BundleID))
		pair("published", before.Published, saved.Published)
		if len(detail) == 1 {
			return &apiError{400, "no_change", "没有任何改动"}
		}
		if before.AppVersion != saved.AppVersion || before.URL != saved.URL {
			saved.ReleasedAt = now
		}
		saved.Version, saved.UpdatedBy, saved.UpdatedAt = saved.Version+1, &actor, now
		if err := tx.Select("*").Save(&saved).Error; err != nil {
			return err
		}
		return consoleAudit(tx, actor, AuditDownloadUpdate, saved.ID, saved.Version, detail, now)
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		// Two administrators added the same variant at once: the unique index decides.
		err = &apiError{409, "duplicate", "该平台的此架构与格式已存在，请直接编辑"}
	}
	if planFailure(c, err) {
		return
	}
	status := 200
	if creating {
		status = 201
	}
	c.JSON(status, gin.H{"download": saved})
}

func (s *Server) consoleDownloadDelete(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "download_not_found", "下载条目不存在")
		return
	}
	now, actor := s.now(), currentUser(c).ID
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := lockConsoleAdmin(tx, c); err != nil {
			return err
		}
		var row model.ClientDownload
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &apiError{404, "download_not_found", "下载条目不存在"}
		}
		if err != nil {
			return err
		}
		if err = tx.Delete(&row).Error; err != nil {
			return err
		}
		return consoleAudit(tx, actor, AuditDownloadDelete, row.ID, row.Version+1, gin.H{"platform": row.Platform, "arch": row.Arch, "format": row.Format,
			"app_version": row.AppVersion, "file": downloadFile(row.URL), "sha256": derefString(row.SHA256)}, now)
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"deleted": true})
}

// publicDownload is what anyone may read: no address, no id (D4).
type publicDownload struct {
	Platform          string  `json:"platform"`
	Arch              string  `json:"arch"`
	Format            string  `json:"format"`
	AppVersion        string  `json:"app_version"`
	SizeBytes         *int64  `json:"size_bytes"`
	SHA256            *string `json:"sha256"`
	SigningCertSHA256 *string `json:"signing_cert_sha256"`
	BundleID          *string `json:"bundle_id"`
	ReleasedAt        string  `json:"released_at"`
}

func (s *Server) publicDownloads(c *gin.Context) {
	rows := []model.ClientDownload{}
	if s.database(c).Where("published = ?", true).Limit(maxClientDownloads*2).Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "下载信息暂时不可用")
		return
	}
	sortDownloads(rows)
	out := make([]publicDownload, 0, len(rows))
	for _, row := range rows {
		// A row the catalogue no longer knows (a variant removed from the code) is hidden.
		if !validVariant(row.Platform, row.Arch, row.Format) {
			continue
		}
		out = append(out, publicDownload{Platform: row.Platform, Arch: row.Arch, Format: row.Format, AppVersion: row.AppVersion, SizeBytes: row.SizeBytes,
			SHA256: row.SHA256, SigningCertSHA256: row.SigningCertSHA256, BundleID: row.BundleID, ReleasedAt: row.ReleasedAt.UTC().Format("2006-01-02T15:04:05Z")})
	}
	c.Header("Cache-Control", "public, max-age=30")
	c.JSON(200, gin.H{"downloads": out})
}

// publicDownloadRedirect sends the browser to the registered file (D4, D5). The address
// is checked again, so a row edited by hand can never become a javascript: or plain-http
// redirect.
func (s *Server) publicDownloadRedirect(c *gin.Context) {
	platform, arch, format := c.Param("platform"), c.Param("arch"), c.Param("format")
	c.Header("Cache-Control", "no-store")
	if !validVariant(platform, arch, format) {
		fail(c, 404, "not_found", "下载不存在")
		return
	}
	var row model.ClientDownload
	err := s.database(c).First(&row, "platform = ? AND arch = ? AND format = ? AND published = ?", platform, arch, format, true).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "not_found", "下载不存在")
		return
	}
	if err != nil {
		fail(c, 503, "database_unavailable", "下载信息暂时不可用")
		return
	}
	if checkDownloadURL(row.URL, row.Format, s.cfg.Env == "production") != nil {
		fail(c, 404, "not_found", "下载不存在")
		return
	}
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(302, row.URL)
}
