package api

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Node group allocation (handoff_node_use_history_and_load_balancing_c.md 6.2). Every
// admission to a group runs here, under the group's row lock, so the load read and
// the lease written after it cannot interleave with another admission to the same
// group; other groups are not blocked. Order of preference:
//  1. this device's own live server in the group (its own lease is not load);
//  2. the user's sticky server for the group, whatever its load, while it is healthy
//     and not full;
//  3. the lowest load ratio (live leases / max_connections, compared by integer
//     cross-multiplication); ties are broken uniformly at random;
//  4. only when no healthy server has room: cooling servers, by the same rule.
//
// Disabled, deleted, misconfigured and (for grants) agent-offline servers are never
// chosen, and neither is avoid_node_id.

type admissionMode int

const (
	// admitConnections is POST /connections: client_reported groups only.
	admitConnections admissionMode = iota
	// admitLegacy is an older Core's GET /config.
	admitLegacy
	// admitGrant is POST /grants: agent-governed, agent-online servers only.
	admitGrant
)

// candidate is one server that may receive the admission.
type candidate struct {
	ID  string
	Max int
	// Load counts live leases other than this device's own.
	Load int64
	// Mine: this device holds a live lease on this server.
	Mine    bool
	Cooling bool
}

func (c candidate) free() bool { return c.Load < int64(c.Max) }

// lighter reports whether a's load ratio is below b's.
func lighter(a, b candidate) int {
	l, r := a.Load*int64(b.Max), b.Load*int64(a.Max)
	switch {
	case l < r:
		return -1
	case l > r:
		return 1
	}
	return 0
}

// pickServer applies the order above to candidates (hard exclusions already removed)
// and returns the chosen index, or -1 when every candidate is full.
func pickServer(cands []candidate, preferred string, random func(int) int) int {
	for i, c := range cands {
		if c.Mine && c.free() {
			return i
		}
	}
	for i, c := range cands {
		if c.ID == preferred && !c.Cooling && c.free() {
			return i
		}
	}
	for _, cooling := range []bool{false, true} {
		best := []int{}
		for i, c := range cands {
			if c.Cooling != cooling || !c.free() {
				continue
			}
			if len(best) == 0 {
				best = append(best, i)
				continue
			}
			switch lighter(c, cands[best[0]]) {
			case -1:
				best = append(best[:0], i)
			case 0:
				best = append(best, i)
			}
		}
		if len(best) > 0 {
			return best[random(len(best))]
		}
	}
	return -1
}

type allocation struct {
	Profile catalogProfile
	Target  leaseTarget
	// Agent governs the chosen server (admitGrant only).
	Agent model.NodeAgent
}

func groupFull() error {
	return &apiError{409, "group_full", "该线路当前已满，请稍后再试或选择其他线路"}
}

func grantRequired() error {
	return &apiError{409, "grant_required", "此线路需使用节点授权连接"}
}

func agentUnavailable() error {
	return &apiError{503, "agent_unavailable", "该线路的节点控制服务暂不可用"}
}

// lockGroup resolves ref (a group id, or a server id from an older client: D3) and
// takes the group row lock (plan 4.3: after user, session and subscription).
func (s *Server) lockGroup(tx *gorm.DB, ref string) (*model.NodeGroup, error) {
	if ref == "" {
		return nil, groupUnavailable()
	}
	var group model.NodeGroup
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = COALESCE((SELECT id FROM "+s.cfg.Schema+".node_groups WHERE id = ?), (SELECT group_id FROM "+s.cfg.Schema+".nodes WHERE id = ?))", ref, ref).
		Take(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, groupUnavailable()
	}
	return &group, err
}

