package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/operations"
	"vpn/backend/internal/payment"
)

// Round 1 of handoff_shop.md (S1-02 … S1-10): the shop backend against the real router
// and a private PostgreSQL.

func intPtr(v int) *int { return &v }

// S1-02: the yearly price rule, in whole cents, rounded down.
func TestShopYearlyPriceMatrix(t *testing.T) {
	cases := []struct {
		monthly                       int64
		pct                           int
		original, price, monthlyEquiv int64
	}{
		{1990, 90, 23880, 21492, 1791}, {1990, 85, 23880, 20298, 1691}, {1990, 100, 23880, 23880, 1990}, {1990, 50, 23880, 11940, 995},
		{3990, 90, 47880, 43092, 3591}, {3990, 85, 47880, 40698, 3391}, {3990, 100, 47880, 47880, 3990}, {3990, 50, 47880, 23940, 1995},
		{7990, 90, 95880, 86292, 7191}, {7990, 85, 95880, 81498, 6791}, {7990, 100, 95880, 95880, 7990}, {7990, 50, 95880, 47940, 3995},
		// 23988 x 85% = 20389.8: the fraction of a cent is dropped, in the buyer's favour.
		{1999, 85, 23988, 20389, 1699},
		{1, 50, 12, 6, 0},
	}
	for _, c := range cases {
		original, price, equiv := yearlyPrice(c.monthly, c.pct)
		if original != c.original || price != c.price || equiv != c.monthlyEquiv {
			t.Errorf("%d@%d: got %d %d %d want %d %d %d", c.monthly, c.pct, original, price, equiv, c.original, c.price, c.monthlyEquiv)
		}
	}
	base := model.Plan{ID: "p", PriceCents: 3990, DurationDays: 30, TrafficBytes: 300 * gib, YearlyEnabled: true, YearlyDurationDays: 365}
	if terms, err := termsFor(base, model.CycleYearly, 90); err != nil || terms.PriceCents != 43092 || terms.SavingCents != 4788 || terms.TrafficBytes != 3600*gib || terms.DiscountPercent != 90 || terms.DurationDays != 365 {
		t.Fatalf("follow global %+v %v", terms, err)
	}
	override := base
	override.YearlyDiscountPercent = intPtr(80)
	if terms, err := termsFor(override, model.CycleYearly, 90); err != nil || terms.PriceCents != 38304 || terms.DiscountPercent != 80 {
		t.Fatalf("override %+v %v", terms, err)
	}
	own := base
	own.YearlyTrafficBytes = new(int64)
	*own.YearlyTrafficBytes = 1000 * gib
	if terms, _ := termsFor(own, model.CycleYearly, 90); terms.TrafficBytes != 1000*gib {
		t.Fatalf("own yearly traffic %+v", terms)
	}
	for _, global := range []int{49, 101, 0, -5} {
		if _, err := termsFor(base, model.CycleYearly, global); err != errCycleUnavailable {
			t.Errorf("global %d accepted: %v", global, err)
		}
	}
	off := base
	off.YearlyEnabled = false
	big := base
	big.TrafficBytes = maxPlanBytes/12 + 1
	for name, plan := range map[string]model.Plan{"off": off, "oversize": big} {
		if _, err := termsFor(plan, model.CycleYearly, 90); err != errCycleUnavailable {
			t.Errorf("%s: %v", name, err)
		}
	}
	if terms, err := termsFor(off, model.CycleMonthly, 90); err != nil || terms.PriceCents != 3990 || terms.DiscountPercent != 100 || terms.OriginalCents != 3990 {
		t.Fatalf("monthly %+v %v", terms, err)
	}
	if _, ok := parseCycle("weekly"); ok {
		t.Fatal("unknown cycle accepted")
	}
}

