package api

import (
	"context"
	"log"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/loginlog"
	"vpn/backend/internal/model"
)

// Login records (handoff_payment_account.md 8). LastLoginAt/LastLoginIP are written in
// the login transaction, which already holds the user lock. The LoginEvent row is
// inserted after the session commits, with a short timeout: if it fails, the login
// still succeeds and only a log line remains. A refused attempt is recorded in the
// background so that an existing account costs the same time as an unknown address.

const (
	loginRecordTimeout = 2 * time.Second
	maxUserAgentBytes  = 200
)

// Failure reasons stored in LoginEvent.FailureReason.
const (
	loginBadPassword = "bad_password"
	loginDisabled    = "account_disabled"
	loginStale       = "credentials_changed"
)

// loginSource is what a record needs from the request, copied out because the gin
// context is recycled after the handler returns.
type loginSource struct {
	ip, userAgent string
}

func requestSource(c *gin.Context) loginSource {
	return loginSource{ip: c.ClientIP(), userAgent: clipUserAgent(c.GetHeader("User-Agent"))}
}

// clipUserAgent keeps at most 200 bytes without cutting a character in half.
func clipUserAgent(ua string) string {
	if len(ua) <= maxUserAgentBytes {
		return ua
	}
	cut := maxUserAgentBytes
	for cut > 0 && !utf8.RuneStart(ua[cut]) {
		cut--
	}
	return ua[:cut]
}

func (s *Server) loginEvent(src loginSource, userID, channel, platform, result, reason string) model.LoginEvent {
	ev := model.LoginEvent{ID: uuid.NewString(), UserID: userID, Channel: channel, Platform: platform, IP: src.ip, UserAgent: src.userAgent,
		Result: result, FailureReason: reason, OccurredAt: s.now(), GeoStatus: model.GeoPending}
	switch {
	case !loginlog.Valid(src.ip):
		ev.GeoStatus = model.GeoFailed
	case loginlog.Local(src.ip):
		ev.GeoStatus, ev.GeoText = model.GeoDone, model.GeoLocal
	case !s.cfg.Geo.Enabled():
		ev.GeoStatus = model.GeoSkipped
	}
	if ev.GeoStatus != model.GeoPending {
		resolved := ev.OccurredAt
		ev.GeoResolvedAt = &resolved
	}
	return ev
}

// recordLogin inserts a record now; used after a committed sign-in.
func (s *Server) recordLogin(ev model.LoginEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), loginRecordTimeout)
	defer cancel()
	if err := s.db.WithContext(ctx).Create(&ev).Error; err != nil {
		log.Print("login record not stored (sign-in unaffected)")
	}
}

// recordLoginAsync inserts a record without making the caller wait.
func (s *Server) recordLoginAsync(ev model.LoginEvent) { go s.recordLogin(ev) }

// stampLogin updates the account's last sign-in inside the login transaction. It does
// not touch updated_at: a sign-in is not an account change.
func (s *Server) stampLogin(tx *gorm.DB, userID string, src loginSource) error {
	return tx.Model(&model.User{}).Where("id = ?", userID).
		UpdateColumns(map[string]any{"last_login_at": s.now(), "last_login_ip": src.ip}).Error
}
