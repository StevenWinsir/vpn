package api

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Support tickets, the console side (handoff_ticket.md 7, 10). Every route sits behind
// requireConsoleAdmin; writes re-check the administrator under its row lock first (lock
// order: User(administrator) -> Ticket) and never lock the ticket owner's row. The audit
// keeps lengths only, never ticket text.

const (
	AuditTicketReply = "admin.ticket_reply"
	AuditTicketClose = "admin.ticket_close"
)

// consoleTicketItem is one row of the console queue.
type consoleTicketItem struct {
	ticketItem
	Platform     string     `json:"platform"`
	ClosedAt     *time.Time `json:"closed_at"`
	ClosedBy     string     `json:"closed_by"`
	WaitingSince *time.Time `json:"waiting_since"`
	UserID       string     `json:"user_id"`
	UserEmail    string     `json:"user_email"`
	UserStatus   string     `json:"user_status"`
}

type consoleTicketMessage struct {
	ticketMessageView
	AuthorID    string `json:"author_id"`
	AuthorEmail string `json:"author_email"`
}

type consoleTicketOwner struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	Status        string     `json:"status"`
	PlanName      string     `json:"plan_name"`
	PlanExpiresAt *time.Time `json:"plan_expires_at"`
}

type consoleTicketDetail struct {
	consoleTicketItem
	Version            int64                  `json:"version"`
	ClosedByAdminEmail string                 `json:"closed_by_admin_email"`
	User               consoleTicketOwner     `json:"user"`
	Order              *ticketOrder           `json:"order"`
	Messages           []consoleTicketMessage `json:"messages"`
}

// ticketRow is a ticket with its owner, as the console lists it.
type ticketRow struct {
	model.Ticket
	UserEmail  string
	UserStatus string
}

func (r ticketRow) item() consoleTicketItem {
	return consoleTicketItem{ticketItem: newTicketItem(r.Ticket), Platform: r.Platform, ClosedAt: utcPtr(r.ClosedAt), ClosedBy: r.ClosedBy, WaitingSince: utcPtr(r.WaitingSince),
		UserID: r.UserID, UserEmail: r.UserEmail, UserStatus: r.UserStatus}
}

func consoleTicketUnavailable(c *gin.Context, err error) bool { return consoleFailure(c, err) }

func (s *Server) consoleTicketRows(db *gorm.DB) *gorm.DB {
	return db.Table(s.cfg.Schema + ".tickets AS t").Joins("JOIN " + s.cfg.Schema + ".users AS u ON u.id = t.user_id").Select("t.*, u.email AS user_email, u.status AS user_status")
}

// likePrefix turns a keyword into a LIKE pattern that matches it literally at the start.
func likePrefix(keyword string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(keyword) + "%"
}

func (s *Server) consoleTicketList(c *gin.Context) {
	page, size, ok := consolePaging(c)
	if !ok {
		return
	}
	status, ok := enumParam(c, "status", model.TicketOpen, model.TicketAnswered, model.TicketClosed, "all")
	if !ok {
		return
	}
	category, ok := enumParam(c, "category", model.TicketCategories...)
	if !ok {
		return
	}
	keyword, ok := keywordParam(c, "keyword")
	if !ok {
		return
	}
	if status == "" {
		status = model.TicketOpen
	}
	now := s.now()
	filtered := func() *gorm.DB {
		q := s.consoleTicketRows(s.database(c)).Where("t.expires_at > ?", now)
		if status != "all" {
			q = q.Where("t.status = ?", status)
		}
		if category != "" {
			q = q.Where("t.category = ?", category)
		}
		if keyword != "" {
			q = q.Where("t.ticket_no LIKE ? OR u.email LIKE ? OR t.subject LIKE ?", likePrefix(strings.ToUpper(keyword)), likeContains(strings.ToLower(keyword)), likeContains(keyword))
		}
		return q
	}
	// The queue serves the longest wait first; everything else shows the latest activity.
	order := "t.last_message_at DESC, t.id"
	if status == model.TicketOpen {
		order = "t.waiting_since ASC, t.id"
	}
	var total int64
	var rows []ticketRow
	if filtered().Count(&total).Error != nil || filtered().Order(order).Limit(size).Offset((page-1)*size).Scan(&rows).Error != nil {
		consoleFailure(c, errors.New("ticket list"))
		return
	}
	items := make([]consoleTicketItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.item())
	}
	c.JSON(200, gin.H{"tickets": items, "total": total, "page": page, "page_size": size})
}