// planBody is a valid create request; tests override single fields.
func planBody(over map[string]any) map[string]any {
	b := map[string]any{"name": "E2E 套餐", "description": "给测试用", "note": "内部备注-不可外泄", "price_cents": 1000, "duration_days": 30, "traffic_bytes": 10 * gib,
		"max_devices": 3, "allow_dedicated": false, "yearly_enabled": true, "yearly_duration_days": 365, "featured": false, "sort": 50, "active": false}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (e *consoleEnv) createPlan(over map[string]any) map[string]any {
	e.t.Helper()
	out := e.want(e.post("/plans", planBody(over)), 201)
	return out["plan"].(map[string]any)
}

// editPlan posts the plan back with changes, at the version it was read at.
func (e *consoleEnv) editPlan(plan map[string]any, over map[string]any) *httptest.ResponseRecorder {
	body := map[string]any{}
	for _, k := range []string{"name", "description", "note", "price_cents", "duration_days", "traffic_bytes", "max_devices", "allow_dedicated", "yearly_enabled", "yearly_duration_days",
		"yearly_traffic_bytes", "yearly_discount_percent", "featured", "sort", "active", "version"} {
		body[k] = plan[k]
	}
	for k, v := range over {
		body[k] = v
	}
	return e.post("/plans/"+plan["id"].(string), body)
}

func jsonNum(v any) int64 { f, _ := v.(float64); return int64(f) }

func (e *consoleEnv) adminPlans() map[string]map[string]any {
	e.t.Helper()
	out := e.want(e.get("/plans?include_deleted=true"), 200)
	byID := map[string]map[string]any{}
	for _, p := range out["plans"].([]any) {
		m := p.(map[string]any)
		byID[m["id"].(string)] = m
	}
	return byID
}

// S1-08 + S1-09 (audit) + D9: administrator plan lifecycle.
func TestShopAdminPlanLifecycle(t *testing.T) {
	e := newConsoleEnv(t, nil)
	badName := strings.Repeat("长", 41)
	for _, c := range []struct {
		over  map[string]any
		field string
	}{
		{map[string]any{"name": "  "}, "name"}, {map[string]any{"name": badName}, "name"}, {map[string]any{"description": strings.Repeat("a", 301)}, "description"},
		{map[string]any{"note": strings.Repeat("a", 201)}, "note"}, {map[string]any{"price_cents": 0}, "price_cents"}, {map[string]any{"price_cents": -5}, "price_cents"},
		{map[string]any{"price_cents": maxMonthlyCents + 1}, "price_cents"}, {map[string]any{"duration_days": 0}, "duration_days"}, {map[string]any{"duration_days": 366}, "duration_days"},
		{map[string]any{"traffic_bytes": 0}, "traffic_bytes"}, {map[string]any{"traffic_bytes": maxPlanBytes + 1}, "traffic_bytes"}, {map[string]any{"max_devices": 0}, "max_devices"},
		{map[string]any{"max_devices": 101}, "max_devices"}, {map[string]any{"sort": 10001}, "sort"}, {map[string]any{"yearly_duration_days": 400}, "yearly_duration_days"},
		{map[string]any{"yearly_discount_percent": 9}, "yearly_discount_percent"}, {map[string]any{"yearly_discount_percent": 101}, "yearly_discount_percent"},
		{map[string]any{"yearly_traffic_bytes": 0}, "yearly_traffic_bytes"},
		// 12 x the monthly allowance is over the cap and no yearly allowance was given.
		{map[string]any{"traffic_bytes": maxPlanBytes/12 + 1}, "yearly_traffic_bytes"},
		{map[string]any{"id": "Upper"}, "id"}, {map[string]any{"id": "x"}, "id"}, {map[string]any{"id": "-bad"}, "id"}, {map[string]any{"id": "has space"}, "id"},
	} {
		w := e.post("/plans", planBody(c.over))
		out := e.want(w, 400)
		errBody := out["error"].(map[string]any)
		if errBody["code"] != "invalid_plan" || errBody["field"] != c.field {
			t.Errorf("%v: got %v", c.over, errBody)
		}
	}
	e.code(e.post("/plans", map[string]any{"name": "x", "bogus": 1}), 400, "invalid_json")

	// The same oversize monthly allowance is fine once yearly is off or has its own value.
	huge := e.createPlan(map[string]any{"id": "huge-ok", "traffic_bytes": maxPlanBytes/12 + 1, "yearly_enabled": false})
	if huge["yearly"] != nil {
		t.Fatalf("yearly shown for a plan with yearly off: %v", huge["yearly"])
	}

	created := e.createPlan(map[string]any{"id": "e2e-one"})
	if created["id"] != "e2e-one" || created["active"] != false || jsonNum(created["version"]) != 1 || created["yearly_enabled"] != true {
		t.Fatalf("created %v", created)
	}
	yearly := created["yearly"].(map[string]any)
	if jsonNum(yearly["price_cents"]) != 10800 || jsonNum(yearly["original_cents"]) != 12000 || jsonNum(yearly["saving_cents"]) != 1200 || jsonNum(yearly["monthly_equivalent_cents"]) != 900 || jsonNum(yearly["traffic_bytes"]) != 120*gib {
		t.Fatalf("derived yearly %v", yearly)
	}
	e.code(e.post("/plans", planBody(map[string]any{"id": "e2e-one"})), 409, "plan_exists")
	generated := e.createPlan(map[string]any{"yearly_enabled": false})
	if id := generated["id"].(string); !strings.HasPrefix(id, "plan-") || len(id) != 11 || generated["yearly_enabled"] != false {
		t.Fatalf("generated id %q / %v", id, generated["yearly_enabled"])
	}

	// A plan that is not on sale is invisible to the shop and cannot be bought.
	u := e.user("shopper", "user")
	if strings.Contains(e.call(nil, "GET", "/api/v1/plans", nil, "").Body.String(), "e2e-one") {
		t.Fatal("an inactive plan is public")
	}
	e.code(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "e2e-one"}, ""), 404, "plan_not_found")

	// Edit: version, no-change, activation.
	e.code(e.editPlan(created, map[string]any{}), 400, "no_change")
	e.code(e.editPlan(created, map[string]any{"version": 7, "price_cents": 2000}), 409, "version_conflict")
	e.code(e.editPlan(created, map[string]any{"price_cents": 0}), 400, "invalid_plan")
	e.code(e.post("/plans/e2e-one", planBody(map[string]any{"id": "other", "version": 1})), 400, "invalid_plan")
	e.code(e.post("/plans/not-there", planBody(map[string]any{"version": 1})), 404, "plan_not_found")
	updated := e.want(e.editPlan(created, map[string]any{"price_cents": 2000, "active": true, "note": "新的内部备注"}), 200)["plan"].(map[string]any)
	if jsonNum(updated["version"]) != 2 || jsonNum(updated["price_cents"]) != 2000 || updated["active"] != true {
		t.Fatalf("updated %v", updated)
	}
	e.code(e.editPlan(created, map[string]any{"price_cents": 2500}), 409, "version_conflict") // the stale form
	if !strings.Contains(e.call(nil, "GET", "/api/v1/plans", nil, "").Body.String(), "e2e-one") {
		t.Fatal("an active plan is not public")
	}

	// A yearly override of null clears it again; writes keep false values.
	withOverride := e.want(e.editPlan(updated, map[string]any{"yearly_discount_percent": 80, "yearly_enabled": false}), 200)["plan"].(map[string]any)
	if withOverride["yearly_enabled"] != false || jsonNum(withOverride["yearly_discount_percent"]) != 80 || withOverride["yearly_discount_source"] != "plan" {
		t.Fatalf("override %v", withOverride)
	}
	cleared := e.want(e.editPlan(withOverride, map[string]any{"yearly_discount_percent": nil, "yearly_enabled": true}), 200)["plan"].(map[string]any)
	if cleared["yearly_discount_percent"] != nil || cleared["yearly_discount_source"] != "global" || cleared["yearly_enabled"] != true {
		t.Fatalf("cleared %v", cleared)
	}

	// Delete rules: off the shelf, no valid subscription, no live pending order, no group.
	deleteBody := func(p map[string]any) map[string]any { return map[string]any{"version": p["version"]} }
	inUse := func(p map[string]any, want string) map[string]any {
		t.Helper()
		out := e.want(e.post("/plans/"+p["id"].(string)+"/delete", deleteBody(p)), 409)
		errBody := out["error"].(map[string]any)
		if errBody["code"] != "plan_in_use" || !strings.Contains(errBody["message"].(string), want) {
			t.Fatalf("want in-use %q got %v", want, errBody)
		}
		return errBody
	}
	inUse(cleared, "尚未下架")
	off := e.want(e.editPlan(cleared, map[string]any{"active": false}), 200)["plan"].(map[string]any)

	group := model.NodeGroup{ID: uuid.NewString(), Name: "JP-线路", Region: "JP", LineType: "direct", RatePermille: 1000, PlanIDs: `["e2e-one"]`, Enabled: true, Version: 1, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	if err := e.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if blocked := inUse(off, "JP-线路"); blocked["groups"].([]any)[0] != "JP-线路" {
		t.Fatalf("groups %v", blocked)
	}
	listed := e.adminPlans()["e2e-one"]
	if jsonNum(listed["group_count"]) != 1 || listed["groups"].([]any)[0] != "JP-线路" {
		t.Fatalf("list groups %v", listed)
	}
	e.db.Delete(&group)

	e.want(e.post("/plans", planBody(map[string]any{"id": "e2e-two", "active": true})), 201)
	buyer := e.user("buyer", "user")
	e.want(e.post("/plans/e2e-two", map[string]any{}), 400) // body without terms is invalid
	pending := e.place(buyer, "e2e-two", payment.TypeWeChat, "")
	two := e.adminPlans()["e2e-two"]
	if jsonNum(two["pending_orders"]) != 1 {
		t.Fatalf("pending count %v", two["pending_orders"])
	}
	offTwo := e.want(e.editPlan(two, map[string]any{"active": false}), 200)["plan"].(map[string]any)
	inUse(offTwo, "待付订单 1 笔")
	opened := mustOpen(t, e, pending)
	e.clock.Advance(10 * time.Minute) // the pending window passes; the order is no longer live
	e.want(e.pay(opened), 200)
	if sub := e.subscription(buyer.ID); sub == nil || sub.PlanID != "e2e-two" {
		t.Fatalf("late payment %+v", sub)
	}
	inUse(offTwo, "有效订阅 1 个")
	e.clock.Advance(40 * 24 * time.Hour)
	e.want(e.post("/plans/e2e-two/delete", deleteBody(offTwo)), 200)

	// Soft delete: the row and its history stay, the id is never reusable.
	var row model.Plan
	if err := e.db.First(&row, "id = ?", "e2e-two").Error; err != nil || row.DeletedAt == nil || row.Active || row.Version != jsonNum(offTwo["version"])+1 {
		t.Fatalf("soft-deleted row %+v %v", row, err)
	}
	if _, listedAny := e.adminPlansDefault()["e2e-two"]; listedAny {
		t.Fatal("deleted plan in the default list")
	}
	if e.adminPlans()["e2e-two"]["deleted_at"] == nil {
		t.Fatal("deleted plan missing from the include_deleted list")
	}
	e.code(e.post("/plans", planBody(map[string]any{"id": "e2e-two"})), 409, "plan_exists")
	e.code(e.post("/plans/e2e-two", planBody(map[string]any{"version": row.Version})), 404, "plan_not_found")
	e.code(e.post("/plans/e2e-two/delete", deleteBody(offTwo)), 404, "plan_not_found")
	e.code(e.call(buyer.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "e2e-two"}, ""), 404, "plan_not_found")
	e.want(e.post("/plans/e2e-one/delete", deleteBody(off)), 200)
	e.code(e.post("/plans/e2e-one/delete", deleteBody(off)), 404, "plan_not_found")

	// Groups and coupons may pick an inactive plan (D8) but never a deleted one.
	e.want(e.post("/plans", planBody(map[string]any{"id": "e2e-three"})), 201)
	if w := e.call(e.admin.cookies, "POST", "/api/v1/admin/console/coupons", couponBody("SHOP-THREE", []string{"e2e-three"}, "all"), ""); w.Code != 201 {
		t.Fatalf("coupon on an inactive plan: %d %s", w.Code, w.Body.String())
	}
	e.code(e.call(e.admin.cookies, "POST", "/api/v1/admin/console/coupons", couponBody("SHOP-DEAD", []string{"e2e-two"}, "all"), ""), 400, "invalid_coupon")
	if err := requirePlansExist(e.db, []string{"e2e-three"}); err != nil {
		t.Fatalf("inactive plan refused for a group: %v", err)
	}
	if err := requirePlansExist(e.db, []string{"e2e-two"}); err == nil {
		t.Fatal("deleted plan accepted for a group")
	}
	// The administrator can still grant a user an inactive plan, not a deleted one.
	e.want(e.post("/users/"+buyer.ID+"/subscription", map[string]any{"version": e.subscription(buyer.ID).Version, "plan_id": "e2e-three", "expires_at": e.clock.Now().AddDate(0, 0, 10)}), 200)
	e.code(e.post("/users/"+buyer.ID+"/subscription", map[string]any{"version": e.subscription(buyer.ID).Version, "plan_id": "e2e-two", "expires_at": e.clock.Now().AddDate(0, 0, 10)}), 400, "plan_not_found")

	// Audit: every write has one entry and the note text never appears.
	var audits []model.AdminAudit
	e.db.Where("action LIKE ?", "admin.plan_%").Order("created_at, id").Find(&audits)
	actions := map[string]int{}
	for _, a := range audits {
		actions[a.Action]++
		if strings.Contains(a.Detail, "内部备注") || strings.Contains(a.Detail, "新的内部备注") {
			t.Fatalf("audit leaks the note: %s", a.Detail)
		}
		if a.ActorID != e.admin.ID {
			t.Fatal("audit actor")
		}
	}
	if actions[AuditPlanCreate] < 4 || actions[AuditPlanUpdate] < 4 || actions[AuditPlanDelete] != 2 {
		t.Fatalf("audit counts %v", actions)
	}
	var update model.AdminAudit
	e.db.Where("action = ? AND detail LIKE ?", AuditPlanUpdate, "%price_cents%").First(&update)
	if !strings.Contains(update.Detail, `"to":2000`) || !strings.Contains(update.Detail, `"note":"changed"`) {
		t.Fatalf("update detail %s", update.Detail)
	}
}

