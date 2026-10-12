package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// The node-agent API is served on its own mTLS listener (handoff_app.md 7.4). The
// node a request controls comes from the pinned certificate, never from the body.

const maxAgentList = 4096

// NewAgent builds the internal router for node agents.
func NewAgent(db *gorm.DB, cfg config.Config) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{db: db, cfg: cfg}
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		defer func() {
			if recover() != nil {
				fail(c, 500, "internal_error", "服务暂时不可用")
			}
		}()
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.RequestTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
		if c.Request.Method == http.MethodPost && c.GetHeader("Content-Type") != "application/json" {
			fail(c, 415, "json_required", "请求必须使用 application/json")
			return
		}
		c.Next()
	})
	group := r.Group("/internal/v1/node-agents")
	group.Use(s.requireAgent())
	group.POST("/sync", s.agentSync)
	group.POST("/usage", s.agentUsage)
	r.NoRoute(func(c *gin.Context) { fail(c, 404, "not_found", "接口不存在") })
	return r
}

func (s *Server) requireAgent() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.TLS == nil || len(c.Request.TLS.PeerCertificates) == 0 {
			fail(c, 401, "agent_required", "需要节点代理客户端证书")
			return
		}
		sum := sha256.Sum256(c.Request.TLS.PeerCertificates[0].Raw)
		var agent model.NodeAgent
		err := s.database(c).Where("cert_fingerprint = ? AND enabled = ?", hex.EncodeToString(sum[:]), true).First(&agent).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 401, "agent_unknown", "节点代理未登记或已停用")
			return
		}
		if err != nil {
			fail(c, 503, "agent_service_unavailable", "服务暂时不可用")
			return
		}
		c.Set("node_agent", agent)
		c.Next()
	}
}

func protocolList(raw string) []string {
	var list []string
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	return list
}

type agentSyncRequest struct {
	Epoch        int64    `json:"epoch"`
	AgentVersion string   `json:"agent_version"`
	Protocols    []string `json:"protocols"`
	Installed    []string `json:"installed"`
	Removed      []string `json:"removed"`
}

type agentOrder struct {
	GrantID          string            `json:"grant_id"`
	Action           string            `json:"action"`
	Protocol         string            `json:"protocol,omitempty"`
	Credential       map[string]string `json:"credential,omitempty"`
	RatePermille     int64             `json:"rate_permille,omitempty"`
	ExpiresAt        *time.Time        `json:"expires_at,omitempty"`
	EpochBudgetBytes int64             `json:"epoch_budget_bytes"`
}

func uuidSet(list []string) (map[string]bool, bool) {
	if len(list) > maxAgentList {
		return nil, false
	}
	out := make(map[string]bool, len(list))
	for _, item := range list {
		id, err := uuid.Parse(item)
		if err != nil || id == uuid.Nil {
			return nil, false
		}
		out[id.String()] = true
	}
	return out, true
}

