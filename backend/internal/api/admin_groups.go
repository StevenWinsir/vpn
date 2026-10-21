package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// Node group administration (plan 8.3). A group is the line users see; its servers
// carry the encrypted YAML and the capacity.

const maxNodeGroups = nodes.MaxNodes

type groupInput struct {
	Name         string   `json:"name"`
	Region       string   `json:"region"`
	LineType     string   `json:"line_type"`
	RatePermille int64    `json:"rate_permille"`
	PlanIDs      []string `json:"plan_ids"`
	Enabled      bool     `json:"enabled"`
	Sort         int      `json:"sort"`
	Version      int64    `json:"version"`
}

type adminServerView struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Enabled  bool   `json:"enabled"`
	// Health is ok, cooldown, agent_offline or config_error.
	Health         string     `json:"health"`
	UnhealthyUntil *time.Time `json:"unhealthy_until"`
	MaxConnections int        `json:"max_connections"`
	Online         int64      `json:"online"`
	// OverCapacity: more devices online than the (lowered) capacity; none are kicked.
	OverCapacity bool  `json:"over_capacity"`
	AgentManaged bool  `json:"agent_managed"`
	Version      int64 `json:"version"`
}

type adminGroupView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Region       string   `json:"region"`
	LineType     string   `json:"line_type"`
	RatePermille int64    `json:"rate_permille"`
	PlanIDs      []string `json:"plan_ids"`
	Enabled      bool     `json:"enabled"`
	Sort         int      `json:"sort"`
	Version      int64    `json:"version"`
	// MeteringSource is node_reported when an agent governs any server of the group.
	MeteringSource string `json:"metering_source"`
	// MixedMetering warns that agent-governed and plain servers share the group; only
	// the governed ones are allocated.
	MixedMetering bool              `json:"mixed_metering"`
	Online        int64             `json:"online"`
	Capacity      int64             `json:"capacity"`
	Servers       []adminServerView `json:"servers"`
}

// liveLoad counts the live leases of every server in one aggregate query.
func (s *Server) liveLoad(db *gorm.DB, now time.Time) (map[string]int64, error) {
	var rows []struct {
		NodeID string
		Online int64
	}
	if err := s.liveLeases(db, now).Select("l.node_id::text AS node_id, COUNT(*) AS online").Group("l.node_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.NodeID] = r.Online
	}
	return out, nil
}

