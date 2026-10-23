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
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

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
	// Present only for node_reported sessions that hold a grant.
	GrantID        string     `json:"grant_id,omitempty"`
	GrantState     string     `json:"grant_state,omitempty"`
	GrantExpiresAt *time.Time `json:"grant_expires_at,omitempty"`
	// Present once the session has held an online lease (contract v3): the latest
	// lease, its state now (active, expired or ended) and its expiry.
	ConnectionID        string     `json:"connection_id,omitempty"`
	ConnectionState     string     `json:"connection_state,omitempty"`
	ConnectionExpiresAt *time.Time `json:"connection_expires_at,omitempty"`
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
	platformOK := in.Platform == "android" || in.Platform == "ios" || in.Platform == "macos" || in.Platform == "windows" || in.Platform == "linux"
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
	src := requestSource(c)
	if err != nil || !matched || user.Status != "active" {
		if err == nil && user.Status != model.UserDeleting {
			reason := loginBadPassword
			if matched {
				reason = loginDisabled
			}
			s.recordLoginAsync(s.loginEvent(src, user.ID, model.LoginNative, in.Platform, model.LoginFailure, reason))
		}
		fail(c, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(c, 503, "session_failed", "无法创建客户端会话")
		return
	}
	now := s.now()
	sess := model.NativeSession{ID: uuid.NewString(), UserID: user.ID, TokenHash: refreshHash(token), DeviceID: device.String(), Platform: in.Platform, AppVersion: in.AppVersion, LastSeenAt: now, ExpiresAt: now.Add(s.cfg.SessionTTL)}
	var status nativeState
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		// Same guard as the web login: a reset that committed after bcrypt wins.
		if e := lockVerifiedUser(tx, &user); e != nil {
			return e
		}
		// Signing in again on a device replaces its session; its lease ends with it.
		if e := s.endLeases(tx, now, model.LeaseSessionRevoked, "user_id = ? AND device_id = ?", user.ID, device.String()); e != nil {
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
		return s.stampLogin(tx, user.ID, src)
	})
	if errors.Is(err, errStaleCredentials) {
		s.recordLoginAsync(s.loginEvent(src, user.ID, model.LoginNative, in.Platform, model.LoginFailure, loginStale))
		fail(c, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	if s.nativeFailure(c, err) {
		return
	}
	s.recordLogin(s.loginEvent(src, user.ID, model.LoginNative, in.Platform, model.LoginSuccess, ""))
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
		now := s.now()
		err := s.database(c).Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND last_seen_at > ?", refreshHash(strings.TrimPrefix(header, "Bearer ")), now, now.Add(-s.cfg.IdleTTL())).First(&sess).Error
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
		now := s.now()
		if sess.RevokedAt != nil || !now.Before(sess.ExpiresAt) || !sess.LastSeenAt.After(now.Add(-s.cfg.IdleTTL())) {
			return &apiError{401, "native_session_expired", "客户端会话已失效，请重新登录"}
		}
		sub, e := readNativeSubscription(tx, user.ID)
		if e != nil {
			return e
		}
		if e = action(tx, &sess, sub, now); e != nil {
			return e
		}
		if sess.LastSeenAt.Equal(now) {
			return nil // the action already stamped the session in its own update
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
	out := s.sessionSnapshot(sess, sub, now, transactions...)
	// Lease fields need the request's transaction; snapshots built without one (the
	// shared contract tests) keep the original shape.
	if len(transactions) == 1 {
		if lease, err := latestLease(transactions[0], sess.ID); err == nil {
			s.withLease(&out, lease, now)
		}
	}
	return out
}

// nativeSnapshotWith is nativeSnapshot for a caller that already holds the session's
// latest lease (the one it just renewed): one query fewer on every /traffic.
func (s *Server) nativeSnapshotWith(sess model.NativeSession, sub *model.Subscription, now time.Time, tx *gorm.DB, lease *model.ConnectionLease) nativeState {
	out := s.sessionSnapshot(sess, sub, now, tx)
	s.withLease(&out, lease, now)
	return out
}

// sessionSnapshot is the session and entitlement verdict without lease fields.
func (s *Server) sessionSnapshot(sess model.NativeSession, sub *model.Subscription, now time.Time, transactions ...*gorm.DB) nativeState {
	db := s.db
	if len(transactions) == 1 {
		db = transactions[0]
	}
	return s.sessionSnapshotKnowing(sess, sub, now, db, nil)
}

// admittedSnapshot is the snapshot of a session just admitted: the profile and lease
// it was given are already at hand, so the admission (under the group lock) reads
// neither again.
func (s *Server) admittedSnapshot(sess model.NativeSession, sub *model.Subscription, now time.Time, tx *gorm.DB, lease *model.ConnectionLease, profile *catalogProfile) nativeState {
	out := s.sessionSnapshotKnowing(sess, sub, now, tx, profile)
	s.withLease(&out, lease, now)
	return out
}

// sessionSnapshotKnowing may reuse known, the session's own rendered profile.
func (s *Server) sessionSnapshotKnowing(sess model.NativeSession, sub *model.Subscription, now time.Time, db *gorm.DB, known *catalogProfile) nativeState {
	out := nativeState{ServerTime: now, SessionIdleTimeout: int(s.cfg.IdleTTL() / time.Second), ProfileVersion: sess.ProfileVersion, SessionID: sess.ID, ExpiresAt: sess.ExpiresAt, Reason: s.nativeEntitlement(sub, now), LastSequence: sess.LastSequence, UploadBytes: sess.UploadBytes, DownloadBytes: sess.DownloadBytes, ReportInterval: int(s.cfg.ReportInterval() / time.Second), LeaseSeconds: int(s.cfg.Lease() / time.Second), MeteringSource: sessionSource(sess), RatePermille: sessionRate(sess)}
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
		case sess.MeteringSource == meteringNode:
			out.Reason = s.grantReason(db, sess, sub, now, &out)
		default:
			var data []byte
			var err error
			if s.cfg.ClientNodeCatalog {
				var profile catalogProfile
				if known != nil && known.Node.ID == sess.NodeID {
					profile = *known
				} else {
					profile, err = s.catalogProfile(db, sub, sess.NodeID)
				}
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
		deadline := now.Add(s.cfg.Lease())
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
		if e := s.renewGrantLease(tx, sess, sub, now); e != nil {
			return e
		}
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
	if !s.cfg.DevMetering() {
		// Shared-credential delivery is development-only; production clients use grants.
		fail(c, 426, "client_upgrade_required", "此服务端仅支持节点计量的新版客户端，请升级")
		return
	}
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
		if e := s.requireAppVersion(sess); e != nil {
			return e
		}
		if reason := s.nativeEntitlement(sub, now); reason != "" {
			return &apiError{403, reason, "请先升级有效的付费 VIP 套餐并确认剩余流量"}
		}
		if sess.SubscriptionID != nil && !nativeAuthorizationBound(*sess, sub) {
			return &apiError{409, "subscription_changed", "套餐周期已变更，请重新登录"}
		}
		if sess.MeteringSource == meteringNode {
			return &apiError{409, "metering_source_mismatch", "此会话已使用节点计量，不能同时使用客户端上报"}
		}
		var e error
		target := staticTarget
		if s.cfg.ClientNodeCatalog {
			if sess.SubscriptionID != nil {
				sequence, parseErr := strconv.ParseInt(c.Query("last_sequence"), 10, 64)
				if parseErr != nil || sequence != sess.LastSequence {
					return &apiError{409, "traffic_sequence", "切换节点前必须确认当前流量批次"}
				}
			}
			// The selection names a group (an older catalog's server id resolves to its
			// group); without one the Core stays on its current line, else the first.
			selected := nodeID
			if selected == "" {
				selected = sess.NodeID
			}
			alloc, allocErr := s.allocate(tx, sess, sub, selected, "", admitLegacy, now)
			if business := (*apiError)(nil); nodeID == "" && errors.As(allocErr, &business) && business.Code == "group_unavailable" {
				alloc, allocErr = s.allocate(tx, sess, sub, s.firstVisibleGroup(tx, sub), "", admitLegacy, now)
			}
			if business := (*apiError)(nil); errors.As(allocErr, &business) && business.Code == "group_unavailable" {
				// The code an older Core already knows (plan 5.5).
				return &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
			}
			if allocErr != nil {
				return allocErr
			}
			profile := alloc.Profile
			catalog = &profile
			data, sess.NodeID, sess.RatePermille = profile.Data, profile.Node.ID, profile.Node.RatePermille
			target = alloc.Target
		} else {
			data, e = s.readNativeProfile(sub.PlanID)
			if e != nil {
				return e
			}
		}
		fingerprint := nativeEntitlementFingerprint(sub)
		// An older Core selects (and later connects) through /config: it is admitted here,
		// under the same online cap and capacity as POST /connections (plan 5.5).
		lease, e := s.acquireLease(tx, sess, sub, target, model.LeaseByLegacy, "", "", now)
		if e != nil {
			return e
		}
		sess.SubscriptionID, sess.SubscriptionStartsAt = &sub.ID, &sub.StartsAt
		sess.EntitlementFingerprint, sess.ProfileVersion, sess.MeteringSource, sess.LastSeenAt = fingerprint, nativeProfileVersion(data), meteringClient, now
		if e = tx.Model(sess).Updates(map[string]any{"subscription_id": sub.ID, "subscription_starts_at": sub.StartsAt, "entitlement_fingerprint": fingerprint, "profile_version": sess.ProfileVersion, "node_id": sess.NodeID, "rate_permille": sessionRate(*sess), "metering_source": meteringClient, "last_seen_at": now}).Error; e != nil {
			return e
		}
		status = s.admittedSnapshot(*sess, sub, now, tx, lease, catalog)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	response := gin.H{"yaml": string(data), "version": nativeProfileVersion(data), "session": status}
	if catalog != nil {
		// node_id echoes the selected line (the group), which the catalog lists and every
		// shipped Core checks against its selection; the server stays opaque.
		response["nodes"], response["node_id"] = catalog.Nodes, catalog.Group.ID
	}
	c.JSON(200, response)
}

// requireAppVersion is the CLIENT_MIN_APP_VERSION gate of every admission path
// (/config, /connections and /grants).
func (s *Server) requireAppVersion(sess *model.NativeSession) error {
	if min := s.cfg.ClientMinAppVersion; min != "" && config.VersionLess(sess.AppVersion, min) {
		return &apiError{426, "client_upgrade_required", "客户端版本过低，请升级后再连接"}
	}
	return nil
}

func sessionSource(sess model.NativeSession) string {
	if sess.MeteringSource == meteringNode {
		return meteringNode
	}
	return meteringClient
}

func (s *Server) nativeTraffic(c *gin.Context) {
	if !s.cfg.DevMetering() {
		fail(c, 426, "client_upgrade_required", "此服务端仅支持节点计量的新版客户端，请升级")
		return
	}
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
		if sess.MeteringSource == meteringNode {
			// Node-reported sessions are billed from the forwarder only: accepting a
			// client report as well would charge the same bytes twice.
			return &apiError{409, "metering_source_mismatch", "此会话由节点计量，不接受客户端流量上报"}
		}
		var delta usageDelta
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
			sess.LastSequence, sess.UploadBytes, sess.DownloadBytes, sess.LastSeenAt = in.Sequence, in.UploadBytes, in.DownloadBytes, now
			if e = tx.Model(sess).Updates(map[string]any{"last_sequence": in.Sequence, "upload_bytes": in.UploadBytes, "download_bytes": in.DownloadBytes, "last_seen_at": now}).Error; e != nil {
				return e
			}
			// History is written with the charge, never apart from it (plan 7.1).
			delta = usageDelta{up, down, units}
		}
		refused, renewed, e := s.renewClientLease(tx, sess, sub, now, delta)
		if e != nil {
			return e
		}
		if renewed != nil {
			status = s.nativeSnapshotWith(*sess, sub, now, tx, renewed)
		} else {
			status = s.nativeSnapshot(*sess, sub, now, tx)
		}
		if refused != "" && status.Reason == "" {
			blockSnapshot(&status, refused)
		}
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"session": status, "replayed": replayed})
}

func (s *Server) nativeLogout(c *gin.Context) {
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, _ *model.Subscription, now time.Time) error {
		if e := endSessionLeases(tx, now, model.LeaseSessionRevoked, sess.ID); e != nil {
			return e
		}
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