// allocate chooses the server for this device's admission to group ref and returns
// its rendered profile and the (already capacity-checked) lease target. The sticky
// assignment is written here; a later refusal rolls it back with the transaction.
func (s *Server) allocate(tx *gorm.DB, sess *model.NativeSession, sub *model.Subscription, ref, avoid string, mode admissionMode, now time.Time) (allocation, error) {
	var out allocation
	group, err := s.lockGroup(tx, ref)
	if err != nil {
		return out, err
	}
	if visible, e := groupVisible(*group, sub); e != nil || !visible {
		return out, groupUnavailable()
	}
	// The group's enabled servers with their live load (this device's own lease is not
	// load) and the user's preference, read under the group lock in one statement.
	loads := s.liveLeases(tx, now).Where("l.node_id IN (SELECT id FROM "+s.cfg.Schema+".nodes WHERE group_id = ?)", group.ID).
		Select("l.node_id, COUNT(*) FILTER (WHERE NOT (l.user_id = ? AND l.device_id = ?)) AS load, bool_or(l.user_id = ? AND l.device_id = ?) AS mine", sess.UserID, sess.DeviceID, sess.UserID, sess.DeviceID).
		Group("l.node_id")
	var servers []struct {
		ID             string
		MaxConnections int
		UnhealthyUntil *time.Time
		Preferred      bool
		Load           int64
		Mine           bool
	}
	if err = tx.Table(s.cfg.Schema+".nodes AS n").
		Select("n.id, n.max_connections, n.unhealthy_until, COALESCE(n.id = (SELECT a.node_id FROM "+s.cfg.Schema+".node_assignments AS a WHERE a.user_id = ? AND a.group_id = ?), false) AS preferred, COALESCE(ld.load, 0) AS load, COALESCE(ld.mine, false) AS mine", sess.UserID, group.ID).
		Joins("JOIN "+s.cfg.Schema+".node_configs AS c ON c.node_id = n.id").
		Joins("LEFT JOIN (?) AS ld ON ld.node_id = n.id", loads).
		Where("n.group_id = ? AND n.enabled = ?", group.ID, true).Order("n.id").Find(&servers).Error; err != nil {
		return out, err
	}
	governed, err := s.agentNodes(tx)
	if err != nil {
		return out, err
	}
	agentGroup := false
	for _, srv := range servers {
		if _, ok := governed[srv.ID]; ok {
			agentGroup = true
		}
	}
	switch {
	case mode == admitConnections && agentGroup:
		return out, grantRequired()
	case mode == admitGrant && !agentGroup:
		return out, agentUnavailable()
	}
	agents := map[string]model.NodeAgent{}
	if mode == admitGrant {
		var list []model.NodeAgent
		if err = tx.Where("enabled = ?", true).Find(&list).Error; err != nil {
			return out, err
		}
		for _, a := range list {
			if a.LastSeenAt != nil && now.Sub(*a.LastSeenAt) <= s.cfg.AgentStale() {
				agents[a.NodeID] = a
			}
		}
	}
	cands, ids, preferred := []candidate{}, []string{}, ""
	for _, srv := range servers {
		_, governs := governed[srv.ID]
		_, online := agents[srv.ID]
		switch {
		case srv.ID == avoid, !s.listable(srv.ID, governed):
			continue
		case agentGroup && !governs: // a mixed group is metered by its agents only
			continue
		case mode == admitGrant && !online:
			continue
		}
		if srv.Preferred {
			preferred = srv.ID
		}
		cands = append(cands, candidate{ID: srv.ID, Max: srv.MaxConnections, Load: srv.Load, Mine: srv.Mine, Cooling: srv.UnhealthyUntil != nil && now.Before(*srv.UnhealthyUntil)})
		ids = append(ids, srv.ID)
	}
	if len(cands) == 0 {
		if mode == admitGrant {
			return out, agentUnavailable()
		}
		return out, groupUnavailable()
	}
	for {
		i := pickServer(cands, preferred, s.randomIndex)
		if i < 0 {
			return out, groupFull()
		}
		chosen := cands[i]
		profile, e := s.catalogProfileWith(tx, sub, chosen.ID, governed)
		usable := e == nil && profile.Node.ID == chosen.ID
		if usable && mode == admitGrant {
			proxyType, _ := profile.Proxy["type"].(string)
			out.Agent = agents[chosen.ID]
			usable = agentSupports(out.Agent, proxyType)
		}
		if !usable {
			// Undecryptable, invalid or unsupported: excluded like a disabled server.
			cands, ids = slices.Delete(cands, i, i+1), slices.Delete(ids, i, i+1)
			if len(cands) == 0 {
				if mode == admitGrant {
					return out, agentUnavailable()
				}
				return out, groupUnavailable()
			}
			continue
		}
		out.Profile = profile
		out.Target = leaseTarget{GroupID: group.ID, NodeID: chosen.ID, MaxConnections: chosen.Max, checked: true}
		// The preference moves only when the preferred server is unusable (disabled,
		// deleted, agent offline, cooling or just failed); merely full, it stays.
		keep := false
		if preferred != "" {
			if j := slices.Index(ids, preferred); j >= 0 && !cands[j].Cooling {
				keep = true
			}
		}
		return out, s.touchAssignment(tx, sess.UserID, group.ID, chosen.ID, keep, now)
	}
}

