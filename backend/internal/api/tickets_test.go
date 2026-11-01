package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/authmail"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
)

// Round 1 of handoff_ticket.md: support tickets, backend (T1-02 .. T1-11).

func ticketBody(subject string) map[string]any {
	return map[string]any{"category": "connection", "subject": subject, "body": "节点连接不上\n第二行说明"}
}

func (e *consoleEnv) submit(u payUser, body map[string]any) *httptest.ResponseRecorder {
	return e.call(u.cookies, "POST", "/api/v1/me/tickets", body, uuid.NewString())
}

type ticketAnswer struct {
	Ticket   ticketDetail `json:"ticket"`
	Replayed bool         `json:"replayed"`
}

type consoleTicketAnswer struct {
	Ticket   consoleTicketDetail `json:"ticket"`
	Replayed bool                `json:"replayed"`
}

func (e *consoleEnv) newTicket(u payUser, subject string) ticketDetail {
	e.t.Helper()
	var out ticketAnswer
	e.decode(e.submit(u, ticketBody(subject)), 201, &out)
	return out.Ticket
}

func (e *consoleEnv) ticketRow(id string) model.Ticket {
	e.t.Helper()
	var t model.Ticket
	if err := e.db.First(&t, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return t
}

func (e *consoleEnv) userReply(u payUser, id, body string) *httptest.ResponseRecorder {
	return e.call(u.cookies, "POST", "/api/v1/me/tickets/"+id+"/messages", map[string]any{"body": body}, uuid.NewString())
}

func (e *consoleEnv) adminReply(admin payUser, id, body string) *httptest.ResponseRecorder {
	return e.call(admin.cookies, "POST", "/api/v1/admin/console/tickets/"+id+"/messages", map[string]any{"body": body}, uuid.NewString())
}

func (e *consoleEnv) adminClose(id string, version int64, note string) *httptest.ResponseRecorder {
	return e.post("/tickets/"+id+"/close", map[string]any{"version": version, "note": note})
}

func (e *consoleEnv) userClose(u payUser, id string) *httptest.ResponseRecorder {
	return e.call(u.cookies, "POST", "/api/v1/me/tickets/"+id+"/close", map[string]any{}, "")
}

func (e *consoleEnv) myTickets(u payUser, query string) (items []ticketItem, open int64) {
	e.t.Helper()
	var out struct {
		Tickets   []ticketItem `json:"tickets"`
		OpenCount int64        `json:"open_count"`
	}
	e.decode(e.call(u.cookies, "GET", "/api/v1/me/tickets?"+query, nil, ""), 200, &out)
	return out.Tickets, out.OpenCount
}

func (e *consoleEnv) consoleTickets(query string) []consoleTicketItem {
	e.t.Helper()
	var out struct {
		Tickets []consoleTicketItem `json:"tickets"`
	}
	e.decode(e.get("/tickets?"+query), 200, &out)
	return out.Tickets
}

func ticketSubjects[T interface{ subject() string }](list []T) []string {
	out := make([]string, len(list))
	for i, t := range list {
		out[i] = t.subject()
	}
	return out
}

func (t ticketItem) subject() string        { return t.Subject }
func (t consoleTicketItem) subject() string { return t.Subject }

// T1-02
func TestTicketCreateValidation(t *testing.T) {
	e := newConsoleEnv(t, nil)
	n := 0
	fresh := func() payUser { n++; return e.user(fmt.Sprintf("v%d", n), "user") }

	// Every category, every device value and no device are accepted.
	for _, category := range model.TicketCategories {
		var out ticketAnswer
		e.decode(e.submit(fresh(), map[string]any{"category": category, "subject": "分类 " + category, "body": "说明"}), 201, &out)
		if out.Ticket.Category != category || out.Ticket.Platform != "" {
			t.Fatalf("category %s: %+v", category, out.Ticket)
		}
	}
	for _, platform := range model.TicketPlatforms {
		var out ticketAnswer
		e.decode(e.submit(fresh(), map[string]any{"category": "client", "subject": "设备", "body": "说明", "platform": platform}), 201, &out)
		if out.Ticket.Platform != platform {
			t.Fatalf("platform %s: %+v", platform, out.Ticket)
		}
	}

	u := fresh()
	invalid := []struct {
		field string
		body  map[string]any
	}{
		{"category", map[string]any{"category": "", "subject": "x", "body": "x"}},
		{"category", map[string]any{"category": "refund", "subject": "x", "body": "x"}},
		{"subject", map[string]any{"category": "other", "subject": "", "body": "x"}},
		{"subject", map[string]any{"category": "other", "subject": "   ", "body": "x"}},
		{"subject", map[string]any{"category": "other", "subject": strings.Repeat("标", 61), "body": "x"}},
		{"subject", map[string]any{"category": "other", "subject": "两\n行", "body": "x"}},
		{"subject", map[string]any{"category": "other", "subject": "制\t表\x01", "body": "x"}},
		{"body", map[string]any{"category": "other", "subject": "x", "body": " \n "}},
		{"body", map[string]any{"category": "other", "subject": "x", "body": strings.Repeat("问", 3001)}},
		{"body", map[string]any{"category": "other", "subject": "x", "body": "含\x00控制字符"}},
		{"body", map[string]any{"category": "other", "subject": "x", "body": "响铃\a"}},
		{"platform", map[string]any{"category": "other", "subject": "x", "body": "x", "platform": "linux"}},
	}
	for _, c := range invalid {
		out := e.want(e.submit(u, c.body), 400)
		detail, _ := out["error"].(map[string]any)
		if payErrorCode(out) != "invalid_ticket" || detail["field"] != c.field {
			t.Fatalf("%v: %v", c.body, out)
		}
	}
	// Unknown fields are refused by the shared decoder.
	e.code(e.submit(u, map[string]any{"category": "other", "subject": "x", "body": "x", "priority": "high"}), 400, "invalid_json")
	// Go's JSON decoder turns invalid UTF-8 into U+FFFD, so stored text is always valid.
	raw := httptest.NewRequest("POST", "/api/v1/me/tickets", bytes.NewReader([]byte("{\"category\":\"other\",\"subject\":\"bad \xff byte\",\"body\":\"x\"}")))
	raw.Header.Set("Content-Type", "application/json")
	raw.Header.Set("Origin", e.cfg.Origins[0])
	raw.Header.Set("Idempotency-Key", uuid.NewString())
	for _, c := range u.cookies {
		raw.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, raw)
	var replaced ticketAnswer
	e.decode(w, 201, &replaced)
	if !utf8.ValidString(e.ticketRow(replaced.Ticket.ID).Subject) {
		t.Fatal("invalid UTF-8 stored")
	}

	// 3000 characters (9000 bytes, above the default 8 KB request limit) are accepted.
	long := strings.Repeat("问", 3000)
	var big ticketAnswer
	e.decode(e.submit(fresh(), map[string]any{"category": "other", "subject": strings.Repeat("标", 60), "body": long}), 201, &big)
	if big.Ticket.Messages[0].Body != long {
		t.Fatal("long body changed")
	}
	// Text is trimmed; inner line breaks and tabs stay.
	var trimmed ticketAnswer
	e.decode(e.submit(fresh(), map[string]any{"category": "other", "subject": "  标题  ", "body": "\n 第一行\n\t第二行 \n"}), 201, &trimmed)
	if trimmed.Ticket.Subject != "标题" || trimmed.Ticket.Messages[0].Body != "第一行\n\t第二行" {
		t.Fatalf("trim: %q %q", trimmed.Ticket.Subject, trimmed.Ticket.Messages[0].Body)
	}

	// Linked orders: only the user's own, as a UUID.
	owner, other := fresh(), fresh()
	mine := e.place(owner, "starter", "wxpay", "").Order
	theirs := e.place(other, "starter", "wxpay", "").Order
	bodyWith := func(order any) map[string]any {
		return map[string]any{"category": "billing", "subject": "付款", "body": "x", "order_id": order}
	}
	e.code(e.submit(owner, bodyWith(theirs.ID)), 400, "invalid_order")
	e.code(e.submit(owner, bodyWith("not-a-uuid")), 400, "invalid_order")
	e.code(e.submit(owner, bodyWith(uuid.NewString())), 400, "invalid_order")
	var linked ticketAnswer
	e.decode(e.submit(owner, bodyWith(mine.ID)), 201, &linked)
	if linked.Ticket.Order == nil || linked.Ticket.Order.ID != mine.ID || linked.Ticket.Order.TradeNo == nil || *linked.Ticket.Order.TradeNo != *mine.TradeNo {
		t.Fatalf("linked order: %+v", linked.Ticket.Order)
	}
	e.decode(e.submit(owner, bodyWith(nil)), 201, nil)
	e.decode(e.submit(owner, bodyWith("")), 201, nil)
	// The list offers the user's own recent orders only.
	var list struct {
		OrderOptions []ticketOrder `json:"order_options"`
	}
	e.decode(e.call(owner.cookies, "GET", "/api/v1/me/tickets", nil, ""), 200, &list)
	if len(list.OrderOptions) != 1 || list.OrderOptions[0].ID != mine.ID {
		t.Fatalf("order options %+v", list.OrderOptions)
	}

	// Ticket numbers: T + UTC+8 date + six digits.
	pattern := regexp.MustCompile(`^T\d{12}$`)
	prefix := "T" + e.clock.Now().In(consoleLocation).Format("060102")
	if !pattern.MatchString(big.Ticket.TicketNo) || !strings.HasPrefix(big.Ticket.TicketNo, prefix) {
		t.Fatalf("ticket number %q (want prefix %s)", big.Ticket.TicketNo, prefix)
	}
	// A taken number is redrawn; five in a row are 503 tickets_unavailable.
	s := e.server()
	draws := []string{big.Ticket.TicketNo, big.Ticket.TicketNo, "T261031000001"}
	s.ticketNumbers = func(time.Time) (string, error) {
		next := draws[0]
		draws = draws[1:]
		return next, nil
	}
	if got, err := s.newTicketNo(e.db, e.clock.Now()); err != nil || got != "T261031000001" || len(draws) != 0 {
		t.Fatalf("collision retry: %q %v (left %v)", got, err, draws)
	}
	calls := 0
	s.ticketNumbers = func(time.Time) (string, error) { calls++; return big.Ticket.TicketNo, nil }
	if _, err := s.newTicketNo(e.db, e.clock.Now()); !errors.Is(err, errTicketUnavailable) || calls != ticketNumberAttempts {
		t.Fatalf("five collisions: %v after %d draws", err, calls)
	}
}

// T1-03
func TestTicketIdempotencyAndCaps(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("caps", "user")

	key := uuid.NewString()
	body := ticketBody("第一张")
	var first, again ticketAnswer
	e.decode(e.call(u.cookies, "POST", "/api/v1/me/tickets", body, key), 201, &first)
	e.decode(e.call(u.cookies, "POST", "/api/v1/me/tickets", body, key), 200, &again)
	if again.Ticket.ID != first.Ticket.ID || !again.Replayed || first.Replayed {
		t.Fatalf("replay: %+v / %+v", first, again)
	}
	changed := ticketBody("第一张")
	changed["body"] = "别的内容"
	e.code(e.call(u.cookies, "POST", "/api/v1/me/tickets", changed, key), 409, "idempotency_conflict")
	e.code(e.call(u.cookies, "POST", "/api/v1/me/tickets", body, ""), 400, "idempotency_required")
	e.code(e.call(u.cookies, "POST", "/api/v1/me/tickets", body, "not-a-uuid"), 400, "idempotency_required")

	// At most three open tickets; closing one frees a place.
	e.newTicket(u, "第二张")
	third := e.newTicket(u, "第三张")
	e.code(e.submit(u, ticketBody("第四张")), 409, "too_many_open_tickets")
	if _, open := e.myTickets(u, ""); open != 3 {
		t.Fatalf("open count %d", open)
	}
	e.want(e.userClose(u, third.ID), 200)
	e.newTicket(u, "第四张")
	// Four successful submissions counted (the replay and the refusals did not): one more
	// is allowed today, then 429 with Retry-After.
	e.want(e.userClose(u, first.Ticket.ID), 200)
	e.newTicket(u, "第五张")
	e.want(e.userClose(u, e.myTicketsFirstOpen(u)), 200)
	w := e.submit(u, ticketBody("第六张"))
	e.code(w, 429, "ticket_rate_limited")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}

	// Refused content never uses up the allowance.
	v := e.user("validation", "user")
	for range 10 {
		e.code(e.submit(v, map[string]any{"category": "other", "subject": "", "body": "x"}), 400, "invalid_ticket")
	}
	for i := range 5 {
		created := e.newTicket(v, fmt.Sprintf("有效 %d", i))
		e.want(e.userClose(v, created.ID), 200)
	}

	// Two concurrent submissions for the last free place: exactly one wins.
	r := e.user("race", "user")
	e.newTicket(r, "一")
	e.newTicket(r, "二")
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.submit(r, ticketBody(fmt.Sprintf("并发 %d", i))).Code
		}()
	}
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != 201 || codes[1] != 409 {
		t.Fatalf("concurrent submissions: %v", codes)
	}
	if _, open := e.myTickets(r, ""); open != 3 {
		t.Fatalf("open after race %d", open)
	}
}

