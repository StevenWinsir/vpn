package api

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// Orders and payments in the console (handoff_payment_account.md 9.3), and the sign-in
// log (9.4).

// consoleOrder is one order row with its buyer. The review reason is for administrators.
type consoleOrder struct {
	ID           string  `json:"id"`
	TradeNo      *string `json:"trade_no"`
	UserID       string  `json:"user_id"`
	Email        string  `json:"email"`
	PlanID       string  `json:"plan_id"`
	PlanName     string  `json:"plan_name"`
	Provider     string  `json:"provider"`
	PayType      string  `json:"pay_type"`
	Status       string  `json:"status"`
	ReviewReason string  `json:"review_reason"`
	PriceSource  string  `json:"price_source"`
	// Shop snapshots (handoff_shop.md 4.3). OriginalCents is 0 on orders that predate them.
	BillingCycle         string     `json:"billing_cycle"`
	CycleDiscountPercent int        `json:"cycle_discount_percent"`
	OriginalCents        int64      `json:"original_cents"`
	EntMaxDevices        *int       `json:"ent_max_devices"`
	EntAllowDedicated    *bool      `json:"ent_allow_dedicated"`
	ListPriceCents       int64      `json:"list_price_cents"`
	DiscountCents        int64      `json:"discount_cents"`
	AmountCents          int64      `json:"amount_cents"`
	PaidCents            *int64     `json:"paid_cents"`
	PriceCents           int64      `json:"price_cents"`
	CouponCode           string     `json:"coupon_code"`
	CreatedAt            time.Time  `json:"created_at"`
	PaidAt               *time.Time `json:"paid_at"`
	ExpiresAt            *time.Time `json:"expires_at"`
}

func (s *Server) consoleOrderQuery(db *gorm.DB) *gorm.DB {
	schema := s.cfg.Schema
	return db.Table(schema + ".orders AS o").Joins("JOIN " + schema + ".users AS u ON u.id = o.user_id").
		Select(`o.id, o.trade_no, o.user_id, u.email, o.plan_id, o.plan_name, o.provider, o.pay_type, o.status, o.review_reason, o.price_source,
			o.billing_cycle, o.cycle_discount_percent, o.original_cents, o.ent_max_devices, o.ent_allow_dedicated, o.list_price_cents, o.discount_cents, o.amount_cents, o.paid_cents, o.price_cents, o.coupon_code, o.created_at, o.paid_at, o.expires_at`)
}

var orderStatuses = []string{model.OrderPending, model.OrderPaid, model.OrderExpired, model.OrderFailed, model.OrderReview, model.OrderPaidTest}

func (s *Server) consoleOrderList(c *gin.Context) {
	page, size, ok := consolePaging(c)
	if !ok {
		return
	}
	status, ok1 := enumParam(c, "status", orderStatuses...)
	payType, ok2 := enumParam(c, "pay_type", "wxpay", "alipay")
	source, ok3 := enumParam(c, "source", priceList, priceTest, priceTestPurchase)
	cycle, okCycle := enumParam(c, "billing_cycle", model.CycleMonthly, model.CycleYearly)
	email, ok4 := keywordParam(c, "email")
	tradeNo, ok5 := keywordParam(c, "trade_no")
	orderID, ok6 := keywordParam(c, "order_id")
	createdFrom, ok7 := timeParam(c, "created_from", false)
	createdTo, ok8 := timeParam(c, "created_to", true)
	paidFrom, ok9 := timeParam(c, "paid_from", false)
	paidTo, ok10 := timeParam(c, "paid_to", true)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8 && ok9 && ok10 && okCycle) {
		return
	}
	filtered := func() *gorm.DB {
		q := s.consoleOrderQuery(s.database(c))
		if status != "" {
			q = q.Where("o.status = ?", status)
		}
		if payType != "" {
			q = q.Where("o.pay_type = ?", payType)
		}
		if source != "" {
			q = q.Where("o.price_source = ?", source)
		}
		if cycle != "" {
			q = q.Where("o.billing_cycle = ?", cycle)
		}
		if email != "" {
			q = q.Where(`u.email ILIKE ? ESCAPE '\'`, likeContains(email))
		}
		if tradeNo != "" {
			q = q.Where("o.trade_no = ?", tradeNo)
		}
		if orderID != "" {
			if _, err := uuid.Parse(orderID); err != nil {
				q = q.Where("false")
			} else {
				q = q.Where("o.id = ?", orderID)
			}
		}
		if createdFrom != nil {
			q = q.Where("o.created_at >= ?", *createdFrom)
		}
		if createdTo != nil {
			q = q.Where("o.created_at < ?", *createdTo)
		}
		if paidFrom != nil {
			q = q.Where("o.paid_at >= ?", *paidFrom)
		}
		if paidTo != nil {
			q = q.Where("o.paid_at < ?", *paidTo)
		}
		return q
	}
	var total int64
	rows := []consoleOrder{}
	if filtered().Select("count(*)").Scan(&total).Error != nil ||
		filtered().Order("o.created_at DESC, o.id").Limit(size).Offset((page-1)*size).Scan(&rows).Error != nil {
		fail(c, 503, "console_unavailable", "订单列表暂时不可用")
		return
	}
	now := s.now().In(consoleLocation)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, consoleLocation)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, consoleLocation)
	summary := func(since time.Time) (gin.H, error) {
		var r struct {
			Count int64
			Cents int64
		}
		err := s.database(c).Model(&model.Order{}).Where("status = ? AND price_source <> ? AND paid_at >= ?", model.OrderPaid, priceTest, since).
			Select("count(*) AS count, COALESCE(SUM(paid_cents), 0) AS cents").Scan(&r).Error
		return gin.H{"count": r.Count, "cents": r.Cents}, err
	}
	day, err1 := summary(today)
	mon, err2 := summary(month)
	if err1 != nil || err2 != nil {
		fail(c, 503, "console_unavailable", "订单列表暂时不可用")
		return
	}
	c.JSON(200, gin.H{"orders": rows, "total": total, "page": page, "page_size": size,
		"summary": gin.H{"today": day, "month": mon, "note": "按 UTC+8 的自然日和自然月统计已支付订单，不含测试价订单"}})
}

