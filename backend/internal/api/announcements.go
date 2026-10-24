package api

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Announcements (handoff_payment_account.md 9.6). Users must acknowledge each published
// announcement once; the acknowledgement is a row keyed by (announcement, user), so a
// repeat is a no-op. An impersonation session sees none and cannot acknowledge any.

const (
	// announcementBodyLimit bounds a request: 5000 characters are up to 20 KB of UTF-8.
	announcementBodyLimit = 32 << 10
	maxAnnouncementTitle  = 100
	maxAnnouncementBody   = 5000
	pendingAnnouncements  = 20

	AuditAnnouncementCreate  = "admin.announcement_create"
	AuditAnnouncementUpdate  = "admin.announcement_update"
	AuditAnnouncementPublish = "admin.announcement_publish"
	AuditAnnouncementArchive = "admin.announcement_archive"
	AuditAnnouncementDelete  = "admin.announcement_delete"
)

type announcementView struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	PublishedAt *time.Time `json:"published_at"`
}

// myAnnouncements lists what this user still has to acknowledge, oldest first.
func (s *Server) myAnnouncements(c *gin.Context) {
	out := []announcementView{}
	if impersonationOf(c) != nil {
		c.JSON(200, gin.H{"announcements": out})
		return
	}
	err := s.database(c).Table(s.cfg.Schema+".announcements AS a").Select("a.id, a.title, a.body, a.published_at").
		Where("a.status = ?", model.AnnouncementPublished).
		Where("NOT EXISTS (SELECT 1 FROM "+s.cfg.Schema+".announcement_reads AS r WHERE r.announcement_id = a.id AND r.user_id = ?)", currentUser(c).ID).
		Order("a.published_at ASC, a.id ASC").Limit(pendingAnnouncements).Scan(&out).Error
	if err != nil {
		fail(c, 503, "announcements_unavailable", "公告暂时不可用")
		return
	}
	c.JSON(200, gin.H{"announcements": out})
}

// ackAnnouncement records the acknowledgement; repeating it is harmless.
func (s *Server) ackAnnouncement(c *gin.Context) {
	if refuseImpersonated(c) {
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "announcement_not_found", "公告不存在")
		return
	}
	user, now := currentUser(c), s.now()
	var a model.Announcement
	err := s.database(c).Select("id", "status").First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && a.Status == model.AnnouncementDraft {
		fail(c, 404, "announcement_not_found", "公告不存在")
		return
	}
	if err != nil {
		fail(c, 503, "announcements_unavailable", "公告暂时不可用")
		return
	}
	// An announcement taken offline while its dialog was open needs no record.
	if a.Status == model.AnnouncementPublished {
		err = s.database(c).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.AnnouncementRead{AnnouncementID: id, UserID: user.ID, AcknowledgedAt: now}).Error
		if err != nil {
			fail(c, 503, "announcements_unavailable", "公告暂时不可用")
			return
		}
	}
	c.JSON(200, gin.H{"acknowledged": true})
}

// cleanText accepts plain text: valid UTF-8 without control characters other than
// line breaks and tabs. HTML and Markdown are not interpreted anywhere, only stored.
func cleanText(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

type announcementInput struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Version int64  `json:"version"`
}

func validateAnnouncement(in *announcementInput) error {
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	bad := func(message string) error { return &apiError{400, "invalid_announcement", message} }
	switch {
	case in.Title == "" || len([]rune(in.Title)) > maxAnnouncementTitle:
		return bad("标题须为 1–100 个字符")
	case in.Body == "" || len([]rune(in.Body)) > maxAnnouncementBody:
		return bad("正文须为 1–5000 个字符")
	case !cleanText(in.Title) || strings.ContainsAny(in.Title, "\r\n") || !cleanText(in.Body):
		return bad("标题和正文只能是纯文本")
	}
	return nil
}

type consoleAnnouncement struct {
	model.Announcement
	ReadCount int64 `json:"read_count"`
}

