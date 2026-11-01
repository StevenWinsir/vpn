package api

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

// Support tickets, the user side (handoff_ticket.md 6, 10). A user only ever reaches their
// own, unexpired tickets: anything else is 404, the same answer as a ticket that does not
// exist. Replies are shown as coming from "staff"; no administrator identity is returned.
// Ticket text never goes to a log or an audit record (decision D14).

const (
	// ticketBodyLimit bounds a request: 3000 characters are up to 12 KB of UTF-8.
	ticketBodyLimit = 32 << 10

	maxTicketSubject   = 60
	maxTicketBody      = 3000
	maxTicketNote      = 500
	maxOpenTickets     = 3
	ticketPageSize     = 20
	ticketOrderOptions = 20

	scopeTicketCreate  = "ticket_create"
	ticketCreateWindow = 24 * time.Hour
	ticketCreateLimit  = 5
	scopeTicketMessage = "ticket_message"
	ticketMessageWin   = time.Hour
	ticketMessageLimit = 30

	ticketNumberAttempts = 5
)

var (
	errTicketNotFound    = &apiError{404, "ticket_not_found", "工单不存在或已超过保留期限"}
	errTicketClosed      = &apiError{409, "ticket_closed", "工单已关闭，如有新问题请重新提交"}
	errTicketFull        = &apiError{409, "ticket_message_limit", "该工单的消息已达上限，请关闭后重新提交"}
	errTicketIdempotency = &apiError{409, "idempotency_conflict", "此请求标识已用于其他内容"}
	errTicketsDisabled   = &apiError{403, "tickets_disabled", "工单服务暂停受理，请稍后再试"}
	errTicketUnavailable = &apiError{503, "tickets_unavailable", "工单服务暂时不可用，请稍后重试"}
)

