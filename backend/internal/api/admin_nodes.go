package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
	"vpn/nodepolicy"
)

// nodeInput is the body of POST /admin/nodes[/:id] in two shapes (plan 8.3):
//   - grouped: yaml, group_id, max_connections, label, enabled, version. Public
//     metadata belongs to the group; every proxy of an import joins that group.
//   - legacy (no group_id): yaml, region, line_type, rate_permille, enabled,
//     plan_ids, version. Each proxy is a server with a one-server group of its own id
//     (D3), kept in step by mirrorLegacyGroup. It stays until the round-4 admin page.
type nodeInput struct {
	YAML           string   `json:"yaml"`
	Region         string   `json:"region"`
	LineType       string   `json:"line_type"`
	RatePermille   int64    `json:"rate_permille"`
	Enabled        bool     `json:"enabled"`
	PlanIDs        []string `json:"plan_ids"`
	Version        int64    `json:"version"`
	GroupID        string   `json:"group_id,omitempty"`
	MaxConnections int      `json:"max_connections,omitempty"`
	Label          string   `json:"label,omitempty"`
}

type adminNode struct {
	model.Node
	Enabled bool     `json:"enabled"`
	PlanIDs []string `json:"plan_ids"`
	YAML    string   `json:"yaml,omitempty"`
}

// adminNodeDetail adds the server-only fields to the detail view.
type adminNodeDetailView struct {
	adminNode
	GroupID        string `json:"group_id"`
	MaxConnections int    `json:"max_connections"`
	Label          string `json:"label"`
}

// adminServerSaved is a server as the grouped save returns it.
type adminServerSaved struct {
	ID             string `json:"id"`
	GroupID        string `json:"group_id"`
	Name           string `json:"name"`
	Label          string `json:"label"`
	Enabled        bool   `json:"enabled"`
	MaxConnections int    `json:"max_connections"`
	Version        int64  `json:"version"`
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

// adminTransaction runs an admin catalog write: the actor must still be an active
// administrator, and advisory lock 708614922 serialises every catalog writer. Locks
// below it follow plan 4.3: node group rows, then server rows, then leases. Admin
// writes never lock users, so they cannot form a cycle with client admissions.
func (s *Server) adminTransaction(c *gin.Context, action func(tx *gorm.DB, actor model.User, now time.Time) error) error {
	return s.database(c).Transaction(func(tx *gorm.DB) error {
		var actor model.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&actor, "id = ? AND role = ? AND status = ?", currentUser(c).ID, "admin", "active").Error; e != nil {
			return &apiError{403, "admin_required", "管理员权限已变更"}
		}
		if e := tx.Exec("SELECT pg_advisory_xact_lock(708614922)").Error; e != nil {
			return e
		}
		return action(tx, actor, s.now())
	})
}

func audit(tx *gorm.DB, actor model.User, action, target string, version int64) error {
	return tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: actor.ID, Action: action, TargetID: target, Version: version}).Error
}

// lockGroupRow takes a group row lock for an admin write (404 when it is missing).
func lockGroupRow(tx *gorm.DB, id string) (*model.NodeGroup, error) {
	var group model.NodeGroup
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &apiError{404, "node_group_not_found", "节点组不存在"}
	}
	return &group, err
}

// withdrawServer ends every active lease on a server that stops carrying clients
// (disabled, deleted or moved): the devices are told connection_reassigned
// (profile_changed for an older Core) and reconnect to the group (plan 6.4).
func (s *Server) withdrawServer(tx *gorm.DB, nodeID string, now time.Time) error {
	return s.endLeases(tx, now, model.LeaseNodeUnavailable, "node_id = ?", nodeID)
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
	c.JSON(200, gin.H{"nodes": result, "supported_protocols": nodepolicy.Supported(), "core_revision": nodepolicy.CoreRevision, "metering_source": sessionSourceFor(s.cfg), "production_ready": false})
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
	group := ""
	if node.GroupID != nil {
		group = *node.GroupID
	}
	c.JSON(200, adminNodeDetailView{adminNode: adminNode{Node: node, Enabled: node.Enabled, PlanIDs: plans, YAML: string(data)}, GroupID: group, MaxConnections: node.MaxConnections, Label: node.Label})
}

