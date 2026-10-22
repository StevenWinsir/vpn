package api

import (
	"errors"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// Gateway payments (handoff_payment_account.md 6). The browser never supplies an
// amount: every price is recomputed here from the plan, the test-price list and the
// coupon, and the gateway URL is built only from server values.

func (s *Server) gateway() payment.Gateway {
	return payment.Gateway{BaseURL: s.cfg.Payment.GatewayURL, Channel: s.cfg.Payment.Channel, Secret: s.cfg.Payment.Secret}
}

func (s *Server) requirePayment(c *gin.Context) bool {
	if !s.cfg.Payment.Enabled {
		fail(c, 503, "payment_disabled", "在线支付暂未开放")
		return false
	}
	return true
}

type quoteInput struct {
	PlanID     string `json:"plan_id"`
	CouponCode string `json:"coupon_code"`
}

type couponView struct {
	Code    string `json:"code"`
	Applied bool   `json:"applied"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type quoteView struct {
	PlanID         string      `json:"plan_id"`
	PlanName       string      `json:"plan_name"`
	DurationDays   int         `json:"duration_days"`
	TrafficBytes   int64       `json:"traffic_bytes"`
	ListPriceCents int64       `json:"list_price_cents"`
	TestPrice      bool        `json:"test_price"`
	BaseCents      int64       `json:"base_cents"`
	DiscountCents  int64       `json:"discount_cents"`
	AmountCents    int64       `json:"amount_cents"`
	Coupon         *couponView `json:"coupon"`
	Purchasable    bool        `json:"purchasable"`
	Kind           string      `json:"kind,omitempty"`
	Reason         string      `json:"reason,omitempty"`
	Message        string      `json:"message,omitempty"`
}

func validPlanID(id string) bool { return id != "" && len(id) <= 32 }

// loadPurchasablePlan returns an active plan with a positive price (no 0-yuan orders, D13).
func loadPurchasablePlan(tx *gorm.DB, id string) (model.Plan, error) {
	var plan model.Plan
	err := tx.Where("id = ? AND active = ?", id, true).First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return plan, &apiError{404, "plan_not_found", "套餐不存在或已下架"}
	}
	if err != nil {
		return plan, err
	}
	if plan.PriceCents <= 0 || !usablePlan(plan) {
		return plan, &apiError{409, "plan_not_purchasable", "此套餐暂不支持在线购买"}
	}
	return plan, nil
}

// couponGate refuses a code check while the user is over the invalid-attempt limit.
func (s *Server) couponGate(c *gin.Context, user model.User, code string, now time.Time) bool {
	if code == "" {
		return true
	}
	count, until, err := s.peekLimit(s.database(c), scopePayCoupon, user.ID, payCouponWindow, now)
	if err != nil {
		fail(c, 503, "payment_unavailable", "支付服务暂时不可用")
		return false
	}
	if count >= payCouponLimit {
		retryAfter(c, until, now)
		fail(c, 429, "coupon_rate_limited", "优惠码尝试次数过多，请稍后再试")
		return false
	}
	return true
}

// countCouponMiss records one failed code attempt; best effort, outside any transaction.
func (s *Server) countCouponMiss(c *gin.Context, user model.User, now time.Time) {
	_, _, _ = s.bumpLimit(s.database(c), scopePayCoupon, user.ID, payCouponWindow, now)
}

func (s *Server) paymentQuote(c *gin.Context) {
	if !s.requirePayment(c) {
		return
	}
	var in quoteInput
	if !decode(c, &in) {
		return
	}
	code := normalizeCoupon(in.CouponCode)
	if !validPlanID(in.PlanID) || len(code) > 64 {
		fail(c, 400, "invalid_plan", "请选择有效套餐")
		return
	}
	user, now := currentUser(c), s.now()
	if !s.couponGate(c, user, code, now) {
		return
	}
	var view quoteView
	var problem *couponProblem
	var pending int64
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		plan, err := loadPurchasablePlan(tx, in.PlanID)
		if err != nil {
			return err
		}
		sub, err := readSubscription(tx, user.ID)
		if err != nil {
			return err
		}
		p, prob, err := s.price(tx, user, plan, code, false, now)
		if err != nil {
			return err
		}
		problem = prob
		view = quoteView{PlanID: plan.ID, PlanName: plan.Name, DurationDays: plan.DurationDays, TrafficBytes: plan.TrafficBytes, ListPriceCents: p.ListCents, TestPrice: p.Source == priceTest, BaseCents: p.BaseCents, DiscountCents: p.DiscountCents, AmountCents: p.AmountCents, Purchasable: true}
		if code != "" {
			view.Coupon = &couponView{Code: code, Applied: prob == nil}
			if prob != nil {
				view.Coupon.Reason, view.Coupon.Message = prob.Reason, prob.Message
			}
		}
		renew, decision := planDecision(sub, plan, now, false)
		var business *apiError
		switch {
		case errors.As(decision, &business):
			view.Purchasable, view.Reason, view.Message = false, business.Code, business.Message
		case decision != nil:
			return decision
		case renew:
			view.Kind = "renew"
		default:
			view.Kind = "new"
		}
		return livePending(tx.Model(&model.Order{}), now).Where("user_id = ?", user.ID).Count(&pending).Error
	})
	if problem != nil {
		s.countCouponMiss(c, user, now)
	}
	if paymentFailure(c, err) {
		return
	}
	c.JSON(200, gin.H{"quote": view, "pending_orders": pending})
}

type orderInput struct {
	PlanID     string `json:"plan_id"`
	PayType    string `json:"pay_type"`
	CouponCode string `json:"coupon_code"`
}

// orderView is the user's view of a gateway order. The review reason stays internal.
type orderView struct {
	model.Order
	GatewayPaid bool `json:"gateway_paid,omitempty"`
}

func (s *Server) createPaymentOrder(c *gin.Context) {
	if !s.requirePayment(c) {
		return
	}
	var in orderInput
	if !decode(c, &in) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if _, err := uuid.Parse(key); err != nil {
		fail(c, 400, "idempotency_required", "请提供 UUID 格式的 Idempotency-Key")
		return
	}
	code := normalizeCoupon(in.CouponCode)
	if !validPlanID(in.PlanID) || len(code) > 64 {
		fail(c, 400, "invalid_plan", "请选择有效套餐")
		return
	}
	if !payment.ValidType(in.PayType) {
		fail(c, 400, "invalid_pay_type", "请选择微信支付或支付宝支付")
		return
	}
	user, now := currentUser(c), s.now()
	count, until, err := s.bumpLimit(s.database(c), scopePayOrder, user.ID, payOrderWindow, now)
	if err != nil {
		fail(c, 503, "payment_unavailable", "支付服务暂时不可用")
		return
	}
	if count > payOrderLimit {
		retryAfter(c, until, now)
		fail(c, 429, "order_rate_limited", "下单过于频繁，请稍后再试")
		return
	}
	if !s.couponGate(c, user, code, now) {
		return
	}
	var order model.Order
	var problem *couponProblem
	var others int64
	replayed := false
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		// Lock order: User -> Coupon (inside price) -> Order insert.
		locked, err := lockUserRow(tx, user.ID)
		if err != nil {
			return err
		}
		if locked == nil || locked.Status != "active" {
			return &apiError{403, "account_disabled", "账号已停用"}
		}
		err = tx.Where("user_id = ? AND idempotency_key = ?", user.ID, key).First(&order).Error
		if err == nil {
			if order.TradeNo == nil || order.PlanID != in.PlanID || order.PayType != in.PayType || order.CouponCode != code {
				return &apiError{409, "idempotency_conflict", "此请求标识已用于其他订单"}
			}
			replayed = true
			return livePending(tx.Model(&model.Order{}), now).Where("user_id = ? AND id <> ?", user.ID, order.ID).Count(&others).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var live []model.Order
		if err = livePending(tx.Select("id", "expires_at"), now).Where("user_id = ?", user.ID).Order("expires_at asc").Find(&live).Error; err != nil {
			return err
		}
		if len(live) >= s.cfg.Payment.Pending() {
			retryAfter(c, *live[0].ExpiresAt, now)
			return &apiError{429, "too_many_pending_orders", "未完成的订单过多，请先完成支付或等待订单超时"}
		}
		others = int64(len(live))
		plan, err := loadPurchasablePlan(tx, in.PlanID)
		if err != nil {
			return err
		}
		sub, err := readSubscription(tx, user.ID)
		if err != nil {
			return err
		}
		if _, err = planDecision(sub, plan, now, false); err != nil {
			return err
		}
		p, prob, err := s.price(tx, *locked, plan, code, true, now)
		if err != nil {
			return err
		}
		if prob != nil {
			problem = prob
			return &apiError{409, prob.Reason, prob.Message}
		}
		tradeNo, err := s.newTradeNo(tx, now)
		if err != nil {
			return err
		}
		expires := now.Add(s.cfg.Payment.Window())
		order = model.Order{ID: uuid.NewString(), UserID: user.ID, IdempotencyKey: key, PlanID: plan.ID, PlanName: plan.Name, PriceCents: p.AmountCents, DurationDays: plan.DurationDays, TrafficBytes: plan.TrafficBytes,
			Status: model.OrderPending, Provider: "gateway", CreatedAt: now, TradeNo: &tradeNo, PayType: in.PayType, ListPriceCents: p.ListCents, DiscountCents: p.DiscountCents, AmountCents: p.AmountCents,
			PriceSource: p.Source, ExpiresAt: &expires, UpdatedAt: &now}
		if p.Coupon != nil {
			order.CouponID, order.CouponCode = &p.Coupon.ID, p.Coupon.Code
		}
		return tx.Omit("User").Create(&order).Error
	})
	if problem != nil {
		s.countCouponMiss(c, user, now)
	}
	if paymentFailure(c, err) {
		return
	}
	status := 201
	if replayed {
		status = 200
	}
	c.JSON(status, gin.H{"order": orderView{Order: order}, "pay_url": s.payURL(order), "replayed": replayed, "other_pending_orders": others})
}

// newTradeNo draws a fresh 20-digit number; a collision (practically impossible) is
// redrawn before the insert so the transaction never aborts on the unique index.
func (s *Server) newTradeNo(tx *gorm.DB, now time.Time) (string, error) {
	for range 3 {
		candidate, err := payment.NewTradeNo(now)
		if err != nil {
			return "", err
		}
		var taken int64
		if err = tx.Model(&model.Order{}).Where("trade_no = ?", candidate).Count(&taken).Error; err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("trade number space exhausted")
}

// payURL is the gateway submit URL for an order. It carries the signature: never log it.
func (s *Server) payURL(o model.Order) string {
	return s.gateway().SubmitURL(payment.SubmitRequest{Type: o.PayType, TradeNo: *o.TradeNo, NotifyURL: s.cfg.Payment.NotifyURL, ReturnURL: s.returnURL(o.ID), Name: o.PlanName, AmountCents: o.AmountCents})
}

// returnURL is the fixed result page; it carries the local order id only.
func (s *Server) returnURL(orderID string) string {
	return s.cfg.WebsiteBaseURL + "/pay/result?order=" + url.QueryEscape(orderID)
}

// statusRecheck bounds how often one order's result page may query the gateway.
const statusRecheck = 10 * time.Second

func (s *Server) paymentOrder(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	var order model.Order
	err := s.database(c).Where("id = ? AND user_id = ?", id, currentUser(c).ID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "order_not_found", "订单不存在")
		return
	}
	if err != nil {
		fail(c, 503, "database_unavailable", "订单暂时不可用")
		return
	}
	view := orderView{Order: order}
	// Display hint only: a gateway "paid" never fulfils an order (D5).
	if order.Status == model.OrderPending && order.TradeNo != nil && s.cfg.Payment.Enabled {
		now := s.now()
		claim := s.database(c).Model(&model.Order{}).Where("id = ? AND status = ? AND (gateway_checked_at IS NULL OR gateway_checked_at <= ?)", order.ID, model.OrderPending, now.Add(-statusRecheck)).Update("gateway_checked_at", now)
		if claim.Error == nil && claim.RowsAffected == 1 {
			if result, e := s.gateway().Status(c.Request.Context(), *order.TradeNo); e == nil && result.Status == payment.StatusPaid {
				view.GatewayPaid = true
			}
		}
	}
	c.JSON(200, gin.H{"order": view})
}

// paymentFailure maps business errors to their status and everything else to 503.
func paymentFailure(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var business *apiError
	if errors.As(err, &business) {
		fail(c, business.Status, business.Code, business.Message)
		return true
	}
	fail(c, 503, "payment_unavailable", "支付服务暂时不可用，请稍后重试")
	return true
}

// lockOrder re-reads an order under its row lock (the caller already holds the user lock).
func lockOrder(tx *gorm.DB, id string) (*model.Order, error) {
	var order model.Order
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &order, err
}

// paymentMeta is the public part of the payment configuration: switches only, never
// the merchant number, callback address or whether a test price is active.
func paymentMeta(cfg config.Config) gin.H {
	methods := []string{}
	if cfg.Payment.Enabled {
		methods = []string{payment.TypeWeChat, payment.TypeAlipay}
	}
	return gin.H{"payment_enabled": cfg.Payment.Enabled, "payment_methods": methods, "payment_pending_seconds": int(cfg.Payment.Window().Seconds())}
}
