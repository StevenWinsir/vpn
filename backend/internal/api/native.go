package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/model"
)

const nativeIdleTTL = 3 * time.Minute
const nativeLeaseSeconds = 90
const maxNativeProfileBytes = 1 << 20

var nativePlanID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

type nativeState struct {
	ServerTime             time.Time  `json:"server_time"`
	SubscriptionExpiresAt  *time.Time `json:"subscription_expires_at"`
	AuthorizationExpiresAt *time.Time `json:"authorization_expires_at"`
	SessionIdleTimeout     int        `json:"session_idle_timeout_seconds"`
	ProfileVersion         string     `json:"profile_version"`
	SessionID              string     `json:"session_id"`
	ExpiresAt              time.Time  `json:"expires_at"`
	CanConnect             bool       `json:"can_connect"`
	Reason                 string     `json:"reason"`
	RemainingBytes         int64      `json:"remaining_bytes"`
	TotalBytes             int64      `json:"total_bytes"`
	UsedUnits              int64      `json:"used_units"`
	PlanName               string     `json:"plan_name"`
	LastSequence           int64      `json:"last_sequence"`
	UploadBytes            int64      `json:"upload_bytes"`
	DownloadBytes          int64      `json:"download_bytes"`
	ReportInterval         int        `json:"report_interval_seconds"`
	LeaseSeconds           int        `json:"lease_seconds"`
	MeteringSource         string     `json:"metering_source"`
	RatePermille           int64      `json:"rate_permille"`
}

func (s *Server) nativeLogin(c *gin.Context) {
	var in struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceID   string `json:"device_id"`
		Platform   string `json:"platform"`
		AppVersion string `json:"app_version"`
	}
	if !decode(c, &in) {
		return
	}
	email, valid := validEmail(in.Email)
	device, deviceErr := uuid.Parse(in.DeviceID)
	platformOK := in.Platform == "android" || in.Platform == "macos" || in.Platform == "windows" || in.Platform == "linux"
	if !valid || len(in.Password) > 72 || deviceErr != nil || device == uuid.Nil || !platformOK || len(in.AppVersion) > 64 {
		fail(c, 400, "invalid_client_login", "客户端登录参数无效")
		return
	}
	var user model.User
	err := s.database(c).Where("email = ?", email).First(&user).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 503, "auth_unavailable", "登录服务暂时不可用")
		return
	}
	hash := s.dummyHash
	if err == nil {
		hash = []byte(user.PasswordHash)
	}
	matched := bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) == nil
	if err != nil || !matched || user.Status != "active" {
		fail(c, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(c, 503, "session_failed", "无法创建客户端会话")
		return
	}
	now := time.Now().UTC()
	sess := model.NativeSession{ID: uuid.NewString(), UserID: user.ID, TokenHash: refreshHash(token), DeviceID: device.String(), Platform: in.Platform, AppVersion: in.AppVersion, LastSeenAt: now, ExpiresAt: now.Add(s.cfg.SessionTTL)}
	var status nativeState
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ? AND status = ?", user.ID, "active").Error; e != nil {
			return e
		}
		if e := tx.Model(&model.NativeSession{}).Where("user_id = ? AND device_id = ? AND revoked_at IS NULL", user.ID, device.String()).Update("revoked_at", now).Error; e != nil {
			return e
		}
		if e := tx.Omit("User").Create(&sess).Error; e != nil {
			return e
		}
		sub, e := readNativeSubscription(tx, user.ID)
		if e != nil {
			return e
		}
		status = s.nativeSnapshot(sess, sub, now, tx)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"token": token, "user": user, "session": status})
}

func (s *Server) requireNativeSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || len(header) != len("Bearer ")+43 {
			fail(c, 401, "native_session_required", "请重新登录客户端")
			return
		}
		var sess model.NativeSession
		now := time.Now().UTC()
		err := s.database(c).Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND last_seen_at > ?", refreshHash(strings.TrimPrefix(header, "Bearer ")), now, now.Add(-nativeIdleTTL)).First(&sess).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 401, "native_session_expired", "客户端会话已失效，请重新登录")
			return
		}
		if s.nativeFailure(c, err) {
			return
		}
		c.Set("native_session", sess)
		c.Next()
	}
}