// validText is the shared rule for short admin-visible strings.
func validText(text string, max int) bool {
	return strings.TrimSpace(text) == text && utf8.RuneCountInString(text) <= max && utf8.ValidString(text) && !strings.ContainsFunc(text, unicode.IsControl)
}

func validateNodeInput(in nodeInput) bool {
	if in.RatePermille < 1 || in.RatePermille > 10000 || in.LineType != "direct" && in.LineType != "dedicated" || in.Region == "" || !validText(in.Region, 16) || len(in.PlanIDs) > 32 {
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

// validGroupedInput checks the grouped shape: no group metadata, a capacity and a label.
func validGroupedInput(in nodeInput) bool {
	return in.Region == "" && in.LineType == "" && in.RatePermille == 0 && in.PlanIDs == nil &&
		in.MaxConnections >= 1 && in.MaxConnections <= 10000 && validText(in.Label, 40)
}

func (s *Server) adminNodeSave(c *gin.Context) {
	var in nodeInput
	if !decode(c, &in) {
		return
	}
	proxies, err := nodes.Parse(in.YAML)
	if err != nil {
		fail(c, 400, "invalid_node_config", fmt.Sprintf("节点 YAML 无效（%s）。请检查协议必填项与嵌套参数；禁止外部配置、文件路径及不受支持的选项。", err))
		return
	}
	grouped := in.GroupID != ""
	if grouped {
		group, parseErr := uuid.Parse(in.GroupID)
		if parseErr != nil || group == uuid.Nil || !validGroupedInput(in) {
			fail(c, 400, "invalid_node_config", "服务器属性无效：请检查节点组、最大在线数（1–10000）与备注（最多 40 字）")
			return
		}
		in.GroupID = group.String()
	} else if !validateNodeInput(in) || in.MaxConnections != 0 || in.Label != "" {
		fail(c, 400, "invalid_node_config", "节点属性无效：请检查地区、线路和套餐，倍率为 1–10000‰")
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
	var response any
	err = s.adminTransaction(c, func(tx *gorm.DB, actor model.User, now time.Time) error {
		var total int64
		if e := tx.Model(&model.Node{}).Count(&total).Error; e != nil {
			return e
		}
		if id == "" && total+int64(len(proxies)) > nodes.MaxNodes {
			return &apiError{409, "node_limit", "节点目录最多支持 128 台服务器"}
		}
		if grouped {
			saved, e := s.saveGroupedServers(tx, actor, id, in, proxies, now)
			response = gin.H{"nodes": saved}
			return e
		}
		saved, e := s.saveLegacyServers(tx, actor, id, in, proxies, now)
		response = gin.H{"nodes": saved}
		return e
	})
	if s.adminFailure(c, err) {
		return
	}
	s.catalogUpdates.notify()
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	c.JSON(status, response)
}

// sealServer encrypts one proxy for a server and stores it with the group's plans.
func (s *Server) sealServer(tx *gorm.DB, node *model.Node, proxy map[string]any, planJSON string) ([]byte, error) {
	data, err := nodes.Marshal([]map[string]any{proxy})
	if err != nil {
		return nil, err
	}
	ciphertext, err := nodes.Encrypt(s.cfg.NodeEncryptionKey, node.ID, data)
	if err != nil {
		return nil, err
	}
	return data, tx.Omit("Node").Save(&model.NodeConfig{NodeID: node.ID, Ciphertext: ciphertext, PlanIDs: planJSON}).Error
}

// storedYAML is a server's current configuration in the canonical form nodes.Marshal
// writes, so formatting alone never counts as a change (nil when unreadable).
func (s *Server) storedYAML(tx *gorm.DB, nodeID string) []byte {
	var secret model.NodeConfig
	if tx.First(&secret, "node_id = ?", nodeID).Error != nil {
		return nil
	}
	data, err := nodes.Decrypt(s.cfg.NodeEncryptionKey, nodeID, secret.Ciphertext)
	if err != nil {
		return nil
	}
	proxies, err := nodes.Parse(string(data))
	if err != nil {
		return nil
	}
	canonical, err := nodes.Marshal(proxies)
	if err != nil {
		return nil
	}
	return canonical
}

// saveGroupedServers imports servers into a group, or edits one. The server version
// moves only with its YAML, its enabled state or a move to another group; capacity
// and label edits disturb no session (plan 4.1, 6.5).
func (s *Server) saveGroupedServers(tx *gorm.DB, actor model.User, id string, in nodeInput, proxies []map[string]any, now time.Time) ([]adminServerSaved, error) {
	saved := []adminServerSaved{}
	var current model.Node
	if id != "" {
		if e := tx.First(&current, "id = ?", id).Error; e != nil {
			return nil, e
		}
		if current.GroupID != nil && *current.GroupID != in.GroupID {
			if _, e := lockGroupRow(tx, *current.GroupID); e != nil {
				return nil, e
			}
		}
	}
	group, err := lockGroupRow(tx, in.GroupID)
	if err != nil {
		return nil, err
	}
	for _, proxy := range proxies {
		node := model.Node{}
		var before []byte
		if id != "" {
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", id).Error; e != nil {
				return nil, e
			}
			if node.Version != in.Version {
				return nil, &apiError{409, "node_version_conflict", "节点已被其他管理员修改，请重新读取后编辑"}
			}
			before = s.storedYAML(tx, node.ID)
		} else {
			node.ID, node.Version = uuid.NewString(), 1
		}
		moved := node.GroupID == nil || *node.GroupID != group.ID
		wasEnabled := node.Enabled
		data, e := nodes.Marshal([]map[string]any{proxy})
		if e != nil {
			return nil, e
		}
		if id != "" && (moved || node.Enabled != in.Enabled || !bytes.Equal(before, data)) {
			node.Version++
		}
		if id != "" && wasEnabled && (moved || !in.Enabled) {
			if e = s.withdrawServer(tx, node.ID, now); e != nil {
				return nil, e
			}
		}
		if id != "" && moved {
			// The preference belonged to the old group.
			if e = tx.Where("node_id = ? AND group_id <> ?", node.ID, group.ID).Delete(&model.NodeAssignment{}).Error; e != nil {
				return nil, e
			}
		}
		node.Name, node.Enabled, node.GroupID, node.MaxConnections, node.Label = proxy["name"].(string), in.Enabled, &group.ID, in.MaxConnections, in.Label
		// Legacy copies of the group's public metadata (dual write, plan 4.1).
		node.Region, node.LineType, node.RatePermille = group.Region, group.LineType, group.RatePermille
		if e = tx.Omit("Group").Save(&node).Error; e != nil {
			return nil, e
		}
		if _, e = s.sealServer(tx, &node, proxy, group.PlanIDs); e != nil {
			return nil, e
		}
		if e = audit(tx, actor, "node.saved", node.ID, node.Version); e != nil {
			return nil, e
		}
		saved = append(saved, adminServerSaved{ID: node.ID, GroupID: group.ID, Name: node.Name, Label: node.Label, Enabled: node.Enabled, MaxConnections: node.MaxConnections, Version: node.Version})
	}
	return saved, nil
}

// saveLegacyServers is the per-server shape: each server owns a one-server group of
// its id. Servers no longer need unique names; group names must be unique within an
// overlapping plan scope, reported with the code the current admin page knows.
func (s *Server) saveLegacyServers(tx *gorm.DB, actor model.User, id string, in nodeInput, proxies []map[string]any, now time.Time) ([]adminNode, error) {
	slices.Sort(in.PlanIDs)
	if in.PlanIDs == nil {
		in.PlanIDs = []string{}
	}
	planJSON, _ := json.Marshal(in.PlanIDs)
	if err := requireActivePlans(tx, in.PlanIDs); err != nil {
		return nil, err
	}
	result := make([]adminNode, 0, len(proxies))
	for _, proxy := range proxies {
		node := model.Node{}
		if id != "" {
			if e := tx.First(&node, "id = ?", id).Error; e != nil {
				return nil, e
			}
			if node.GroupID != nil {
				if _, e := lockGroupRow(tx, *node.GroupID); e != nil {
					return nil, e
				}
				var members int64
				if e := tx.Model(&model.Node{}).Where("group_id = ?", *node.GroupID).Count(&members).Error; e != nil {
					return nil, e
				}
				if *node.GroupID != node.ID || members != 1 {
					return nil, &apiError{409, "node_group_managed", "该服务器属于节点组，请在节点组中编辑"}
				}
			}
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", id).Error; e != nil {
				return nil, e
			}
			if node.Version != in.Version {
				return nil, &apiError{409, "node_version_conflict", "节点已被其他管理员修改，请重新读取后编辑"}
			}
			node.Version++
		} else {
			node.ID, node.Version = uuid.NewString(), 1
		}
		name := proxy["name"].(string)
		taken, e := groupNameTaken(tx, name, in.PlanIDs, node.ID)
		if e != nil {
			return nil, e
		}
		if taken {
			return nil, &apiError{409, "node_name_exists", "同一套餐范围内已存在同名线路，请使用其他名称"}
		}
		if id != "" && node.Enabled && !in.Enabled {
			// A one-server group: disabling the server takes the line away.
			if e = s.endLeases(tx, now, model.LeaseGroupUnavailable, "node_id = ?", node.ID); e != nil {
				return nil, e
			}
		}
		node.Name, node.Region, node.LineType, node.RatePermille, node.Enabled = name, in.Region, in.LineType, in.RatePermille, in.Enabled
		if e = mirrorLegacyGroup(tx, &node, string(planJSON)); e != nil {
			return nil, e
		}
		if e = tx.Omit("Group").Save(&node).Error; e != nil {
			return nil, e
		}
		if _, e = s.sealServer(tx, &node, proxy, string(planJSON)); e != nil {
			return nil, e
		}
		if e = audit(tx, actor, "node.saved", node.ID, node.Version); e != nil {
			return nil, e
		}
		result = append(result, adminNode{Node: node, Enabled: node.Enabled, PlanIDs: in.PlanIDs})
	}
	return result, nil
}

func requireActivePlans(tx *gorm.DB, plans []string) error {
	if len(plans) == 0 {
		return nil
	}
	var count int64
	if e := tx.Model(&model.Plan{}).Where("id IN ? AND active = ?", plans, true).Count(&count).Error; e != nil {
		return e
	}
	if count != int64(len(plans)) {
		return &apiError{400, "invalid_plan", "所选套餐不存在或未上架"}
	}
	return nil
}

// groupNameTaken: another group of this name shares part of the plan scope (an empty
// list means every plan), so one user could see two lines with the same name.
func groupNameTaken(tx *gorm.DB, name string, plans []string, except string) (bool, error) {
	var others []model.NodeGroup
	if err := tx.Select("id", "plan_ids").Where("name = ? AND id <> ?", name, except).Find(&others).Error; err != nil {
		return false, err
	}
	for _, other := range others {
		var scope []string
		if json.Unmarshal([]byte(other.PlanIDs), &scope) != nil {
			return false, nodes.ErrInvalid
		}
		if len(scope) == 0 || len(plans) == 0 || slices.ContainsFunc(scope, func(p string) bool { return slices.Contains(plans, p) }) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) adminNodeDelete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		fail(c, 400, "invalid_node", "节点标识无效")
		return
	}
	var in struct {
		Version int64 `json:"version"`
	}
	if !decode(c, &in) {
		return
	}
	if in.Version < 1 {
		fail(c, 400, "invalid_node", "删除时需要当前节点版本号")
		return
	}
	err = s.adminTransaction(c, func(tx *gorm.DB, actor model.User, now time.Time) error {
		var node model.Node
		if e := tx.First(&node, "id = ?", id.String()).Error; e != nil {
			return e
		}
		if node.GroupID != nil {
			if _, e := lockGroupRow(tx, *node.GroupID); e != nil {
				return e
			}
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", id.String()).Error; e != nil {
			return e
		}
		if node.Version != in.Version {
			return &apiError{409, "node_version_conflict", "节点已被其他管理员修改，请刷新目录后重新确认删除"}
		}
		// Its devices move to the group's other servers; its sticky assignments and
		// health events go with it (foreign keys).
		if e := s.withdrawServer(tx, node.ID, now); e != nil {
			return e
		}
		if e := tx.Delete(&model.NodeConfig{}, "node_id = ?", node.ID).Error; e != nil {
			return e
		}
		if e := tx.Delete(&node).Error; e != nil {
			return e
		}
		// A one-server group of the server's own id (D3, legacy saves) goes with it;
		// a managed group stays until it is deleted on its own.
		if node.GroupID != nil && *node.GroupID == node.ID {
			if e := tx.Where("id = ? AND NOT EXISTS (SELECT 1 FROM "+s.cfg.Schema+".nodes WHERE group_id = ?)", node.ID, node.ID).Delete(&model.NodeGroup{}).Error; e != nil {
				return e
			}
		}
		return audit(tx, actor, "node.deleted", node.ID, node.Version)
	})
	if s.adminFailure(c, err) {
		return
	}
	s.catalogUpdates.notify()
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id.String(), "version": in.Version})
}