func (s *Server) agentSync(c *gin.Context) {
	agent := c.MustGet("node_agent").(model.NodeAgent)
	var in agentSyncRequest
	if !decode(c, &in) {
		return
	}
	installed, okInstalled := uuidSet(in.Installed)
	removed, okRemoved := uuidSet(in.Removed)
	if in.Epoch <= 0 || in.Epoch > 1<<40 || len(in.AgentVersion) > 64 || len(in.Protocols) > 16 || !okInstalled || !okRemoved {
		fail(c, 400, "invalid_agent_sync", "同步请求无效")
		return
	}
	for _, p := range in.Protocols {
		if _, known := nodes.CredentialFields[p]; !known {
			fail(c, 400, "invalid_agent_sync", "同步请求无效")
			return
		}
	}
	slices.Sort(in.Protocols)
	protocols, _ := json.Marshal(slices.Compact(in.Protocols))
	now := time.Now().UTC()
	result := s.database(c).Model(&model.NodeAgent{}).Where("id = ? AND epoch <= ?", agent.ID, in.Epoch).
		Updates(map[string]any{"epoch": in.Epoch, "agent_version": in.AgentVersion, "protocols": string(protocols), "last_seen_at": now})
	if result.Error != nil {
		fail(c, 503, "agent_service_unavailable", "服务暂时不可用")
		return
	}
	if result.RowsAffected == 0 {
		fail(c, 409, "agent_epoch_stale", "代理进程代次已过期")
		return
	}
	var open []model.ConnectionGrant
	if err := s.database(c).Where("node_id = ? AND state IN ? AND released_at IS NULL", agent.NodeID, []string{grantPending, grantReady, grantRevoking}).
		Order("created_at").Limit(maxAgentList + 1).Find(&open).Error; err != nil || len(open) > maxAgentList {
		fail(c, 503, "agent_service_unavailable", "服务暂时不可用")
		return
	}
	orders := []agentOrder{}
	known := make(map[string]bool, len(open))
	for _, g := range open {
		known[g.ID] = true
		var order *agentOrder
		err := s.database(c).Transaction(func(tx *gorm.DB) (e error) {
			order, e = s.syncGrant(tx, g.ID, agent, in.Epoch, installed[g.ID], removed[g.ID], now)
			return e
		})
		if err != nil {
			fail(c, 503, "agent_service_unavailable", "服务暂时不可用")
			return
		}
		if order != nil {
			orders = append(orders, *order)
		}
	}
	// Anything the agent still holds that the control plane no longer wants open.
	for id := range installed {
		if !known[id] {
			orders = append(orders, agentOrder{GrantID: id, Action: "revoke"})
		}
	}
	c.JSON(200, gin.H{"server_time": now, "node_id": agent.NodeID, "lease_seconds": int(s.cfg.Lease() / time.Second), "grants": orders})
}

// epochBudget converts the grant's cumulative allowance into the cap the agent applies
// to its own per-epoch counters: closed epochs are already charged.
func epochBudget(g model.ConnectionGrant, agentEpoch int64) int64 {
	base := g.BaseUpload + g.BaseDownload
	if g.Epoch < agentEpoch {
		base += g.EpochUpload + g.EpochDownload
	}
	return max(0, g.BudgetBytes-base)
}

// grantVerdict decides whether a grant may keep running and until when. "" = allowed.
func (s *Server) grantVerdict(tx *gorm.DB, g model.ConnectionGrant, user *model.User, period *model.SubscriptionPeriod, sub *model.Subscription, now time.Time) (string, time.Time) {
	if user == nil || user.Status != "active" {
		return "account_disabled", time.Time{}
	}
	var sess model.NativeSession
	if tx.First(&sess, "id = ?", g.SessionID).Error != nil || sess.RevokedAt != nil || !now.Before(sess.ExpiresAt) || !sess.LastSeenAt.After(now.Add(-s.cfg.IdleTTL())) {
		return "native_session_expired", time.Time{}
	}
	if sub == nil || !periodIsCurrent(period, sub) {
		return "subscription_changed", time.Time{}
	}
	if reason := s.nativeEntitlement(sub, now); reason != "" {
		return reason, time.Time{}
	}
	if !s.nodeStillGranted(tx, g.NodeID, sub) {
		return "node_unavailable", time.Time{}
	}
	return "", s.grantDeadline(now, &sess, sub, sess.LastSeenAt)
}

func (s *Server) nodeStillGranted(tx *gorm.DB, nodeID string, sub *model.Subscription) bool {
	var row catalogRow
	err := tx.Table(s.cfg.Schema+".nodes AS n").Select("n.*, c.plan_ids").Joins("JOIN "+s.cfg.Schema+".node_configs AS c ON c.node_id = n.id").
		Where("n.id = ? AND n.enabled = ?", nodeID, true).Take(&row).Error
	if err != nil {
		return false
	}
	var plans []string
	if json.Unmarshal([]byte(row.PlanIDs), &plans) != nil {
		return false
	}
	return !(row.LineType == "dedicated" && !sub.AllowDedicated || len(plans) != 0 && !slices.Contains(plans, sub.PlanID))
}