func (e *consoleEnv) myTicketsFirstOpen(u payUser) string {
	e.t.Helper()
	items, _ := e.myTickets(u, "status=active")
	if len(items) == 0 {
		e.t.Fatal("no open ticket")
	}
	return items[0].ID
}

// T1-04
func TestTicketStateMachine(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("states", "user")
	check := func(step string, id, status string, waiting time.Time, unread bool, count int, version int64) model.Ticket {
		t.Helper()
		row := e.ticketRow(id)
		if row.Status != status || row.WaitingSince == nil || !row.WaitingSince.Equal(waiting) || row.UserUnread != unread || row.MessageCount != count || row.Version != version {
			t.Fatalf("%s: status %s waiting %v unread %t count %d version %d", step, row.Status, row.WaitingSince, row.UserUnread, row.MessageCount, row.Version)
		}
		var messages int64
		e.db.Model(&model.TicketMessage{}).Where("ticket_id = ?", id).Count(&messages)
		if int(messages) != count {
			t.Fatalf("%s: %d message rows, count %d", step, messages, count)
		}
		return row
	}
	t0 := e.clock.Now()
	tk := e.newTicket(u, "状态机")
	check("submit", tk.ID, model.TicketOpen, t0, false, 1, 1)
	e.clock.Advance(time.Minute)
	e.want(e.userReply(u, tk.ID, "补充"), 201)
	row := check("user adds", tk.ID, model.TicketOpen, t0, false, 2, 2)
	if !row.LastMessageAt.Equal(e.clock.Now()) {
		t.Fatal("last_message_at")
	}
	e.clock.Advance(time.Minute)
	e.want(e.adminReply(e.admin, tk.ID, "客服回复"), 201)
	check("staff reply", tk.ID, model.TicketAnswered, t0, true, 3, 3)
	e.clock.Advance(time.Minute)
	e.want(e.adminReply(e.admin, tk.ID, "再次回复"), 201)
	check("staff again", tk.ID, model.TicketAnswered, t0, true, 4, 4)
	e.want(e.call(u.cookies, "POST", "/api/v1/me/tickets/"+tk.ID+"/read", map[string]any{}, ""), 200)
	check("read", tk.ID, model.TicketAnswered, t0, false, 4, 4)
	e.clock.Advance(time.Minute)
	t4 := e.clock.Now()
	e.want(e.userReply(u, tk.ID, "用户回复"), 201)
	check("user answers", tk.ID, model.TicketOpen, t4, false, 5, 5)
	e.clock.Advance(time.Minute)
	var closed consoleTicketAnswer
	e.decode(e.adminClose(tk.ID, 5, "已为你处理"), 200, &closed)
	row = check("staff close", tk.ID, model.TicketClosed, t4, true, 6, 6)
	if row.ClosedBy != "staff" || row.ClosedByAdminID == nil || *row.ClosedByAdminID != e.admin.ID || row.ClosedAt == nil {
		t.Fatalf("closed by %+v", row)
	}
	if last := closed.Ticket.Messages[5]; last.Author != "staff" || last.Body != "已为你处理" || closed.Ticket.ClosedByAdminEmail != e.admin.Email {
		t.Fatalf("closing note %+v", closed.Ticket)
	}
	// Closed is final: nobody writes any more; the user's own close stays harmless.
	e.code(e.userReply(u, tk.ID, "还有问题"), 409, "ticket_closed")
	e.code(e.adminReply(e.admin, tk.ID, "补一句"), 409, "ticket_closed")
	e.code(e.adminClose(tk.ID, 6, ""), 409, "ticket_closed")
	e.want(e.userClose(u, tk.ID), 200)
	check("closed again", tk.ID, model.TicketClosed, t4, true, 6, 6)

	// The user closes their own; the unread mark stays as it was.
	mine := e.newTicket(u, "自己关闭")
	e.want(e.userClose(u, mine.ID), 200)
	if row := e.ticketRow(mine.ID); row.Status != model.TicketClosed || row.ClosedBy != "user" || row.ClosedByAdminID != nil || row.Version != 2 || row.UserUnread {
		t.Fatalf("user close %+v", row)
	}

	// The 101st message is refused, for both sides.
	full := e.newTicket(u, "消息上限")
	e.db.Model(&model.Ticket{}).Where("id = ?", full.ID).Update("message_count", model.MaxTicketMessages)
	e.code(e.userReply(u, full.ID, "多一条"), 409, "ticket_message_limit")
	e.code(e.adminReply(e.admin, full.ID, "多一条"), 409, "ticket_message_limit")
	e.code(e.adminClose(full.ID, 1, "带说明关闭"), 409, "ticket_message_limit")
	e.want(e.adminClose(full.ID, 1, ""), 200)

	// A reply replayed with its key is stored once; the same key with other text is 409.
	again := e.newTicket(u, "重放")
	key := uuid.NewString()
	e.want(e.call(u.cookies, "POST", "/api/v1/me/tickets/"+again.ID+"/messages", map[string]any{"body": "一次"}, key), 201)
	var replay ticketAnswer
	e.decode(e.call(u.cookies, "POST", "/api/v1/me/tickets/"+again.ID+"/messages", map[string]any{"body": "一次"}, key), 200, &replay)
	if !replay.Replayed || replay.Ticket.MessageCount != 2 {
		t.Fatalf("reply replay %+v", replay)
	}
	e.code(e.call(u.cookies, "POST", "/api/v1/me/tickets/"+again.ID+"/messages", map[string]any{"body": "两次"}, key), 409, "idempotency_conflict")

	// Thirty replies an hour; the 31st is 429 and counts nothing.
	busy := e.user("busy", "user")
	chat := e.newTicket(busy, "回复限流")
	for i := range ticketMessageLimit {
		e.want(e.userReply(busy, chat.ID, fmt.Sprintf("第 %d 条", i)), 201)
	}
	w := e.userReply(busy, chat.ID, "第 31 条")
	e.code(w, 429, "ticket_rate_limited")
	if w.Header().Get("Retry-After") == "" || e.ticketRow(chat.ID).MessageCount != ticketMessageLimit+1 {
		t.Fatal("31st reply")
	}
	e.clock.Advance(time.Hour)
	e.want(e.userReply(busy, chat.ID, "下一小时"), 201)
}