func (e *consoleEnv) adminPlansDefault() map[string]map[string]any {
	e.t.Helper()
	out := e.want(e.get("/plans"), 200)
	byID := map[string]map[string]any{}
	for _, p := range out["plans"].([]any) {
		m := p.(map[string]any)
		byID[m["id"].(string)] = m
	}
	return byID
}

func mustOpen(t *testing.T, e *consoleEnv, p placed) *fakeGatewayOrder {
	t.Helper()
	gw, status := e.gw.open(t, p.PayURL)
	if status != 200 {
		t.Fatalf("gateway status %d", status)
	}
	return gw
}

func requirePlansExist(db *gorm.DB, ids []string) error { return requireActivePlans(db, ids) }

func couponBody(code string, plans []string, cycles string) map[string]any {
	return map[string]any{"code": code, "note": "", "discount_type": "percent", "percent_off": 50, "plan_ids": plans, "max_uses": 0, "per_user_limit": 5, "enabled": true, "billing_cycles": cycles}
}

// Plan and settings limits: 30 plans, 12 on sale.
func TestShopPlanLimits(t *testing.T) {
	e := newConsoleEnv(t, nil)
	// starter, pro and max are on sale: nine more fill the twelve slots.
	for i := range 9 {
		e.createPlan(map[string]any{"id": "fill-" + string(rune('a'+i)), "active": true})
	}
	e.code(e.post("/plans", planBody(map[string]any{"id": "one-too-many", "active": true})), 409, "plan_limit")
	inactive := e.createPlan(map[string]any{"id": "off-shelf"})
	e.code(e.editPlan(inactive, map[string]any{"active": true}), 409, "plan_limit")
	for i := range 30 - 13 {
		e.createPlan(map[string]any{"id": "bulk-" + strings.Repeat("x", i%3) + string(rune('a'+i))})
	}
	e.code(e.post("/plans", planBody(nil)), 409, "plan_limit")
}

// S1-09: who may touch the plan endpoints.
func TestShopAccessControl(t *testing.T) {
	e := newConsoleEnv(t, nil)
	plain := e.user("plain", "user")
	routes := []struct{ method, path string }{
		{"GET", "/plans"}, {"POST", "/plans"}, {"POST", "/plans/starter"}, {"POST", "/plans/starter/delete"}, {"GET", "/shop-settings"}, {"POST", "/shop-settings"},
	}
	for _, r := range routes {
		var body any
		if r.method == "POST" {
			body = map[string]any{}
		}
		if w := e.call(nil, r.method, "/api/v1/admin/console"+r.path, body, ""); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d", r.method, r.path, w.Code)
		}
		if w := e.call(plain.cookies, r.method, "/api/v1/admin/console"+r.path, body, ""); w.Code != 403 {
			t.Errorf("user %s %s: %d", r.method, r.path, w.Code)
		}
	}
	// An impersonation session of a plain user is refused too.
	admin := e.browser(e.admin.cookies)
	imp := e.impersonate(admin, plain.ID)
	if imp.Code != 200 {
		t.Fatalf("impersonate %d %s", imp.Code, imp.Body.String())
	}
	for _, r := range routes {
		var body any
		if r.method == "POST" {
			body = map[string]any{}
		}
		w := admin.do(r.method, "/api/v1/admin/console"+r.path, body)
		if w.Code != 403 {
			t.Errorf("impersonated %s %s: %d", r.method, r.path, w.Code)
		}
	}
}