func lockUserRow(tx *gorm.DB, id string) (*model.User, error) {
	var user model.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

func lockPeriodByID(tx *gorm.DB, id string) (*model.SubscriptionPeriod, error) {
	var p model.SubscriptionPeriod
	return &p, tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", id).Error
}

// finalize closes a grant's life: the node confirmed removal (or the lease safely
// lapsed) so its remaining reservation returns to the period's pool.
func finalize(g *model.ConnectionGrant, period *model.SubscriptionPeriod, now time.Time) {
	if g.ReleasedAt == nil {
		period.ReservedUnits = max(0, period.ReservedUnits-reserved(*g))
		g.ReleasedAt = &now
	}
	if g.State == grantRevoking {
		g.State = grantRevoked
	} else if g.State == grantPending || g.State == grantReady {
		g.State = grantExpired
	}
	g.RevokedAt = &now
}

// closedCleanly: the node declared its last report for the newest epoch that reported
// (or never reported at all), so nothing can be missing.
func closedCleanly(g model.ConnectionGrant) bool {
	return g.Epoch == 0 || g.FinalEpoch >= g.Epoch
}

func (s *Server) syncGrant(tx *gorm.DB, grantID string, agent model.NodeAgent, epoch int64, held, removed bool, now time.Time) (*agentOrder, error) {
	var head model.ConnectionGrant
	if err := tx.First(&head, "id = ?", grantID).Error; err != nil {
		return nil, err
	}
	user, err := lockUserRow(tx, head.UserID)
	if err != nil {
		return nil, err
	}
	var sub *model.Subscription
	if user != nil {
		if sub, err = readNativeSubscription(tx, user.ID); err != nil {
			return nil, err
		}
	}
	period, err := lockPeriodByID(tx, head.PeriodID)
	if err != nil {
		return nil, err
	}
	var g model.ConnectionGrant
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, "id = ?", grantID).Error; err != nil {
		return nil, err
	}
	if !grantOpen(g.State) || g.ReleasedAt != nil {
		return nil, nil
	}
	if sub != nil && periodIsCurrent(period, sub) {
		syncPeriod(period, sub)
	}
	// Removal confirmed by the node, with the final cursor already recorded.
	if removed && !held && closedCleanly(g) {
		finalize(&g, period, now)
		return nil, saveGrant(tx, &g, period)
	}
	reason, deadline := s.grantVerdict(tx, g, user, period, sub, now)
	if reason != "" && (g.State == grantPending || g.State == grantReady) {
		g.State = grantRevoking
	}
	if g.State == grantRevoking {
		if err = saveGrant(tx, &g, period); err != nil {
			return nil, err
		}
		return &agentOrder{GrantID: g.ID, Action: "revoke", EpochBudgetBytes: 0}, nil
	}
	if held && g.State == grantPending {
		g.State, g.InstalledAt = grantReady, &now
	}
	g.ExpiresAt = deadline
	// Top the budget up from the shared pool while the grant is healthy.
	chunk := s.cfg.GrantBudget()
	if remaining := g.BudgetBytes - g.ChargedBytes; remaining < chunk/2 {
		available := billing.AvailableUnits(period.LimitBytes, period.UsedUnits, period.ReservedUnits)
		if add := billing.BudgetBytes(available, g.RatePermille, chunk-max(remaining, 0)); add > 0 {
			before := reserved(g)
			g.BudgetBytes += add
			period.ReservedUnits += reserved(g) - before
		}
	}
	if err = saveGrant(tx, &g, period); err != nil {
		return nil, err
	}
	order := &agentOrder{GrantID: g.ID, Action: "keep", ExpiresAt: &g.ExpiresAt, RatePermille: g.RatePermille, EpochBudgetBytes: epochBudget(g, epoch)}
	if !held {
		credential, e := nodes.OpenCredential(s.cfg.NodeEncryptionKey, g.ID, g.CredentialCipher)
		if e != nil {
			return nil, e
		}
		order.Action, order.Protocol, order.Credential = "install", g.Protocol, credential
	}
	return order, nil
}

