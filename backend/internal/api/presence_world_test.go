package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

// presenceWorld is the round-6 part of the billing world (handoff_node_use_history_and_load_
// balancing_c.md 12, round 6): one line made of two servers ("pair", a node group the platform
// suites connect to), and the control calls the platform acceptance needs to prove, on the
// device, the device limit at connect time, the automatic reconnect after a server is withdrawn
// and the release of the online slot when the app stops:
//
//   - occupy {user, count}: count other devices of the account sign in and connect to the pair
//     line through the real client API, and keep their slots by reporting /traffic every interval
//     (what a running Core does);
//   - vacate {user, count}: that many of them disconnect (real release), 0 = all;
//   - presence {user}: the account's live online leases (the server's own count, the
//     liveLeases conditions) and its latest ended ones with the recorded reason;
//   - withdraw {user}: the administrator disables the pair server the app's device is on, through
//     the admin API (the live lease ends node_unavailable and the device is told
//     connection_reassigned);
//   - restore: every withdrawn server is enabled again.
//
// Everything goes through the production router; nothing here exists in a shipped build.
type presenceWorld struct {
	t        *testing.T
	db       *gorm.DB
	cfg      config.Config
	router   http.Handler
	admin    func(method, path string, body any) *httptest.ResponseRecorder
	yaml     func(name string) string
	password string
	emails   map[string]string

	groupID string
	servers map[string]adminServerSaved

	mu      sync.Mutex
	holders map[string][]*slotHolder
}

const pairLine = "Pair-01"

type slotHolder struct {
	device, token, lease string
	stop, done           chan struct{}
}

func newPresenceWorld(t *testing.T, db *gorm.DB, cfg config.Config, router http.Handler, admin func(string, string, any) *httptest.ResponseRecorder,
	yaml func(string) string, password string, emails map[string]string) *presenceWorld {
	t.Helper()
	p := &presenceWorld{t: t, db: db, cfg: cfg, router: router, admin: admin, yaml: yaml, password: password, emails: emails,
		servers: map[string]adminServerSaved{}, holders: map[string][]*slotHolder{}}
	w := admin("POST", "/admin/node-groups", map[string]any{"name": pairLine, "region": "JP", "line_type": "direct", "rate_permille": 1000,
		"plan_ids": []string{}, "enabled": true, "sort": 0, "version": 0})
	if w.Code != http.StatusCreated {
		t.Fatalf("pair group: %d %s", w.Code, w.Body.String())
	}
	var group struct {
		Group adminGroupView `json:"group"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &group)
	p.groupID = group.Group.ID
	for _, label := range []string{"pair-a", "pair-b"} {
		w := admin("POST", "/admin/nodes", map[string]any{"yaml": yaml(pairLine), "group_id": p.groupID, "max_connections": 10, "label": label, "enabled": true, "version": 0})
		if w.Code != http.StatusCreated {
			t.Fatalf("pair server: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Nodes []adminServerSaved `json:"nodes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		p.servers[out.Nodes[0].ID] = out.Nodes[0]
	}
	return p
}

func (p *presenceWorld) client(method, path, token string, headers map[string]string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/api/v1/client"+path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	p.router.ServeHTTP(w, r)
	return w
}