// T1-05
func TestTicketConsoleQueueAndActions(t *testing.T) {
	e := newConsoleEnv(t, nil)
	peer := e.user("peer-admin", "admin")
	u1, u2 := e.user("alice", "user"), e.user("bob", "user")
	a := e.newTicket(u1, "最早的问题")
	e.clock.Advance(time.Minute)
	b := e.newTicket(u2, "节点很慢")
	e.clock.Advance(time.Minute)
	billing := map[string]any{"category": "billing", "subject": "付款没到账", "body": "x"}
	var c ticketAnswer
	e.decode(e.submit(u1, billing), 201, &c)

	// The queue serves the longest wait first; a reply moves a ticket out of it and a
	// user answer puts it back at the end.
	if got := strings.Join(ticketSubjects(e.consoleTickets("")), ","); got != "最早的问题,节点很慢,付款没到账" {
		t.Fatalf("queue %s", got)
	}
	var summary struct {
		OpenCount          int64      `json:"open_count"`
		OldestWaitingSince *time.Time `json:"oldest_waiting_since"`
	}
	e.decode(e.get("/tickets/summary"), 200, &summary)
	if summary.OpenCount != 3 || summary.OldestWaitingSince == nil || !summary.OldestWaitingSince.Equal(e.clock.Now().Add(-2*time.Minute)) {
		t.Fatalf("summary %+v", summary)
	}
	e.want(e.adminReply(e.admin, a.ID, "请重启客户端"), 201)
	e.clock.Advance(time.Minute)
	e.want(e.userReply(u1, a.ID, "还是不行"), 201)
	if got := strings.Join(ticketSubjects(e.consoleTickets("status=open")), ","); got != "节点很慢,付款没到账,最早的问题" {
		t.Fatalf("queue after answer %s", got)
	}
	// Filters and keyword (ticket number prefix, e-mail, subject).
	if got := ticketSubjects(e.consoleTickets("status=all&category=billing")); len(got) != 1 || got[0] != "付款没到账" {
		t.Fatalf("category filter %v", got)
	}
	if got := ticketSubjects(e.consoleTickets("status=all&keyword=" + strings.ToLower(b.TicketNo[:13]))); len(got) != 1 || got[0] != "节点很慢" {
		t.Fatalf("number keyword %v", got)
	}
	if got := ticketSubjects(e.consoleTickets("status=all&keyword=BOB@")); len(got) != 1 || got[0] != "节点很慢" {
		t.Fatalf("email keyword %v", got)
	}
	if got := ticketSubjects(e.consoleTickets("status=all&keyword=到账")); len(got) != 1 {
		t.Fatalf("subject keyword %v", got)
	}
	if got := e.consoleTickets("status=all&keyword=%25"); len(got) != 0 {
		t.Fatalf("wildcard matched %d", len(got))
	}
	e.code(e.get("/tickets?status=pending"), 400, "invalid_filter")
	e.code(e.get("/tickets?category=refund"), 400, "invalid_filter")

	// A reply is audited with its length only.
	var audit model.AdminAudit
	e.db.Where("action = ? AND target_id = ?", AuditTicketReply, a.ID).First(&audit)
	if audit.ActorID != e.admin.ID || audit.Detail != `{"length":6}` {
		t.Fatalf("reply audit %+v", audit)
	}
	// Close without a note: no message is added.
	before := e.ticketRow(b.ID)
	var closed consoleTicketAnswer
	e.decode(e.adminClose(b.ID, before.Version, "  "), 200, &closed)
	if closed.Ticket.Status != model.TicketClosed || closed.Ticket.MessageCount != 1 || len(closed.Ticket.Messages) != 1 {
		t.Fatalf("close without note %+v", closed.Ticket)
	}
	var closeAudit model.AdminAudit
	e.db.Where("action = ? AND target_id = ?", AuditTicketClose, b.ID).First(&closeAudit)
	if audit = closeAudit; audit.Detail != `{"note_length":0,"with_note":false}` {
		t.Fatalf("close audit %s", audit.Detail)
	}
	// A stale version cannot close: the user wrote meanwhile.
	seen := e.ticketRow(c.Ticket.ID).Version
	e.want(e.userReply(u1, c.Ticket.ID, "补充订单号"), 201)
	e.code(e.adminClose(c.Ticket.ID, seen, ""), 409, "ticket_changed")
	e.code(e.adminClose(c.Ticket.ID, seen, strings.Repeat("长", 501)), 400, "invalid_ticket")

	// Two administrators answering at once both get through, in a definite order.
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, admin := range []payUser{e.admin, peer} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = e.adminReply(admin, c.Ticket.ID, fmt.Sprintf("管理员 %d", i)).Code
		}()
	}
	wg.Wait()
	var detail consoleTicketAnswer
	e.decode(e.get("/tickets/"+c.Ticket.ID), 200, &detail)
	if statuses[0] != 201 || statuses[1] != 201 || detail.Ticket.MessageCount != 4 || len(detail.Ticket.Messages) != 4 {
		t.Fatalf("concurrent replies %v %+v", statuses, detail.Ticket)
	}
	if m := detail.Ticket.Messages; m[1].Body != "补充订单号" || m[2].Author != "staff" || m[3].Author != "staff" || m[2].Body == m[3].Body {
		t.Fatalf("conversation order %+v", m)
	}
	emails := []string{detail.Ticket.Messages[2].AuthorEmail, detail.Ticket.Messages[3].AuthorEmail}
	slices.Sort(emails)
	want := []string{e.admin.Email, peer.Email}
	slices.Sort(want)
	if !slices.Equal(emails, want) || detail.Ticket.User.Email != u1.Email || detail.Ticket.User.Status != "active" {
		t.Fatalf("console detail %+v", detail.Ticket)
	}

	// An owner being deleted can only have tickets closed; a reply sent before that is
	// still recognised as a replay.
	d := e.newTicket(u2, "注销中")
	replyKey := uuid.NewString()
	e.want(e.call(e.admin.cookies, "POST", "/api/v1/admin/console/tickets/"+d.ID+"/messages", map[string]any{"body": "先回复"}, replyKey), 201)
	e.db.Model(&model.User{}).Where("id = ?", u2.ID).Update("status", model.UserDeleting)
	var replayed consoleTicketAnswer
	e.decode(e.call(e.admin.cookies, "POST", "/api/v1/admin/console/tickets/"+d.ID+"/messages", map[string]any{"body": "先回复"}, replyKey), 200, &replayed)
	if !replayed.Replayed || replayed.Ticket.MessageCount != 2 {
		t.Fatalf("staff replay %+v", replayed)
	}
	if got := e.consoleTickets("status=answered&keyword=bob@"); len(got) != 1 || got[0].UserStatus != model.UserDeleting {
		t.Fatalf("deleting owner %+v", got)
	}
	e.code(e.adminReply(e.admin, d.ID, "你好"), 409, "user_unavailable")
	e.want(e.adminClose(d.ID, 2, ""), 200)

	// Access: users and impersonation sessions never reach the console.
	plain := e.user("plain", "user")
	for _, route := range []struct{ method, path string }{{"GET", "/tickets"}, {"GET", "/tickets/summary"}, {"GET", "/tickets/" + a.ID}, {"POST", "/tickets/" + a.ID + "/messages"}, {"POST", "/tickets/" + a.ID + "/close"}} {
		e.code(e.call(plain.cookies, route.method, "/api/v1/admin/console"+route.path, map[string]any{"body": "x", "version": 1}, uuid.NewString()), 403, "admin_required")
	}
	admin := e.browser(e.admin.cookies)
	e.want(e.impersonate(admin, u1.ID), 200)
	e.code(admin.get("/admin/console/tickets"), 403, "impersonation_read_only")

	// An administrator demoted between the route check and the transaction is refused
	// under the lock.
	var demote atomic.Bool
	callback := "ticket:demote"
	if err := e.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		table := tx.Statement.Table[strings.LastIndexByte(tx.Statement.Table, '.')+1:]
		if _, locking := tx.Statement.Clauses["FOR"]; locking && table == "users" && demote.CompareAndSwap(true, false) {
			e.db.Exec(`UPDATE "`+e.cfg.Schema+`".users SET role = 'user' WHERE id = ?`, peer.ID)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.db.Callback().Query().Remove(callback) })
	open := e.newTicket(plain, "降级")
	demote.Store(true)
	e.code(e.adminReply(peer, open.ID, "我已经不是管理员"), 403, "admin_required")
	if e.ticketRow(open.ID).MessageCount != 1 {
		t.Fatal("a demoted administrator wrote")
	}
}