// publicPlanFields is the whole public vocabulary (S1-10).
var publicPlanFields = []string{"allow_dedicated", "description", "duration_days", "featured", "id", "max_devices", "monthly", "name", "price_cents", "traffic_bytes", "yearly"}
var publicTierFields = []string{"discount_percent", "duration_days", "monthly_equivalent_cents", "original_cents", "price_cents", "saving_cents", "traffic_bytes"}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// S1-10
func TestShopPublicView(t *testing.T) {
	e := newConsoleEnv(t, func(c *config.Config) { c.ClientMaxOnlineDevices = 4 })
	e.createPlan(map[string]any{"id": "pub-on", "name": "公开套餐", "active": true, "featured": true, "max_devices": 9, "yearly_discount_percent": 80, "sort": 0})
	e.createPlan(map[string]any{"id": "pub-no-year", "active": true, "yearly_enabled": false, "sort": 4, "max_devices": 2})
	e.createPlan(map[string]any{"id": "pub-hidden", "active": false})
	gone := e.createPlan(map[string]any{"id": "pub-gone"})
	e.want(e.post("/plans/pub-gone/delete", map[string]any{"version": gone["version"]}), 200)

	w := e.call(nil, "GET", "/api/v1/plans", nil, "")
	body := w.Body.String()
	for _, secret := range []string{"内部备注", "pub-hidden", "pub-gone", "version", "sort", "note", "deleted_at", "active\""} {
		if strings.Contains(body, secret) {
			t.Fatalf("public catalogue leaks %q: %s", secret, body)
		}
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if got := sortedKeys(out); !slices.Equal(got, []string{"max_yearly_saving_percent", "plans", "yearly_discount_percent"}) {
		t.Fatalf("top-level fields %v", got)
	}
	if jsonNum(out["yearly_discount_percent"]) != 90 || jsonNum(out["max_yearly_saving_percent"]) != 20 {
		t.Fatalf("discounts %v", out)
	}
	plans := map[string]map[string]any{}
	order := []string{}
	for _, p := range out["plans"].([]any) {
		m := p.(map[string]any)
		plans[m["id"].(string)] = m
		order = append(order, m["id"].(string))
		if got := sortedKeys(m); !slices.Equal(got, publicPlanFields) {
			t.Fatalf("plan fields %v", got)
		}
		if got := sortedKeys(m["monthly"].(map[string]any)); !slices.Equal(got, publicTierFields) {
			t.Fatalf("tier fields %v", got)
		}
	}
	if !slices.Equal(order, []string{"pub-on", "starter", "pro", "max", "pub-no-year"}) {
		t.Fatalf("order %v", order)
	}
	on := plans["pub-on"]
	if jsonNum(on["max_devices"]) != 4 || on["featured"] != true || jsonNum(plans["pub-no-year"]["max_devices"]) != 2 || jsonNum(plans["max"]["max_devices"]) != 4 {
		t.Fatalf("devices are not the effective value: %v", on)
	}
	if plans["pub-no-year"]["yearly"] != nil || plans["pro"]["featured"] != true || plans["starter"]["featured"] != false {
		t.Fatal("yearly or featured flags")
	}
	yearly := on["yearly"].(map[string]any)
	if got := sortedKeys(yearly); !slices.Equal(got, publicTierFields) || jsonNum(yearly["discount_percent"]) != 80 || jsonNum(yearly["price_cents"]) != 9600 {
		t.Fatalf("yearly tier %v", yearly)
	}
	// Top-level monthly fields stay as before, for frontends that predate the yearly tier.
	if jsonNum(plans["starter"]["price_cents"]) != 1990 || jsonNum(plans["starter"]["duration_days"]) != 30 || jsonNum(plans["starter"]["traffic_bytes"]) != 100*gib {
		t.Fatalf("legacy fields %v", plans["starter"])
	}
	// With every plan at 100 there is nothing to advertise.
	e.want(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 100, "version": 1}), 200)
	for _, id := range []string{"pub-on"} {
		p := e.adminPlans()[id]
		e.want(e.editPlan(p, map[string]any{"yearly_discount_percent": nil}), 200)
	}
	_ = json.Unmarshal(e.call(nil, "GET", "/api/v1/plans", nil, "").Body.Bytes(), &out)
	if jsonNum(out["max_yearly_saving_percent"]) != 0 {
		t.Fatalf("saving at 100%%: %v", out["max_yearly_saving_percent"])
	}
}

func (e *payEnv) yearlyQuote(u payUser, plan, cycle, coupon string) map[string]any {
	e.t.Helper()
	body := map[string]any{"plan_id": plan, "coupon_code": coupon}
	if cycle != "" {
		body["billing_cycle"] = cycle
	}
	return e.want(e.call(u.cookies, "POST", "/api/v1/payments/quote", body, ""), 200)["quote"].(map[string]any)
}

func (e *payEnv) placeCycle(u payUser, plan, cycle, coupon string, expected *int64, key string) *httptest.ResponseRecorder {
	body := map[string]any{"plan_id": plan, "pay_type": payment.TypeWeChat, "coupon_code": coupon, "billing_cycle": cycle}
	if expected != nil {
		body["expected_amount_cents"] = *expected
	}
	if key == "" {
		key = uuid.NewString()
	}
	return e.call(u.cookies, "POST", "/api/v1/payments/orders", body, key)
}

