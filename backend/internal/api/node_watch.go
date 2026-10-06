package api

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

type catalogSignal struct {
	mu   sync.Mutex
	wake chan struct{}
}

func (s *catalogSignal) listen() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{})
	}
	return s.wake
}

func (s *catalogSignal) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake != nil {
		close(s.wake)
	}
	s.wake = make(chan struct{})
}

type nativeCatalog struct {
	SessionID string       `json:"session_id"`
	Revision  string       `json:"revision"`
	Reason    string       `json:"reason"`
	Nodes     []nodeChoice `json:"nodes"`
}

func (s *Server) readNativeCatalog(c *gin.Context) (nativeCatalog, error) {
	identity := c.MustGet("native_session").(model.NativeSession)
	out := nativeCatalog{SessionID: identity.ID, Nodes: []nodeChoice{}}
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var user model.User
		if e := tx.Select("id").First(&user, "id = ? AND status = ?", identity.UserID, "active").Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return &apiError{401, "native_session_expired", "请重新登录客户端"}
			}
			return e
		}
		var sess model.NativeSession
		if e := tx.First(&sess, "id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ? AND last_seen_at > ?", identity.ID, identity.UserID, now, now.Add(-nativeIdleTTL)).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return &apiError{401, "native_session_expired", "请重新登录客户端"}
			}
			return e
		}
		var subscription model.Subscription
		var sub *model.Subscription
		e := tx.Where("user_id = ?", identity.UserID).First(&subscription).Error
		if e == nil {
			sub = &subscription
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		out.Reason = s.nativeEntitlement(sub, now)
		if out.Reason == "" && sess.SubscriptionID != nil && !nativeAuthorizationBound(sess, sub) {
			out.Reason = "subscription_changed"
		}
		if out.Reason != "" {
			return nil
		}
		rows, e := s.catalogMetadata(tx, sub)
		if e != nil {
			return e
		}
		for _, node := range rows {
			out.Nodes = append(out.Nodes, nodeChoice{node.ID, node.Name, node.Region, node.LineType, node.RatePermille, node.Version})
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err == nil {
		data, _ := json.Marshal(struct {
			Reason string       `json:"reason"`
			Nodes  []nodeChoice `json:"nodes"`
		}{out.Reason, out.Nodes})
		out.Revision = nativeProfileVersion(data)
	}
	return out, err
}

func (s *Server) nativeNodes(c *gin.Context) {
	if !s.cfg.ClientNodeCatalog {
		fail(c, 503, "node_catalog_disabled", "节点目录未启用")
		return
	}
	revision := c.Query("revision")
	if revision != "" {
		decoded, err := hex.DecodeString(revision)
		if err != nil || len(decoded) != 32 {
			fail(c, 400, "invalid_catalog_revision", "目录版本无效")
			return
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	timedOut := false
	for {
		wake := s.catalogUpdates.listen()
		catalog, err := s.readNativeCatalog(c)
		if s.nativeFailure(c, err) {
			return
		}
		if timedOut || revision != catalog.Revision {
			c.JSON(200, catalog)
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-wake:
		case <-poll.C:
		case <-deadline.C:
			timedOut = true
		}
	}
}
