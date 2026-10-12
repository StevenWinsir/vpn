package api

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type agentInput struct {
	NodeID          string `json:"node_id"`
	Name            string `json:"name"`
	CertFingerprint string `json:"cert_fingerprint"`
	Enabled         bool   `json:"enabled"`
}

type adminAgent struct {
	model.NodeAgent
	Protocols      []string `json:"protocols"`
	Online         bool     `json:"online"`
	OpenGrants     int64    `json:"open_grants"`
	NeedsReconcile int64    `json:"needs_reconcile"`
}

func normalizeFingerprint(raw string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), ":", ""))
}

func (s *Server) adminAgentList(c *gin.Context) {
	var agents []model.NodeAgent
	if err := s.database(c).Order("name, id").Limit(500).Find(&agents).Error; s.adminFailure(c, err) {
		return
	}
	now := time.Now().UTC()
	out := make([]adminAgent, 0, len(agents))
	for _, a := range agents {
		view := adminAgent{NodeAgent: a, Protocols: protocolList(a.Protocols)}
		if view.Protocols == nil {
			view.Protocols = []string{}
		}
		view.Online = a.Enabled && a.LastSeenAt != nil && now.Sub(*a.LastSeenAt) <= s.cfg.AgentStale()
		db := s.database(c).Model(&model.ConnectionGrant{})
		if err := db.Where("node_id = ? AND state IN ? AND released_at IS NULL", a.NodeID, []string{grantPending, grantReady, grantRevoking}).Count(&view.OpenGrants).Error; s.adminFailure(c, err) {
			return
		}
		if err := s.database(c).Model(&model.ConnectionGrant{}).Where("node_id = ? AND needs_reconcile = ?", a.NodeID, true).Count(&view.NeedsReconcile).Error; s.adminFailure(c, err) {
			return
		}
		out = append(out, view)
	}
	c.JSON(200, gin.H{"agents": out, "stale_seconds": int(s.cfg.AgentStale() / time.Second), "agent_listener_enabled": s.cfg.AgentEnabled()})
}

func (s *Server) adminAgentSave(c *gin.Context) {
	var in agentInput
	if !decode(c, &in) {
		return
	}
	in.CertFingerprint = normalizeFingerprint(in.CertFingerprint)
	in.Name = strings.TrimSpace(in.Name)
	nodeID, nodeErr := uuid.Parse(in.NodeID)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 || !fingerprintPattern.MatchString(in.CertFingerprint) {
		fail(c, 400, "invalid_agent", "代理名称或证书指纹无效")
		return
	}
	agentID := c.Param("id")
	var saved model.NodeAgent
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if agentID == "" {
			if nodeErr != nil || nodeID == uuid.Nil {
				return &apiError{400, "invalid_agent", "节点标识无效"}
			}
			if e := tx.First(&model.Node{}, "id = ?", nodeID.String()).Error; e != nil {
				if errors.Is(e, gorm.ErrRecordNotFound) {
					return &apiError{404, "node_not_found", "节点不存在"}
				}
				return e
			}
			saved = model.NodeAgent{ID: uuid.NewString(), NodeID: nodeID.String(), Name: in.Name, CertFingerprint: in.CertFingerprint, Enabled: in.Enabled, Protocols: "[]"}
			return tx.Omit("Node").Create(&saved).Error
		}
		id, e := uuid.Parse(agentID)
		if e != nil {
			return &apiError{400, "invalid_agent", "代理标识无效"}
		}
		if e = tx.First(&saved, "id = ?", id.String()).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return &apiError{404, "agent_not_found", "代理不存在"}
			}
			return e
		}
		saved.Name, saved.CertFingerprint, saved.Enabled = in.Name, in.CertFingerprint, in.Enabled
		return tx.Omit("Node").Save(&saved).Error
	})
	if err != nil {
		var business *apiError
		switch {
		case errors.As(err, &business):
			fail(c, business.Status, business.Code, business.Message)
		case errors.Is(err, gorm.ErrDuplicatedKey):
			fail(c, 409, "agent_conflict", "该节点或证书指纹已登记代理")
		default:
			s.adminFailure(c, err)
		}
		return
	}
	s.catalogUpdates.notify()
	status := 200
	if agentID == "" {
		status = 201
	}
	c.JSON(status, gin.H{"agent": saved})
}

func (s *Server) adminAgentDelete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, 400, "invalid_agent", "代理标识无效")
		return
	}
	result := s.database(c).Delete(&model.NodeAgent{}, "id = ?", id.String())
	if s.adminFailure(c, result.Error) {
		return
	}
	if result.RowsAffected == 0 {
		fail(c, 404, "agent_not_found", "代理不存在")
		return
	}
	s.catalogUpdates.notify()
	c.JSON(200, gin.H{"deleted": true})
}

// adminGrantReconcile lists grants whose usage may be incomplete, for manual review.
func (s *Server) adminGrantReconcile(c *gin.Context) {
	var grants []model.ConnectionGrant
	if err := s.database(c).Select("id", "node_id", "user_id", "state", "charged_bytes", "charged_units", "rate_permille", "epoch", "final_epoch", "updated_at").
		Where("needs_reconcile = ?", true).Order("updated_at desc").Limit(200).Find(&grants).Error; s.adminFailure(c, err) {
		return
	}
	data, _ := json.Marshal(grants)
	c.Data(200, "application/json", data)
}