func jsonKeys(t *testing.T, value any) []string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func rawKeys(t *testing.T, body []byte, path ...string) []string {
	t.Helper()
	var node any
	if err := json.Unmarshal(body, &node); err != nil {
		t.Fatal(err)
	}
	for _, p := range path {
		switch v := node.(type) {
		case map[string]any:
			node = v[p]
		case []any:
			node = v[0]
		}
	}
	return jsonKeys(t, node)
}

// T1-06
func TestTicketUserFieldWhitelist(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u, other := e.user("owner", "user"), e.user("stranger", "user")
	tk := e.newTicket(u, "白名单")
	e.want(e.adminReply(e.admin, tk.ID, "客服回复"), 201)
	e.want(e.adminClose(tk.ID, 2, "关闭"), 200)

	list := e.call(u.cookies, "GET", "/api/v1/me/tickets", nil, "").Body.Bytes()
	if got := strings.Join(rawKeys(t, list), ","); got != "max_open,open_count,order_options,page,page_size,tickets,total" {
		t.Fatalf("list keys %s", got)
	}
	if got := strings.Join(rawKeys(t, list, "tickets", "0"), ","); got != "category,created_at,expires_at,id,last_message_at,message_count,status,subject,ticket_no,user_unread" {
		t.Fatalf("list item keys %s", got)
	}
	detail := e.call(u.cookies, "GET", "/api/v1/me/tickets/"+tk.ID, nil, "").Body.Bytes()
	if got := strings.Join(rawKeys(t, detail, "ticket"), ","); got != "category,closed_at,closed_by,created_at,expires_at,id,last_message_at,message_count,messages,order,platform,status,subject,ticket_no,user_unread" {
		t.Fatalf("detail keys %s", got)
	}
	if got := strings.Join(rawKeys(t, detail, "ticket", "messages", "0"), ","); got != "author,body,created_at,id" {
		t.Fatalf("message keys %s", got)
	}
	// No administrator identity anywhere in what the user receives.
	for _, leak := range []string{e.admin.ID, e.admin.Email, "author_id", "author_email", "closed_by_admin"} {
		if bytes.Contains(detail, []byte(leak)) || bytes.Contains(list, []byte(leak)) {
			t.Fatalf("user answer contains %q", leak)
		}
	}
	summary := e.call(u.cookies, "GET", "/api/v1/me/tickets/summary", nil, "").Body.Bytes()
	if got := strings.Join(rawKeys(t, summary), ","); got != "max_open,open_count,unread_count" {
		t.Fatalf("summary keys %s", got)
	}

	// Someone else's ticket answers exactly like a ticket that does not exist.
	foreign := e.call(other.cookies, "GET", "/api/v1/me/tickets/"+tk.ID, nil, "")
	missing := e.call(other.cookies, "GET", "/api/v1/me/tickets/"+uuid.NewString(), nil, "")
	if foreign.Code != 404 || missing.Code != 404 || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign %d %s / missing %d %s", foreign.Code, foreign.Body, missing.Code, missing.Body)
	}
	for _, path := range []string{"/messages", "/close", "/read"} {
		e.code(e.call(other.cookies, "POST", "/api/v1/me/tickets/"+tk.ID+path, map[string]any{"body": "x"}, uuid.NewString()), 404, "ticket_not_found")
	}
	e.code(e.call(u.cookies, "GET", "/api/v1/me/tickets/junk", nil, ""), 404, "ticket_not_found")
	e.code(e.get("/tickets/junk"), 404, "ticket_not_found")
	e.want(e.call(nil, "GET", "/api/v1/me/tickets", nil, ""), 401)
	if items, _ := e.myTickets(other, ""); len(items) != 0 {
		t.Fatal("a stranger lists another user's ticket")
	}
}