type consoleNotification struct {
	ID          string    `json:"id"`
	ReceivedAt  time.Time `json:"received_at"`
	MoneyText   string    `json:"money_text"`
	TradeStatus string    `json:"trade_status"`
	SourceIP    string    `json:"source_ip"`
	Result      string    `json:"result"`
}

func (s *Server) consoleOrderDetail(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	var order consoleOrder
	res := s.consoleOrderQuery(s.database(c)).Where("o.id = ?", id).Limit(1).Scan(&order)
	if res.Error != nil {
		fail(c, 503, "console_unavailable", "订单详情暂时不可用")
		return
	}
	if res.RowsAffected == 0 {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	notes := []consoleNotification{}
	q := s.database(c).Model(&model.PaymentNotification{}).Select("id, received_at, money_text, trade_status, source_ip, result")
	if order.TradeNo != nil {
		q = q.Where("order_id = ? OR trade_no = ?", id, *order.TradeNo)
	} else {
		q = q.Where("order_id = ?", id)
	}
	var checked struct{ GatewayCheckedAt *time.Time }
	if q.Order("received_at DESC, id").Limit(50).Scan(&notes).Error != nil ||
		s.database(c).Model(&model.Order{}).Select("gateway_checked_at").Where("id = ?", id).Scan(&checked).Error != nil {
		fail(c, 503, "console_unavailable", "订单详情暂时不可用")
		return
	}
	c.JSON(200, gin.H{"order": order, "notifications": notes, "gateway_checked_at": checked.GatewayCheckedAt})
}

// consoleOrderCheck asks the gateway for the order's status. It is read-only: a "paid"
// answer is a hint for the administrator, never an entitlement (decision D5).
func (s *Server) consoleOrderCheck(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	if !s.requirePayment(c) {
		return
	}
	var order model.Order
	if err := s.database(c).Select("id", "trade_no").First(&order, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "order_not_found", "订单不存在")
			return
		}
		fail(c, 503, "console_unavailable", "订单暂时不可用")
		return
	}
	if order.TradeNo == nil {
		fail(c, 409, "no_gateway_order", "此订单不是网关订单，无法查询")
		return
	}
	result, err := s.gateway().Status(c.Request.Context(), *order.TradeNo)
	if err != nil {
		fail(c, 502, "gateway_unavailable", "暂时无法连接支付网关，请稍后再试")
		return
	}
	c.JSON(200, gin.H{"gateway_status": result.Status, "expired": result.Expired, "checked_at": s.now()})
}

const maxConsoleNote = 200