// All native writes lock user -> session -> subscription, matching purchase's user lock.
func (s *Server) nativeTransaction(c *gin.Context, action func(*gorm.DB, *model.NativeSession, *model.Subscription, time.Time) error) error {
	identity := c.MustGet("native_session").(model.NativeSession)
	return s.database(c).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ? AND status = ?", identity.UserID, "active").Error; e != nil {
			return e
		}
		var sess model.NativeSession
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sess, "id = ? AND user_id = ?", identity.ID, identity.UserID).Error; e != nil {
			return e
		}
		now := time.Now().UTC()
		if sess.RevokedAt != nil || !now.Before(sess.ExpiresAt) || !sess.LastSeenAt.After(now.Add(-nativeIdleTTL)) {
			return &apiError{401, "native_session_expired", "客户端会话已失效，请重新登录"}
		}
		sub, e := readNativeSubscription(tx, user.ID)
		if e != nil {
			return e
		}
		if e = action(tx, &sess, sub, now); e != nil {
			return e
		}
		return tx.Model(&sess).Update("last_seen_at", now).Error
	})
}

func readNativeSubscription(tx *gorm.DB, userID string) (*model.Subscription, error) {
	var sub model.Subscription
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sub, err
}

func (s *Server) nativeEntitlement(sub *model.Subscription, now time.Time) string {
	if sub == nil {
		return "upgrade_required"
	}
	if sub.IsTest && !(s.cfg.Env != "production" && s.cfg.TestPurchase && s.cfg.ClientAllowTestEntitlements) {
		return "paid_vip_required"
	}
	if now.Before(sub.StartsAt) || !now.Before(sub.ExpiresAt) {
		return "subscription_expired"
	}
	if sub.TrafficLimitBytes <= 0 || sub.TrafficLimitBytes > billing.MaxClientCounter || sub.UsedUnits < 0 || sub.TrafficLimitBytes*1000-sub.UsedUnits < 1000 {
		return "quota_exhausted"
	}
	return ""
}

func nativeBound(sess model.NativeSession, sub *model.Subscription) bool {
	return sub != nil && sess.SubscriptionID != nil && *sess.SubscriptionID == sub.ID && sess.SubscriptionStartsAt != nil && sess.SubscriptionStartsAt.Equal(sub.StartsAt)
}

func nativeEntitlementFingerprint(sub *model.Subscription) string {
	data, _ := json.Marshal(struct {
		PlanID         string
		IsTest         bool
		AllowDedicated bool
		MaxDevices     int
	}{sub.PlanID, sub.IsTest, sub.AllowDedicated, sub.MaxDevices})
	return nativeProfileVersion(data)
}

