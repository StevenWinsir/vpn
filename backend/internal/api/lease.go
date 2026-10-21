package api

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Online connection leases (handoff_node_use_history_and_load_balancing_c.md 5). A
// lease is the single fact behind "this device is connected": dashboard presence, the
// per-user online cap, per-server load and slot release all read connection_leases and
// nothing else. A lease counts only while it is active, unexpired and its native
// session is live; those conditions are in every query (liveLeases), so a late
// maintenance sweep never changes an answer.
//
// Lock order (plan 4.3): user -> native session -> subscription (nativeTransaction)
// -> node group (FOR UPDATE) -> lease rows.

// staticTarget stands for the plan-wide development profile, which has no catalog
// server or group.
var staticTarget = leaseTarget{GroupID: uuid.Nil.String(), NodeID: uuid.Nil.String()}

type leaseTarget struct {
	GroupID, NodeID string
	// MaxConnections is the server's capacity; 0 means not limited (static profile).
	MaxConnections int
	// checked: the allocator already holds the group lock and verified capacity.
	checked bool
}

// connectionView is the client-facing summary of one lease.
type connectionView struct {
	ID        string    `json:"id"`
	GroupID   string    `json:"group_id"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

// deviceLimit is decision D1: min(subscription max_devices, CLIENT_MAX_ONLINE_DEVICES).
func (s *Server) deviceLimit(sub *model.Subscription) int {
	if sub == nil || sub.MaxDevices <= 0 {
		return 0
	}
	return min(sub.MaxDevices, s.cfg.MaxOnlineDevices())
}

// liveLeases selects (as l, joined with its session as ns) every lease that counts now.
// A disabled account's devices never count, whichever path disabled it.
func (s *Server) liveLeases(tx *gorm.DB, now time.Time) *gorm.DB {
	return tx.Table(s.cfg.Schema+".connection_leases AS l").
		Joins("JOIN "+s.cfg.Schema+".native_sessions AS ns ON ns.id = l.session_id").
		Joins("JOIN "+s.cfg.Schema+".users AS lu ON lu.id = l.user_id AND lu.status = 'active'").
		Where("l.state = ? AND l.expires_at > ? AND ns.revoked_at IS NULL AND ns.expires_at > ?", model.LeaseActive, now, now)
}

// lockTarget takes the node group row lock that serialises admissions to one group
// (and the capacity check below) without blocking other groups.
func (s *Server) lockTarget(tx *gorm.DB, target leaseTarget) error {
	if target == staticTarget {
		return nil
	}
	var group model.NodeGroup
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&group, "id = ?", target.GroupID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && target.GroupID == target.NodeID {
		// A server written without a group (an older binary, before the next -migrate) is
		// its own group (D3); its row stands in for the group lock.
		var server model.Node
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&server, "id = ? AND group_id IS NULL", target.NodeID).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return groupUnavailable()
	}
	return err
}

func groupUnavailable() error {
	return &apiError{503, "group_unavailable", "该线路暂无可用服务器，请选择其他线路"}
}

// catalogTarget is the lease target of a server chosen by the catalog.
func catalogTarget(n model.Node) leaseTarget {
	group := n.ID
	if n.GroupID != nil {
		group = *n.GroupID
	}
	return leaseTarget{GroupID: group, NodeID: n.ID, MaxConnections: n.MaxConnections}
}

// acquireLease admits this device. It ends the device's (and the session's) previous
// lease, refuses when the user's OTHER devices already fill the cap or the server is
// full, and writes a new active lease. The caller holds the user, session and
// subscription locks; a refusal rolls the whole request back, the ended lease included.
func (s *Server) acquireLease(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, target leaseTarget, method, grantID, key string, now time.Time) (*model.ConnectionLease, error) {
	if !target.checked {
		if err := s.lockTarget(tx, target); err != nil {
			return nil, err
		}
	}
	// One statement ends this device's (and session's) previous lease (replaced) and
	// the leases of the user's sessions whose authorization a subscription change
	// invalidated: those can no longer renew, so they leave now instead of holding a
	// slot until they lapse (plan 5.4).
	own := "session_id = @session OR device_id = @device"
	if err := leaseEndQuery(tx, "user_id = @user AND ("+own+" OR session_id IN (SELECT id FROM "+s.cfg.Schema+".native_sessions WHERE user_id = @user AND id <> @session AND (subscription_id IS DISTINCT FROM @sub OR subscription_starts_at IS DISTINCT FROM @starts OR entitlement_fingerprint <> @fingerprint)))",
		sql.Named("user", sess.UserID), sql.Named("session", sess.ID), sql.Named("device", sess.DeviceID), sql.Named("sub", sub.ID), sql.Named("starts", sub.StartsAt), sql.Named("fingerprint", nativeEntitlementFingerprint(sub))).
		Updates(map[string]any{"state": model.LeaseEnded, "ended_at": now,
			"end_reason": gorm.Expr("CASE WHEN session_id = ? OR device_id = ? THEN ? ELSE ? END", sess.ID, sess.DeviceID, model.LeaseReplaced, model.LeaseEntitlementEnded)}).Error; err != nil {
		return nil, err
	}
	var others int64
	if err := s.liveLeases(tx, now).Where("l.user_id = ? AND l.device_id <> ?", sess.UserID, sess.DeviceID).Count(&others).Error; err != nil {
		return nil, err
	}
	if others >= int64(s.deviceLimit(sub)) {
		return nil, &apiError{403, "device_limit", "已达到同时在线设备上限，请先断开其他设备"}
	}
	if target.MaxConnections > 0 && !target.checked {
		var load int64
		if err := s.liveLeases(tx, now).Where("l.node_id = ?", target.NodeID).Count(&load).Error; err != nil {
			return nil, err
		}
		if load >= int64(target.MaxConnections) {
			return nil, groupFull()
		}
	}
	lease := model.ConnectionLease{ID: uuid.NewString(), UserID: sess.UserID, SessionID: sess.ID, DeviceID: sess.DeviceID, Platform: sess.Platform, AppVersion: sess.AppVersion,
		GroupID: target.GroupID, NodeID: target.NodeID, Method: method, GrantID: grantID, IdempotencyKey: key, State: model.LeaseActive,
		AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(s.cfg.Lease()), StartUploadBytes: sess.UploadBytes, StartDownloadBytes: sess.DownloadBytes}
	if err := tx.Omit("User", "Session").Create(&lease).Error; err != nil {
		return nil, err
	}
	return &lease, nil
}

// renewLease extends this session's active, still-valid lease. A lapsed lease is
// never revived: the device has to be admitted again.
// It returns the renewed row (nil when there was nothing to renew), so the snapshot
// needs no second read.
func (s *Server) renewLease(tx *gorm.DB, sessionID string, now time.Time) (*model.ConnectionLease, error) {
	var lease model.ConnectionLease
	result := tx.Model(&lease).Clauses(clause.Returning{}).Where("session_id = ? AND state = ? AND expires_at > ?", sessionID, model.LeaseActive, now).
		Updates(map[string]any{"renewed_at": now, "expires_at": now.Add(s.cfg.Lease())})
	if result.Error != nil || result.RowsAffected == 0 {
		return nil, result.Error
	}
	return &lease, nil
}

// endLeases ends every active lease matching where (expired ones included, so the
// recorded reason is the real one rather than "expired").
func (s *Server) endLeases(tx *gorm.DB, now time.Time, reason, where string, args ...any) error {
	return leaseEndQuery(tx, where, args...).Updates(map[string]any{"state": model.LeaseEnded, "ended_at": now, "end_reason": reason}).Error
}

// leaseEndQuery selects the active leases matching where; GORM keeps a condition that
// contains OR in parentheses (TestEndLeasesGroupsCallerCondition).
func leaseEndQuery(tx *gorm.DB, where string, args ...any) *gorm.DB {
	return tx.Model(&model.ConnectionLease{}).Where("state = ?", model.LeaseActive).Where(where, args...)
}

// endSessionLeases is used by every path that revokes native sessions, inside the
// same transaction (plan 5.4).
func endSessionLeases(tx *gorm.DB, now time.Time, reason string, sessionIDs ...string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return tx.Model(&model.ConnectionLease{}).Where("state = ? AND session_id IN ?", model.LeaseActive, sessionIDs).
		Updates(map[string]any{"state": model.LeaseEnded, "ended_at": now, "end_reason": reason}).Error
}

// endGrantLease ends the lease created together with a grant once that grant stops
// being usable (revoking, revoked or expired).
func endGrantLease(tx *gorm.DB, now time.Time, reason, grantID string) error {
	return tx.Model(&model.ConnectionLease{}).Where("state = ? AND grant_id = ?", model.LeaseActive, grantID).
		Updates(map[string]any{"state": model.LeaseEnded, "ended_at": now, "end_reason": reason}).Error
}

// grantEndReason maps a grant verdict to the lease end reason.
func grantEndReason(verdict string) string {
	switch verdict {
	case "account_disabled", "native_session_expired":
		return model.LeaseSessionRevoked
	case "node_unavailable":
		return model.LeaseNodeUnavailable
	}
	return model.LeaseEntitlementEnded
}

// renewGrantLease is the node_reported renewal (plan 5.2): GET /session renews the
// lease while the session's grant is live. An entitlement that no longer holds ends it.
func (s *Server) renewGrantLease(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) error {
	if sess.MeteringSource != meteringNode || sess.GrantID == "" {
		return nil
	}
	if s.nativeEntitlement(sub, now) != "" || !nativeAuthorizationBound(*sess, sub) {
		return endGrantLease(tx, now, model.LeaseEntitlementEnded, sess.GrantID)
	}
	var g model.ConnectionGrant
	if err := tx.Select("id", "state", "expires_at").Take(&g, "id = ? AND session_id = ?", sess.GrantID, sess.ID).Error; err != nil || !grantLive(g, now) {
		return nil
	}
	return tx.Model(&model.ConnectionLease{}).Where("grant_id = ? AND state = ? AND expires_at > ?", g.ID, model.LeaseActive, now).
		Updates(map[string]any{"renewed_at": now, "expires_at": now.Add(s.cfg.Lease())}).Error
}

func latestLease(tx *gorm.DB, sessionID string) (*model.ConnectionLease, error) {
	var lease model.ConnectionLease
	// The active lease (at most one per session) is the latest by definition; among
	// ended ones the last acquired, then the last ended, wins.
	err := tx.Where("session_id = ?", sessionID).Order("state = 'active' DESC, acquired_at DESC, ended_at DESC NULLS FIRST, id DESC").Take(&lease).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &lease, err
}

// leaseState is how a session's latest lease looks now: active, expired or ended.
func leaseState(l model.ConnectionLease, now time.Time) string {
	switch {
	case l.State == model.LeaseEnded:
		return model.LeaseEnded
	case !now.Before(l.ExpiresAt):
		return "expired"
	}
	return model.LeaseActive
}

// leaseReason is the snapshot reason a session's latest lease implies ("" for none).
// Only an outcome the client must act on is reported, and only while it is recent: a
// device that has simply been offline for longer than the idle window is just offline.
//   - new Core (connections, grant): lapsed -> connection_expired; ended because the
//     server became unavailable -> connection_reassigned;
//   - old Core (legacy_config): a server-side end -> profile_changed, which it already
//     answers by binding again through /config; a lapse is healed on its next /traffic.
func (s *Server) leaseReason(l *model.ConnectionLease, now time.Time) string {
	if l == nil {
		return ""
	}
	at := l.ExpiresAt
	if l.EndedAt != nil {
		at = *l.EndedAt
	}
	if now.Sub(at) > s.cfg.IdleTTL() {
		return ""
	}
	legacy := l.Method == model.LeaseByLegacy
	switch state := leaseState(*l, now); {
	case state == "expired" || state == model.LeaseEnded && l.EndReason == model.LeaseExpired:
		if legacy {
			return ""
		}
		return "connection_expired"
	case state == model.LeaseEnded && l.EndReason == model.LeaseNodeUnavailable:
		if legacy {
			return "profile_changed"
		}
		return "connection_reassigned"
	}
	return ""
}

func connectionOf(l model.ConnectionLease, now time.Time) connectionView {
	return connectionView{ID: l.ID, GroupID: l.GroupID, State: leaseState(l, now), ExpiresAt: l.ExpiresAt.UTC()}
}

// withLease adds the session's connection fields and lease verdict to a snapshot.
func (s *Server) withLease(out *nativeState, l *model.ConnectionLease, now time.Time) {
	if l == nil {
		return
	}
	expires := l.ExpiresAt.UTC()
	out.ConnectionID, out.ConnectionState, out.ConnectionExpiresAt = l.ID, leaseState(*l, now), &expires
	switch reason := s.leaseReason(l, now); {
	case reason == "":
	case out.Reason == "":
		blockSnapshot(out, reason)
	case reason == "connection_reassigned" || reason == "profile_changed":
		// The session's server was withdrawn, so its profile no longer renders; what the
		// client must do is reconnect to the group, not give the line up (plan 6.4).
		if out.Reason == "client_config_unavailable" || out.Reason == "profile_changed" {
			blockSnapshot(out, reason)
		}
	}
}

// blockSnapshot records a verdict that forbids connecting.
func blockSnapshot(out *nativeState, reason string) {
	out.Reason, out.CanConnect, out.AuthorizationExpiresAt = reason, false, nil
}

// usageDelta is one accepted batch's contribution to the history.
type usageDelta struct{ up, down, units int64 }

// renewRecording renews the session's valid lease and, for a non-empty batch, adds it
// to the history bucket in the same statement: the data-modifying CTE runs exactly
// once whether or not a lease matched, so history never depends on presence.
func (s *Server) renewRecording(tx *gorm.DB, sess *model.NativeSession, now time.Time, d usageDelta) (*model.ConnectionLease, error) {
	if d == (usageDelta{}) {
		return s.renewLease(tx, sess.ID, now)
	}
	var lease model.ConnectionLease
	err := tx.Raw(`WITH bucket AS (
			INSERT INTO `+s.cfg.Schema+`.usage_buckets AS b (user_id, bucket_start, upload_bytes, download_bytes, charged_units, updated_at)
			VALUES (@user, @bucket, @up, @down, @units, @now)
			ON CONFLICT (user_id, bucket_start) DO UPDATE SET upload_bytes = b.upload_bytes + EXCLUDED.upload_bytes,
				download_bytes = b.download_bytes + EXCLUDED.download_bytes, charged_units = b.charged_units + EXCLUDED.charged_units, updated_at = EXCLUDED.updated_at)
		UPDATE `+s.cfg.Schema+`.connection_leases SET renewed_at = @now, expires_at = @expires
		WHERE session_id = @session AND state = @active AND expires_at > @now
		RETURNING *`,
		sql.Named("user", sess.UserID), sql.Named("bucket", now.UTC().Truncate(model.UsageGranularity)), sql.Named("up", d.up), sql.Named("down", d.down), sql.Named("units", d.units),
		sql.Named("now", now), sql.Named("expires", now.Add(s.cfg.Lease())), sql.Named("session", sess.ID), sql.Named("active", model.LeaseActive)).Scan(&lease).Error
	if err != nil || lease.ID == "" {
		return nil, err
	}
	return &lease, nil
}

// addUsage folds one accepted batch into the user's current 5-minute history bucket
// (server receive time), in the billing transaction itself (plan 7.1).
func addUsage(tx *gorm.DB, userID string, now time.Time, up, down, units int64) error {
	if up == 0 && down == 0 && units == 0 {
		return nil
	}
	bucket := model.UsageBucket{UserID: userID, BucketStart: now.UTC().Truncate(model.UsageGranularity), UploadBytes: up, DownloadBytes: down, ChargedUnits: units, UpdatedAt: now}
	return tx.Omit("User").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "bucket_start"}},
		DoUpdates: clause.Set{
			{Column: clause.Column{Name: "upload_bytes"}, Value: gorm.Expr("usage_buckets.upload_bytes + EXCLUDED.upload_bytes")},
			{Column: clause.Column{Name: "download_bytes"}, Value: gorm.Expr("usage_buckets.download_bytes + EXCLUDED.download_bytes")},
			{Column: clause.Column{Name: "charged_units"}, Value: gorm.Expr("usage_buckets.charged_units + EXCLUDED.charged_units")},
			{Column: clause.Column{Name: "updated_at"}, Value: gorm.Expr("EXCLUDED.updated_at")},
		},
	}).Create(&bucket).Error
}

// renewClientLease is the client_reported renewal, run by every accepted /traffic
// (plan 5.2): there is no extra request while connected. An entitlement that no
// longer holds ends the lease. A lapsed lease is not extended; an older Core, whose
// first report after a pause is the only sign it is connecting again, is admitted
// anew under the full cap and capacity checks, and a refusal is returned as the
// snapshot reason (the report itself is still accounted).
// The batch's history delta is recorded here too, in the same statement as the
// renewal (one round trip on every report). The renewed lease is returned for the
// snapshot (nil when none was renewed).
func (s *Server) renewClientLease(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time, delta usageDelta) (string, *model.ConnectionLease, error) {
	if reason := s.nativeEntitlement(sub, now); reason != "" {
		if err := addUsage(tx, sess.UserID, now, delta.up, delta.down, delta.units); err != nil {
			return "", nil, err
		}
		return "", nil, endSessionLeases(tx, now, model.LeaseEntitlementEnded, sess.ID)
	}
	renewed, err := s.renewRecording(tx, sess, now, delta)
	if err != nil || renewed != nil {
		return "", renewed, err
	}
	refused, err := s.readmitLegacy(tx, sess, sub, now)
	return refused, nil, err
}

// readmitLegacy admits an older Core again once its lease has lapsed (see above).
func (s *Server) readmitLegacy(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, now time.Time) (string, error) {
	latest, err := latestLease(tx, sess.ID)
	if err != nil {
		return "", err
	}
	if latest != nil && (latest.Method != model.LeaseByLegacy || latest.State == model.LeaseEnded && latest.EndReason != model.LeaseExpired && latest.EndReason != model.LeaseEntitlementEnded) {
		// A new Core reconnects by itself; a server-side end is answered by profile_changed.
		return "", nil
	}
	target := staticTarget
	if s.cfg.ClientNodeCatalog {
		profile, e := s.catalogProfile(tx, sub, sess.NodeID)
		if e != nil || profile.Node.ID != sess.NodeID {
			return "", nil // the snapshot reports the unavailable line
		}
		target = catalogTarget(profile.Node)
	}
	err = tx.Transaction(func(inner *gorm.DB) error {
		_, e := s.acquireLease(inner, sess, sub, target, model.LeaseByLegacy, "", "", now)
		return e
	})
	var business *apiError
	if errors.As(err, &business) {
		return business.Code, nil
	}
	return "", err
}