// S1-03 + S1-05: yearly quote and order, snapshots, fulfilment and renewal.
func TestShopYearlyQuoteOrderAndRenewal(t *testing.T) {
	e := newConsoleEnv(t, func(c *config.Config) { c.ClientMaxOnlineDevices = 3 })
	u := e.user("yearly", "user")

	monthly := e.yearlyQuote(u, "pro", "", "")
	explicit := e.yearlyQuote(u, "pro", "monthly", "")
	if monthly["billing_cycle"] != "monthly" || jsonNum(monthly["amount_cents"]) != 3990 || jsonNum(monthly["original_cents"]) != 3990 || jsonNum(monthly["cycle_saving_cents"]) != 0 ||
		jsonNum(monthly["duration_days"]) != 30 || jsonNum(monthly["traffic_bytes"]) != 300*gib || jsonNum(monthly["cycle_discount_percent"]) != 100 {
		t.Fatalf("monthly quote %v", monthly)
	}
	if monthly["billing_cycle"] != explicit["billing_cycle"] || jsonNum(monthly["amount_cents"]) != jsonNum(explicit["amount_cents"]) {
		t.Fatal("an absent cycle is not monthly")
	}
	q := e.yearlyQuote(u, "pro", "yearly", "")
	if q["billing_cycle"] != "yearly" || jsonNum(q["amount_cents"]) != 43092 || jsonNum(q["list_price_cents"]) != 43092 || jsonNum(q["original_cents"]) != 47880 || jsonNum(q["cycle_saving_cents"]) != 4788 ||
		jsonNum(q["monthly_equivalent_cents"]) != 3591 || jsonNum(q["duration_days"]) != 365 || jsonNum(q["traffic_bytes"]) != 3600*gib || jsonNum(q["cycle_discount_percent"]) != 90 ||
		jsonNum(q["device_limit"]) != 3 || q["allow_dedicated"] != true || q["kind"] != "new" {
		t.Fatalf("yearly quote %v", q)
	}
	e.code(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "pro", "billing_cycle": "weekly"}, ""), 400, "invalid_billing_cycle")

	// Order: cycle, snapshots, amount and the gateway product name.
	key := uuid.NewString()
	w := e.placeCycle(u, "pro", "yearly", "", nil, key)
	var first placed
	_ = json.Unmarshal(e.want2(w, 201), &first)
	o := e.order(first.Order.ID)
	if o.BillingCycle != "yearly" || o.CycleDiscountPercent != 90 || o.OriginalCents != 47880 || o.ListPriceCents != 43092 || o.AmountCents != 43092 || o.DurationDays != 365 || o.TrafficBytes != 3600*gib ||
		o.EntMaxDevices == nil || *o.EntMaxDevices != 5 || o.EntAllowDedicated == nil || !*o.EntAllowDedicated {
		t.Fatalf("order %+v", o)
	}
	parsed, _ := url.Parse(first.PayURL)
	if parsed.Query().Get("name") != "进阶计划·年付" || parsed.Query().Get("money") != "430.92" {
		t.Fatalf("pay URL %v", parsed.Query())
	}
	// Replays: the same key with another cycle is a conflict; the same request replays.
	e.code(e.placeCycle(u, "pro", "monthly", "", nil, key), 409, "idempotency_conflict")
	e.code(e.placeCycle(u, "pro", "", "", nil, key), 409, "idempotency_conflict")
	if w = e.placeCycle(u, "pro", "yearly", "", nil, key); w.Code != 200 {
		t.Fatalf("replay %d %s", w.Code, w.Body.String())
	}
	var live int64
	e.db.Model(&model.Order{}).Where("user_id = ?", u.ID).Count(&live)
	if live != 1 {
		t.Fatalf("%d orders", live)
	}

	// A monthly key replayed without the cycle still replays (older frontends).
	mkey := uuid.NewString()
	if w = e.placeCycle(u, "starter", "monthly", "", nil, mkey); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	legacy := e.call(u.cookies, "POST", "/api/v1/payments/orders", map[string]any{"plan_id": "starter", "pay_type": payment.TypeWeChat}, mkey)
	if legacy.Code != 200 {
		t.Fatalf("legacy replay %d %s", legacy.Code, legacy.Body.String())
	}

	// Pay the yearly order.
	gw, status := e.gw.open(t, first.PayURL)
	if status != 200 || gw.Cents != 43092 {
		t.Fatalf("gateway %d", status)
	}
	e.want(e.pay(gw), 200)
	sub := e.subscription(u.ID)
	if sub == nil || sub.PlanID != "pro" || sub.TrafficLimitBytes != 3600*gib || !sub.ExpiresAt.Equal(sub.StartsAt.AddDate(0, 0, 365)) || sub.MaxDevices != 5 || !sub.AllowDedicated {
		t.Fatalf("subscription %+v", sub)
	}
	before := nativeEntitlementFingerprint(sub)

	// S1-05: a monthly subscription renewed as yearly keeps its start and its fingerprint.
	v := e.user("switcher", "user")
	m, _ := e.gw.open(t, e.place(v, "pro", payment.TypeWeChat, "").PayURL)
	e.want(e.pay(m), 200)
	monthlySub := e.subscription(v.ID)
	fp := nativeEntitlementFingerprint(monthlySub)
	if q = e.yearlyQuote(v, "pro", "yearly", ""); q["kind"] != "renew" || q["purchasable"] != true {
		t.Fatalf("renew quote %v", q)
	}
	var renewal placed
	_ = json.Unmarshal(e.want2(e.placeCycle(v, "pro", "yearly", "", nil, ""), 201), &renewal)
	y, _ := e.gw.open(t, renewal.PayURL)
	e.want(e.pay(y), 200)
	renewed := e.subscription(v.ID)
	if !renewed.StartsAt.Equal(monthlySub.StartsAt) || !renewed.ExpiresAt.Equal(monthlySub.ExpiresAt.AddDate(0, 0, 365)) || renewed.TrafficLimitBytes != monthlySub.TrafficLimitBytes+3600*gib || renewed.UsedUnits != monthlySub.UsedUnits {
		t.Fatalf("renewed %+v from %+v", renewed, monthlySub)
	}
	if nativeEntitlementFingerprint(renewed) != fp || before == "" {
		t.Fatal("the client entitlement fingerprint changed on a monthly -> yearly renewal")
	}
	// ...and back: a yearly subscription renews monthly.
	back, _ := e.gw.open(t, e.place(v, "pro", payment.TypeAlipay, "").PayURL)
	e.want(e.pay(back), 200)
	if again := e.subscription(v.ID); !again.ExpiresAt.Equal(renewed.ExpiresAt.AddDate(0, 0, 30)) {
		t.Fatalf("monthly renewal of a yearly subscription %+v", again)
	}

	// Another plan, in any cycle, is refused while the subscription is valid.
	for _, cycle := range []string{"monthly", "yearly"} {
		quote := e.yearlyQuote(v, "max", cycle, "")
		if quote["purchasable"] != false || quote["reason"] != "plan_change_not_supported" {
			t.Fatalf("%s: %v", cycle, quote)
		}
		e.code(e.placeCycle(v, "max", cycle, "", nil, ""), 409, "plan_change_not_supported")
	}

	// The ten-year ceiling.
	e.db.Model(&model.Subscription{}).Where("user_id = ?", v.ID).Update("expires_at", e.clock.Now().AddDate(10, 0, 1))
	limit := e.yearlyQuote(v, "pro", "yearly", "")
	if limit["purchasable"] != false || limit["reason"] != "renewal_limit" {
		t.Fatalf("renewal limit quote %v", limit)
	}
	e.code(e.placeCycle(v, "pro", "yearly", "", nil, ""), 409, "renewal_limit")
	// The ceiling is checked against the current expiry, as before: just under it, one more
	// purchase of either cycle is fine.
	e.db.Model(&model.Subscription{}).Where("user_id = ?", v.ID).Update("expires_at", e.clock.Now().AddDate(9, 11, 0))
	if ok := e.yearlyQuote(v, "pro", "yearly", ""); ok["purchasable"] != true {
		t.Fatalf("under the ceiling %v", ok)
	}
}

func (e *payEnv) want2(w *httptest.ResponseRecorder, status int) []byte {
	e.t.Helper()
	if w.Code != status {
		e.t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	return w.Body.Bytes()
}

// A plan that cannot sell yearly answers cycle_not_available (S1-03).
func TestShopCycleNotAvailable(t *testing.T) {
	e := newConsoleEnv(t, nil)
	e.createPlan(map[string]any{"id": "month-only", "active": true, "yearly_enabled": false})
	u := e.user("monthly-only", "user")
	e.code(e.call(u.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "month-only", "billing_cycle": "yearly"}, ""), 409, "cycle_not_available")
	e.code(e.placeCycle(u, "month-only", "yearly", "", nil, ""), 409, "cycle_not_available")
	if q := e.yearlyQuote(u, "month-only", "", ""); jsonNum(q["amount_cents"]) != 1000 {
		t.Fatalf("monthly quote %v", q)
	}
	// A global setting of 100 sells yearly at the full twelve months (no discount shown).
	e.want(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 100, "version": 1}), 200)
	if q := e.yearlyQuote(u, "starter", "yearly", ""); jsonNum(q["amount_cents"]) != 23880 || jsonNum(q["cycle_saving_cents"]) != 0 {
		t.Fatalf("100%% quote %v", q)
	}
	// An already placed yearly order is untouched by a later change of the setting.
	e.want(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 90, "version": 2}), 200)
	var o placed
	_ = json.Unmarshal(e.want2(e.placeCycle(u, "starter", "yearly", "", nil, ""), 201), &o)
	e.want(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 50, "version": 3}), 200)
	if got := e.order(o.Order.ID); got.AmountCents != 21492 || got.CycleDiscountPercent != 90 {
		t.Fatalf("placed order moved with the setting: %+v", got)
	}
}