// touchAssignment records the sticky server of (user, group): the first allocation
// creates it, an unusable preference is replaced, and every use refreshes it.
func (s *Server) touchAssignment(tx *gorm.DB, userID, groupID, nodeID string, keep bool, now time.Time) error {
	assignment := model.NodeAssignment{UserID: userID, GroupID: groupID, NodeID: nodeID, AssignedAt: now, LastUsedAt: now}
	set := clause.Set{{Column: clause.Column{Name: "last_used_at"}, Value: gorm.Expr("EXCLUDED.last_used_at")}}
	if !keep {
		set = append(set,
			clause.Assignment{Column: clause.Column{Name: "node_id"}, Value: gorm.Expr("EXCLUDED.node_id")},
			clause.Assignment{Column: clause.Column{Name: "assigned_at"}, Value: gorm.Expr("CASE WHEN node_assignments.node_id = EXCLUDED.node_id THEN node_assignments.assigned_at ELSE EXCLUDED.assigned_at END")})
	}
	return tx.Omit("User", "Group", "Node").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "group_id"}}, DoUpdates: set}).Create(&assignment).Error
}

// firstVisibleGroup is the group an older Core without a selection is bound to.
func (s *Server) firstVisibleGroup(tx *gorm.DB, sub *model.Subscription) string {
	groups, _, _, err := s.visibleGroups(tx, sub, "", nil)
	if err != nil || len(groups) == 0 {
		return ""
	}
	return groups[0].Group.ID
}

// Soft cooldown (plan 6.4): failure reports from NODE_HEALTH_FAILURE_USERS distinct
// users within healthWindow put a server into NODE_HEALTH_COOLDOWN_SECONDS of
// cooldown. One user reporting repeatedly never triggers it.
const healthWindow = 5 * time.Minute

// reportFailure records that userID's connection to server nodeID failed. The caller
// holds the server's group lock (plan 4.3: group -> server -> lease), the same order
// the admin API uses, so a report never deadlocks with an admin save.
func (s *Server) reportFailure(tx *gorm.DB, userID, nodeID, reason string, now time.Time) error {
	var exists int64
	if err := tx.Model(&model.Node{}).Where("id = ?", nodeID).Count(&exists).Error; err != nil || exists == 0 {
		return err // the server is gone: nothing to cool down
	}
	event := model.NodeHealthEvent{ID: uuid.NewString(), NodeID: nodeID, UserID: userID, Reason: reason, OccurredAt: now}
	if err := tx.Omit("Node", "User").Create(&event).Error; err != nil {
		return err
	}
	var users int64
	if err := tx.Model(&model.NodeHealthEvent{}).Where("node_id = ? AND occurred_at > ?", nodeID, now.Add(-healthWindow)).Distinct("user_id").Count(&users).Error; err != nil {
		return err
	}
	if users < int64(s.cfg.HealthFailureUsers()) {
		return nil
	}
	until := now.Add(s.cfg.HealthCooldown())
	return tx.Model(&model.Node{}).Where("id = ? AND (unhealthy_until IS NULL OR unhealthy_until < ?)", nodeID, until).UpdateColumn("unhealthy_until", until).Error
}