type confirmInput struct {
	AdminPassword string `json:"admin_password"`
	// PaidCents is the amount the administrator verified in the gateway's merchant console.
	PaidCents int64  `json:"paid_cents"`
	Note      string `json:"note"`
}

// maxManualPaidCents bounds the amount an administrator can record (10,000 yuan).
const maxManualPaidCents = int64(1_000_000)

// consoleOrderConfirm settles an order under review by hand, after the administrator
// checked the payment in the gateway's merchant console. It takes the same locks as
// the callback (user, then order) and goes through the same fulfilment, so a callback
// racing it still fulfils exactly once.
func (s *Server) consoleOrderConfirm(c *gin.Context) {
	var in confirmInput
	if !decode(c, &in) {
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" || len([]rune(note)) > maxConsoleNote {
		fail(c, 400, "invalid_note", "请填写核实备注（不超过 200 字）")
		return
	}
	if in.PaidCents < 1 || in.PaidCents > maxManualPaidCents {
		fail(c, 400, "invalid_amount", "请填写在支付网关商户后台核实到的实付金额")
		return
	}
	admin, now := currentUser(c), s.now()
	if !s.reauth(c, admin, in.AdminPassword, now) {
		return
	}
	var head model.Order
	if err := s.database(c).Select("id", "user_id").First(&head, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "order_not_found", "订单不存在")
			return
		}
		fail(c, 503, "console_unavailable", "订单暂时不可用")
		return
	}
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		// Lock order: administrator and buyer by primary key, then the order.
		_, buyer, e := lockConsoleUsers(tx, admin.ID, admin.PasswordHash, head.UserID)
		if e != nil {
			return e
		}
		if buyer == nil {
			return &apiError{409, "user_unavailable", "订单所属账号已不存在"}
		}
		order, e := lockOrder(tx, id)
		if e != nil {
			return e
		}
		if order == nil {
			return &apiError{404, "order_not_found", "订单不存在"}
		}
		if order.Status != model.OrderReview {
			return &apiError{409, "order_not_reviewable", "订单已被处理或不在待核对状态，请刷新后查看"}
		}
		if buyer.Status != "active" {
			return &apiError{409, "user_unavailable", "订单所属账号已停用或正在删除"}
		}
		ok, e := s.fulfilOrder(tx, order, in.PaidCents, now)
		if e != nil {
			return e
		}
		if !ok {
			return &apiError{409, "fulfil_conflict", "当前订阅状态不允许开通此订单（例如套餐不同）。请先处理该用户的订阅后再确认。"}
		}
		return consoleAudit(tx, admin.ID, AuditPaymentConfirm, order.ID, 0,
			gin.H{"trade_no_tail": tailOf(order.TradeNo), "review_reason": order.ReviewReason, "expected_cents": order.AmountCents, "paid_cents": in.PaidCents, "note": note}, now)
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"confirmed": true, "message": "订单已确认到账并开通。"})
}

func tailOf(tradeNo *string) string {
	if tradeNo == nil {
		return ""
	}
	return tail(*tradeNo)
}

type closeInput struct {
	Note string `json:"note"`
}

// consoleOrderClose closes an order under review without granting anything. A closed
// order is final: a later callback for it is recorded and ignored (money received for
// it is settled offline).
func (s *Server) consoleOrderClose(c *gin.Context) {
	var in closeInput
	if !decode(c, &in) {
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" || len([]rune(note)) > maxConsoleNote {
		fail(c, 400, "invalid_note", "请填写关闭原因（不超过 200 字）")
		return
	}
	admin, now := currentUser(c), s.now()
	var head model.Order
	if err := s.database(c).Select("id", "user_id").First(&head, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, "order_not_found", "订单不存在")
			return
		}
		fail(c, 503, "console_unavailable", "订单暂时不可用")
		return
	}
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if _, _, e := lockConsoleUsers(tx, admin.ID, "", head.UserID); e != nil {
			return e
		}
		order, e := lockOrder(tx, id)
		if e != nil {
			return e
		}
		if order == nil {
			return &apiError{404, "order_not_found", "订单不存在"}
		}
		if order.Status != model.OrderReview {
			return &apiError{409, "order_not_reviewable", "订单已被处理或不在待核对状态，请刷新后查看"}
		}
		if e = tx.Model(order).Updates(map[string]any{"status": model.OrderFailed, "review_reason": model.ReviewClosed, "updated_at": now}).Error; e != nil {
			return e
		}
		return consoleAudit(tx, admin.ID, AuditPaymentClose, order.ID, 0,
			gin.H{"trade_no_tail": tailOf(order.TradeNo), "review_reason": order.ReviewReason, "note": note}, now)
	})
	if consoleFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"closed": true, "message": "订单已关闭，未开通任何权益。"})
}