// adminNodeHealth clears a server's soft cooldown and the failure reports behind it.
func (s *Server) adminNodeHealth(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	var in struct{}
	if !decode(c, &in) {
		return
	}
	if err != nil || id == uuid.Nil {
		fail(c, 400, "invalid_node", "节点标识无效")
		return
	}
	var node model.Node
	err = s.adminTransaction(c, func(tx *gorm.DB, actor model.User, now time.Time) error {
		if e := tx.First(&node, "id = ?", id.String()).Error; e != nil {
			return e
		}
		if node.GroupID != nil {
			if _, e := lockGroupRow(tx, *node.GroupID); e != nil {
				return e
			}
		}
		if e := tx.Model(&model.Node{}).Where("id = ?", node.ID).UpdateColumn("unhealthy_until", nil).Error; e != nil {
			return e
		}
		if e := tx.Where("node_id = ?", node.ID).Delete(&model.NodeHealthEvent{}).Error; e != nil {
			return e
		}
		return audit(tx, actor, "node.health_cleared", node.ID, node.Version)
	})
	if s.adminFailure(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": node.ID, "health": "ok", "unhealthy_until": nil})
}

// mirrorLegacyGroup keeps the group of a server saved through the legacy per-server
// shape in step with it: the group carries the same public metadata and plan list,
// and its version moves only when that metadata changes. A new server gets a group
// with its own id (D3). The caller holds advisory lock 708614922.
func mirrorLegacyGroup(tx *gorm.DB, node *model.Node, planJSON string) error {
	groupID := node.ID
	if node.GroupID != nil {
		groupID = *node.GroupID
	}
	var group model.NodeGroup
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, "id = ?", groupID).Error
	created := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !created {
		return err
	}
	before := group
	group.ID, group.Name, group.Region, group.LineType, group.RatePermille, group.PlanIDs, group.Enabled = groupID, node.Name, node.Region, node.LineType, node.RatePermille, planJSON, node.Enabled
	node.GroupID = &group.ID
	switch {
	case created:
		group.Version = 1
		return tx.Create(&group).Error
	case group == before:
		return nil
	default:
		group.Version++
		return tx.Save(&group).Error
	}
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
