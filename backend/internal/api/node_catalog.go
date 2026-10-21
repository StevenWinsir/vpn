package api

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

// catalogRow is one server with its stored configuration (admin listing).
type catalogRow struct {
	model.Node `gorm:"embedded"`
	Ciphertext string
	PlanIDs    string
}

type nodeChoice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	LineType     string `json:"line_type"`
	RatePermille int64  `json:"rate_permille"`
	Version      int64  `json:"version"`
	// MeteringSource is "node_reported" for nodes governed by an enabled NodeAgent and
	// omitted otherwise, so catalogs without agents keep their original shape.
	MeteringSource string `json:"metering_source,omitempty"`
}

type catalogProfile struct {
	Data  []byte
	Nodes []nodeChoice
	// Node is the selected server; its public fields (region, line type, rate) are
	// its group's.
	Node  model.Node
	Group model.NodeGroup
	// Proxy is the decrypted, validated definition of the selected server, renamed to
	// its group. It never leaves the server except through Data (shared mode) or a
	// credentialed grant.
	Proxy map[string]any
	// Tag identifies the visible catalog; it is embedded in Data so any catalog edit
	// changes the profile version (conservative, see handoff_app.md 2.2 item 5).
	Tag string
}

const meteringNode = "node_reported"
const meteringClient = "client_reported"

// agentNodes returns node ids governed by an enabled agent, mapped to the protocols
// that agent claims to enforce.
func (s *Server) agentNodes(db *gorm.DB) (map[string][]string, error) {
	var agents []model.NodeAgent
	if err := db.Where("enabled = ?", true).Find(&agents).Error; err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(agents))
	for _, a := range agents {
		var protocols []string
		if json.Unmarshal([]byte(a.Protocols), &protocols) != nil {
			protocols = nil
		}
		out[a.NodeID] = protocols
	}
	return out, nil
}

// listable decides whether a server can carry a client. Production never uses
// servers that no agent governs: their shared credentials cannot be metered.
func (s *Server) listable(nodeID string, governed map[string][]string) bool {
	_, ok := governed[nodeID]
	return ok || s.cfg.DevMetering()
}

func (s *Server) catalogRows(db *gorm.DB, enabled bool) ([]catalogRow, error) {
	rows := []catalogRow{}
	query := db.Table(s.cfg.Schema + ".nodes AS n").Select("n.*, c.ciphertext, c.plan_ids").Joins("JOIN " + s.cfg.Schema + ".node_configs AS c ON c.node_id = n.id")
	if enabled {
		query = query.Where("n.enabled = ?", true)
	}
	err := query.Order("n.id").Limit(nodes.MaxNodes + 1).Find(&rows).Error
	if len(rows) > nodes.MaxNodes {
		return nil, nodes.ErrInvalid
	}
	return rows, err
}

// groupVisible is the plan and dedicated-line rule of plan 6.1 for one group.
func groupVisible(g model.NodeGroup, sub *model.Subscription) (bool, error) {
	var plans []string
	if json.Unmarshal([]byte(g.PlanIDs), &plans) != nil {
		return false, nodes.ErrInvalid
	}
	return g.Enabled && !(g.LineType == "dedicated" && !sub.AllowDedicated) && (len(plans) == 0 || slices.Contains(plans, sub.PlanID)), nil
}

// serverRow is one enabled server of an enabled group with that group's metadata.
// Ciphertext is loaded only for the server a profile is rendered for.
type serverRow struct {
	ServerID, ServerName string
	ServerVersion        int64
	MaxConnections       int
	Ciphertext           string
	model.NodeGroup      `gorm:"embedded"`
}

// catalogGroup is one group a subscription may see (plan 6.1).
type catalogGroup struct {
	Group  model.NodeGroup
	Choice nodeChoice
}