func (s *Server) adminGroupList(c *gin.Context) {
	db, now := s.database(c), s.now()
	var groups []model.NodeGroup
	if err := db.Order("sort, name, id").Limit(maxNodeGroups + 1).Find(&groups).Error; s.adminFailure(c, err) {
		return
	}
	rows, err := s.catalogRows(db, false)
	if s.adminFailure(c, err) {
		return
	}
	var agents []model.NodeAgent
	if err = db.Where("enabled = ?", true).Find(&agents).Error; s.adminFailure(c, err) {
		return
	}
	online, err := s.liveLoad(db, now)
	if s.adminFailure(c, err) {
		return
	}
	agentOf := map[string]model.NodeAgent{}
	for _, a := range agents {
		agentOf[a.NodeID] = a
	}
	servers := map[string][]adminServerView{}
	for _, row := range rows {
		if row.GroupID == nil {
			continue // reported by doctor as ungrouped_servers until the next migration
		}
		view := adminServerView{ID: row.ID, Label: row.Label, Name: row.Name, Enabled: row.Enabled, Health: "ok", MaxConnections: row.MaxConnections, Online: online[row.ID], Version: row.Version}
		view.OverCapacity = view.Online > int64(view.MaxConnections)
		if data, e := nodes.Decrypt(s.cfg.NodeEncryptionKey, row.ID, row.Ciphertext); e == nil {
			if proxies, e := nodes.Parse(string(data)); e == nil && len(proxies) == 1 {
				view.Protocol, _ = proxies[0]["type"].(string)
			}
		}
		agent, governed := agentOf[row.ID]
		view.AgentManaged = governed
		switch {
		case view.Protocol == "":
			view.Health = "config_error"
		case governed && (agent.LastSeenAt == nil || now.Sub(*agent.LastSeenAt) > s.cfg.AgentStale()):
			view.Health = "agent_offline"
		case row.UnhealthyUntil != nil && now.Before(*row.UnhealthyUntil):
			view.Health, view.UnhealthyUntil = "cooldown", row.UnhealthyUntil
		}
		servers[*row.GroupID] = append(servers[*row.GroupID], view)
	}
	out := make([]adminGroupView, 0, len(groups))
	for _, g := range groups {
		plans := []string{}
		if json.Unmarshal([]byte(g.PlanIDs), &plans) != nil {
			fail(c, 503, "node_catalog_unavailable", "节点目录不可用")
			return
		}
		view := adminGroupView{ID: g.ID, Name: g.Name, Region: g.Region, LineType: g.LineType, RatePermille: g.RatePermille, PlanIDs: plans, Enabled: g.Enabled, Sort: g.Sort, Version: g.Version,
			MeteringSource: meteringClient, Servers: servers[g.ID]}
		if view.Servers == nil {
			view.Servers = []adminServerView{}
		}
		managed, plain := false, false
		for _, srv := range view.Servers {
			view.Online += srv.Online
			if srv.Enabled {
				view.Capacity += int64(srv.MaxConnections)
				managed, plain = managed || srv.AgentManaged, plain || !srv.AgentManaged
			}
		}
		if managed {
			view.MeteringSource = meteringNode
		}
		view.MixedMetering = managed && plain
		out = append(out, view)
	}
	c.JSON(200, gin.H{"groups": out, "server_time": now, "max_groups": maxNodeGroups, "max_servers": nodes.MaxNodes})
}

// validGroupName follows the proxy-name rule of nodepolicy, since a group name becomes
// the proxy name of every profile rendered for it.
func validGroupName(name string) bool {
	reserved := []string{"VPN", "DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL"}
	return name != "" && validText(name, 120) && utf8.RuneCountInString(name) <= 120 && !slices.Contains(reserved, strings.ToUpper(name))
}

func (s *Server) adminGroupSave(c *gin.Context) {
	var in groupInput
	if !decode(c, &in) {
		return
	}
	if !validGroupName(in.Name) || !validateNodeInput(nodeInput{Region: in.Region, LineType: in.LineType, RatePermille: in.RatePermille, PlanIDs: in.PlanIDs}) || in.Sort < 0 || in.Sort > 10000 {
		fail(c, 400, "invalid_node_group", "节点组属性无效：请检查名称、地区、线路、套餐与排序，倍率为 1–10000‰")
		return
	}
	id := c.Param("id")
	if id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || in.Version < 1 {
			fail(c, 400, "invalid_node_group", "编辑时需要节点组标识及当前版本号")
			return
		}
		id = parsed.String()
	} else if in.Version != 0 {
		fail(c, 400, "invalid_node_group", "新节点组版本必须为零")
		return
	}
	slices.Sort(in.PlanIDs)
	if in.PlanIDs == nil {
		in.PlanIDs = []string{}
	}
	planJSON, _ := json.Marshal(in.PlanIDs)
	var saved model.NodeGroup
	err := s.adminTransaction(c, func(tx *gorm.DB, actor model.User, now time.Time) error {
		if e := requireActivePlans(tx, in.PlanIDs); e != nil {
			return e
		}
		group := model.NodeGroup{ID: uuid.NewString(), Version: 1}
		if id != "" {
			locked, e := lockGroupRow(tx, id)
			if e != nil {
				return e
			}
			if group = *locked; group.Version != in.Version {
				return &apiError{409, "node_group_version_conflict", "节点组已被其他管理员修改，请重新读取后编辑"}
			}
		} else {
			var count int64
			if e := tx.Model(&model.NodeGroup{}).Count(&count).Error; e != nil {
				return e
			}
			if count >= maxNodeGroups {
				return &apiError{409, "node_group_limit", "最多支持 128 个节点组"}
			}
		}
		taken, e := groupNameTaken(tx, in.Name, in.PlanIDs, group.ID)
		if e != nil {
			return e
		}
		if taken {
			return &apiError{409, "node_group_name_exists", "同一套餐范围内已存在同名节点组"}
		}
		before := group
		group.Name, group.Region, group.LineType, group.RatePermille, group.PlanIDs, group.Enabled, group.Sort = in.Name, in.Region, in.LineType, in.RatePermille, string(planJSON), in.Enabled, in.Sort
		switch {
		case id == "":
			if e = tx.Create(&group).Error; e != nil {
				return e
			}
		case group != before:
			// Public metadata changed: the version moves and every session of the group
			// sees a new profile (plan 6.5).
			group.Version++
			if e = tx.Save(&group).Error; e != nil {
				return e
			}
			if before.Enabled && !group.Enabled {
				if e = s.endLeases(tx, now, model.LeaseGroupUnavailable, "group_id = ?", group.ID); e != nil {
					return e
				}
			}
			if e = syncGroupCopies(tx, s.cfg.Schema, group); e != nil {
				return e
			}
		}
		saved = group
		return audit(tx, actor, "node_group.saved", group.ID, group.Version)
	})
	if s.adminFailure(c, err) {
		return
	}
	s.catalogUpdates.notify()
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"group": adminGroupView{ID: saved.ID, Name: saved.Name, Region: saved.Region, LineType: saved.LineType, RatePermille: saved.RatePermille, PlanIDs: in.PlanIDs,
		Enabled: saved.Enabled, Sort: saved.Sort, Version: saved.Version, MeteringSource: meteringClient, Servers: []adminServerView{}}})
}

