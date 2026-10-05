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
}

type catalogProfile struct {
	Data  []byte
	Nodes []nodeChoice
	Node  model.Node
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
	var selected *catalogRow
	for i := range rows {
		row := &rows[i]
		var plans []string
		if json.Unmarshal([]byte(row.PlanIDs), &plans) != nil {
			return out, unavailable
		}
		if row.LineType == "dedicated" && !sub.AllowDedicated || len(plans) != 0 && !slices.Contains(plans, sub.PlanID) {
			continue
		}
		out.Nodes = append(out.Nodes, nodeChoice{row.ID, row.Name, row.Region, row.LineType, row.RatePermille, row.Version})
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
	profile, err := yaml.Marshal(map[string]any{
		"mode": "rule", "proxies": proxies,
		"proxy-groups": []map[string]any{{"name": nodes.GroupName, "type": "select", "proxies": []string{selected.Name}}},
		"rules":        []string{"MATCH," + nodes.GroupName},
	})
	if err != nil {
		return out, unavailable
	}
	metadata, _ := json.Marshal(out.Nodes)
	out.Data = append([]byte("# catalog: "+nativeProfileVersion(metadata)+"\n"), profile...)
	out.Node = selected.Node
	return out, nil
}

func sessionRate(sess model.NativeSession) int64 {
	if sess.NodeID == "" {
		return 1000
	}
	return sess.RatePermille
}

func (s *Server) catalogMetadata(db *gorm.DB, sub *model.Subscription) ([]model.Node, error) {
	rows, err := s.catalogRows(db, true)
	if err != nil {
		return nil, err
	}
	result := []model.Node{}
	for _, row := range rows {
		var plans []string
		if json.Unmarshal([]byte(row.PlanIDs), &plans) != nil {
			return nil, nodes.ErrInvalid
		}
		if row.LineType == "dedicated" && !sub.AllowDedicated || len(plans) != 0 && !slices.Contains(plans, sub.PlanID) {
			continue
		}
		result = append(result, row.Node)
	}
	return result, nil
}