// T1-07 (the API side; the sweep itself is tested in internal/maintenance)
func TestTicketExpiry(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("expiry", "user")
	tk := e.newTicket(u, "将要过期")
	if !tk.ExpiresAt.Equal(tk.CreatedAt.Add(30 * 24 * time.Hour)) {
		t.Fatalf("expires %v created %v", tk.ExpiresAt, tk.CreatedAt)
	}
	e.want(e.adminReply(e.admin, tk.ID, "回复"), 201)
	e.clock.Advance(model.TicketRetention - time.Second)
	e.want(e.call(u.cookies, "GET", "/api/v1/me/tickets/"+tk.ID, nil, ""), 200)
	e.want(e.userReply(u, tk.ID, "最后一秒"), 201)
	e.clock.Advance(time.Second)
	if items, open := e.myTickets(u, ""); len(items) != 0 || open != 0 {
		t.Fatalf("expired ticket listed: %d %d", len(items), open)
	}
	var summary map[string]any
	e.decode(e.call(u.cookies, "GET", "/api/v1/me/tickets/summary", nil, ""), 200, &summary)
	if summary["unread_count"].(float64) != 0 || summary["open_count"].(float64) != 0 {
		t.Fatalf("summary %v", summary)
	}
	e.code(e.call(u.cookies, "GET", "/api/v1/me/tickets/"+tk.ID, nil, ""), 404, "ticket_not_found")
	e.code(e.userReply(u, tk.ID, "过期了"), 404, "ticket_not_found")
	e.code(e.userClose(u, tk.ID), 404, "ticket_not_found")
	e.code(e.call(u.cookies, "POST", "/api/v1/me/tickets/"+tk.ID+"/read", map[string]any{}, ""), 404, "ticket_not_found")
	e.code(e.get("/tickets/"+tk.ID), 404, "ticket_not_found")
	e.code(e.adminReply(e.admin, tk.ID, "过期了"), 404, "ticket_not_found")
	e.code(e.adminClose(tk.ID, 3, ""), 404, "ticket_not_found")
	if len(e.consoleTickets("status=all")) != 0 {
		t.Fatal("console lists an expired ticket")
	}
	// An expired ticket no longer counts against the open cap.
	for range 3 {
		e.newTicket(u, "新问题")
	}
}

