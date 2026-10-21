package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// Client-reported admissions (contract v3, plan 8.1). A new Core asks for a connection
// when it actually connects; selecting a line or reading the catalog never takes a
// slot. Node-reported groups are admitted through POST /grants instead.

type connectionResponse struct {
	Connection connectionView `json:"connection"`
	// NodeID is the opaque server id the profile was rendered for.
	NodeID  string       `json:"node_id"`
	YAML    string       `json:"yaml"`
	Version string       `json:"version"`
	Nodes   []nodeChoice `json:"nodes"`
	Session nativeState  `json:"session"`
}

// releaseReasons maps the client's allow-listed reason to the recorded end reason.
var releaseReasons = map[string]string{
	"user": model.LeaseReleased, "stopped": model.LeaseReleased, "network_changed": model.LeaseReleased, "lifecycle": model.LeaseReleased,
	"tunnel_unhealthy": model.LeaseTunnelUnhealthy, "tunnel_failed": model.LeaseTunnelFailed,
}

// optionalUUID accepts "" (absent) or a canonical non-nil UUID.
func optionalUUID(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return "", false
	}
	return id.String(), true
}

func (s *Server) createConnection(c *gin.Context) {
	if !s.cfg.DevMetering() {
		fail(c, 426, "client_upgrade_required", "此服务端仅支持节点计量的新版客户端，请升级")
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if !idempotencyKey.MatchString(key) {
		fail(c, 400, "invalid_idempotency_key", "缺少或无效的 Idempotency-Key")
		return
	}
	var in struct {
		GroupID      string `json:"group_id"`
		LastSequence int64  `json:"last_sequence"`
		AvoidNodeID  string `json:"avoid_node_id"`
	}
	if !decode(c, &in) {
		return
	}
	groupID, err := uuid.Parse(in.GroupID)
	avoid, avoidOK := optionalUUID(in.AvoidNodeID)
	if err != nil || groupID == uuid.Nil || !avoidOK || in.LastSequence < 0 || !s.cfg.ClientNodeCatalog {
		fail(c, 400, "invalid_managed_selection", "节点选择无效")
		return
	}
	var out connectionResponse
	status := http.StatusCreated
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
		if sess.MeteringSource == meteringNode {
			return &apiError{409, "metering_source_mismatch", "此会话已使用节点计量，不能同时使用客户端上报"}
		}
		var lease *model.ConnectionLease
		var replay model.ConnectionLease
		switch e := tx.Where("session_id = ? AND idempotency_key = ?", sess.ID, key).Take(&replay).Error; {
		case e == nil:
			if replay.GroupID != groupID.String() {
				return &apiError{409, "idempotency_conflict", "此请求标识已用于其他线路"}
			}
			lease, status = &replay, http.StatusOK
		case !errors.Is(e, gorm.ErrRecordNotFound):
			return e
		}
		if lease == nil && sess.SubscriptionID != nil && in.LastSequence != sess.LastSequence {
			return &apiError{409, "traffic_sequence", "切换节点前必须确认当前流量批次"}
		}
		server := ""
		if lease != nil {
			server = lease.NodeID
		} else {
			var e error
			if server, e = s.groupServer(tx, groupID.String(), avoid); e != nil {
				return e
			}
		}
		profile, e := s.catalogProfile(tx, sub, server)
		if e != nil || profile.Node.ID != server {
			return groupUnavailable()
		}
		governed, e := s.agentNodes(tx)
		if e != nil {
			return e
		}
		if _, ok := governed[server]; ok {
			return &apiError{409, "grant_required", "此线路需使用节点授权连接"}
		}
		if lease == nil {
			if lease, e = s.acquireLease(tx, sess, sub, catalogTarget(profile.Node), model.LeaseByConnections, "", key, now); e != nil {
				return e
			}
			fingerprint := nativeEntitlementFingerprint(sub)
			sess.SubscriptionID, sess.SubscriptionStartsAt = &sub.ID, &sub.StartsAt
			sess.EntitlementFingerprint, sess.ProfileVersion, sess.MeteringSource = fingerprint, nativeProfileVersion(profile.Data), meteringClient
			sess.NodeID, sess.RatePermille = profile.Node.ID, profile.Node.RatePermille
			if e = tx.Model(sess).Updates(map[string]any{"subscription_id": sub.ID, "subscription_starts_at": sub.StartsAt, "entitlement_fingerprint": fingerprint, "profile_version": sess.ProfileVersion, "node_id": sess.NodeID, "rate_permille": sessionRate(*sess), "metering_source": meteringClient}).Error; e != nil {
				return e
			}
		}
		out = connectionResponse{Connection: connectionOf(*lease, now), NodeID: server, YAML: string(profile.Data), Version: nativeProfileVersion(profile.Data), Nodes: profile.Nodes, Session: s.nativeSnapshot(*sess, sub, now, tx)}
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(status, out)
}

// releaseConnection ends this session's lease (idempotent). The Core calls it after it
// stopped forwarding and confirmed its last traffic batch.
func (s *Server) releaseConnection(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(c, &in) {
		return
	}
	endReason, known := releaseReasons[in.Reason]
	if err != nil || !known {
		fail(c, 400, "invalid_release", "释放请求无效")
		return
	}
	var out struct {
		Connection connectionView `json:"connection"`
		Session    nativeState    `json:"session"`
	}
	err = s.nativeTransaction(c, func(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
		var lease model.ConnectionLease
		if e := tx.Where("id = ? AND session_id = ? AND user_id = ?", id.String(), sess.ID, sess.UserID).Take(&lease).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return &apiError{404, "connection_not_found", "连接记录不存在，请重新连接"}
			}
			return e
		}
		if lease.Method == model.LeaseByGrant {
			return &apiError{409, "grant_required", "此线路需使用节点授权连接"}
		}
		if lease.State == model.LeaseActive {
			lease.State, lease.EndedAt, lease.EndReason = model.LeaseEnded, &now, endReason
			if e := tx.Model(&lease).Updates(map[string]any{"state": lease.State, "ended_at": now, "end_reason": endReason}).Error; e != nil {
				return e
			}
		}
		out.Connection, out.Session = connectionOf(lease, now), s.nativeSnapshot(*sess, sub, now, tx)
		return nil
	})
	if s.nativeFailure(c, err) {
		return
	}
	c.JSON(http.StatusOK, out)
}