func saveGrant(tx *gorm.DB, g *model.ConnectionGrant, period *model.SubscriptionPeriod) error {
	if err := tx.Save(g).Error; err != nil {
		return err
	}
	return tx.Model(period).Updates(map[string]any{"reserved_units": period.ReservedUnits}).Error
}

type agentUsageRequest struct {
	GrantID       string `json:"grant_id"`
	Epoch         int64  `json:"epoch"`
	Sequence      int64  `json:"sequence"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	Final         bool   `json:"final"`
}

func (s *Server) agentUsage(c *gin.Context) {
	agent := c.MustGet("node_agent").(model.NodeAgent)
	var in agentUsageRequest
	if !decode(c, &in) {
		return
	}
	id, err := uuid.Parse(in.GrantID)
	report := billing.UsageReport{Epoch: in.Epoch, Sequence: in.Sequence, Upload: in.UploadBytes, Download: in.DownloadBytes}
	if err != nil || id == uuid.Nil || !report.Valid() {
		fail(c, 400, "invalid_usage", "用量报告无效")
		return
	}
	if in.Epoch > agent.Epoch {
		fail(c, 409, "usage_epoch_unknown", "请先同步当前进程代次")
		return
	}
	var head model.ConnectionGrant
	if err = s.database(c).First(&head, "id = ? AND node_id = ?", id.String(), agent.NodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "grant_not_found", "授权不存在")
			return
		}
		fail(c, 503, "agent_service_unavailable", "服务暂时不可用")
		return
	}
	replayed := false
	var out model.ConnectionGrant
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		user, e := lockUserRow(tx, head.UserID)
		if e != nil {
			return e
		}
		if user == nil {
			return &apiError{404, "grant_not_found", "授权不存在"}
		}
		sub, e := readNativeSubscription(tx, user.ID)
		if e != nil {
			return e
		}
		period, e := lockPeriodByID(tx, head.PeriodID)
		if e != nil {
			return e
		}
		var g model.ConnectionGrant
		if e = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, "id = ?", head.ID).Error; e != nil {
			return e
		}
		current := sub != nil && periodIsCurrent(period, sub)
		if current {
			syncPeriod(period, sub)
		}
		var stored model.NodeUsageReport
		switch e = tx.Where("grant_id = ? AND epoch = ? AND sequence = ?", g.ID, in.Epoch, in.Sequence).First(&stored).Error; {
		case e == nil:
			if stored.UploadBytes != in.UploadBytes || stored.DownloadBytes != in.DownloadBytes || stored.Final != in.Final {
				return &apiError{409, "usage_conflict", "重复序号的用量内容不一致"}
			}
			replayed, out = true, g
			return nil
		case !errors.Is(e, gorm.ErrRecordNotFound):
			return e
		}
		kind, up, down, e := billing.ApplyUsage(billing.UsageCursor{Epoch: g.Epoch, Sequence: g.LastSequence, Upload: g.EpochUpload, Download: g.EpochDownload}, report)
		switch {
		case errors.Is(e, billing.ErrUsageStale):
			return &apiError{409, "agent_epoch_stale", "用量代次已过期"}
		case errors.Is(e, billing.ErrUsageSequence):
			return &apiError{409, "usage_sequence", "用量序号不连续"}
		case errors.Is(e, billing.ErrUsageRegress):
			return &apiError{409, "usage_regression", "累计用量不得回退"}
		case e != nil:
			return &apiError{400, "invalid_usage", "用量报告无效"}
		case kind == billing.UsageReplay:
			return &apiError{409, "usage_conflict", "重复序号缺少原始记录"}
		}
		if g.FinalEpoch == in.Epoch && g.Epoch == in.Epoch {
			return &apiError{409, "usage_closed", "该代次已提交最终用量"}
		}
		bytes, units, e := billing.Charge(g.ChargedBytes, g.ChargedUnits, up, down, g.RatePermille)
		if e != nil || period.UsedUnits > math.MaxInt64-units || period.UploadBytes > math.MaxInt64-up || period.DownloadBytes > math.MaxInt64-down {
			return &apiError{409, "usage_overflow", "用量超出可记账范围"}
		}
		before := reserved(g)
		if kind == billing.UsageNewEpoch {
			if g.Epoch > 0 {
				g.BaseUpload += g.EpochUpload
				g.BaseDownload += g.EpochDownload
				if g.FinalEpoch < g.Epoch {
					g.NeedsReconcile = true
				}
			}
			g.Epoch = in.Epoch
		}
		g.LastSequence, g.EpochUpload, g.EpochDownload = in.Sequence, in.UploadBytes, in.DownloadBytes
		g.ChargedBytes, g.ChargedUnits = bytes, g.ChargedUnits+units
		if in.Final {
			g.FinalEpoch = in.Epoch
		}
		period.ReservedUnits = max(0, period.ReservedUnits+reserved(g)-before)
		period.UsedUnits += units
		period.UploadBytes += up
		period.DownloadBytes += down
		if current {
			// The live subscription is the current period's balance.
			sub.UsedUnits += units
			sub.UploadBytes += up
			sub.DownloadBytes += down
			if e = tx.Model(sub).Updates(map[string]any{"used_units": sub.UsedUnits, "upload_bytes": sub.UploadBytes, "download_bytes": sub.DownloadBytes}).Error; e != nil {
				return e
			}
			syncPeriod(period, sub)
		}
		record := model.NodeUsageReport{ID: uuid.NewString(), GrantID: g.ID, Epoch: in.Epoch, Sequence: in.Sequence, UploadBytes: in.UploadBytes, DownloadBytes: in.DownloadBytes,
			UploadDelta: up, DownloadDelta: down, ChargedUnits: units, RatePermille: g.RatePermille, Final: in.Final}
		if e = tx.Omit("Grant").Create(&record).Error; e != nil {
			return e
		}
		if e = tx.Save(period).Error; e != nil {
			return e
		}
		out = g
		return tx.Save(&g).Error
	})
	if err != nil {
		var business *apiError
		if errors.As(err, &business) {
			fail(c, business.Status, business.Code, business.Message)
		} else {
			fail(c, 503, "agent_service_unavailable", "服务暂时不可用")
		}
		return
	}
	c.JSON(200, gin.H{"replayed": replayed, "state": out.State, "epoch": in.Epoch, "sequence": in.Sequence, "epoch_budget_bytes": epochBudget(out, agent.Epoch)})
}

// ReconcileGrants releases the budget of grants whose node never confirmed removal,
// but only after the absolute deadline plus a settle grace has passed: by then the
// node has stopped forwarding on its own. A grant that was installed and never closed
// cleanly is flagged so missing usage is visible instead of silently zero.
func ReconcileGrants(ctx context.Context, db *gorm.DB, cfg config.Config) (int, error) {
	now := time.Now().UTC()
	var stale []model.ConnectionGrant
	if err := db.WithContext(ctx).Select("id").Where("state IN ? AND released_at IS NULL AND expires_at < ?", []string{grantPending, grantReady, grantRevoking}, now.Add(-cfg.SettleGrace())).
		Limit(500).Find(&stale).Error; err != nil {
		return 0, err
	}
	released := 0
	for _, head := range stale {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var full model.ConnectionGrant
			if e := tx.First(&full, "id = ?", head.ID).Error; e != nil {
				return e
			}
			if _, e := lockUserRow(tx, full.UserID); e != nil {
				return e
			}
			period, e := lockPeriodByID(tx, full.PeriodID)
			if e != nil {
				return e
			}
			var g model.ConnectionGrant
			if e = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, "id = ?", head.ID).Error; e != nil {
				return e
			}
			if !grantOpen(g.State) || g.ReleasedAt != nil || !g.ExpiresAt.Before(now.Add(-cfg.SettleGrace())) {
				return nil
			}
			if g.InstalledAt != nil && !closedCleanly(g) {
				g.NeedsReconcile = true
			}
			finalize(&g, period, now)
			released++
			return saveGrant(tx, &g, period)
		})
		if err != nil {
			return released, err
		}
	}
	return released, nil
}