// T1-08
func TestTicketAccountPurge(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u, keep := e.user("leaver", "user"), e.user("stayer", "user")
	for i := range 2 {
		tk := e.newTicket(u, fmt.Sprintf("注销前 %d", i))
		e.want(e.adminReply(e.admin, tk.ID, "回复"), 201)
	}
	kept := e.newTicket(keep, "保留")
	now := e.clock.Now()
	e.db.Model(&model.User{}).Where("id = ?", u.ID).Updates(map[string]any{"status": model.UserDeleting, "deletion_requested_at": now})
	result, err := PurgeDeletedAccounts(t.Context(), e.db, e.cfg)
	if err != nil || result.Purged != 1 {
		t.Fatalf("purge %+v %v", result, err)
	}
	var tickets, messages int64
	e.db.Model(&model.Ticket{}).Count(&tickets)
	e.db.Model(&model.TicketMessage{}).Count(&messages)
	if tickets != 1 || messages != 1 || e.ticketRow(kept.ID).UserID != keep.ID {
		t.Fatalf("after purge: %d tickets, %d messages", tickets, messages)
	}
	// An older binary's purge deletes the user row directly: the foreign keys cascade.
	if err = e.db.Exec(`DELETE FROM "`+e.cfg.Schema+`".users WHERE id = ?`, keep.ID).Error; err != nil {
		t.Fatal(err)
	}
	e.db.Model(&model.Ticket{}).Count(&tickets)
	e.db.Model(&model.TicketMessage{}).Count(&messages)
	if tickets != 0 || messages != 0 {
		t.Fatalf("cascade left %d tickets, %d messages", tickets, messages)
	}
}