// consoleTicketSummary feeds the console badge.
func (s *Server) consoleTicketSummary(c *gin.Context) {
	now := s.now()
	var out struct {
		OpenCount          int64      `json:"open_count"`
		OldestWaitingSince *time.Time `json:"oldest_waiting_since"`
	}
	err := s.database(c).Model(&model.Ticket{}).Select("count(*) AS open_count, min(waiting_since) AS oldest_waiting_since").
		Where("status = ? AND expires_at > ?", model.TicketOpen, now).Scan(&out).Error
	if err != nil {
		consoleFailure(c, err)
		return
	}
	out.OldestWaitingSince = utcPtr(out.OldestWaitingSince)
	c.JSON(200, out)
}

// consoleTicket loads one live ticket with everything the console shows beside it.
func (s *Server) consoleTicket(db *gorm.DB, id string, now time.Time) (*consoleTicketDetail, error) {
	var rows []ticketRow
	if err := s.consoleTicketRows(db).Where("t.id = ? AND t.expires_at > ?", id, now).Limit(1).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errTicketNotFound
	}
	r := rows[0]
	out := &consoleTicketDetail{consoleTicketItem: r.item(), Version: r.Version, User: consoleTicketOwner{ID: r.UserID, Email: r.UserEmail, Status: r.UserStatus}, Messages: []consoleTicketMessage{}}
	sub, err := readSubscription(db, r.UserID, now)
	if err != nil {
		return nil, err
	}
	if sub != nil {
		expires := sub.ExpiresAt.UTC()
		out.User.PlanName, out.User.PlanExpiresAt = sub.PlanName, &expires
	}
	if r.OrderID != nil {
		var orders []ticketOrder
		if err = db.Model(&model.Order{}).Select("id, trade_no, plan_name, price_cents, status, created_at").Where("id = ? AND user_id = ?", *r.OrderID, r.UserID).Scan(&orders).Error; err != nil {
			return nil, err
		}
		if len(orders) == 1 {
			out.Order = &orders[0]
		}
	}
	var messages []model.TicketMessage
	if err = db.Where("ticket_id = ?", r.ID).Order("position").Find(&messages).Error; err != nil {
		return nil, err
	}
	staff := map[string]string{}
	ids := []string{}
	if r.ClosedByAdminID != nil {
		ids = append(ids, *r.ClosedByAdminID)
	}
	for _, m := range messages {
		if m.AuthorKind == model.TicketAuthorStaff {
			ids = append(ids, m.AuthorID)
		}
	}
	if len(ids) > 0 {
		var admins []model.User
		if err = db.Select("id", "email").Where("id IN ?", ids).Find(&admins).Error; err != nil {
			return nil, err
		}
		for _, a := range admins {
			staff[a.ID] = a.Email
		}
	}
	for _, m := range messages {
		view := consoleTicketMessage{ticketMessageView: ticketMessageView{ID: m.ID, Author: m.AuthorKind, Body: m.Body, CreatedAt: m.CreatedAt.UTC()}, AuthorID: m.AuthorID}
		if m.AuthorKind == model.TicketAuthorStaff {
			view.AuthorEmail = staff[m.AuthorID]
		}
		out.Messages = append(out.Messages, view)
	}
	if r.ClosedByAdminID != nil {
		out.ClosedByAdminEmail = staff[*r.ClosedByAdminID]
	}
	return out, nil
}

func (s *Server) consoleTicketDetail(c *gin.Context) {
	id, ok := ticketID(c)
	if !ok {
		return
	}
	detail, err := s.consoleTicket(s.database(c), id, s.now())
	if ticketFailure(c, err, consoleTicketUnavailable) {
		return
	}
	c.JSON(200, gin.H{"ticket": detail})
}

func (s *Server) answerConsoleTicket(c *gin.Context, status int, id string, now time.Time, replayed bool) {
	detail, err := s.consoleTicket(s.database(c), id, now)
	if ticketFailure(c, err, consoleTicketUnavailable) {
		return
	}
	c.JSON(status, gin.H{"ticket": detail, "replayed": replayed})
}

// ticketOwnerActive reports whether the ticket's owner can still be answered (an account
// being deleted can only have its tickets closed).
func ticketOwnerActive(tx *gorm.DB, userID string) (bool, error) {
	var n int64
	err := tx.Model(&model.User{}).Where("id = ? AND status = ?", userID, "active").Count(&n).Error
	return n == 1, err
}

