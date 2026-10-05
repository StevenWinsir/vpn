package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

type nodeInput struct {
	YAML         string   `json:"yaml"`
	Region       string   `json:"region"`
	LineType     string   `json:"line_type"`
	RatePermille int64    `json:"rate_permille"`
	Enabled      bool     `json:"enabled"`
	PlanIDs      []string `json:"plan_ids"`
	Version      int64    `json:"version"`
}

type adminNode struct {
	model.Node
	Enabled bool     `json:"enabled"`
	PlanIDs []string `json:"plan_ids"`
	YAML    string   `json:"yaml,omitempty"`
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if currentUser(c).Role != "admin" {
			fail(c, 403, "admin_required", "需要管理员权限")
			return
		}
		if !s.cfg.ClientNodeCatalog {
			fail(c, 503, "node_catalog_disabled", "请在后端启用数据库节点目录并配置加密密钥")
			return
		}
		c.Next()
	}
}

func (s *Server) adminNodeList(c *gin.Context) {
	rows, err := s.catalogRows(s.database(c), false)
	if s.adminFailure(c, err) {
		return
	}
	result := make([]adminNode, 0, len(rows))
	for _, row := range rows {
		plans := []string{}
		if json.Unmarshal([]byte(row.PlanIDs), &plans) != nil {
			fail(c, 503, "node_catalog_unavailable", "节点目录不可用")
			return
		}
		result = append(result, adminNode{Node: row.Node, Enabled: row.Enabled, PlanIDs: plans})
	}
	c.JSON(200, gin.H{"nodes": result, "metering_source": "client_reported", "production_ready": false})
}

func (s *Server) adminNodeDetail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		fail(c, 400, "invalid_node", "节点标识无效")
		return
	}
	var node model.Node
	var secret model.NodeConfig
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := tx.First(&node, "id = ?", id.String()).Error; e != nil {
			return e
		}
		return tx.First(&secret, "node_id = ?", node.ID).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if s.adminFailure(c, err) {
		return
	}
	data, err := nodes.Decrypt(s.cfg.NodeEncryptionKey, node.ID, secret.Ciphertext)
	plans := []string{}
	if err != nil || json.Unmarshal([]byte(secret.PlanIDs), &plans) != nil {
		fail(c, 503, "node_config_unavailable", "节点配置不可读取，请检查加密密钥")
		return
	}
	c.JSON(200, adminNode{Node: node, Enabled: node.Enabled, PlanIDs: plans, YAML: string(data)})
}

func validateNodeInput(in nodeInput) bool {
	if in.RatePermille < 1 || in.RatePermille > 10000 || in.LineType != "direct" && in.LineType != "dedicated" || strings.TrimSpace(in.Region) != in.Region || in.Region == "" || utf8.RuneCountInString(in.Region) > 16 || strings.ContainsFunc(in.Region, unicode.IsControl) || len(in.PlanIDs) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range in.PlanIDs {
		if !nativePlanID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (s *Server) adminNodeSave(c *gin.Context) {
	var in nodeInput
	if !decode(c, &in) {
		return
	}
	proxies, err := nodes.Parse(in.YAML)
	if err != nil || !validateNodeInput(in) {
		fail(c, 400, "invalid_node_config", "节点参数无效：仅接受 proxies 列表、SS AEAD / HTTP / SOCKS5 内联节点，倍率为 1–10000‰")
		return
	}
	id := c.Param("id")
	if id != "" {
		parsed, parseErr := uuid.Parse(id)
		if parseErr != nil || parsed == uuid.Nil || len(proxies) != 1 || in.Version < 1 {
			fail(c, 400, "invalid_node", "编辑时需要一个节点及当前版本号")
			return
		}
		id = parsed.String()
	} else if in.Version != 0 {
		fail(c, 400, "invalid_node", "新节点版本必须为零")
		return
	}
	slices.Sort(in.PlanIDs)
	if in.PlanIDs == nil {
		in.PlanIDs = []string{}
	}
	planJSON, _ := json.Marshal(in.PlanIDs)
	result := make([]adminNode, 0, len(proxies))
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		var actor model.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&actor, "id = ? AND role = ? AND status = ?", currentUser(c).ID, "admin", "active").Error; e != nil {
			return &apiError{403, "admin_required", "管理员权限已变更"}
		}
		if e := tx.Exec("SELECT pg_advisory_xact_lock(708614922)").Error; e != nil {
			return e
		}
		if len(in.PlanIDs) > 0 {
			var count int64
			if e := tx.Model(&model.Plan{}).Where("id IN ? AND active = ?", in.PlanIDs, true).Count(&count).Error; e != nil {
				return e
			}
			if count != int64(len(in.PlanIDs)) {
				return &apiError{400, "invalid_plan", "所选套餐不存在或未上架"}
			}
		}
		var total int64
		if e := tx.Model(&model.Node{}).Count(&total).Error; e != nil {
			return e
		}
		if id == "" && total+int64(len(proxies)) > nodes.MaxNodes {
			return &apiError{409, "node_limit", "节点目录最多支持 128 个节点"}
		}
		for _, proxy := range proxies {
			node := model.Node{}
			if id != "" {
				if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", id).Error; e != nil {
					return e
				}
				if node.Version != in.Version {
					return &apiError{409, "node_version_conflict", "节点已被其他管理员修改，请重新读取后编辑"}
				}
				node.Version++
			} else {
				node.ID, node.Version = uuid.NewString(), 1
			}
			name := proxy["name"].(string)
			var duplicates int64
			if e := tx.Model(&model.Node{}).Where("name = ? AND id <> ?", name, node.ID).Count(&duplicates).Error; e != nil {
				return e
			}
			if duplicates != 0 {
				return &apiError{409, "node_name_exists", "节点名称已存在，请使用唯一名称"}
			}
			node.Name, node.Region, node.LineType, node.RatePermille, node.Enabled = name, in.Region, in.LineType, in.RatePermille, in.Enabled
			data, e := nodes.Marshal([]map[string]any{proxy})
			if e != nil {
				return e
			}
			ciphertext, e := nodes.Encrypt(s.cfg.NodeEncryptionKey, node.ID, data)
			if e != nil {
				return e
			}
			if e = tx.Save(&node).Error; e != nil {
				return e
			}
			secret := model.NodeConfig{NodeID: node.ID, Ciphertext: ciphertext, PlanIDs: string(planJSON)}
			if e = tx.Omit("Node").Save(&secret).Error; e != nil {
				return e
			}
			audit := model.AdminAudit{ID: uuid.NewString(), ActorID: actor.ID, Action: "node.saved", TargetID: node.ID, Version: node.Version}
			if e = tx.Create(&audit).Error; e != nil {
				return e
			}
			result = append(result, adminNode{Node: node, Enabled: node.Enabled, PlanIDs: in.PlanIDs})
		}
		return nil
	})
	if s.adminFailure(c, err) {
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"nodes": result})
}

func (s *Server) adminFailure(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var business *apiError
	if errors.As(err, &business) {
		fail(c, business.Status, business.Code, business.Message)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "node_not_found", "节点不存在")
	} else {
		fail(c, 503, "node_catalog_unavailable", "节点目录暂时不可用")
	}
	return true
}