func (s *Server) consoleAnnouncementList(c *gin.Context) {
	page, size, ok := consolePaging(c)
	if !ok {
		return
	}
	db := s.database(c)
	var total, audience int64
	rows := []model.Announcement{}
	if db.Model(&model.Announcement{}).Count(&total).Error != nil ||
		db.Order("created_at DESC, id").Limit(size).Offset((page-1)*size).Find(&rows).Error != nil ||
		db.Model(&model.User{}).Where("status = ?", "active").Count(&audience).Error != nil {
		fail(c, 503, "console_unavailable", "公告列表暂时不可用")
		return
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts := map[string]int64{}
	if len(ids) > 0 {
		var grouped []struct {
			AnnouncementID string
			N              int64
		}
		if db.Model(&model.AnnouncementRead{}).Where("announcement_id IN ?", ids).Select("announcement_id, count(*) AS n").Group("announcement_id").Scan(&grouped).Error != nil {
			fail(c, 503, "console_unavailable", "公告列表暂时不可用")
			return
		}
		for _, g := range grouped {
			counts[g.AnnouncementID] = g.N
		}
	}
	out := make([]consoleAnnouncement, len(rows))
	for i, r := range rows {
		out[i] = consoleAnnouncement{Announcement: r, ReadCount: counts[r.ID]}
	}
	c.JSON(200, gin.H{"announcements": out, "total": total, "page": page, "page_size": size, "audience": audience})
}

// lockAnnouncement reads the row under its lock (after the administrator's).
func lockAnnouncement(tx *gorm.DB, id string) (*model.Announcement, error) {
	var a model.Announcement
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &apiError{404, "announcement_not_found", "公告不存在"}
	}
	return &a, err
}

func announcementID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "announcement_not_found", "公告不存在")
		return "", false
	}
	return id, true
}

// consoleAnnouncementSave creates a draft, or edits one (published text is locked).
func (s *Server) consoleAnnouncementSave(c *gin.Context) {
	var in announcementInput
	if !decode(c, &in) {
		return
	}
	id := c.Param("id")
	creating := id == ""
	if !creating {
		var ok bool
		if id, ok = announcementID(c); !ok {
			return
		}
	}
	now, actor := s.now(), currentUser(c).ID
	var saved model.Announcement
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockConsoleAdmin(tx, c); e != nil {
			return e
		}
		if e := validateAnnouncement(&in); e != nil {
			return e
		}
		if creating {
			saved = model.Announcement{ID: uuid.NewString(), Title: in.Title, Body: in.Body, Status: model.AnnouncementDraft, CreatedBy: actor, Version: 1, CreatedAt: now, UpdatedAt: now}
			if e := tx.Create(&saved).Error; e != nil {
				return e
			}
			return consoleAudit(tx, actor, AuditAnnouncementCreate, saved.ID, 1, gin.H{"title_length": len([]rune(saved.Title))}, now)
		}
		a, e := lockAnnouncement(tx, id)
		if e != nil {
			return e
		}
		if a.Status != model.AnnouncementDraft {
			return &apiError{409, "announcement_locked", "已发布的公告不能修改，请下线或复制为新草稿"}
		}
		if in.Version != a.Version {
			return &apiError{409, "version_conflict", "公告已被其他管理员修改，请刷新后重试"}
		}
		a.Title, a.Body, a.Version, a.UpdatedAt = in.Title, in.Body, a.Version+1, now
		if e = tx.Save(a).Error; e != nil {
			return e
		}
		saved = *a
		return consoleAudit(tx, actor, AuditAnnouncementUpdate, a.ID, a.Version, gin.H{"title_length": len([]rune(a.Title))}, now)
	})
	if paymentFailure(c, err) {
		return
	}
	status := 200
	if creating {
		status = 201
	}
	c.JSON(status, gin.H{"announcement": consoleAnnouncement{Announcement: saved}})
}