// S1-04: coupon cycle scope and the test price under yearly.
func TestShopCouponCycleScopeAndTestPrice(t *testing.T) {
	e := newConsoleEnv(t, func(c *config.Config) {
		c.Payment.TestPrice, c.Payment.TestEmails = true, []string{"tester@example.com"}
	})
	u := e.user("plain", "user")
	e.coupon(model.Coupon{Code: "ALL-CYCLES", DiscountType: model.DiscountPercent, PercentOff: 10, Enabled: true, PerUserLimit: 9, BillingCycles: "all"})
	e.coupon(model.Coupon{Code: "MONTH-ONLY", DiscountType: model.DiscountPercent, PercentOff: 50, Enabled: true, PerUserLimit: 9, BillingCycles: "monthly"})
	e.coupon(model.Coupon{Code: "YEAR-ONLY", DiscountType: model.DiscountPercent, PercentOff: 50, Enabled: true, PerUserLimit: 9, BillingCycles: "yearly"})
	e.coupon(model.Coupon{Code: "BIG-AMOUNT", DiscountType: model.DiscountAmount, AmountOffCents: 99999999, Enabled: true, PerUserLimit: 9, BillingCycles: "yearly"})
	e.coupon(model.Coupon{Code: "LEGACY", DiscountType: model.DiscountPercent, PercentOff: 10, Enabled: true, PerUserLimit: 9}) // column default

	q := e.yearlyQuote(u, "starter", "yearly", "ALL-CYCLES")
	// 21492 x 10% = 2149.2 -> 2149 off.
	if jsonNum(q["discount_cents"]) != 2149 || jsonNum(q["amount_cents"]) != 19343 || jsonNum(q["list_price_cents"]) != 21492 || q["coupon"].(map[string]any)["applied"] != true {
		t.Fatalf("all-cycles coupon on yearly %v", q)
	}
	q = e.yearlyQuote(u, "starter", "yearly", "YEAR-ONLY")
	if jsonNum(q["amount_cents"]) != 10746 || jsonNum(q["discount_cents"]) != 10746 {
		t.Fatalf("yearly-only coupon on yearly %v", q)
	}
	for _, c := range []struct{ code, cycle string }{{"MONTH-ONLY", "yearly"}, {"YEAR-ONLY", "monthly"}, {"YEAR-ONLY", ""}} {
		q = e.yearlyQuote(u, "starter", c.cycle, c.code)
		coupon := q["coupon"].(map[string]any)
		if coupon["applied"] != false || coupon["reason"] != "coupon_not_applicable" || jsonNum(q["discount_cents"]) != 0 || !strings.Contains(coupon["message"].(string), "付费周期") {
			t.Fatalf("%v: %v", c, q)
		}
		e.code(e.placeCycle(u, "starter", c.cycle, c.code, nil, ""), 409, "coupon_not_applicable")
	}
	if q = e.yearlyQuote(u, "starter", "monthly", "MONTH-ONLY"); jsonNum(q["amount_cents"]) != 995 {
		t.Fatalf("monthly-only coupon on monthly %v", q)
	}
	if q = e.yearlyQuote(u, "starter", "yearly", "LEGACY"); q["coupon"].(map[string]any)["applied"] != true {
		t.Fatalf("legacy coupon %v", q)
	}
	// Never below one cent.
	if q = e.yearlyQuote(u, "starter", "yearly", "BIG-AMOUNT"); jsonNum(q["amount_cents"]) != 1 {
		t.Fatalf("floor %v", q)
	}

	// D12: a listed account pays the test price for yearly too; the order keeps the real
	// yearly list price and original.
	tester := e.user("tester", "user")
	q = e.yearlyQuote(tester, "pro", "yearly", "")
	if q["test_price"] != true || jsonNum(q["amount_cents"]) != 1 || jsonNum(q["list_price_cents"]) != 43092 || jsonNum(q["original_cents"]) != 47880 {
		t.Fatalf("test-price quote %v", q)
	}
	var o placed
	_ = json.Unmarshal(e.want2(e.placeCycle(tester, "pro", "yearly", "", nil, ""), 201), &o)
	if o.Order.AmountCents != 1 || o.Order.ListPriceCents != 43092 || o.Order.OriginalCents != 47880 || o.Order.PriceSource != priceTest || o.Order.BillingCycle != "yearly" {
		t.Fatalf("test-price order %+v", o.Order)
	}
	gw, _ := e.gw.open(t, o.PayURL)
	e.want(e.pay(gw), 200)
	if sub := e.subscription(tester.ID); sub.TrafficLimitBytes != 3600*gib || !sub.ExpiresAt.Equal(sub.StartsAt.AddDate(0, 0, 365)) {
		t.Fatalf("test-price yearly fulfilment %+v", sub)
	}
}

// S1-07 (price guard) and S1-03: expected_amount_cents only ever refuses.
func TestShopExpectedAmountGuard(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("guarded", "user")
	e.coupon(model.Coupon{Code: "GUARD-10", DiscountType: model.DiscountPercent, PercentOff: 10, Enabled: true, MaxUses: 1, BillingCycles: "all"})
	amount := func(v int64) *int64 { return &v }

	e.code(e.placeCycle(u, "starter", "yearly", "", amount(1), ""), 409, "price_changed")
	e.code(e.placeCycle(u, "starter", "yearly", "", amount(23880), ""), 409, "price_changed") // the undiscounted price is not the amount
	e.code(e.placeCycle(u, "starter", "yearly", "", amount(0), ""), 400, "invalid_expected_amount")
	e.code(e.placeCycle(u, "starter", "yearly", "", amount(-3), ""), 400, "invalid_expected_amount")
	// A refusal creates nothing and reserves nothing.
	e.code(e.placeCycle(u, "starter", "yearly", "GUARD-10", amount(100), ""), 409, "price_changed")
	var orders int64
	e.db.Model(&model.Order{}).Count(&orders)
	if orders != 0 {
		t.Fatalf("%d orders after refusals", orders)
	}
	if w := e.placeCycle(u, "starter", "yearly", "GUARD-10", amount(19343), ""); w.Code != 201 {
		t.Fatalf("matching amount refused: %d %s", w.Code, w.Body.String())
	}

	// The administrator changes the price between the quote and the order.
	q := e.yearlyQuote(u, "pro", "yearly", "")
	plan := e.adminPlans()["pro"]
	e.want(e.editPlan(plan, map[string]any{"price_cents": 5000}), 200)
	e.code(e.placeCycle(u, "pro", "yearly", "", amount(jsonNum(q["amount_cents"])), ""), 409, "price_changed")
	fresh := e.yearlyQuote(u, "pro", "yearly", "")
	if jsonNum(fresh["amount_cents"]) != 54000 {
		t.Fatalf("fresh quote %v", fresh)
	}
	if w := e.placeCycle(u, "pro", "yearly", "", amount(54000), ""); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// The same guard covers a change of the shop-wide discount.
	e.want(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "version": 1}), 200)
	e.code(e.placeCycle(u, "max", "yearly", "", amount(86292), ""), 409, "price_changed")
	if w := e.placeCycle(u, "max", "yearly", "", amount(81498), ""); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// S1-06: the order carries what was sold.