// visibleGroups lists, in group id order, the groups sub may see: enabled, allowed by
// plan and dedicated-line entitlement, and holding at least one enabled server that
// can carry a client. A group with an agent-governed server is node_reported.
// Cooldown and load never affect visibility (D5). selected names the one server whose
// encrypted configuration is loaded along (its row is returned when it is listable).
// governed may carry the agent map the caller already read (nil: read it here).
func (s *Server) visibleGroups(db *gorm.DB, sub *model.Subscription, selected string, governed map[string][]string) ([]catalogGroup, *serverRow, map[string][]string, error) {
	rows := []serverRow{}
	err := db.Table(s.cfg.Schema+".nodes AS n").
		Select("n.id AS server_id, n.name AS server_name, n.version AS server_version, n.max_connections, CASE WHEN n.id::text = ? THEN c.ciphertext ELSE '' END AS ciphertext, g.*", selected).
		Joins("JOIN "+s.cfg.Schema+".node_configs AS c ON c.node_id = n.id").
		Joins("JOIN "+s.cfg.Schema+".node_groups AS g ON g.id = n.group_id").
		Where("n.enabled = ? AND g.enabled = ?", true, true).Order("g.id, n.id").Limit(nodes.MaxNodes + 1).Find(&rows).Error
	if err != nil {
		return nil, nil, nil, err
	}
	if len(rows) > nodes.MaxNodes {
		return nil, nil, nil, nodes.ErrInvalid
	}
	if governed == nil {
		if governed, err = s.agentNodes(db); err != nil {
			return nil, nil, nil, err
		}
	}
	out := []catalogGroup{}
	var chosen *serverRow
	for i := 0; i < len(rows); {
		g := rows[i].NodeGroup
		visible, e := groupVisible(g, sub)
		if e != nil {
			return nil, nil, nil, e
		}
		listed, agent := false, false
		for ; i < len(rows) && rows[i].ID == g.ID; i++ {
			if !s.listable(rows[i].ServerID, governed) {
				continue
			}
			listed = true
			_, governs := governed[rows[i].ServerID]
			agent = agent || governs
			if visible && rows[i].ServerID == selected {
				chosen = &rows[i]
			}
		}
		if !visible || !listed {
			continue
		}
		choice := nodeChoice{ID: g.ID, Name: g.Name, Region: g.Region, LineType: g.LineType, RatePermille: g.RatePermille, Version: g.Version}
		if agent {
			choice.MeteringSource = meteringNode
		}
		out = append(out, catalogGroup{Group: g, Choice: choice})
	}
	return out, chosen, governed, nil
}

func choices(groups []catalogGroup) []nodeChoice {
	out := make([]nodeChoice, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Choice)
	}
	return out
}

// catalogProfile renders the client document for one server (nodeID) of a group sub
// may see. The proxy is renamed to the group name, so every server of a group looks
// the same to the client (plan 6.5).
func (s *Server) catalogProfile(db *gorm.DB, sub *model.Subscription, nodeID string) (catalogProfile, error) {
	return s.catalogProfileWith(db, sub, nodeID, nil)
}

// catalogProfileWith reuses an agent map the caller already holds (the allocator).
func (s *Server) catalogProfileWith(db *gorm.DB, sub *model.Subscription, nodeID string, governed map[string][]string) (catalogProfile, error) {
	unavailable := &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	out := catalogProfile{Nodes: []nodeChoice{}}
	if db == nil || sub == nil || nodeID == "" {
		return out, unavailable
	}
	groups, selected, _, err := s.visibleGroups(db, sub, nodeID, governed)
	if err != nil {
		return out, unavailable
	}
	out.Nodes = choices(groups)
	if selected == nil {
		return out, unavailable
	}
	data, err := nodes.Decrypt(s.cfg.NodeEncryptionKey, selected.ServerID, selected.Ciphertext)
	if err != nil {
		return out, unavailable
	}
	proxies, err := nodes.Parse(string(data))
	if err != nil || len(proxies) != 1 || proxies[0]["name"] != selected.ServerName || selected.RatePermille < 1 || selected.RatePermille > 10000 {
		return out, unavailable
	}
	proxy := maps.Clone(proxies[0])
	proxy["name"] = selected.Name
	metadata, _ := json.Marshal(out.Nodes)
	out.Tag = nativeProfileVersion(metadata)
	out.Proxy = proxy
	if out.Data, err = renderProfile(out.Tag, proxy); err != nil {
		return out, unavailable
	}
	group := selected.NodeGroup
	out.Group = group
	out.Node = model.Node{ID: selected.ServerID, Name: selected.ServerName, Region: group.Region, LineType: group.LineType, RatePermille: group.RatePermille, Enabled: true,
		Version: selected.ServerVersion, GroupID: &group.ID, MaxConnections: selected.MaxConnections}
	return out, nil
}

// renderProfile is the single place a client document is produced: one proxy, one
// select group, one MATCH rule.
func renderProfile(tag string, proxy map[string]any) ([]byte, error) {
	name, _ := proxy["name"].(string)
	profile, err := yaml.Marshal(map[string]any{
		"mode": "rule", "proxies": []map[string]any{proxy},
		"proxy-groups": []map[string]any{{"name": nodes.GroupName, "type": "select", "proxies": []string{name}}},
		"rules":        []string{"MATCH," + nodes.GroupName},
	})
	if err != nil {
		return nil, err
	}
	return append([]byte("# catalog: "+tag+"\n"), profile...), nil
}

func sessionRate(sess model.NativeSession) int64 {
	if sess.NodeID == "" {
		return 1000
	}
	return sess.RatePermille
}

// catalogMetadata is the visible group list for browsers and the native catalog,
// ordered for display. It never loads encrypted connection parameters.
func (s *Server) catalogMetadata(db *gorm.DB, sub *model.Subscription) ([]nodeChoice, error) {
	groups, _, _, err := s.visibleGroups(db, sub, "", nil)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(groups, func(a, b catalogGroup) int {
		if a.Group.Sort != b.Group.Sort {
			return a.Group.Sort - b.Group.Sort
		}
		if c := strings.Compare(a.Group.Name, b.Group.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Group.ID, b.Group.ID)
	})
	return choices(groups), nil
}
