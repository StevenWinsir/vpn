package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
)

// Round 3 of handoff_ticket.md (T3-04, T3-05): the security review of support tickets, kept
// as executable checks. Ownership (404 for a stranger's ticket, byte-identical to a missing
// one), the field whitelist without any administrator identity, impersonation, the switch,
// rate limits and the privacy of logs and audits are pinned by round 1 (tickets_test.go);
// the pieces below are the ones only a review of the whole feature asks for.

// rawTicketCall sends exactly the bytes, origin and content type given.
func rawTicketCall(e *consoleEnv, cookies []*http.Cookie, path string, body []byte, origin, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Idempotency-Key", uuid.NewString())
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// padded is a JSON document followed by spaces up to exactly size bytes (whitespace after the
// value is valid JSON, so only the size decides).
func padded(t *testing.T, value any, size int) []byte {
	t.Helper()
	data, _ := json.Marshal(value)
	if len(data) > size {
		t.Fatalf("document already %d bytes", len(data))
	}
	return append(data, bytes.Repeat([]byte(" "), size-len(data))...)
}

// T3-04: every ticket write needs a trusted Origin, JSON and a session; the 32 KB limit holds
// exactly at its edge for the user and the console prefixes.
func TestTicketRequestBoundaries(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("boundary", "user")
	tk := e.newTicket(u, "边界检查")
	origin := e.cfg.Origins[0]
	writes := []struct {
		cookies []*http.Cookie
		path    string
		body    any
	}{
		{u.cookies, "/api/v1/me/tickets", ticketBody("不应创建")},
		{u.cookies, "/api/v1/me/tickets/" + tk.ID + "/messages", map[string]any{"body": "不应追加"}},
		{u.cookies, "/api/v1/me/tickets/" + tk.ID + "/close", map[string]any{}},
		{u.cookies, "/api/v1/me/tickets/" + tk.ID + "/read", map[string]any{}},
		{e.admin.cookies, "/api/v1/admin/console/tickets/" + tk.ID + "/messages", map[string]any{"body": "不应回复"}},
		{e.admin.cookies, "/api/v1/admin/console/tickets/" + tk.ID + "/close", map[string]any{"version": 1}},
	}
	for _, write := range writes {
		data, _ := json.Marshal(write.body)
		e.code(rawTicketCall(e, write.cookies, write.path, data, "", "application/json"), 403, "origin_required")
		e.code(rawTicketCall(e, write.cookies, write.path, data, "https://evil.example", "application/json"), 403, "origin_forbidden")
		e.code(rawTicketCall(e, write.cookies, write.path, data, origin, "text/plain"), 415, "json_required")
		e.code(rawTicketCall(e, write.cookies, write.path, data, origin, "application/x-www-form-urlencoded"), 415, "json_required")
		if w := rawTicketCall(e, nil, write.path, data, origin, "application/json"); w.Code != 401 {
			t.Fatalf("%s without a session: %d", write.path, w.Code)
		}
	}
	// Reads need a session as well.
	for _, path := range []string{"/api/v1/me/tickets", "/api/v1/me/tickets/summary", "/api/v1/me/tickets/" + tk.ID,
		"/api/v1/admin/console/tickets", "/api/v1/admin/console/tickets/summary", "/api/v1/admin/console/tickets/" + tk.ID} {
		if w := e.call(nil, "GET", path, nil, ""); w.Code != 401 {
			t.Fatalf("%s without a session: %d", path, w.Code)
		}
	}
	row := e.ticketRow(tk.ID)
	var count int64
	e.db.Model(&model.Ticket{}).Where("user_id = ?", u.ID).Count(&count)
	if count != 1 || row.Status != model.TicketOpen || row.MessageCount != 1 || row.Version != 1 {
		t.Fatalf("a refused request changed something: %d tickets, %+v", count, row)
	}

	// The 32 KB edge, past the 8 KB default: 3000 characters of text plus padding.
	long := strings.Repeat("长", 3000)
	at := rawTicketCall(e, u.cookies, "/api/v1/me/tickets", padded(t, map[string]any{"category": "other", "subject": "恰好上限", "body": long}, ticketBodyLimit), origin, "application/json")
	e.want(at, 201)
	over := rawTicketCall(e, u.cookies, "/api/v1/me/tickets", padded(t, map[string]any{"category": "other", "subject": "超出上限", "body": long}, ticketBodyLimit+1), origin, "application/json")
	e.code(over, 400, "invalid_json")
	e.want(rawTicketCall(e, e.admin.cookies, "/api/v1/admin/console/tickets/"+tk.ID+"/messages", padded(t, map[string]any{"body": long}, ticketBodyLimit), origin, "application/json"), 201)
	e.code(rawTicketCall(e, e.admin.cookies, "/api/v1/admin/console/tickets/"+tk.ID+"/messages", padded(t, map[string]any{"body": long}, ticketBodyLimit+1), origin, "application/json"), 400, "invalid_json")
	e.db.Model(&model.Ticket{}).Where("user_id = ?", u.ID).Count(&count)
	if count != 2 || e.ticketRow(tk.ID).MessageCount != 2 {
		t.Fatalf("the edge: %d tickets, %d messages", count, e.ticketRow(tk.ID).MessageCount)
	}
	// Outside the ticket prefixes the default limit still applies.
	e.code(rawTicketCall(e, u.cookies, "/api/v1/me/password", padded(t, map[string]any{"current_password": "x", "new_password": "y"}, 8193), origin, "application/json"), 400, "invalid_json")
}

// T3-04: markup a user or support writes is data. It is stored as written, every answer is
// JSON with the markup escaped and nosniff set, so no browser reads it as HTML.
func TestTicketMarkupStaysText(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("markup", "user")
	const subject = `<img src=x onerror=alert(1)>标题`
	body := "<script>alert(1)</script>\n<a href=\"javascript:alert(2)\">点我</a>\n<b onmouseover=\"alert(3)\">粗</b> https://example.com/?a=<x>"
	var created ticketAnswer
	e.decode(e.submit(u, map[string]any{"category": "other", "subject": subject, "body": body}), 201, &created)
	reply := `<iframe src="javascript:alert(4)"></iframe>`
	e.want(e.adminReply(e.admin, created.Ticket.ID, reply), 201)
	var stored []model.TicketMessage
	e.db.Order("position").Find(&stored, "ticket_id = ?", created.Ticket.ID)
	if e.ticketRow(created.Ticket.ID).Subject != subject || len(stored) != 2 || stored[0].Body != body || stored[1].Body != reply {
		t.Fatal("markup was not stored as written")
	}
	for _, w := range []*httptest.ResponseRecorder{
		e.call(u.cookies, "GET", "/api/v1/me/tickets", nil, ""),
		e.call(u.cookies, "GET", "/api/v1/me/tickets/"+created.Ticket.ID, nil, ""),
		e.get("/tickets?status=all"),
		e.get("/tickets/" + created.Ticket.ID),
	} {
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
		raw := w.Body.String()
		if strings.ContainsAny(raw, "<>") || !strings.Contains(raw, `\u003cimg src=x onerror=alert(1)\u003e`) {
			t.Fatalf("markup reaches the response unescaped: %s", raw)
		}
	}
	var detail ticketAnswer
	e.decode(e.call(u.cookies, "GET", "/api/v1/me/tickets/"+created.Ticket.ID, nil, ""), 200, &detail)
	if detail.Ticket.Subject != subject || detail.Ticket.Messages[0].Body != body || detail.Ticket.Messages[1].Body != reply {
		t.Fatal("the text read back differs from what was written")
	}
}

// T3-05: the readiness report carries the ticket checks in a development and in a production
// configuration, warns about a long wait and a lagging expiry, and shows counts only.
func TestTicketReadinessInBothConfigurations(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"development": func(c *config.Config) { c.Env = "development" },
		"production": func(c *config.Config) {
			c.Env, c.CookieSecure, c.SSLMode, c.TestPurchase = "production", true, "verify-full", false
			c.Payment.TestPrice, c.Payment.TestEmails = false, nil
			c.TrustedProxies = []string{"10.0.0.1"}
		},
		"production, switched off": func(c *config.Config) {
			c.Env, c.CookieSecure, c.SSLMode, c.TestPurchase = "production", true, "verify-full", false
			c.Payment.TestPrice, c.Payment.TestEmails = false, nil
			c.Tickets.Disabled = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newConsoleEnv(t, mutate)
			if (e.cfg.Env == "production") != strings.HasPrefix(name, "production") || e.cfg.TicketsEnabled() == strings.HasSuffix(name, "off") {
				t.Fatalf("configuration mix-up in %s", name)
			}
			u, v := e.user("rdy-a", "user"), e.user("rdy-b", "user")
			const subject, body = "就绪检查标题", "就绪检查正文 readiness-secret"
			var tickets []ticketDetail
			for _, owner := range []payUser{u, u, v} {
				var out ticketAnswer
				w := e.submit(owner, map[string]any{"category": "connection", "subject": subject, "body": body})
				if e.cfg.TicketsEnabled() {
					e.decode(w, 201, &out)
				} else {
					// Switched off nobody submits; the rows an earlier release left are still counted.
					e.code(w, 403, "tickets_disabled")
					out.Ticket = e.seedTicket(owner, subject, body)
				}
				tickets = append(tickets, out.Ticket)
			}
			e.want(e.adminReply(e.admin, tickets[2].ID, "就绪回复"), 201)
			inspect := func() (operations.ReadinessReport, string) {
				t.Helper()
				report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(report)
				return report, string(raw)
			}
			report, _ := inspect()
			if report.Tickets.Enabled != e.cfg.TicketsEnabled() || report.Tickets.Open != 2 || report.Tickets.Answered != 1 || report.Tickets.ExpiredNotDeleted != 0 ||
				slices.Contains(report.Warnings, "tickets_waiting_over_24h") || slices.Contains(report.Warnings, "tickets_expiry_lagging") || len(report.MissingTables) != 0 {
				t.Fatalf("%s: %+v %v", name, report.Tickets, report.Warnings)
			}
			// The doctor reads the real clock.
			real := time.Now().UTC()
			e.db.Model(&model.Ticket{}).Where("id = ?", tickets[0].ID).Update("waiting_since", real.Add(-25*time.Hour))
			e.db.Model(&model.Ticket{}).Where("id = ?", tickets[1].ID).Update("expires_at", real.Add(-2*time.Hour))
			report, raw := inspect()
			if report.Tickets.Open != 1 || report.Tickets.ExpiredNotDeleted != 1 || report.Tickets.OldestWaitSeconds < 25*3600 ||
				!slices.Contains(report.Warnings, "tickets_waiting_over_24h") || !slices.Contains(report.Warnings, "tickets_expiry_lagging") {
				t.Fatalf("%s: warnings missing %+v %v", name, report.Tickets, report.Warnings)
			}
			// One hour is the grace: an expiry 30 minutes old is not lagging yet.
			e.db.Model(&model.Ticket{}).Where("id = ?", tickets[1].ID).Update("expires_at", real.Add(-30*time.Minute))
			if report, _ := inspect(); report.Tickets.ExpiredNotDeleted != 0 || slices.Contains(report.Warnings, "tickets_expiry_lagging") {
				t.Fatalf("%s: lag reported inside the grace %+v", name, report.Tickets)
			}
			leaks := []string{subject, body, "就绪回复", "readiness-secret"}
			for _, tk := range tickets {
				leaks = append(leaks, tk.ID, tk.TicketNo)
			}
			for _, person := range []payUser{u, v, e.admin} {
				leaks = append(leaks, person.ID, person.Email)
			}
			for _, leak := range leaks {
				if strings.Contains(raw, leak) {
					t.Fatalf("%s: the report carries %q", name, leak)
				}
			}
		})
	}
}

// seedTicket writes a ticket row and its first message the way the API would.
func (e *consoleEnv) seedTicket(owner payUser, subject, body string) ticketDetail {
	e.t.Helper()
	now := e.clock.Now()
	t := model.Ticket{ID: uuid.NewString(), TicketNo: fmt.Sprintf("T%s%06d", now.Format("060102"), uuid.New().ID()%1000000), UserID: owner.ID, IdempotencyKey: uuid.NewString(), Category: "connection",
		Subject: subject, Status: model.TicketOpen, MessageCount: 1, WaitingSince: &now, LastMessageAt: now, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(model.TicketRetention)}
	if err := e.db.Create(&t).Error; err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.Create(&model.TicketMessage{ID: uuid.NewString(), TicketID: t.ID, AuthorKind: model.TicketAuthorUser, AuthorID: owner.ID, Body: body, Position: 1, CreatedAt: now}).Error; err != nil {
		e.t.Fatal(err)
	}
	return ticketDetail{ticketItem: newTicketItem(t)}
}