func TestShopOrderSnapshotFulfilment(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("snap", "user")
	var o placed
	_ = json.Unmarshal(e.want2(e.placeCycle(u, "pro", "yearly", "", nil, ""), 201), &o)

	// After the order: fewer devices, no dedicated line, other price and traffic.
	pro := e.adminPlans()["pro"]
	e.want(e.editPlan(pro, map[string]any{"max_devices": 1, "allow_dedicated": false, "price_cents": 9999, "traffic_bytes": 7 * gib, "duration_days": 10, "name": "改名后"}), 200)
	gw, _ := e.gw.open(t, o.PayURL)
	e.want(e.pay(gw), 200)
	sub := e.subscription(u.ID)
	if sub.MaxDevices != 5 || !sub.AllowDedicated || sub.TrafficLimitBytes != 3600*gib || sub.PlanName != "进阶计划" || !sub.ExpiresAt.Equal(sub.StartsAt.AddDate(0, 0, 365)) {
		t.Fatalf("the plan's later terms leaked into the purchase: %+v", sub)
	}

	// An order without snapshots (from before them, or an older binary) falls back to the
	// plan row.
	v := e.user("legacy-order", "user")
	var lo placed
	_ = json.Unmarshal(e.want2(e.placeCycle(v, "max", "", "", nil, ""), 201), &lo)
	e.db.Model(&model.Order{}).Where("id = ?", lo.Order.ID).Updates(map[string]any{"ent_max_devices": nil, "ent_allow_dedicated": nil})
	maxPlan := e.adminPlans()["max"]
	e.want(e.editPlan(maxPlan, map[string]any{"max_devices": 7, "allow_dedicated": false}), 200)
	lg, _ := e.gw.open(t, lo.PayURL)
	e.want(e.pay(lg), 200)
	if s := e.subscription(v.ID); s.MaxDevices != 7 || s.AllowDedicated || s.TrafficLimitBytes != 800*gib {
		t.Fatalf("legacy order fallback %+v", s)
	}

	// Manual confirmation uses the snapshot too, even for a plan deleted since.
	w := e.user("manual", "user")
	e.createPlan(map[string]any{"id": "short-lived", "active": true, "max_devices": 4, "allow_dedicated": true, "traffic_bytes": 5 * gib, "duration_days": 20})
	var mo placed
	_ = json.Unmarshal(e.want2(e.placeCycle(w, "short-lived", "yearly", "", nil, ""), 201), &mo)
	e.db.Model(&model.Order{}).Where("id = ?", mo.Order.ID).Updates(map[string]any{"status": model.OrderReview, "review_reason": model.ReviewAmountMismatch})
	short := e.adminPlans()["short-lived"]
	off := e.want(e.editPlan(short, map[string]any{"active": false, "max_devices": 1, "allow_dedicated": false}), 200)["plan"].(map[string]any)
	e.want(e.post("/plans/short-lived/delete", map[string]any{"version": off["version"]}), 200)
	e.want(e.post("/orders/"+mo.Order.ID+"/confirm", map[string]any{"admin_password": payPassword, "paid_cents": mo.Order.AmountCents, "note": "ok"}), 200)
	ms := e.subscription(w.ID)
	if ms.MaxDevices != 4 || !ms.AllowDedicated || ms.TrafficLimitBytes != 60*gib || !ms.ExpiresAt.Equal(ms.StartsAt.AddDate(0, 0, 365)) {
		t.Fatalf("manual confirmation %+v", ms)
	}
}

// Test purchases take a cycle too (7.4).
func TestShopTestPurchaseCycle(t *testing.T) {
	e := newConsoleEnv(t, func(c *config.Config) { c.TestPurchase = true })
	u := e.user("devbuyer", "user")
	post := func(key string, body map[string]any) *httptest.ResponseRecorder {
		return e.call(u.cookies, "POST", "/api/v1/orders/test-purchase", body, key)
	}
	key := uuid.NewString()
	w := post(key, map[string]any{"plan_id": "starter", "billing_cycle": "yearly"})
	var out struct {
		Order model.Order `json:"order"`
	}
	_ = json.Unmarshal(e.want2(w, 201), &out)
	o := e.order(out.Order.ID)
	if o.BillingCycle != "yearly" || o.PriceCents != 21492 || o.ListPriceCents != 21492 || o.OriginalCents != 23880 || o.DurationDays != 365 || o.TrafficBytes != 1200*gib || o.Status != model.OrderPaidTest || o.EntMaxDevices == nil {
		t.Fatalf("test order %+v", o)
	}
	sub := e.subscription(u.ID)
	if !sub.IsTest || sub.TrafficLimitBytes != 1200*gib || !sub.ExpiresAt.Equal(sub.StartsAt.AddDate(0, 0, 365)) {
		t.Fatalf("test subscription %+v", sub)
	}
	e.code(post(key, map[string]any{"plan_id": "starter"}), 409, "idempotency_conflict")
	if again := post(key, map[string]any{"plan_id": "starter", "billing_cycle": "yearly"}); again.Code != 200 {
		t.Fatalf("replay %d", again.Code)
	}
	e.code(post(uuid.NewString(), map[string]any{"plan_id": "starter", "billing_cycle": "weekly"}), 400, "invalid_billing_cycle")
	// Without a cycle the request is the old monthly one.
	e.code(post(uuid.NewString(), map[string]any{"plan_id": "pro"}), 409, "plan_change_not_supported")
	v := e.user("devbuyer2", "user")
	w = e.call(v.cookies, "POST", "/api/v1/orders/test-purchase", map[string]any{"plan_id": "starter"}, uuid.NewString())
	_ = json.Unmarshal(e.want2(w, 201), &out)
	if out.Order.BillingCycle != "monthly" || out.Order.PriceCents != 1990 || out.Order.DurationDays != 30 {
		t.Fatalf("monthly test order %+v", out.Order)
	}
}

