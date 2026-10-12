package api

import (
	"encoding/json"
	"slices"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
	"vpn/backend/internal/nodes"
)

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
	Node  model.Node
	// Proxy is the decrypted, validated definition of the selected node. It never
	// leaves the server except through Data (shared mode) or a credentialed grant.
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

// listable decides whether a node appears in a client catalog. Production never
// lists nodes that no agent governs: their shared credentials cannot be metered.
func (s *Server) listable(nodeID string, governed map[string][]string) bool {
	_, ok := governed[nodeID]
	return ok || s.cfg.DevMetering()
}

func choiceOf(n model.Node, governed map[string][]string) nodeChoice {
	choice := nodeChoice{ID: n.ID, Name: n.Name, Region: n.Region, LineType: n.LineType, RatePermille: n.RatePermille, Version: n.Version}
	if _, ok := governed[n.ID]; ok {
		choice.MeteringSource = meteringNode
	}
	return choice
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

func (s *Server) catalogProfile(db *gorm.DB, sub *model.Subscription, nodeID string) (catalogProfile, error) {
	unavailable := &apiError{503, "client_config_unavailable", "套餐线路配置尚未就绪，请联系管理员"}
	out := catalogProfile{Nodes: []nodeChoice{}}
	if db == nil || sub == nil {
		return out, unavailable
	}
	rows, err := s.catalogRows(db, true)
	if err != nil {
		return out, unavailable
	}
	governed, err := s.agentNodes(db)
	if err != nil {
		return out, unavailable
	}
	var selected *catalogRow
	for i := range rows {
		row := &rows[i]
		var plans []string
		if json.Unmarshal([]byte(row.PlanIDs), &plans) != nil {
			return out, unavailable
		}
		if row.LineType == "dedicated" && !sub.AllowDedicated || len(plans) != 0 && !slices.Contains(plans, sub.PlanID) || !s.listable(row.ID, governed) {
			continue
		}
		out.Nodes = append(out.Nodes, choiceOf(row.Node, governed))
		if row.ID == nodeID || nodeID == "" && selected == nil {
			selected = row
		}
	}
	if selected == nil {
		return out, unavailable
	}
	data, err := nodes.Decrypt(s.cfg.NodeEncryptionKey, selected.ID, selected.Ciphertext)
	if err != nil {
		return out, unavailable
	}
	proxies, err := nodes.Parse(string(data))
	if err != nil || len(proxies) != 1 || proxies[0]["name"] != selected.Name || selected.RatePermille < 1 || selected.RatePermille > 10000 {
		return out, unavailable
	}
	metadata, _ := json.Marshal(out.Nodes)
	out.Tag = nativeProfileVersion(metadata)
	out.Proxy = proxies[0]
	if out.Data, err = renderProfile(out.Tag, proxies[0]); err != nil {
		return out, unavailable
	}
	out.Node = selected.Node
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

func (s *Server) catalogMetadata(db *gorm.DB, sub *model.Subscription) ([]nodeChoice, error) {
	// Browser metadata reads must not even load encrypted connection parameters.
	rows := []catalogRow{}
	err := db.Table(s.cfg.Schema+".nodes AS n").
		Select("n.id, n.name, n.region, n.line_type, n.rate_permille, n.version, c.plan_ids").
		Joins("JOIN "+s.cfg.Schema+".node_configs AS c ON c.node_id = n.id").
		Where("n.enabled = ?", true).Order("n.name, n.id").Limit(nodes.MaxNodes + 1).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) > nodes.MaxNodes {
		return nil, nodes.ErrInvalid
	}
	governed, err := s.agentNodes(db)
	if err != nil {
		return nil, err
	}
	result := []nodeChoice{}
	for _, row := range rows {
		var plans []string
		if json.Unmarshal([]byte(row.PlanIDs), &plans) != nil {
			return nil, nodes.ErrInvalid
		}
		if row.LineType == "dedicated" && !sub.AllowDedicated || len(plans) != 0 && !slices.Contains(plans, sub.PlanID) || !s.listable(row.ID, governed) {
			continue
		}
		result = append(result, choiceOf(row.Node, governed))
	}
	return result, nil
}