// T1-09
func TestTicketImpersonationReadOnly(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("watched", "user")
	tk := e.newTicket(u, "被查看")
	e.want(e.adminReply(e.admin, tk.ID, "回复"), 201)
	admin := e.browser(e.admin.cookies)
	e.want(e.impersonate(admin, u.ID), 200)

	var list struct {
		Tickets []ticketItem `json:"tickets"`
	}
	e.decode(admin.get("/me/tickets"), 200, &list)
	if len(list.Tickets) != 1 || list.Tickets[0].ID != tk.ID || !list.Tickets[0].UserUnread {
		t.Fatalf("impersonated list %+v", list.Tickets)
	}
	e.want(admin.get("/me/tickets/"+tk.ID), 200)
	e.want(admin.get("/me/tickets/summary"), 200)
	for _, path := range []string{"/me/tickets", "/me/tickets/" + tk.ID + "/messages", "/me/tickets/" + tk.ID + "/close", "/me/tickets/" + tk.ID + "/read"} {
		e.code(admin.post(path, map[string]any{"category": "other", "subject": "x", "body": "x"}), 403, "impersonation_read_only")
	}
	if row := e.ticketRow(tk.ID); !row.UserUnread || row.Status != model.TicketAnswered || row.MessageCount != 2 {
		t.Fatalf("impersonation changed the ticket: %+v", row)
	}
}

