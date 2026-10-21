package api

import (
	"errors"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
	"vpn/nodepolicy"
)

// Connection grants (handoff_app.md 7.4). A grant is a per-user, per-node credential
// lease. The client asks for it, the node's agent installs it, and only then does the
// client receive the credential. All accounting for the grant comes from the node.

const (
	grantPending  = "pending"
	grantReady    = "ready"
	grantRevoking = "revoking"
	grantRevoked  = "revoked"
	grantExpired  = "expired"
)

var idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func grantView(g model.ConnectionGrant) grantResponse {
	out := grantResponse{GrantID: g.ID, State: g.State}
	if g.State == grantPending {
		out.Reason = "grant_pending"
	}
	return out
}

func grantOpen(state string) bool {
	return state == grantPending || state == grantReady || state == grantRevoking
}

// grantLive: the client may still obtain or use this grant's credential.
func grantLive(g model.ConnectionGrant, now time.Time) bool {
	return (g.State == grantPending || g.State == grantReady) && now.Before(g.ExpiresAt)
}

// reserved is what the grant still holds in its period's budget pool.
func reserved(g model.ConnectionGrant) int64 {
	return billing.ReservedUnits(g.BudgetBytes, g.ChargedBytes, g.RatePermille, g.ReleasedAt != nil)
}

// ensurePeriod returns the locked period row for the current subscription period,
// creating it from the live subscription on first use (existing balances are copied,
// never reset). Callers must already hold the user's row lock.
func ensurePeriod(tx *gorm.DB, sub *model.Subscription) (*model.SubscriptionPeriod, error) {
	var p model.SubscriptionPeriod
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("subscription_id = ? AND starts_at = ?", sub.ID, sub.StartsAt).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		p = model.SubscriptionPeriod{ID: uuid.NewString(), UserID: sub.UserID, SubscriptionID: sub.ID, StartsAt: sub.StartsAt}
		syncPeriod(&p, sub)
		return &p, tx.Create(&p).Error
	}
	if err != nil {
		return nil, err
	}
	syncPeriod(&p, sub)
	return &p, tx.Save(&p).Error
}

// syncPeriod mirrors the live subscription into its own (current) period.
func syncPeriod(p *model.SubscriptionPeriod, sub *model.Subscription) {
	p.ExpiresAt, p.LimitBytes, p.UsedUnits = sub.ExpiresAt, sub.TrafficLimitBytes, sub.UsedUnits
	p.UploadBytes, p.DownloadBytes = sub.UploadBytes, sub.DownloadBytes
}

func periodIsCurrent(p *model.SubscriptionPeriod, sub *model.Subscription) bool {
	return sub != nil && p.SubscriptionID == sub.ID && p.StartsAt.Equal(sub.StartsAt)
}

type grantResponse struct {
	GrantID string `json:"grant_id"`
	State   string `json:"state"`
	// Reason is "grant_pending" while the node has not confirmed installation.
	Reason  string       `json:"reason,omitempty"`
	YAML    string       `json:"yaml,omitempty"`
	Version string       `json:"version,omitempty"`
	NodeID  string       `json:"node_id,omitempty"`
	Session *nativeState `json:"session,omitempty"`
}

func (s *Server) nativeCapabilities(c *gin.Context) {
	modes := []string{meteringNode}
	if s.cfg.DevMetering() {
		modes = []string{meteringClient, meteringNode}
	}
	c.JSON(200, gin.H{"contract_versions": []int{1, 2, 3}, "metering_modes": modes, "min_app_version": s.cfg.ClientMinAppVersion, "core_policy_revision": nodepolicy.CoreRevision})
}

// grantProfile renders the exact document a grant holder receives: the node's
// definition with the administrator's shared secret replaced by the grant credential.
func (s *Server) grantProfile(db *gorm.DB, sub *model.Subscription, g model.ConnectionGrant) ([]byte, string, error) {
	profile, err := s.catalogProfile(db, sub, g.NodeID)
	if err != nil || profile.Node.ID != g.NodeID {
		return nil, "", &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	}
	credential, err := nodes.OpenCredential(s.cfg.NodeEncryptionKey, g.ID, g.CredentialCipher)
	if err != nil {
		return nil, "", &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	}
	proxy, err := nodes.ApplyCredential(profile.Proxy, credential)
	if err != nil {
		return nil, "", &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	}
	data, err := renderProfile(profile.Tag, proxy)
	if err != nil {
		return nil, "", &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	}
	return data, nativeProfileVersion(data), nil
}

// grantReason is the session verdict for a node_reported session. A session between
// two grants (none held) is simply ready for a new one.
func (s *Server) grantReason(db *gorm.DB, sess model.NativeSession, sub *model.Subscription, now time.Time, out *nativeState) string {
	if sess.GrantID == "" {
		return ""
	}
	var g model.ConnectionGrant
	if db.First(&g, "id = ? AND session_id = ?", sess.GrantID, sess.ID).Error != nil {
		return "client_config_unavailable"
	}
	out.GrantID, out.GrantState, out.GrantExpiresAt = g.ID, g.State, &g.ExpiresAt
	if !grantLive(g, now) {
		return "grant_expired"
	}
	_, version, err := s.grantProfile(db, sub, g)
	if err != nil {
		return "client_config_unavailable"
	}
	if version != sess.ProfileVersion {
		return "profile_changed"
	}
	return ""
}

