package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"vpn/backend/internal/model"
)

// Round 5 of handoff_payment_account.md: announcements (R5-01, R5-02).

type announcementList struct {
	Announcements []consoleAnnouncement `json:"announcements"`
	Total         int64                 `json:"total"`
	Audience      int64                 `json:"audience"`
}

func (e *consoleEnv) draft(title, body string) consoleAnnouncement {
	e.t.Helper()
	var out struct {
		Announcement consoleAnnouncement `json:"announcement"`
	}
	e.decode(e.post("/announcements", map[string]any{"title": title, "body": body}), 201, &out)
	return out.Announcement
}

func (e *consoleEnv) transition(id, action string, status int) consoleAnnouncement {
	e.t.Helper()
	var out struct {
		Announcement consoleAnnouncement `json:"announcement"`
	}
	w := e.post("/announcements/"+id+"/"+action, map[string]any{})
	e.decode(w, status, &out)
	return out.Announcement
}

func (e *consoleEnv) pendingFor(b *browser) []announcementView {
	e.t.Helper()
	var out struct {
		Announcements []announcementView `json:"announcements"`
	}
	e.decode(b.get("/me/announcements"), 200, &out)
	return out.Announcements
}

func titles(list []announcementView) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Title
	}
	return out
}

// R5-01
func TestAnnouncementLifecycle(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u1, u2 := e.user("reader1", "user"), e.user("reader2", "user")
	phone, laptop := e.browser(u1.cookies), e.loginAgain(u1)
	second := e.browser(u2.cookies)

	// Access: users and visitors cannot touch the console side.
	e.code(phone.get("/admin/console/announcements"), 403, "admin_required")
	e.want(e.call(nil, "GET", "/api/v1/admin/console/announcements", nil, ""), 401)
	e.want(e.call(nil, "GET", "/api/v1/me/announcements", nil, ""), 401)

	a := e.draft("第一条", "第一条正文\n第二行")
	b := e.draft("第二条", "第二条正文")
	if len(e.pendingFor(phone)) != 0 {
		t.Fatal("a draft is visible")
	}
	// Drafts can be edited and deleted; the version guards edits.
	var edited struct {
		Announcement consoleAnnouncement `json:"announcement"`
	}
	e.decode(e.post("/announcements/"+a.ID, map[string]any{"title": "第一条（改）", "body": "新正文", "version": a.Version}), 200, &edited)
	e.code(e.post("/announcements/"+a.ID, map[string]any{"title": "旧表单", "body": "x", "version": a.Version}), 409, "version_conflict")
	a = edited.Announcement
	trash := e.draft("要删的草稿", "x")
	e.want(e.call(e.admin.cookies, "DELETE", "/api/v1/admin/console/announcements/"+trash.ID, map[string]any{}, ""), 200)
	e.want(e.get("/announcements/"+trash.ID+"/reads"), 404)

	// Published in order: a first, b later. Users see them oldest first.
	e.transition(a.ID, "publish", 200)
	e.clock.Advance(time.Minute)
	e.transition(b.ID, "publish", 200)
	e.code(e.post("/announcements/"+a.ID+"/publish", map[string]any{}), 409, "announcement_state")
	if got := titles(e.pendingFor(phone)); strings.Join(got, ",") != "第一条（改）,第二条" {
		t.Fatalf("order %v", got)
	}
	// Published text is locked; only archive (or copy) remains. Delete only for drafts.
	e.code(e.post("/announcements/"+a.ID, map[string]any{"title": "偷改", "body": "x", "version": 2}), 409, "announcement_locked")
	e.code(e.call(e.admin.cookies, "DELETE", "/api/v1/admin/console/announcements/"+a.ID, map[string]any{}, ""), 409, "announcement_state")

	// Acknowledging: idempotent, per user, visible on every device of that user.
	e.want(phone.post("/me/announcements/"+a.ID+"/ack", map[string]any{}), 200)
	e.want(phone.post("/me/announcements/"+a.ID+"/ack", map[string]any{}), 200)
	if got := titles(e.pendingFor(laptop)); strings.Join(got, ",") != "第二条" {
		t.Fatalf("other device sees %v", got)
	}
	if got := titles(e.pendingFor(second)); len(got) != 2 {
		t.Fatalf("another user must still see both: %v", got)
	}
	e.code(phone.post("/me/announcements/"+uuid.NewString()+"/ack", map[string]any{}), 404, "announcement_not_found")
	e.code(phone.post("/me/announcements/junk/ack", map[string]any{}), 404, "announcement_not_found")
	d := e.draft("草稿", "x")
	e.code(phone.post("/me/announcements/"+d.ID+"/ack", map[string]any{}), 404, "announcement_not_found")
	e.want(second.post("/me/announcements/"+a.ID+"/ack", map[string]any{}), 200)

	// Counts are accurate: two acknowledged a, nobody b.
	var list announcementList
	e.decode(e.get("/announcements?page_size=50"), 200, &list)
	counts := map[string]int64{}
	for _, row := range list.Announcements {
		counts[row.ID] = row.ReadCount
	}
	if counts[a.ID] != 2 || counts[b.ID] != 0 || list.Total != 3 || list.Audience != 3 {
		t.Fatalf("counts %v total %d audience %d", counts, list.Total, list.Audience)
	}
	var reads struct {
		Reads []struct {
			Email string `json:"email"`
		} `json:"reads"`
		Total int64 `json:"total"`
	}
	e.decode(e.get("/announcements/"+a.ID+"/reads"), 200, &reads)
	if reads.Total != 2 || len(reads.Reads) != 2 {
		t.Fatalf("reads %+v", reads)
	}

	// Taking one offline stops pushing it; a late click on it records nothing.
	e.transition(b.ID, "archive", 200)
	e.code(e.post("/announcements/"+b.ID+"/archive", map[string]any{}), 409, "announcement_state")
	if got := e.pendingFor(second); len(got) != 0 {
		t.Fatalf("archived announcement still pushed: %v", titles(got))
	}
	e.want(phone.post("/me/announcements/"+b.ID+"/ack", map[string]any{}), 200)
	var n int64
	e.db.Model(&model.AnnouncementRead{}).Where("announcement_id = ?", b.ID).Count(&n)
	if n != 0 {
		t.Fatal("an archived announcement collected an acknowledgement")
	}
	for _, action := range []string{AuditAnnouncementCreate, AuditAnnouncementUpdate, AuditAnnouncementPublish, AuditAnnouncementArchive, AuditAnnouncementDelete} {
		var rows int64
		e.db.Model(&model.AdminAudit{}).Where("action = ?", action).Count(&rows)
		if rows == 0 {
			t.Fatalf("no audit row for %s", action)
		}
	}
	// A new account sees what is published and not yet acknowledged, at most 20, oldest first.
	for i := range 22 {
		x := e.draft(strings.Repeat("批", 1)+string(rune('A'+i%26)), "内容")
		e.clock.Advance(time.Second)
		e.transition(x.ID, "publish", 200)
	}
	if got := e.pendingFor(e.browser(e.user("newcomer", "user").cookies)); len(got) != 20 || got[0].Title != "第一条（改）" {
		t.Fatalf("newcomer sees %d, first %v", len(got), titles(got)[:1])
	}
}