// consoleLogin is one sign-in attempt for the log panel.
type consoleLogin struct {
	ID            string    `json:"id"`
	OccurredAt    time.Time `json:"occurred_at"`
	UserID        string    `json:"user_id"`
	Email         string    `json:"email"`
	Channel       string    `json:"channel"`
	Platform      string    `json:"platform"`
	IP            string    `json:"ip"`
	GeoStatus     string    `json:"geo_status"`
	GeoText       string    `json:"geo_text"`
	UserAgent     string    `json:"user_agent"`
	Result        string    `json:"result"`
	FailureReason string    `json:"failure_reason"`
}

func loginView(ev model.LoginEvent, email string) consoleLogin {
	return consoleLogin{ID: ev.ID, OccurredAt: ev.OccurredAt, UserID: ev.UserID, Email: email, Channel: ev.Channel, Platform: ev.Platform, IP: ev.IP,
		GeoStatus: ev.GeoStatus, GeoText: ev.GeoText, UserAgent: ev.UserAgent, Result: ev.Result, FailureReason: ev.FailureReason}
}

// Quick filters of the log panel (handoff_payment_account.md 9.4).
const (
	burstWindow    = time.Hour
	burstFailures  = 5
	sharedIPWindow = 24 * time.Hour
	sharedIPUsers  = 2
)

func (s *Server) consoleLoginList(c *gin.Context) {
	page, size, ok := consolePaging(c)
	if !ok {
		return
	}
	email, ok1 := keywordParam(c, "email")
	ip, ok2 := keywordParam(c, "ip")
	channel, ok3 := enumParam(c, "channel", model.LoginWeb, model.LoginNative)
	result, ok4 := enumParam(c, "result", model.LoginSuccess, model.LoginFailure)
	quick, ok5 := enumParam(c, "quick", "shared_ip", "burst_failures")
	from, ok6 := timeParam(c, "from", false)
	to, ok7 := timeParam(c, "to", true)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) {
		return
	}
	now := s.now()
	schema := s.cfg.Schema
	filtered := func() *gorm.DB {
		q := s.database(c).Table(schema + ".login_events AS e").Joins("JOIN " + schema + ".users AS u ON u.id = e.user_id")
		if email != "" {
			q = q.Where(`u.email ILIKE ? ESCAPE '\'`, likeContains(email))
		}
		if ip != "" {
			q = q.Where("e.ip = ?", ip)
		}
		if channel != "" {
			q = q.Where("e.channel = ?", channel)
		}
		if result != "" {
			q = q.Where("e.result = ?", result)
		}
		if from != nil {
			q = q.Where("e.occurred_at >= ?", *from)
		}
		if to != nil {
			q = q.Where("e.occurred_at < ?", *to)
		}
		switch quick {
		case "shared_ip":
			q = q.Where("e.ip IN (SELECT ip FROM "+schema+".login_events WHERE occurred_at >= ? GROUP BY ip HAVING count(DISTINCT user_id) >= ?)", now.Add(-sharedIPWindow), sharedIPUsers).
				Where("e.occurred_at >= ?", now.Add(-sharedIPWindow))
		case "burst_failures":
			q = q.Where("e.result = ?", model.LoginFailure).Where("e.occurred_at >= ?", now.Add(-burstWindow)).
				Where("e.user_id IN (SELECT user_id FROM "+schema+".login_events WHERE result = ? AND occurred_at >= ? GROUP BY user_id HAVING count(*) >= ?)", model.LoginFailure, now.Add(-burstWindow), burstFailures)
		}
		return q
	}
	var total int64
	rows := []consoleLogin{}
	if filtered().Select("count(*)").Scan(&total).Error != nil ||
		filtered().Select(`e.id, e.occurred_at, e.user_id, u.email, e.channel, e.platform, e.ip, e.geo_status, e.geo_text, e.user_agent, e.result, e.failure_reason`).
			Order("e.occurred_at DESC, e.id").Limit(size).Offset((page-1)*size).Scan(&rows).Error != nil {
		fail(c, 503, "console_unavailable", "登录记录暂时不可用")
		return
	}
	c.JSON(200, gin.H{"logins": rows, "total": total, "page": page, "page_size": size})
}