func (s *Server) createGrant(c *gin.Context) {
	key := c.GetHeader("Idempotency-Key")
	if !idempotencyKey.MatchString(key) {
		fail(c, 400, "invalid_idempotency_key", "缺少或无效的 Idempotency-Key")
		return
	}
	var in struct {
		NodeID string `json:"node_id"`
		// AvoidNodeID is a server the client just failed on (one automatic retry).
		AvoidNodeID string `json:"avoid_node_id"`
	}
	if !decode(c, &in) {
		return
	}
	// node_id names the group (a server id from an older client resolves to its group).
	nodeID, err := uuid.Parse(in.NodeID)
	avoid, avoidOK := optionalUUID(in.AvoidNodeID)
	if err != nil || nodeID == uuid.Nil || !avoidOK || !s.cfg.ClientNodeCatalog {
		fail(c, 400, "invalid_managed_selection", "节点选择无效")
		return
	}
	var out grantResponse
	status := 202
	err = s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		if e := s.requireAppVersion(sess); e != nil {
			return e
		}
		if reason := s.nativeEntitlement(sub, now); reason != "" {
			return &apiError{403, reason, "请先升级有效的付费 VIP 套餐并确认剩余流量"}
		}
		if sess.SubscriptionID != nil && !nativeAuthorizationBound(*sess, sub) {
			return &apiError{409, "subscription_changed", "套餐周期已变更，请重新登录"}
		}
		var existing model.ConnectionGrant
		switch e := tx.Where("session_id = ? AND idempotency_key = ?", sess.ID, key).First(&existing).Error; {
		case e == nil:
			var replay model.ConnectionLease
			sameGroup := tx.Select("group_id").Where("grant_id = ? AND group_id = ?", existing.ID, nodeID.String()).Take(&replay).Error == nil
			if existing.NodeID != nodeID.String() && !sameGroup {
				return &apiError{409, "idempotency_conflict", "此请求标识已用于其他线路"}
			}
			out = grantView(existing)
			if existing.State != grantPending {
				status = 200
			}
			return nil
		case !errors.Is(e, gorm.ErrRecordNotFound):
			return e
		}
		if sess.MeteringSource == meteringClient || sess.LastSequence > 0 {
			return &apiError{409, "metering_source_mismatch", "此会话已使用客户端上报，不能再使用节点计量"}
		}
		alloc, e := s.allocate(tx, sess, sub, nodeID.String(), avoid, admitGrant, now)
		if e != nil {
			return e
		}
		profile, agent, unavailable := alloc.Profile, alloc.Agent, agentUnavailable()
		proxyType, _ := profile.Proxy["type"].(string)
		credential, e := nodes.NewCredential(proxyType)
		if e != nil {
			return unavailable
		}
		proxy, e := nodes.ApplyCredential(profile.Proxy, credential)
		if e != nil {
			return unavailable
		}
		data, e := renderProfile(profile.Tag, proxy)
		if e != nil {
			return unavailable
		}
		fingerprint := nativeEntitlementFingerprint(sub)
		// The grant and the device's online lease are created together; the previous
		// grant's lease (same session) is replaced.
		id := uuid.NewString()
		if _, e = s.acquireLease(tx, sess, sub, alloc.Target, model.LeaseByGrant, id, "", now); e != nil {
			return e
		}
		period, e := ensurePeriod(tx, sub)
		if e != nil {
			return e
		}
		rate := profile.Node.RatePermille
		budget := billing.BudgetBytes(billing.AvailableUnits(period.LimitBytes, period.UsedUnits, period.ReservedUnits), rate, s.cfg.GrantBudget())
		if budget <= 0 {
			return &apiError{403, "quota_exhausted", "套餐可用流量不足"}
		}
		// One live grant per session: a new choice retires the previous one. Its
		// reservation stays held until the node confirms or the lease safely lapses.
		if e = tx.Model(&model.ConnectionGrant{}).Where("session_id = ? AND state IN ?", sess.ID, []string{grantPending, grantReady}).Updates(map[string]any{"state": grantRevoking}).Error; e != nil {
			return e
		}
		sealed, e := nodes.SealCredential(s.cfg.NodeEncryptionKey, id, credential)
		if e != nil {
			return e
		}
		g := model.ConnectionGrant{ID: id, UserID: sess.UserID, SessionID: sess.ID, IdempotencyKey: key, NodeID: profile.Node.ID, AgentID: agent.ID, PeriodID: period.ID,
			State: grantPending, Protocol: proxyType, RatePermille: rate, CredentialCipher: sealed, ExpiresAt: s.grantDeadline(now, sess, sub, now), BudgetBytes: budget}
		if e = tx.Create(&g).Error; e != nil {
			return e
		}
		period.ReservedUnits += reserved(g)
		if e = tx.Model(period).Update("reserved_units", period.ReservedUnits).Error; e != nil {
			return e
		}
		sess.SubscriptionID, sess.SubscriptionStartsAt = &sub.ID, &sub.StartsAt
		sess.EntitlementFingerprint, sess.ProfileVersion = fingerprint, nativeProfileVersion(data)
		sess.NodeID, sess.RatePermille, sess.MeteringSource, sess.GrantID = profile.Node.ID, rate, meteringNode, g.ID
		if e = tx.Model(sess).Updates(map[string]any{"subscription_id": sub.ID, "subscription_starts_at": sub.StartsAt, "entitlement_fingerprint": fingerprint,
			"profile_version": sess.ProfileVersion, "node_id": sess.NodeID, "rate_permille": rate, "metering_source": meteringNode, "grant_id": g.ID}).Error; e != nil {
			return e
		}
		out = grantView(g)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(status, out)
}