func nativeProfileVersion(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func nativeAuthorizationBound(sess model.NativeSession, sub *model.Subscription) bool {
	return nativeBound(sess, sub) && sess.EntitlementFingerprint == nativeEntitlementFingerprint(sub)
}

func (s *Server) nativeSnapshot(sess model.NativeSession, sub *model.Subscription, now time.Time, transactions ...*gorm.DB) nativeState {
	out := nativeState{ServerTime: now, SessionIdleTimeout: int(nativeIdleTTL / time.Second), ProfileVersion: sess.ProfileVersion, SessionID: sess.ID, ExpiresAt: sess.ExpiresAt, Reason: s.nativeEntitlement(sub, now), LastSequence: sess.LastSequence, UploadBytes: sess.UploadBytes, DownloadBytes: sess.DownloadBytes, ReportInterval: 60, LeaseSeconds: nativeLeaseSeconds, MeteringSource: "client_reported", RatePermille: sessionRate(sess)}
	if sub != nil {
		out.SubscriptionExpiresAt = &sub.ExpiresAt
		out.TotalBytes, out.UsedUnits, out.PlanName = sub.TrafficLimitBytes, sub.UsedUnits, sub.PlanName
		if sub.TrafficLimitBytes > 0 && sub.TrafficLimitBytes <= billing.MaxClientCounter && sub.UsedUnits >= 0 {
			out.RemainingBytes = max(0, (sub.TrafficLimitBytes*1000-sub.UsedUnits)/1000)
		}
	}
	if out.Reason == "" {
		switch {
		case sess.SubscriptionID == nil || s.cfg.ClientNodeCatalog && sess.NodeID == "":
			out.Reason = "profile_required"
		case !nativeAuthorizationBound(sess, sub):
			out.Reason = "subscription_changed"
		default:
			var data []byte
			var err error
			if s.cfg.ClientNodeCatalog {
				db := s.db
				if len(transactions) == 1 {
					db = transactions[0]
				}
				var profile catalogProfile
				profile, err = s.catalogProfile(db, sub, sess.NodeID)
				data = profile.Data
			} else {
				data, err = s.readNativeProfile(sub.PlanID)
			}
			if err != nil {
				out.Reason = "client_config_unavailable"
			} else if sess.ProfileVersion != nativeProfileVersion(data) {
				out.Reason = "profile_changed"
			}
		}
	}
	if !now.Before(sess.ExpiresAt) {
		out.Reason = "native_session_expired"
	}
	out.CanConnect = out.Reason == "" && out.RemainingBytes > 0
	if out.CanConnect {
		deadline := now.Add(nativeLeaseSeconds * time.Second)
		if sess.ExpiresAt.Before(deadline) {
			deadline = sess.ExpiresAt
		}
		if sub.ExpiresAt.Before(deadline) {
			deadline = sub.ExpiresAt
		}
		out.AuthorizationExpiresAt = &deadline
	}
	return out
}

func (s *Server) nativeStatus(c *gin.Context) {
	var status nativeState
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		status = s.nativeSnapshot(*sess, sub, now, tx)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(200, status)
}

func (s *Server) readNativeProfile(planID string) ([]byte, error) {
	unavailable := &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	if s.cfg.ClientProfileDir == "" || !nativePlanID.MatchString(planID) {
		return nil, unavailable
	}
	root, err := os.OpenRoot(s.cfg.ClientProfileDir)
	if err != nil {
		return nil, unavailable
	}
	defer root.Close()
	file, err := root.Open(planID + ".yaml")
	if err != nil {
		return nil, unavailable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxNativeProfileBytes {
		return nil, unavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNativeProfileBytes+1))
	if err != nil || len(data) > maxNativeProfileBytes || len(strings.TrimSpace(string(data))) == 0 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, unavailable
	}
	return data, nil
}

func (s *Server) nativeConfig(c *gin.Context) {
	var data []byte
	var status nativeState
	var catalog *catalogProfile
	nodeID := c.Query("node_id")
	if nodeID != "" {
		id, err := uuid.Parse(nodeID)
		if err != nil || id == uuid.Nil || !s.cfg.ClientNodeCatalog {
			fail(c, 400, "invalid_managed_selection", "节点选择无效")
			return
		}
		nodeID = id.String()
	}
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		if reason := s.nativeEntitlement(sub, now); reason != "" {
			return &apiError{403, reason, "请先升级有效的付费 VIP 套餐并确认剩余流量"}
		}
		if sess.SubscriptionID != nil && !nativeAuthorizationBound(*sess, sub) {
			return &apiError{409, "subscription_changed", "套餐周期已变更，请重新登录"}
		}
		var e error
		if s.cfg.ClientNodeCatalog {
			if sess.SubscriptionID != nil {
				sequence, parseErr := strconv.ParseInt(c.Query("last_sequence"), 10, 64)
				if parseErr != nil || sequence != sess.LastSequence {
					return &apiError{409, "traffic_sequence", "切换节点前必须确认当前流量批次"}
				}
			}
			selected := nodeID
			if selected == "" {
				selected = sess.NodeID
			}
			profile, profileErr := s.catalogProfile(tx, sub, selected)
			if profileErr != nil && nodeID == "" && selected != "" {
				profile, profileErr = s.catalogProfile(tx, sub, "")
			}
			if profileErr != nil {
				return profileErr
			}
			catalog = &profile
			data, sess.NodeID, sess.RatePermille = profile.Data, profile.Node.ID, profile.Node.RatePermille
		} else {
			data, e = s.readNativeProfile(sub.PlanID)
			if e != nil {
				return e
			}
		}
		var active int64
		fingerprint := nativeEntitlementFingerprint(sub)
		if e = tx.Model(&model.NativeSession{}).Where("user_id = ? AND id <> ? AND subscription_id = ? AND subscription_starts_at = ? AND entitlement_fingerprint = ? AND revoked_at IS NULL AND expires_at > ? AND last_seen_at > ?", sess.UserID, sess.ID, sub.ID, sub.StartsAt, fingerprint, now, now.Add(-nativeIdleTTL)).Count(&active).Error; e != nil {
			return e
		}
		if sub.MaxDevices <= 0 || active >= int64(sub.MaxDevices) {
			return &apiError{403, "device_limit", "已达到套餐同时在线设备上限，请先退出其他设备"}
		}
		sess.SubscriptionID, sess.SubscriptionStartsAt = &sub.ID, &sub.StartsAt
		sess.EntitlementFingerprint, sess.ProfileVersion = fingerprint, nativeProfileVersion(data)
		if e = tx.Model(sess).Updates(map[string]any{"subscription_id": sub.ID, "subscription_starts_at": sub.StartsAt, "entitlement_fingerprint": fingerprint, "profile_version": sess.ProfileVersion, "node_id": sess.NodeID, "rate_permille": sessionRate(*sess)}).Error; e != nil {
			return e
		}
		status = s.nativeSnapshot(*sess, sub, now, tx)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	response := gin.H{"yaml": string(data), "version": nativeProfileVersion(data), "session": status}
	if catalog != nil {
		response["nodes"], response["node_id"] = catalog.Nodes, catalog.Node.ID
	}
	c.JSON(200, response)
}