// R5-02
func TestAnnouncementText(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("textreader", "user")
	phone := e.browser(u.cookies)
	// Markup is only text: stored and returned unchanged, never interpreted.
	script := `<script>alert(1)</script><img src=x onerror=alert(1)> **bold** [x](javascript:alert(1))`
	a := e.draft("<b>标题</b>", script)
	e.transition(a.ID, "publish", 200)
	got := e.pendingFor(phone)
	if len(got) != 1 || got[0].Body != script || got[0].Title != "<b>标题</b>" {
		t.Fatalf("text changed: %+v", got)
	}
	// 5000 Chinese characters fit through the whole router (the default body limit is 8 KB).
	long := strings.Repeat("汉", 5000)
	w := e.post("/announcements", map[string]any{"title": "长公告", "body": long})
	e.want(w, 201)
	if len(w.Body.Bytes()) < 15000 {
		t.Fatal("unexpectedly small body")
	}
	// One more character, an empty or over-long title, control characters and unknown fields are refused.
	for _, bad := range []map[string]any{
		{"title": "长", "body": long + "字"},
		{"title": "", "body": "x"},
		{"title": "  ", "body": "x"},
		{"title": strings.Repeat("题", 101), "body": "x"},
		{"title": "换\n行", "body": "x"},
		{"title": "t", "body": "bell\x07"},
		{"title": "t", "body": ""},
		{"title": "t", "body": "x", "status": "published"},
	} {
		e.code(e.post("/announcements", bad), 400, map[bool]string{true: "invalid_json", false: "invalid_announcement"}[bad["status"] != nil])
	}
	exactTitle := strings.Repeat("题", 100)
	e.want(e.post("/announcements", map[string]any{"title": exactTitle, "body": "x"}), 201)
	// A body over the raised limit is refused before anything is stored.
	var before int64
	e.db.Model(&model.Announcement{}).Count(&before)
	huge := e.post("/announcements", map[string]any{"title": "巨", "body": strings.Repeat("a", announcementBodyLimit)})
	if huge.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: %d", huge.Code)
	}
	var after int64
	e.db.Model(&model.Announcement{}).Count(&after)
	if before != after {
		t.Fatal("an oversized announcement was stored")
	}
	// Other admin endpoints keep the small limit.
	small := e.post("/coupons", map[string]any{"code": "TOOBIG", "note": strings.Repeat("a", 9000)})
	if small.Code == 201 {
		t.Fatal("the raised limit leaked to other routes")
	}
}
