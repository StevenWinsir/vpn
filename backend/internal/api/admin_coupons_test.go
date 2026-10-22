package api

import (
	"encoding/json"
	"testing"

	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// Coupon administration (handoff_payment_account.md 9.5, API part of round 1).
func TestAdminCoupons(t *testing.T) {
	// The console does not depend on the node catalog switch (requireAdmin does).
	e := newPayEnv(t, func(c *config.Config) { c.ClientNodeCatalog = false })
	admin, user := e.user("console-admin", "admin"), e.user("console-user", "user")
	e.want(e.call(user.cookies, "GET", "/api/v1/admin/console/coupons", nil, ""), 403)
	e.want(e.call(nil, "GET", "/api/v1/admin/console/coupons", nil, ""), 401)
	e.want(e.call(user.cookies, "POST", "/api/v1/admin/console/coupons", map[string]any{"code": "HACK", "discount_type": "percent", "percent_off": 99, "per_user_limit": 1, "enabled": true}, ""), 403)

	type saved struct {
		Coupon couponAdminView `json:"coupon"`
	}
	create := func(body map[string]any, status int) couponAdminView {
		t.Helper()
		w := e.call(admin.cookies, "POST", "/api/v1/admin/console/coupons", body, "")
		e.want(w, status)
		var out saved
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Coupon
	}
	welcome := create(map[string]any{"code": " welcome-10 ", "discount_type": "percent", "percent_off": 10, "plan_ids": []string{"pro", "starter", "pro"}, "per_user_limit": 1, "enabled": true, "note": "新用户"}, 201)
	if welcome.Code != "WELCOME-10" || len(welcome.PlanIDs) != 2 || welcome.Version != 1 {
		t.Fatalf("created %+v", welcome)
	}
	generated := create(map[string]any{"generate": true, "discount_type": "amount", "amount_off_cents": 300, "per_user_limit": 2, "max_uses": 10, "enabled": true}, 201)
	if !couponCodePattern.MatchString(generated.Code) || len(generated.Code) != 10 {
		t.Fatalf("generated %q", generated.Code)
	}
	create(map[string]any{"code": "WELCOME-10", "discount_type": "percent", "percent_off": 5, "per_user_limit": 1}, 409)
	for _, bad := range []map[string]any{
		{"code": "AB", "discount_type": "percent", "percent_off": 5, "per_user_limit": 1},
		{"code": "BAD CODE", "discount_type": "percent", "percent_off": 5, "per_user_limit": 1},
		{"code": "PCT100", "discount_type": "percent", "percent_off": 100, "per_user_limit": 1},
		{"code": "BOTH", "discount_type": "percent", "percent_off": 5, "amount_off_cents": 5, "per_user_limit": 1},
		{"code": "NOTYPE", "percent_off": 5, "per_user_limit": 1},
		{"code": "NOPLAN", "discount_type": "percent", "percent_off": 5, "plan_ids": []string{"missing"}, "per_user_limit": 1},
		{"code": "NOUSER", "discount_type": "percent", "percent_off": 5, "per_user_limit": 0},
		{"code": "BACKWARD", "discount_type": "percent", "percent_off": 5, "per_user_limit": 1, "starts_at": "2030-01-02T00:00:00Z", "ends_at": "2030-01-01T00:00:00Z"},
		{"code": "BOTHWAYS", "generate": true, "discount_type": "percent", "percent_off": 5, "per_user_limit": 1},
		{"code": "EXTRA", "discount_type": "percent", "percent_off": 5, "per_user_limit": 1, "used_count": 99},
	} {
		create(bad, 400)
	}

	edit := func(c couponAdminView, body map[string]any, status int) couponAdminView {
		t.Helper()
		w := e.call(admin.cookies, "POST", "/api/v1/admin/console/coupons/"+c.ID, body, "")
		e.want(w, status)
		var out saved
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Coupon
	}
	terms := map[string]any{"discount_type": "percent", "percent_off": 15, "plan_ids": []string{}, "per_user_limit": 1, "enabled": true, "version": 1}
	edited := edit(welcome, terms, 200)
	if edited.PercentOff != 15 || edited.Version != 2 || len(edited.PlanIDs) != 0 {
		t.Fatalf("edited %+v", edited)
	}
	edit(welcome, terms, 409) // stale version
	terms["version"], terms["code"] = 2, "RENAMED"
	edit(welcome, terms, 400)
	delete(terms, "code")

	// Once an order references the coupon its discount is frozen; other terms still change.
	buyer := e.user("console-buyer", "user")
	e.place(buyer, "starter", payment.TypeWeChat, "WELCOME-10")
	terms["percent_off"] = 20
	if payErrorCode(e.want(e.call(admin.cookies, "POST", "/api/v1/admin/console/coupons/"+welcome.ID, terms, ""), 409)) != "coupon_in_use" {
		t.Fatal("discount of a used coupon changed")
	}
	terms["percent_off"], terms["enabled"] = 15, false
	if off := edit(welcome, terms, 200); off.Enabled || off.Version != 3 {
		t.Fatalf("disable %+v", off)
	}

	var list struct {
		Coupons []couponAdminView `json:"coupons"`
	}
	w := e.call(admin.cookies, "GET", "/api/v1/admin/console/coupons", nil, "")
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Coupons) != 2 {
		t.Fatalf("list %d", len(list.Coupons))
	}
	for _, c := range list.Coupons {
		if c.Code == "WELCOME-10" && (!c.Referenced || c.Reserved != 1) {
			t.Fatalf("usage of %+v", c)
		}
	}

	// Referenced: hidden but kept; unreferenced: removed.
	if out := e.want(e.call(admin.cookies, "DELETE", "/api/v1/admin/console/coupons/"+welcome.ID, map[string]any{}, ""), 200); out["deleted"] != "soft" {
		t.Fatalf("delete referenced %v", out)
	}
	if out := e.want(e.call(admin.cookies, "DELETE", "/api/v1/admin/console/coupons/"+generated.ID, map[string]any{}, ""), 200); out["deleted"] != "hard" {
		t.Fatalf("delete unreferenced %v", out)
	}
	e.want(e.call(admin.cookies, "DELETE", "/api/v1/admin/console/coupons/"+generated.ID, map[string]any{}, ""), 404)
	var kept model.Coupon
	if err := e.db.First(&kept, "id = ?", welcome.ID).Error; err != nil || kept.DeletedAt == nil || kept.Enabled {
		t.Fatalf("soft-deleted coupon %+v %v", kept, err)
	}
	w = e.call(admin.cookies, "GET", "/api/v1/admin/console/coupons", nil, "")
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Coupons) != 0 {
		t.Fatal("deleted coupons listed")
	}
	w = e.call(admin.cookies, "GET", "/api/v1/admin/console/coupons?include_deleted=true", nil, "")
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Coupons) != 1 {
		t.Fatal("soft-deleted coupon not listed on request")
	}
	// A soft-deleted code is refused and its code stays reserved.
	q := e.want(e.call(buyer.cookies, "POST", "/api/v1/payments/quote", map[string]any{"plan_id": "starter", "coupon_code": "WELCOME-10"}, ""), 200)["quote"].(map[string]any)
	if q["coupon"].(map[string]any)["applied"] != false {
		t.Fatal("deleted coupon applied")
	}
	create(map[string]any{"code": "WELCOME-10", "discount_type": "percent", "percent_off": 5, "per_user_limit": 1}, 409)

	var audits int64
	e.db.Model(&model.AdminAudit{}).Where("actor_id = ? AND action LIKE ?", admin.ID, "admin.coupon_%").Count(&audits)
	if audits != 6 {
		t.Fatalf("audits %d", audits)
	}
}