// sessionSourceFor is the metering source this deployment issues to new sessions.
func sessionSourceFor(cfg config.Config) string {
	if cfg.DevMetering() {
		return meteringClient
	}
	return meteringNode
}

// agentSupports reports whether the agent claims enforcement for this proxy type.
func agentSupports(a model.NodeAgent, proxyType string) bool {
	for _, p := range protocolList(a.Protocols) {
		if p == proxyType {
			return true
		}
	}
	return false
}

// grantDeadline is the absolute time the node may keep a grant without hearing from
// the control plane again: it never outlives the session or the subscription.
func (s *Server) grantDeadline(now time.Time, sess *model.NativeSession, sub *model.Subscription, lastSeen time.Time) time.Time {
	deadline := now.Add(s.cfg.Lease())
	for _, limit := range []time.Time{sess.ExpiresAt, sub.ExpiresAt, lastSeen.Add(s.cfg.IdleTTL())} {
		if limit.Before(deadline) {
			deadline = limit
		}
	}
	return deadline
}

func (s *Server) loadOwnGrant(tx *gorm.DB, sess *model.NativeSession, c *gin.Context) (model.ConnectionGrant, error) {
	var g model.ConnectionGrant
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return g, &apiError{404, "grant_not_found", "授权不存在"}
	}
	if e := tx.Where("id = ? AND session_id = ? AND user_id = ?", id.String(), sess.ID, sess.UserID).First(&g).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return g, &apiError{404, "grant_not_found", "授权不存在"}
		}
		return g, e
	}
	return g, nil
}

func (s *Server) getGrant(c *gin.Context) {
	var out grantResponse
	status := 200
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		g, e := s.loadOwnGrant(tx, sess, c)
		if e != nil {
			return e
		}
		out = grantView(g)
		switch {
		case g.State == grantPending && now.Before(g.ExpiresAt):
			status = 202
		case g.State == grantReady && now.Before(g.ExpiresAt):
			if reason := s.nativeEntitlement(sub, now); reason != "" {
				return &apiError{403, reason, "请先升级有效的付费 VIP 套餐并确认剩余流量"}
			}
			if !nativeAuthorizationBound(*sess, sub) {
				return &apiError{409, "subscription_changed", "套餐周期已变更，请重新登录"}
			}
			data, version, e := s.grantProfile(tx, sub, g)
			if e != nil {
				return e
			}
			if version != sess.ProfileVersion {
				return &apiError{409, "profile_changed", "线路配置已更新，请重新选择线路"}
			}
			snapshot := s.nativeSnapshot(*sess, sub, now, tx)
			out.YAML, out.Version, out.NodeID, out.Session = string(data), version, g.NodeID, &snapshot
		case g.State == grantPending || g.State == grantReady:
			return &apiError{410, "grant_expired", "授权已过期，请重新连接"}
		}
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(status, out)
}

// revokeGrant is the client asking to be disconnected at the node. It is idempotent
// and only changes state; the reservation is released when the node confirms.
func (s *Server) revokeGrant(c *gin.Context) {
	// Reason is optional (older Cores send {}); a failure reason is a health report.
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(c, &in) {
		return
	}
	endReason, known := releaseReasons[in.Reason]
	if in.Reason == "" {
		endReason, known = model.LeaseReleased, true
	}
	if !known {
		fail(c, 400, "invalid_release", "释放请求无效")
		return
	}
	var out grantResponse
	err := s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		g, e := s.loadOwnGrant(tx, sess, c)
		if e != nil {
			return e
		}
		if g.State == grantPending || g.State == grantReady {
			g.State = grantRevoking
			if e = tx.Model(&g).Update("state", grantRevoking).Error; e != nil {
				return e
			}
		}
		var lease model.ConnectionLease
		switch e = tx.Where("grant_id = ? AND state = ?", g.ID, model.LeaseActive).Take(&lease).Error; {
		case e == nil:
			if e = s.endWithReport(tx, &lease, endReason, now); e != nil {
				return e
			}
		case !errors.Is(e, gorm.ErrRecordNotFound):
			return e
		}
		if sess.GrantID == g.ID {
			if e = tx.Model(sess).Update("grant_id", "").Error; e != nil {
				return e
			}
		}
		out = grantResponse{GrantID: g.ID, State: g.State}
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(200, out)
}