// ticketItem is one row of the user's list.
type ticketItem struct {
	ID            string    `json:"id"`
	TicketNo      string    `json:"ticket_no"`
	Category      string    `json:"category"`
	Subject       string    `json:"subject"`
	Status        string    `json:"status"`
	UserUnread    bool      `json:"user_unread"`
	MessageCount  int       `json:"message_count"`
	CreatedAt     time.Time `json:"created_at"`
	LastMessageAt time.Time `json:"last_message_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// ticketOrder is the summary of an order linked to a ticket (or offered for linking).
type ticketOrder struct {
	ID         string    `json:"id"`
	TradeNo    *string   `json:"trade_no"`
	PlanName   string    `json:"plan_name"`
	PriceCents int64     `json:"price_cents"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type ticketMessageView struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type ticketDetail struct {
	ticketItem
	Platform string              `json:"platform"`
	ClosedAt *time.Time          `json:"closed_at"`
	ClosedBy string              `json:"closed_by"`
	Order    *ticketOrder        `json:"order"`
	Messages []ticketMessageView `json:"messages"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

func newTicketItem(t model.Ticket) ticketItem {
	return ticketItem{ID: t.ID, TicketNo: t.TicketNo, Category: t.Category, Subject: t.Subject, Status: t.Status, UserUnread: t.UserUnread,
		MessageCount: t.MessageCount, CreatedAt: t.CreatedAt.UTC(), LastMessageAt: t.LastMessageAt.UTC(), ExpiresAt: t.ExpiresAt.UTC()}
}

// ticketFailure answers a ticket error: business errors as they are (with their extra
// fields), anything else as the given unavailable answer.
func ticketFailure(c *gin.Context, err error, unavailable func(*gin.Context, error) bool) bool {
	if err == nil {
		return false
	}
	var detailed *detailedError
	if errors.As(err, &detailed) {
		body := gin.H{"code": detailed.Code, "message": detailed.Message}
		for k, v := range detailed.Extra {
			body[k] = v
		}
		c.AbortWithStatusJSON(detailed.Status, gin.H{"error": body})
		return true
	}
	var business *apiError
	if errors.As(err, &business) {
		fail(c, business.Status, business.Code, business.Message)
		return true
	}
	return unavailable(c, err)
}

func userTicketUnavailable(c *gin.Context, _ error) bool {
	fail(c, errTicketUnavailable.Status, errTicketUnavailable.Code, errTicketUnavailable.Message)
	return true
}

func invalidTicket(field, message string) error {
	return &detailedError{apiError{400, "invalid_ticket", message}, gin.H{"field": field}}
}

// ticketText trims and checks one piece of ticket text (handoff_ticket.md 4.3): 1..max
// characters of plain text; a single line when asked.
func ticketText(raw string, max int, singleLine bool) (string, bool) {
	text := strings.TrimSpace(raw)
	n := len([]rune(text))
	if n == 0 || n > max || !cleanText(text) || singleLine && strings.ContainsAny(text, "\r\n") {
		return "", false
	}
	return text, true
}

func oneOf(value string, allowed []string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

func ticketKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if _, err := uuid.Parse(key); err != nil || len(key) != 36 {
		fail(c, 400, "idempotency_required", "请提供 UUID 格式的 Idempotency-Key")
		return "", false
	}
	return strings.ToLower(key), true
}

func ticketID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
		fail(c, errTicketNotFound.Status, errTicketNotFound.Code, errTicketNotFound.Message)
		return "", false
	}
	return strings.ToLower(id), true
}

// randomTicketNo draws "T" + the UTC+8 date (yymmdd) + six random digits (decision D12).
func randomTicketNo(now time.Time) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("T%s%06d", now.In(consoleLocation).Format("060102"), n.Int64()), nil
}

// newTicketNo draws a free ticket number; a taken one is redrawn before the insert so the
// transaction never aborts on the unique index.
func (s *Server) newTicketNo(tx *gorm.DB, now time.Time) (string, error) {
	draw := s.ticketNumbers
	if draw == nil {
		draw = randomTicketNo
	}
	for range ticketNumberAttempts {
		candidate, err := draw(now)
		if err != nil {
			return "", err
		}
		var taken int64
		if err = tx.Model(&model.Ticket{}).Where("ticket_no = ?", candidate).Count(&taken).Error; err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
	}
	return "", errTicketUnavailable
}

// lockTicket reads a live ticket under its row lock; owner limits it to one user ("" for
// the console). A missing or expired ticket is errTicketNotFound.
func lockTicket(tx *gorm.DB, id, owner string, now time.Time) (*model.Ticket, error) {
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND expires_at > ?", id, now)
	if owner != "" {
		q = q.Where("user_id = ?", owner)
	}
	var t model.Ticket
	err := q.Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errTicketNotFound
	}
	return &t, err
}

// limited checks a per-user window before any work; the action is counted only once it
// succeeded, so a request refused for its content never uses up the allowance.
func (s *Server) limited(c *gin.Context, scope, userID string, window time.Duration, max int, now time.Time) bool {
	count, until, err := s.peekLimit(s.database(c), scope, userID, window, now)
	if err != nil {
		userTicketUnavailable(c, err)
		return true
	}
	if count >= max {
		retryAfter(c, until, now)
		fail(c, 429, "ticket_rate_limited", "操作太频繁，请稍后再试")
		return true
	}
	return false
}

func (s *Server) countTicketAction(c *gin.Context, scope, userID string, window time.Duration, now time.Time) {
	if _, _, err := s.bumpLimit(s.database(c), scope, userID, window, now); err != nil {
		log.Print("ticket rate count failed (the action itself succeeded)")
	}
}

// userTicket loads one of the user's live tickets with its conversation.
func (s *Server) userTicket(db *gorm.DB, userID, id string, now time.Time) (*ticketDetail, error) {
	var t model.Ticket
	err := db.Where("id = ? AND user_id = ? AND expires_at > ?", id, userID, now).Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errTicketNotFound
	}
	if err != nil {
		return nil, err
	}
	out := &ticketDetail{ticketItem: newTicketItem(t), Platform: t.Platform, ClosedAt: utcPtr(t.ClosedAt), ClosedBy: t.ClosedBy, Messages: []ticketMessageView{}}
	if t.OrderID != nil {
		var orders []ticketOrder
		if err = db.Model(&model.Order{}).Select("id, trade_no, plan_name, price_cents, status, created_at").Where("id = ? AND user_id = ?", *t.OrderID, userID).Scan(&orders).Error; err != nil {
			return nil, err
		}
		if len(orders) == 1 {
			out.Order = &orders[0]
		}
	}
	var messages []model.TicketMessage
	if err = db.Where("ticket_id = ?", t.ID).Order("position").Find(&messages).Error; err != nil {
		return nil, err
	}
	for _, m := range messages {
		out.Messages = append(out.Messages, ticketMessageView{ID: m.ID, Author: m.AuthorKind, Body: m.Body, CreatedAt: m.CreatedAt.UTC()})
	}
	return out, nil
}

// openTickets counts the user's live tickets that are not closed.
func openTickets(db *gorm.DB, userID string, now time.Time) (int64, error) {
	var n int64
	err := db.Model(&model.Ticket{}).Where("user_id = ? AND status <> ? AND expires_at > ?", userID, model.TicketClosed, now).Count(&n).Error
	return n, err
}

func (s *Server) myTickets(c *gin.Context) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 || page > 10000 {
		fail(c, 400, "invalid_page", "页码超出范围")
		return
	}
	filter, ok := enumParam(c, "status", "active", "closed")
	if !ok {
		return
	}
	user, now := currentUser(c), s.now()
	db := s.database(c)
	q := db.Model(&model.Ticket{}).Where("user_id = ? AND expires_at > ?", user.ID, now)
	order := "last_message_at DESC, id"
	switch filter {
	case "active":
		q = q.Where("status <> ?", model.TicketClosed)
	case "closed":
		q = q.Where("status = ?", model.TicketClosed)
		order = "closed_at DESC, id"
	}
	var total, open int64
	var rows []model.Ticket
	options := []ticketOrder{}
	if q.Count(&total).Error != nil || q.Order(order).Limit(ticketPageSize).Offset((page-1)*ticketPageSize).Find(&rows).Error != nil {
		userTicketUnavailable(c, nil)
		return
	}
	if open, err = openTickets(db, user.ID, now); err != nil ||
		db.Model(&model.Order{}).Select("id, trade_no, plan_name, price_cents, status, created_at").Where("user_id = ?", user.ID).Order("created_at DESC, id").Limit(ticketOrderOptions).Scan(&options).Error != nil {
		userTicketUnavailable(c, nil)
		return
	}
	items := make([]ticketItem, 0, len(rows))
	for _, t := range rows {
		items = append(items, newTicketItem(t))
	}
	c.JSON(200, gin.H{"tickets": items, "total": total, "page": page, "page_size": ticketPageSize, "open_count": open, "max_open": maxOpenTickets, "order_options": options})
}

// myTicketSummary feeds the sidebar badge; it answers even with tickets switched off.
func (s *Server) myTicketSummary(c *gin.Context) {
	user, now := currentUser(c), s.now()
	db := s.database(c)
	var unread int64
	open, err := openTickets(db, user.ID, now)
	if err != nil || db.Model(&model.Ticket{}).Where("user_id = ? AND user_unread AND expires_at > ?", user.ID, now).Count(&unread).Error != nil {
		userTicketUnavailable(c, nil)
		return
	}
	c.JSON(200, gin.H{"unread_count": unread, "open_count": open, "max_open": maxOpenTickets})
}

func (s *Server) myTicket(c *gin.Context) {
	id, ok := ticketID(c)
	if !ok {
		return
	}
	detail, err := s.userTicket(s.database(c), currentUser(c).ID, id, s.now())
	if ticketFailure(c, err, userTicketUnavailable) {
		return
	}
	c.JSON(200, gin.H{"ticket": detail})
}

// submitGate refuses users' writes while tickets are off and administrators' submissions
// (decision D11). Impersonation sessions never get here: the middleware refuses them.
func (s *Server) submitGate(c *gin.Context) bool {
	if !s.cfg.TicketsEnabled() {
		fail(c, errTicketsDisabled.Status, errTicketsDisabled.Code, errTicketsDisabled.Message)
		return false
	}
	if currentUser(c).Role != "user" {
		fail(c, 403, "admin_cannot_submit", "管理员账号请在管理后台处理工单")
		return false
	}
	return true
}

type ticketInput struct {
	Category string  `json:"category"`
	Subject  string  `json:"subject"`
	Body     string  `json:"body"`
	Platform string  `json:"platform"`
	OrderID  *string `json:"order_id"`
}

func (in *ticketInput) validate() error {
	var ok bool
	if !oneOf(in.Category, model.TicketCategories) {
		return invalidTicket("category", "请选择问题分类")
	}
	if in.Subject, ok = ticketText(in.Subject, maxTicketSubject, true); !ok {
		return invalidTicket("subject", "标题须为 1–60 个字的单行文字")
	}
	if in.Body, ok = ticketText(in.Body, maxTicketBody, false); !ok {
		return invalidTicket("body", "问题描述须为 1–3000 个字的纯文本")
	}
	if in.Platform != "" && !oneOf(in.Platform, model.TicketPlatforms) {
		return invalidTicket("platform", "请选择有效的设备")
	}
	if in.OrderID != nil && *in.OrderID == "" {
		in.OrderID = nil
	}
	if in.OrderID != nil {
		if _, err := uuid.Parse(*in.OrderID); err != nil || len(*in.OrderID) != 36 {
			return &apiError{400, "invalid_order", "请选择自己的订单"}
		}
		lower := strings.ToLower(*in.OrderID)
		in.OrderID = &lower
	}
	return nil
}

func sameOrder(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func (s *Server) createTicket(c *gin.Context) {
	if !s.submitGate(c) {
		return
	}
	var in ticketInput
	if !decode(c, &in) {
		return
	}
	key, ok := ticketKey(c)
	if !ok {
		return
	}
	if ticketFailure(c, in.validate(), userTicketUnavailable) {
		return
	}
	user, now := currentUser(c), s.now()
	if s.limited(c, scopeTicketCreate, user.ID, ticketCreateWindow, ticketCreateLimit, now) {
		return
	}
	var id string
	replayed := false
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		// Lock order: User -> Ticket. The user lock makes the open-ticket cap hold under
		// concurrent submissions and serialises replays of one key.
		locked, err := lockUserRow(tx, user.ID)
		if err != nil {
			return err
		}
		if locked == nil || locked.Status != "active" {
			return &apiError{403, "account_disabled", "账号已停用"}
		}
		var existing model.Ticket
		err = tx.Where("user_id = ? AND idempotency_key = ?", user.ID, key).Take(&existing).Error
		if err == nil {
			var first model.TicketMessage
			if e := tx.Where("ticket_id = ? AND position = 1", existing.ID).Take(&first).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			if existing.Category != in.Category || existing.Subject != in.Subject || existing.Platform != in.Platform || first.Body != in.Body || !sameOrder(existing.OrderID, in.OrderID) {
				return errTicketIdempotency
			}
			if !existing.ExpiresAt.After(now) {
				return errTicketNotFound
			}
			id, replayed = existing.ID, true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		open, err := openTickets(tx, user.ID, now)
		if err != nil {
			return err
		}
		if open >= maxOpenTickets {
			return &apiError{409, "too_many_open_tickets", "处理中的工单最多 3 张，请等待处理完毕或关闭已解决的工单"}
		}
		if in.OrderID != nil {
			var owned int64
			if err = tx.Model(&model.Order{}).Where("id = ? AND user_id = ?", *in.OrderID, user.ID).Count(&owned).Error; err != nil {
				return err
			}
			if owned == 0 {
				return &apiError{400, "invalid_order", "请选择自己的订单"}
			}
		}
		number, err := s.newTicketNo(tx, now)
		if err != nil {
			return err
		}
		t := model.Ticket{ID: uuid.NewString(), TicketNo: number, UserID: user.ID, IdempotencyKey: key, Category: in.Category, Platform: in.Platform, OrderID: in.OrderID,
			Subject: in.Subject, Status: model.TicketOpen, MessageCount: 1, WaitingSince: &now, LastMessageAt: now, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(model.TicketRetention)}
		if err = tx.Omit(clause.Associations).Create(&t).Error; err != nil {
			return err
		}
		id = t.ID
		return tx.Omit(clause.Associations).Create(&model.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorKind: model.TicketAuthorUser, AuthorID: user.ID, Body: in.Body, Position: 1, CreatedAt: now}).Error
	})
	if ticketFailure(c, err, userTicketUnavailable) {
		return
	}
	status := 200
	if !replayed {
		status = 201
		s.countTicketAction(c, scopeTicketCreate, user.ID, ticketCreateWindow, now)
	}
	s.answerUserTicket(c, status, user.ID, id, now, gin.H{"replayed": replayed})
}

// answerUserTicket reads the ticket back after a write and answers with it.
func (s *Server) answerUserTicket(c *gin.Context, status int, userID, id string, now time.Time, extra gin.H) {
	detail, err := s.userTicket(s.database(c), userID, id, now)
	if ticketFailure(c, err, userTicketUnavailable) {
		return
	}
	out := gin.H{"ticket": detail}
	for k, v := range extra {
		out[k] = v
	}
	c.JSON(status, out)
}

type ticketMessageInput struct {
	Body string `json:"body"`
}

// appendMessage adds one message to a locked ticket, replaying an earlier request with the
// same key. It reports whether the message was new.
func appendMessage(tx *gorm.DB, t *model.Ticket, kind, authorID, body, key string, now time.Time) (bool, error) {
	var earlier model.TicketMessage
	err := tx.Where("ticket_id = ? AND idempotency_key = ?", t.ID, key).Take(&earlier).Error
	if err == nil {
		if earlier.AuthorKind != kind || earlier.AuthorID != authorID || earlier.Body != body {
			return false, errTicketIdempotency
		}
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if t.Status == model.TicketClosed {
		return false, errTicketClosed
	}
	if t.MessageCount >= model.MaxTicketMessages {
		return false, errTicketFull
	}
	return true, tx.Omit(clause.Associations).Create(&model.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorKind: kind, AuthorID: authorID, Body: body, IdempotencyKey: &key, Position: t.MessageCount + 1, CreatedAt: now}).Error
}

func (s *Server) replyTicket(c *gin.Context) {
	if !s.submitGate(c) {
		return
	}
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
		ticketFailure(c, invalidTicket("body", "回复须为 1–3000 个字的纯文本"), userTicketUnavailable)
		return
	}
	user, now := currentUser(c), s.now()
	if s.limited(c, scopeTicketMessage, user.ID, ticketMessageWin, ticketMessageLimit, now) {
		return
	}
	added := false
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if _, err := lockUserRow(tx, user.ID); err != nil {
			return err
		}
		t, err := lockTicket(tx, id, user.ID, now)
		if err != nil {
			return err
		}
		if added, err = appendMessage(tx, t, model.TicketAuthorUser, user.ID, body, key, now); err != nil || !added {
			return err
		}
		updates := map[string]any{"status": model.TicketOpen, "last_message_at": now, "message_count": t.MessageCount + 1, "version": t.Version + 1, "updated_at": now}
		if t.Status != model.TicketOpen {
			updates["waiting_since"] = now
		}
		return tx.Model(t).Updates(updates).Error
	})
	if ticketFailure(c, err, userTicketUnavailable) {
		return
	}
	status := 200
	if added {
		status = 201
		s.countTicketAction(c, scopeTicketMessage, user.ID, ticketMessageWin, now)
	}
	s.answerUserTicket(c, status, user.ID, id, now, gin.H{"replayed": !added})
}

// closeMyTicket closes the user's own ticket; closing a closed one changes nothing.
func (s *Server) closeMyTicket(c *gin.Context) {
	if !s.submitGate(c) {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	user, now := currentUser(c), s.now()
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if _, err := lockUserRow(tx, user.ID); err != nil {
			return err
		}
		t, err := lockTicket(tx, id, user.ID, now)
		if err != nil || t.Status == model.TicketClosed {
			return err
		}
		return tx.Model(t).Updates(map[string]any{"status": model.TicketClosed, "closed_at": now, "closed_by": model.TicketAuthorUser, "version": t.Version + 1, "updated_at": now}).Error
	})
	if ticketFailure(c, err, userTicketUnavailable) {
		return
	}
	s.answerUserTicket(c, 200, user.ID, id, now, nil)
}

// readMyTicket clears the unread mark. It moves no version: reading is not a change an
// administrator's close must notice. Impersonation sessions are refused by the middleware.
func (s *Server) readMyTicket(c *gin.Context) {
	id, ok := ticketID(c)
	if !ok {
		return
	}
	user, now := currentUser(c), s.now()
	result := s.database(c).Model(&model.Ticket{}).Where("id = ? AND user_id = ? AND expires_at > ?", id, user.ID, now).Update("user_unread", false)
	if result.Error != nil {
		userTicketUnavailable(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		fail(c, errTicketNotFound.Status, errTicketNotFound.Code, errTicketNotFound.Message)
		return
	}
	c.JSON(200, gin.H{"read": true})
}