func (s *Server) consoleTicketReply(c *gin.Context) {
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var in ticketMessageInput
	if !decode(c, &in) {
		return
	}
	key, ok := ticketKey(c)
	if !ok {
		return
	}
	body, ok := ticketText(in.Body, maxTicketBody, false)
	if !ok {
		ticketFailure(c, invalidTicket("body", "回复须为 1–3000 个字的纯文本"), consoleTicketUnavailable)
		return
	}
	actor, now := currentUser(c).ID, s.now()
	added := false
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockConsoleAdmin(tx, c); e != nil {
			return e
		}
		t, e := lockTicket(tx, id, "", now)
		if e != nil {
			return e
		}
		// A replay is recognised first (appendMessage answers it); only a new reply needs
		// an owner who can still be answered.
		var replays int64
		if e = tx.Model(&model.TicketMessage{}).Where("ticket_id = ? AND idempotency_key = ?", t.ID, key).Count(&replays).Error; e != nil {
			return e
		}
		if replays == 0 && t.Status != model.TicketClosed {
			active, e := ticketOwnerActive(tx, t.UserID)
			if e != nil {
				return e
			}
			if !active {
				return &apiError{409, "user_unavailable", "该账号正在注销，只能关闭工单"}
			}
		}
		if added, e = appendMessage(tx, t, model.TicketAuthorStaff, actor, body, key, now); e != nil || !added {
			return e
		}
		version := t.Version + 1
		if e = tx.Model(t).Updates(map[string]any{"status": model.TicketAnswered, "user_unread": true, "last_message_at": now, "message_count": t.MessageCount + 1, "version": version, "updated_at": now}).Error; e != nil {
			return e
		}
		return consoleAudit(tx, actor, AuditTicketReply, t.ID, version, gin.H{"length": len([]rune(body))}, now)
	})
	if ticketFailure(c, err, consoleTicketUnavailable) {
		return
	}
	status := 200
	if added {
		status = 201
	}
	s.answerConsoleTicket(c, status, id, now, !added)
}

type consoleTicketCloseInput struct {
	Version int64  `json:"version"`
	Note    string `json:"note"`
}

// consoleTicketClose closes a ticket, with or without a closing note (decision D9). The
// version the administrator saw must still be current, so a message the user sent
// meanwhile is never closed away unseen (D10).
func (s *Server) consoleTicketClose(c *gin.Context) {
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var in consoleTicketCloseInput
	if !decode(c, &in) {
		return
	}
	note := ""
	if strings.TrimSpace(in.Note) != "" {
		if note, ok = ticketText(in.Note, maxTicketNote, false); !ok {
			ticketFailure(c, invalidTicket("note", "关闭说明须为 1–500 个字的纯文本"), consoleTicketUnavailable)
			return
		}
	}
	actor, now := currentUser(c).ID, s.now()
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := lockConsoleAdmin(tx, c); e != nil {
			return e
		}
		t, e := lockTicket(tx, id, "", now)
		if e != nil {
			return e
		}
		if t.Status == model.TicketClosed {
			return errTicketClosed
		}
		if in.Version != t.Version {
			return &apiError{409, "ticket_changed", "用户有新回复，请先查看后再关闭"}
		}
		version := t.Version + 1
		updates := map[string]any{"status": model.TicketClosed, "closed_at": now, "closed_by": model.TicketAuthorStaff, "closed_by_admin_id": actor, "user_unread": true, "version": version, "updated_at": now}
		if note != "" {
			if t.MessageCount >= model.MaxTicketMessages {
				return errTicketFull
			}
			if e = tx.Omit(clause.Associations).Create(&model.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorKind: model.TicketAuthorStaff, AuthorID: actor, Body: note, Position: t.MessageCount + 1, CreatedAt: now}).Error; e != nil {
				return e
			}
			updates["message_count"], updates["last_message_at"] = t.MessageCount+1, now
		}
		if e = tx.Model(t).Updates(updates).Error; e != nil {
			return e
		}
		return consoleAudit(tx, actor, AuditTicketClose, t.ID, version, gin.H{"with_note": note != "", "note_length": len([]rune(note))}, now)
	})
	if ticketFailure(c, err, consoleTicketUnavailable) {
		return
	}
	s.answerConsoleTicket(c, 200, id, now, false)
}