// T1-10
func TestTicketsSwitchedOff(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("switch", "user")
	tk := e.newTicket(u, "关闭前提交")
	// Administrators never submit on the user side.
	e.code(e.call(e.admin.cookies, "POST", "/api/v1/me/tickets", ticketBody("管理员"), uuid.NewString()), 403, "admin_cannot_submit")

	off := e.cfg
	off.Tickets.Disabled = true
	e.router = newServerAt(e.db, off, authmail.NewService(e.db, off), e.clock.Now)
	e.cfg = off
	var meta map[string]any
	e.decode(e.call(nil, "GET", "/api/v1/meta", nil, ""), 200, &meta)
	if meta["tickets_enabled"] != false {
		t.Fatalf("meta %v", meta)
	}
	e.code(e.submit(u, ticketBody("关闭后")), 403, "tickets_disabled")
	e.code(e.userReply(u, tk.ID, "关闭后回复"), 403, "tickets_disabled")
	e.code(e.userClose(u, tk.ID), 403, "tickets_disabled")
	e.want(e.call(u.cookies, "GET", "/api/v1/me/tickets", nil, ""), 200)
	e.want(e.call(u.cookies, "GET", "/api/v1/me/tickets/"+tk.ID, nil, ""), 200)
	e.want(e.call(u.cookies, "GET", "/api/v1/me/tickets/summary", nil, ""), 200)
	e.want(e.adminReply(e.admin, tk.ID, "仍可回复"), 201)
	e.want(e.call(u.cookies, "POST", "/api/v1/me/tickets/"+tk.ID+"/read", map[string]any{}, ""), 200)
	e.want(e.adminClose(tk.ID, 2, ""), 200)

	// The default (and a configuration built without Load) has tickets on.
	if !(config.Config{}).TicketsEnabled() {
		t.Fatal("tickets are off by default")
	}
	on := e.cfg
	on.Tickets.Disabled = false
	var metaOn map[string]any
	e.router = newServerAt(e.db, on, authmail.NewService(e.db, on), e.clock.Now)
	e.decode(e.call(nil, "GET", "/api/v1/meta", nil, ""), 200, &metaOn)
	if metaOn["tickets_enabled"] != true {
		t.Fatalf("meta %v", metaOn)
	}
}

// T1-11: ticket text, subjects and addresses never reach the log, the audit or the doctor.
func TestTicketTextStaysPrivate(t *testing.T) {
	var logs bytes.Buffer
	var mu sync.Mutex
	previous := log.Writer()
	log.SetOutput(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return logs.Write(p) }))
	t.Cleanup(func() { log.SetOutput(previous) })

	e := newConsoleEnv(t, nil)
	u := e.user("secretive", "user")
	const subject, body, reply, note = "私密标题甲乙", "私密正文丙丁 secret-body-42", "私密回复戊己", "私密说明庚辛"
	var tk ticketAnswer
	e.decode(e.submit(u, map[string]any{"category": "account", "subject": subject, "body": body}), 201, &tk)
	e.want(e.adminReply(e.admin, tk.Ticket.ID, reply), 201)
	e.want(e.userReply(u, tk.Ticket.ID, body+"2"), 201)
	e.want(e.adminClose(tk.Ticket.ID, 3, note), 200)
	// A ticket waiting more than a day and one expired more than an hour ago make doctor warn.
	// (The doctor reads the real clock.)
	waiting := e.newTicket(u, subject+"等")
	real := time.Now().UTC()
	e.db.Model(&model.Ticket{}).Where("id = ?", tk.Ticket.ID).Update("expires_at", real.Add(-2*time.Hour))
	e.db.Model(&model.Ticket{}).Where("id = ?", waiting.ID).Update("waiting_since", real.Add(-25*time.Hour))
	report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Tickets.Open != 1 || report.Tickets.ExpiredNotDeleted != 1 || report.Tickets.OldestWaitSeconds < 25*3600 || report.Tickets.OldestWaitSeconds > 25*3600+60 || !report.Tickets.Enabled ||
		!slices.Contains(report.Warnings, "tickets_waiting_over_24h") || !slices.Contains(report.Warnings, "tickets_expiry_lagging") || slices.Contains(report.MissingTables, "tickets") {
		t.Fatalf("doctor %+v %v %v", report.Tickets, report.Warnings, report.MissingTables)
	}
	doctor, _ := json.Marshal(report)

	var audits []model.AdminAudit
	e.db.Where("action IN ?", []string{AuditTicketReply, AuditTicketClose}).Find(&audits)
	if len(audits) != 2 {
		t.Fatalf("%d audits", len(audits))
	}
	mu.Lock()
	captured := logs.String()
	mu.Unlock()
	for _, secret := range []string{subject, body, reply, note, u.Email, tk.Ticket.TicketNo} {
		if strings.Contains(captured, secret) || bytes.Contains(doctor, []byte(secret)) {
			t.Fatalf("%q leaked to the log or doctor", secret)
		}
		for _, a := range audits {
			if strings.Contains(a.Detail, secret) {
				t.Fatalf("%q leaked to the audit", secret)
			}
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