// occupy connects count more devices of user to the pair line and keeps them online.
func (p *presenceWorld) occupy(user string, count int) (map[string]any, error) {
	email, ok := p.emails[user]
	if !ok || count < 1 || count > 8 {
		return nil, fmt.Errorf("unknown user or count")
	}
	for range count {
		h := &slotHolder{device: uuid.NewString(), stop: make(chan struct{}), done: make(chan struct{})}
		w := p.client("POST", "/login", "", nil, map[string]any{"email": email, "password": p.password, "device_id": h.device, "platform": "windows", "app_version": "9.9.9"})
		var login struct {
			Token string `json:"token"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &login) != nil {
			return nil, fmt.Errorf("holder login %d %s", w.Code, errorCode(w))
		}
		h.token = login.Token
		w = p.client("POST", "/connections", h.token, map[string]string{"Idempotency-Key": "hold-" + h.device}, map[string]any{"group_id": p.groupID, "last_sequence": 0})
		var admitted connectionResponse
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &admitted) != nil {
			return map[string]any{"refused": errorCode(w)}, nil
		}
		h.lease = admitted.Connection.ID
		go p.keep(h)
		p.mu.Lock()
		p.holders[user] = append(p.holders[user], h)
		p.mu.Unlock()
	}
	return map[string]any{"held": len(p.holders[user])}, nil
}

// keep reports an empty batch every interval, as a connected Core does; that renews the slot.
func (p *presenceWorld) keep(h *slotHolder) {
	defer close(h.done)
	ticker := time.NewTicker(p.cfg.ReportInterval())
	defer ticker.Stop()
	for sequence := int64(1); ; sequence++ {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			p.client("POST", "/traffic", h.token, nil, map[string]int64{"sequence": sequence, "upload_bytes": 0, "download_bytes": 0})
		}
	}
}

// vacate disconnects count holders of user (0 = all): the report loop ends, then the lease is
// released, as a Core's Disconnect does.
func (p *presenceWorld) vacate(user string, count int) map[string]any {
	p.mu.Lock()
	all := p.holders[user]
	if count <= 0 || count > len(all) {
		count = len(all)
	}
	gone := all[:count]
	p.holders[user] = slices.Clone(all[count:])
	p.mu.Unlock()
	for _, h := range gone {
		close(h.stop)
		<-h.done
		p.client("POST", "/connections/"+h.lease+"/release", h.token, nil, map[string]string{"reason": "user"})
	}
	return map[string]any{"released": len(gone), "held": len(p.holders[user])}
}

func (p *presenceWorld) holderDevices() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]bool{}
	for _, list := range p.holders {
		for _, h := range list {
			out[h.device] = true
		}
	}
	return out
}

type presenceLease struct {
	Lease    string `json:"lease"`
	Device   string `json:"device_id"`
	Holder   bool   `json:"holder"`
	Server   string `json:"server"`
	Label    string `json:"label,omitempty"`
	Line     string `json:"line"`
	Method   string `json:"method"`
	Reason   string `json:"end_reason,omitempty"`
	EndedAgo int64  `json:"ended_ms_ago,omitempty"`
	Seconds  int64  `json:"held_seconds"`
}

// presence is the account's live leases (the conditions every count on the server uses) and its
// five most recently ended ones.
func (p *presenceWorld) presence(user string) (map[string]any, error) {
	email, ok := p.emails[user]
	if !ok {
		return nil, fmt.Errorf("unknown user")
	}
	var u model.User
	if err := p.db.First(&u, "email = ?", email).Error; err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	holders := p.holderDevices()
	view := func(l model.ConnectionLease) presenceLease {
		out := presenceLease{Lease: l.ID, Device: l.DeviceID, Holder: holders[l.DeviceID], Server: l.NodeID, Label: p.servers[l.NodeID].Label, Line: l.GroupID,
			Method: l.Method, Reason: l.EndReason, Seconds: int64(now.Sub(l.AcquiredAt) / time.Second)}
		if l.EndedAt != nil {
			out.EndedAgo = now.Sub(*l.EndedAt).Milliseconds()
		}
		if l.GroupID == p.groupID {
			out.Line = "pair"
		}
		return out
	}
	var live []model.ConnectionLease
	err := p.db.Table(p.cfg.Schema+".connection_leases AS l").Select("l.*").
		Joins("JOIN "+p.cfg.Schema+".native_sessions AS ns ON ns.id = l.session_id").
		Where("l.user_id = ? AND l.state = ? AND l.expires_at > ? AND ns.revoked_at IS NULL AND ns.expires_at > ?", u.ID, model.LeaseActive, now, now).
		Order("l.acquired_at").Find(&live).Error
	if err != nil {
		return nil, err
	}
	var ended []model.ConnectionLease
	if err = p.db.Where("user_id = ? AND (state = ? OR expires_at <= ?)", u.ID, model.LeaseEnded, now).Order("COALESCE(ended_at, expires_at) DESC").Limit(5).Find(&ended).Error; err != nil {
		return nil, err
	}
	out := map[string]any{"live": []presenceLease{}, "ended": []presenceLease{}}
	liveViews, endedViews := []presenceLease{}, []presenceLease{}
	for _, l := range live {
		liveViews = append(liveViews, view(l))
	}
	for _, l := range ended {
		v := view(l)
		if l.State == model.LeaseActive { // lapsed, not yet swept: what the sweeper will record
			v.Reason, v.EndedAgo = "expired", now.Sub(l.ExpiresAt).Milliseconds()
		}
		endedViews = append(endedViews, v)
	}
	out["live"], out["ended"] = liveViews, endedViews
	return out, nil
}

// withdraw disables the pair server the account's own device (not a holder) is connected to.
func (p *presenceWorld) withdraw(user string) (map[string]any, error) {
	state, err := p.presence(user)
	if err != nil {
		return nil, err
	}
	target := ""
	for _, l := range state["live"].([]presenceLease) {
		if !l.Holder && l.Line == "pair" {
			target = l.Server
		}
	}
	if target == "" {
		return nil, fmt.Errorf("the account's device holds no pair lease")
	}
	return p.setEnabled(target, false)
}

func (p *presenceWorld) setEnabled(id string, enabled bool) (map[string]any, error) {
	server := p.servers[id]
	w := p.admin("POST", "/admin/nodes/"+id, map[string]any{"yaml": p.yaml(pairLine), "group_id": p.groupID, "max_connections": server.MaxConnections,
		"label": server.Label, "enabled": enabled, "version": server.Version})
	if w.Code != http.StatusOK {
		return nil, fmt.Errorf("save server %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Nodes []adminServerSaved `json:"nodes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	p.servers[id] = out.Nodes[0]
	return map[string]any{"server": id, "label": server.Label, "enabled": enabled}, nil
}

func (p *presenceWorld) restore() error {
	for id, server := range p.servers {
		if !server.Enabled {
			if _, err := p.setEnabled(id, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopAll ends every holder (the world is shutting down).
func (p *presenceWorld) stopAll() {
	for user := range p.emails {
		p.vacate(user, 0)
	}
}

// handle answers the presence control calls; false when action is not one of them.
func (p *presenceWorld) handle(action string, user string, count int, answer func(any), fail func(int, string)) bool {
	var out map[string]any
	var err error
	switch action {
	case "occupy":
		out, err = p.occupy(user, count)
	case "vacate":
		out = p.vacate(user, count)
	case "presence":
		out, err = p.presence(user)
	case "withdraw":
		out, err = p.withdraw(user)
	case "restore":
		err = p.restore()
		out = map[string]any{"restored": true}
	default:
		return false
	}
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return true
	}
	answer(out)
	return true
}