// syncGroupCopies writes the group's metadata into its servers' legacy columns and
// plan lists, so an older binary after a rollback still reads a consistent catalog
// (dual write, plan 4.1). Server versions do not move.
func syncGroupCopies(tx *gorm.DB, schema string, g model.NodeGroup) error {
	if err := tx.Model(&model.Node{}).Where("group_id = ?", g.ID).
		UpdateColumns(map[string]any{"region": g.Region, "line_type": g.LineType, "rate_permille": g.RatePermille}).Error; err != nil {
		return err
	}
	return tx.Model(&model.NodeConfig{}).Where("node_id IN (SELECT id FROM "+schema+".nodes WHERE group_id = ?)", g.ID).UpdateColumn("plan_ids", g.PlanIDs).Error
}

func (s *Server) adminGroupDelete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	var in struct {
		Version int64 `json:"version"`
	}
	if !decode(c, &in) {
		return
	}
	if err != nil || id == uuid.Nil || in.Version < 1 {
		fail(c, 400, "invalid_node_group", "删除时需要节点组标识及当前版本号")
		return
	}
	err = s.adminTransaction(c, func(tx *gorm.DB, actor model.User, now time.Time) error {
		group, e := lockGroupRow(tx, id.String())
		if e != nil {
			return e
		}
		if group.Version != in.Version {
			return &apiError{409, "node_group_version_conflict", "节点组已被其他管理员修改，请刷新后重新确认删除"}
		}
		var servers int64
		if e = tx.Model(&model.Node{}).Where("group_id = ?", group.ID).Count(&servers).Error; e != nil {
			return e
		}
		if servers != 0 {
			return &apiError{409, "node_group_not_empty", "请先删除或移出组内的全部服务器"}
		}
		if e = s.endLeases(tx, now, model.LeaseGroupUnavailable, "group_id = ?", group.ID); e != nil {
			return e
		}
		if e = tx.Delete(group).Error; e != nil {
			return e
		}
		return audit(tx, actor, "node_group.deleted", group.ID, group.Version)
	})
	if s.adminFailure(c, err) {
		return
	}
	s.catalogUpdates.notify()
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id.String(), "version": in.Version})
}