func (s *Server) nativeTraffic(c *gin.Context) {
	var in billing.ClientCounters
	if !decode(c, &in) {
		return
	}
	if !in.Valid() {
		fail(c, 400, "invalid_traffic", "流量计数无效")
		return
	}
	var status nativeState
	replayed := false
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		if !nativeBound(*sess, sub) {
			return &apiError{409, "subscription_changed", "套餐或会话配置已变更，请重新登录"}
		}
		if in.Sequence <= sess.LastSequence {
			var report model.ClientTrafficReport
			if e := tx.Where("session_id = ? AND sequence = ?", sess.ID, in.Sequence).First(&report).Error; e != nil {
				return e
			}
			if report.UploadBytes != in.UploadBytes || report.DownloadBytes != in.DownloadBytes {
				return &apiError{409, "traffic_conflict", "重复流量批次的计数不一致"}
			}
			replayed = true
		} else {
			previous := billing.ClientCounters{Sequence: sess.LastSequence, UploadBytes: sess.UploadBytes, DownloadBytes: sess.DownloadBytes}
			up, down, units, e := billing.ClientDeltaAtRate(previous, in, sub.UsedUnits, sub.UploadBytes, sub.DownloadBytes, sessionRate(*sess))
			if e != nil {
				return &apiError{409, "traffic_sequence", "流量序号或累计计数不连续，请重新登录"}
			}
			report := model.ClientTrafficReport{ID: uuid.NewString(), SessionID: sess.ID, Sequence: in.Sequence, UserID: sess.UserID, SubscriptionID: sub.ID, UploadBytes: in.UploadBytes, DownloadBytes: in.DownloadBytes, UploadDelta: up, DownloadDelta: down, ChargedUnits: units, RatePermille: sessionRate(*sess), NodeID: sess.NodeID, Source: "client_reported"}
			if e = tx.Omit("Session").Create(&report).Error; e != nil {
				return e
			}
			sub.UsedUnits += units
			sub.UploadBytes += up
			sub.DownloadBytes += down
			if e = tx.Model(sub).Updates(map[string]any{"used_units": sub.UsedUnits, "upload_bytes": sub.UploadBytes, "download_bytes": sub.DownloadBytes}).Error; e != nil {
				return e
			}
			sess.LastSequence, sess.UploadBytes, sess.DownloadBytes = in.Sequence, in.UploadBytes, in.DownloadBytes
			if e = tx.Model(sess).Updates(map[string]any{"last_sequence": in.Sequence, "upload_bytes": in.UploadBytes, "download_bytes": in.DownloadBytes}).Error; e != nil {
				return e
			}
		}
		status = s.nativeSnapshot(*sess, sub, now, tx)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"session": status, "replayed": replayed})
}

func (s *Server) nativeLogout(c *gin.Context) {
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, _ *model.Subscription, now time.Time) error {
		return tx.Model(sess).Update("revoked_at", now).Error
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"logged_out": true})
}

func (s *Server) nativeFailure(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var business *apiError
	if errors.As(err, &business) {
		fail(c, business.Status, business.Code, business.Message)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 401, "native_session_expired", "客户端会话已失效，请重新登录")
	} else {
		fail(c, 503, "client_service_unavailable", "客户端服务暂时不可用，请稍后重试")
	}
	return true
}