// S1-07: delete against order creation, in both orders; concurrent edits.
func TestShopConcurrency(t *testing.T) {
	e := newConsoleEnv(t, nil)
	e.createPlan(map[string]any{"id": "race-plan", "active": true})
	buyer := e.user("racer", "user")

	// Pause the order's insert: at that point the order transaction holds the plan's
	// shared lock.
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	if err := e.db.Callback().Create().Before("gorm:create").Register("shop:barrier", func(tx *gorm.DB) {
		if strings.HasSuffix(tx.Statement.Table, "orders") {
			once.Do(func() {
				close(reached)
				<-release
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.db.Callback().Create().Remove("shop:barrier") })

	placedC := make(chan *httptest.ResponseRecorder, 1)
	go func() { placedC <- e.placeCycle(buyer, "race-plan", "yearly", "", nil, "") }()
	select {
	case <-reached:
	case w := <-placedC:
		t.Fatalf("the order finished without reaching the insert: %d %s", w.Code, w.Body.String())
	case <-time.After(10 * time.Second):
		t.Fatal("the order never reached the insert")
	}

	// Taking the plan off the shelf must wait for the order transaction.
	planNow := e.adminPlans()["race-plan"]
	offC := make(chan *httptest.ResponseRecorder, 1)
	go func() { offC <- e.editPlan(planNow, map[string]any{"active": false}) }()
	select {
	case <-offC:
		t.Fatal("the plan was changed while an order held it")
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	if w := <-placedC; w.Code != 201 {
		t.Fatalf("order %d %s", w.Code, w.Body.String())
	}
	off := (<-offC)
	if off.Code != 200 {
		t.Fatalf("off-shelf %d %s", off.Code, off.Body.String())
	}
	var offView struct {
		Plan map[string]any `json:"plan"`
	}
	_ = json.Unmarshal(off.Body.Bytes(), &offView)
	// The order is now visible to the delete check.
	out := e.want(e.post("/plans/race-plan/delete", map[string]any{"version": offView.Plan["version"]}), 409)
	if out["error"].(map[string]any)["code"] != "plan_in_use" {
		t.Fatalf("delete after the order: %v", out)
	}
	// The other order of events: the delete wins and the order finds no plan.
	e.createPlan(map[string]any{"id": "race-two"})
	two := e.adminPlans()["race-two"]
	e.want(e.post("/plans/race-two/delete", map[string]any{"version": two["version"]}), 200)
	e.code(e.placeCycle(buyer, "race-two", "yearly", "", nil, ""), 404, "plan_not_found")

	// Two administrators saving the same version: exactly one wins.
	e.createPlan(map[string]any{"id": "dual-edit"})
	dual := e.adminPlans()["dual-edit"]
	other := e.user("root2", "admin")
	results := make(chan int, 2)
	for i, cookies := range [][]*http.Cookie{e.admin.cookies, other.cookies} {
		go func() {
			body := map[string]any{}
			for _, k := range []string{"name", "description", "note", "price_cents", "duration_days", "traffic_bytes", "max_devices", "allow_dedicated", "yearly_enabled", "yearly_duration_days", "featured", "sort", "active", "version"} {
				body[k] = dual[k]
			}
			body["price_cents"] = 2000 + i
			results <- e.call(cookies, "POST", "/api/v1/admin/console/plans/dual-edit", body, "").Code
		}()
	}
	a, b := <-results, <-results
	if !(a == 200 && b == 409 || a == 409 && b == 200) {
		t.Fatalf("concurrent edits answered %d and %d", a, b)
	}
	// ...and the same for the shop-wide setting.
	settings := []int{85, 80}
	for i, cookies := range [][]*http.Cookie{e.admin.cookies, other.cookies} {
		go func() {
			results <- e.call(cookies, "POST", "/api/v1/admin/console/shop-settings", map[string]any{"yearly_discount_percent": settings[i], "version": 1}, "").Code
		}()
	}
	a, b = <-results, <-results
	if !(a == 200 && b == 409 || a == 409 && b == 200) {
		t.Fatalf("concurrent settings answered %d and %d", a, b)
	}
}

// Global discount settings (6.5, D4).
func TestShopSettings(t *testing.T) {
	e := newConsoleEnv(t, nil)
	var got struct {
		Settings shopSettingsView `json:"settings"`
	}
	e.decode(e.get("/shop-settings"), 200, &got)
	if got.Settings.YearlyDiscountPercent != 90 || got.Settings.Version != 1 || got.Settings.RowMissing {
		t.Fatalf("default %+v", got.Settings)
	}
	for _, bad := range []int{9, 49, 101, 0, -1} {
		out := e.want(e.post("/shop-settings", map[string]any{"yearly_discount_percent": bad, "version": 1}), 400)
		if out["error"].(map[string]any)["field"] != "yearly_discount_percent" {
			t.Fatalf("%d: %v", bad, out)
		}
	}
	e.code(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 90, "version": 1}), 400, "no_change")
	e.code(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "version": 5}), 409, "version_conflict")
	e.decode(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 85, "version": 1}), 200, &got)
	if got.Settings.YearlyDiscountPercent != 85 || got.Settings.Version != 2 || got.Settings.UpdatedBy == nil || *got.Settings.UpdatedBy != e.admin.ID {
		t.Fatalf("saved %+v", got.Settings)
	}
	// Plans that follow the setting move; an own percentage does not.
	e.createPlan(map[string]any{"id": "own-pct", "active": true, "yearly_discount_percent": 70, "price_cents": 1000})
	plans := e.adminPlans()
	if jsonNum(plans["starter"]["yearly"].(map[string]any)["price_cents"]) != 20298 || jsonNum(plans["own-pct"]["yearly"].(map[string]any)["price_cents"]) != 8400 {
		t.Fatalf("derived prices %v %v", plans["starter"]["yearly"], plans["own-pct"]["yearly"])
	}
	list := e.want(e.get("/plans"), 200)
	if jsonNum(list["following_global_plans"]) != 3 || jsonNum(list["client_max_online_devices"]) != int64(e.cfg.MaxOnlineDevices()) {
		t.Fatalf("list summary %v", list)
	}
	var audit model.AdminAudit
	if err := e.db.Where("action = ?", AuditShopSettings).First(&audit).Error; err != nil || !strings.Contains(audit.Detail, `"to":85`) {
		t.Fatalf("audit %v %s", err, audit.Detail)
	}
	// A deleted settings row reads as the default and is recreated by the next save.
	e.db.Exec("DELETE FROM " + e.cfg.Schema + ".shop_settings")
	e.decode(e.get("/shop-settings"), 200, &got)
	if !got.Settings.RowMissing || got.Settings.YearlyDiscountPercent != 90 {
		t.Fatalf("missing row %+v", got.Settings)
	}
	if q := (&payEnv{t: t, db: e.db}); q != nil {
		var global int
		global, _ = globalDiscount(e.db)
		if global != 90 {
			t.Fatalf("missing row discount %d", global)
		}
	}
	e.decode(e.post("/shop-settings", map[string]any{"yearly_discount_percent": 80, "version": 0}), 200, &got)
	if got.Settings.YearlyDiscountPercent != 80 || got.Settings.RowMissing {
		t.Fatalf("recreated %+v", got.Settings)
	}
}

// The order list carries and filters the cycle (S1: console orders).
func TestShopConsoleOrderCycle(t *testing.T) {
	e := newConsoleEnv(t, nil)
	u := e.user("lister", "user")
	e.place(u, "starter", payment.TypeWeChat, "")
	e.want(e.placeCycle(u, "starter", "yearly", "", nil, ""), 201)
	var page struct {
		Orders []consoleOrder `json:"orders"`
		Total  int64          `json:"total"`
	}
	e.decode(e.get("/orders"), 200, &page)
	cycles := []string{}
	for _, o := range page.Orders {
		cycles = append(cycles, o.BillingCycle)
		if o.BillingCycle == "yearly" && (o.OriginalCents != 23880 || o.CycleDiscountPercent != 90 || o.EntMaxDevices == nil || *o.EntMaxDevices != 2) {
			t.Fatalf("yearly row %+v", o)
		}
	}
	slices.Sort(cycles)
	if page.Total != 2 || !slices.Equal(cycles, []string{"monthly", "yearly"}) {
		t.Fatalf("cycles %v", cycles)
	}
	e.decode(e.get("/orders?billing_cycle=yearly"), 200, &page)
	if page.Total != 1 || page.Orders[0].BillingCycle != "yearly" {
		t.Fatalf("filter %+v", page)
	}
	e.code(e.get("/orders?billing_cycle=weekly"), 400, "invalid_filter")
	var detail struct {
		Order consoleOrder `json:"order"`
	}
	e.decode(e.get("/orders/"+page.Orders[0].ID), 200, &detail)
	if detail.Order.BillingCycle != "yearly" || detail.Order.OriginalCents != 23880 {
		t.Fatalf("detail %+v", detail.Order)
	}
}

// Doctor's shop checks (handoff_shop.md 13): the new table, the settings row, plans on
// sale and device quotas above CLIENT_MAX_ONLINE_DEVICES. Counts and categories only.
func TestShopDoctor(t *testing.T) {
	e := newConsoleEnv(t, func(c *config.Config) { c.ClientMaxOnlineDevices = 4 })
	inspect := func() operations.ReadinessReport {
		t.Helper()
		report, err := operations.Inspect(t.Context(), e.db, e.cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	report := inspect()
	if len(report.MissingTables) != 0 || !report.Shop.SettingsRow || report.Shop.YearlyDiscount != 90 || report.Shop.ActivePlans != 3 || report.Shop.ActiveYearlyPlans != 3 || report.Shop.PlansOverDeviceCap != 2 {
		t.Fatalf("shop readiness %+v missing %v", report.Shop, report.MissingTables)
	}
	// starter has 2 devices, pro 5, max 10: two exceed the cap of 4. Counted, not warned
	// about: a default install (cap 2) would otherwise never be clean.
	e.db.Exec("DELETE FROM " + e.cfg.Schema + ".shop_settings")
	e.db.Model(&model.Plan{}).Where("1 = 1").Update("active", false)
	report = inspect()
	if report.Shop.SettingsRow || report.Shop.ActivePlans != 0 || !slices.Contains(report.Warnings, "shop_settings_row_missing") || !slices.Contains(report.Warnings, "shop_no_active_plans") {
		t.Fatalf("degraded shop %+v / %v", report.Shop, report.Warnings)
	}
	e.db.Exec("DROP TABLE " + e.cfg.Schema + ".shop_settings")
	if report = inspect(); !slices.Contains(report.MissingTables, "shop_settings") {
		t.Fatalf("a missing shop_settings table is not reported: %v", report.MissingTables)
	}
}
