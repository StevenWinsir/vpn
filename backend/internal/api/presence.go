package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// Website presence and history (plan 8.2). Explicit response types: no server id,
// address, session id, token or lease id ever leaves through these endpoints.

type onlineDevice struct {
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
	// Device is the last 6 characters of the client's persistent device UUID.
	Device          string    `json:"device"`
	Line            string    `json:"line"`
	ConnectedAt     time.Time `json:"connected_at"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	// UploadBytes/DownloadBytes are this connection's traffic so far.
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
}

type signedInDevice struct {
	Platform   string    `json:"platform"`
	AppVersion string    `json:"app_version"`
	Device     string    `json:"device"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type usageInterval struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
	ChargedUnits  int64     `json:"charged_units"`
}

func shortDevice(id string) string {
	if len(id) <= 6 {
		return id
	}
	return id[len(id)-6:]
}

// onlineDevices lists the user's devices that hold a lease that counts now.
func (s *Server) onlineDevices(db *gorm.DB, userID string, now time.Time) ([]onlineDevice, error) {
	var rows []struct {
		Platform, AppVersion, DeviceID, Line string
		AcquiredAt, RenewedAt                time.Time
		UploadBytes, DownloadBytes           int64
	}
	err := s.liveLeases(db, now).
		Joins("LEFT JOIN "+s.cfg.Schema+".node_groups AS g ON g.id = l.group_id").
		Joins("LEFT JOIN "+s.cfg.Schema+".connection_grants AS cg ON l.method = ? AND cg.id::text = l.grant_id", model.LeaseByGrant).
		Where("l.user_id = ?", userID).
		Select(`l.platform, l.app_version, l.device_id::text AS device_id, COALESCE(g.name, '') AS line, l.acquired_at, l.renewed_at,
			CASE WHEN cg.id IS NULL THEN GREATEST(ns.upload_bytes - l.start_upload_bytes, 0) ELSE cg.base_upload + cg.epoch_upload END AS upload_bytes,
			CASE WHEN cg.id IS NULL THEN GREATEST(ns.download_bytes - l.start_download_bytes, 0) ELSE cg.base_download + cg.epoch_download END AS download_bytes`).
		Order("l.acquired_at, l.id").Scan(&rows).Error
	out := make([]onlineDevice, 0, len(rows))
	for _, r := range rows {
		out = append(out, onlineDevice{Platform: r.Platform, AppVersion: r.AppVersion, Device: shortDevice(r.DeviceID), Line: r.Line, ConnectedAt: r.AcquiredAt.UTC(), LastHeartbeatAt: r.RenewedAt.UTC(), UploadBytes: r.UploadBytes, DownloadBytes: r.DownloadBytes})
	}
	return out, err
}

func (s *Server) meDevices(c *gin.Context) {
	now := s.now()
	user := currentUser(c)
	sub, err := s.subscription(c)
	if err != nil {
		fail(c, 503, "database_unavailable", "设备状态暂时不可用")
		return
	}
	online, err := s.onlineDevices(s.database(c), user.ID, now)
	if err != nil {
		fail(c, 503, "database_unavailable", "设备状态暂时不可用")
		return
	}
	// Signed in (session within its idle window) but not connected: takes no slot.
	signedIn, err := s.idleDevices(s.database(c), user.ID, now)
	if err != nil {
		fail(c, 503, "database_unavailable", "设备状态暂时不可用")
		return
	}
	c.JSON(200, gin.H{"server_time": now, "device_limit": s.deviceLimit(sub), "online_count": len(online), "online": online, "signed_in": signedIn,
		"lease_seconds": int(s.cfg.Lease() / time.Second)})
}

// usageWindow is the 24-hour window ending with the current 5-minute bucket (plan
// 7.2): buckets starting after T-24h, up to and including T, in 8 three-hour intervals
// counted from the oldest end, so the newest interval holds the bucket in progress.
func usageWindow(now time.Time) (time.Time, time.Time) {
	baseline := model.UsageBaseline(now)
	return baseline.Add(-model.UsageWindow + model.UsageGranularity), baseline.Add(model.UsageGranularity)
}

func (s *Server) usageHistory(db *gorm.DB, userID string, now time.Time) ([]usageInterval, error) {
	start, end := usageWindow(now)
	var rows []model.UsageBucket
	// The lower bound is always in the query: a late cleanup never shows older data.
	if err := db.Where("user_id = ? AND bucket_start >= ? AND bucket_start < ?", userID, start, end).Find(&rows).Error; err != nil {
		return nil, err
	}
	count := int(model.UsageWindow / model.UsageBucketSpan)
	out := make([]usageInterval, count)
	for i := range out {
		out[i].Start = start.Add(time.Duration(i) * model.UsageBucketSpan)
		out[i].End = out[i].Start.Add(model.UsageBucketSpan)
	}
	for _, r := range rows {
		i := int(r.BucketStart.Sub(start) / model.UsageBucketSpan)
		out[i].UploadBytes += r.UploadBytes
		out[i].DownloadBytes += r.DownloadBytes
		out[i].ChargedUnits += r.ChargedUnits
	}
	return out, nil
}

func (s *Server) meUsageHistory(c *gin.Context) {
	now := s.now()
	intervals, err := s.usageHistory(s.database(c), currentUser(c).ID, now)
	if err != nil {
		fail(c, 503, "database_unavailable", "流量历史暂时不可用")
		return
	}
	var total usageInterval
	for _, i := range intervals {
		total.UploadBytes += i.UploadBytes
		total.DownloadBytes += i.DownloadBytes
		total.ChargedUnits += i.ChargedUnits
	}
	start, end := usageWindow(now)
	c.JSON(200, gin.H{"server_time": now, "window_start": start, "window_end": end,
		"bucket_seconds": int(model.UsageBucketSpan / time.Second), "granularity_seconds": int(model.UsageGranularity / time.Second),
		"buckets": intervals, "total_upload_bytes": total.UploadBytes, "total_download_bytes": total.DownloadBytes, "total_charged_units": total.ChargedUnits})
}