// transition moves a locked announcement between statuses.
func (s *Server) announcementTransition(c *gin.Context, from, to, action string) {
	id, ok := announcementID(c)
	if !ok {
		return
	}
	now, actor := s.now(), currentUser(c).ID
	var saved model.Announcement
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockConsoleAdmin(tx, c); e != nil {
			return e
		}
		a, e := lockAnnouncement(tx, id)
		if e != nil {
			return e
		}
		if a.Status != from {
			return &apiError{409, "announcement_state", "公告当前状态不允许此操作，请刷新后查看"}
		}
		updates := map[string]any{"status": to, "version": a.Version + 1, "updated_at": now}
		if to == model.AnnouncementPublished {
			updates["published_at"] = now
		}
		if e = tx.Model(a).Updates(updates).Error; e != nil {
			return e
		}
		if e = tx.First(&saved, "id = ?", id).Error; e != nil {
			return e
		}
		return consoleAudit(tx, actor, action, a.ID, saved.Version, gin.H{"to": to}, now)
	})
	if paymentFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"announcement": consoleAnnouncement{Announcement: saved}})
}

func (s *Server) consoleAnnouncementPublish(c *gin.Context) {
	s.announcementTransition(c, model.AnnouncementDraft, model.AnnouncementPublished, AuditAnnouncementPublish)
}

func (s *Server) consoleAnnouncementArchive(c *gin.Context) {
	s.announcementTransition(c, model.AnnouncementPublished, model.AnnouncementArchived, AuditAnnouncementArchive)
}

// consoleAnnouncementDelete removes a draft; published announcements are only archived.
func (s *Server) consoleAnnouncementDelete(c *gin.Context) {
	id, ok := announcementID(c)
	if !ok {
		return
	}
	now, actor := s.now(), currentUser(c).ID
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockConsoleAdmin(tx, c); e != nil {
			return e
		}
		a, e := lockAnnouncement(tx, id)
		if e != nil {
			return e
		}
		if a.Status != model.AnnouncementDraft {
			return &apiError{409, "announcement_state", "只有草稿可以删除，已发布的公告请下线"}
		}
		if e = tx.Delete(a).Error; e != nil {
			return e
		}
		return consoleAudit(tx, actor, AuditAnnouncementDelete, a.ID, a.Version, gin.H{}, now)
	})
	if paymentFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"deleted": true})
}

// consoleAnnouncementReads lists who acknowledged an announcement, newest first.
func (s *Server) consoleAnnouncementReads(c *gin.Context) {
	id, ok := announcementID(c)
	if !ok {
		return
	}
	page, size, ok := consolePaging(c)
	if !ok {
		return
	}
	var found int64
	if s.database(c).Model(&model.Announcement{}).Where("id = ?", id).Count(&found).Error != nil {
		fail(c, 503, "console_unavailable", "确认记录暂时不可用")
		return
	}
	if found == 0 {
		fail(c, 404, "announcement_not_found", "公告不存在")
		return
	}
	schema := s.cfg.Schema
	rows := []struct {
		UserID         string    `json:"user_id"`
		Email          string    `json:"email"`
		AcknowledgedAt time.Time `json:"acknowledged_at"`
	}{}
	filtered := func() *gorm.DB {
		return s.database(c).Table(schema+".announcement_reads AS r").Joins("JOIN "+schema+".users AS u ON u.id = r.user_id").Where("r.announcement_id = ?", id)
	}
	var total int64
	if filtered().Select("count(*)").Scan(&total).Error != nil ||
		filtered().Select("r.user_id, u.email, r.acknowledged_at").Order("r.acknowledged_at DESC, r.user_id").Limit(size).Offset((page-1)*size).Scan(&rows).Error != nil {
		fail(c, 503, "console_unavailable", "确认记录暂时不可用")
		return
	}
	c.JSON(200, gin.H{"reads": rows, "total": total, "page": page, "page_size": size})
}
