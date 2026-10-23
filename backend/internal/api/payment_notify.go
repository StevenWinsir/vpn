package api

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// maxNotifyBytes bounds the callback body; the documented payload is far smaller.
const maxNotifyBytes = 4 << 10

// paymentNotify receives the gateway callback (handoff_payment_account.md 6.3). The
// callback carries no signature and the submit signature does not cover the amount,
// so a callback fulfils an order only when it comes from a configured gateway address
// and pays at least the expected amount. Every callback is logged first. The gateway
// is answered 200 for every handled case; only a storage failure answers 500.
func (s *Server) paymentNotify(c *gin.Context) {
	now := s.now()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxNotifyBytes+1))
	var n payment.Notification
	parseErr := payment.ErrNotification
	if err == nil && len(body) <= maxNotifyBytes {
		n, parseErr = payment.ParseNotification(body)
	}
	record := model.PaymentNotification{ID: uuid.NewString(), TradeNo: n.TradeNo, MoneyText: n.MoneyText, TradeStatus: n.TradeStatus, Name: n.Name, SourceIP: c.ClientIP(), Result: model.NotifyReceived, ReceivedAt: now}
	if err = s.database(c).Create(&record).Error; err != nil {
		log.Print("payment notify: callback could not be stored (details suppressed)")
		c.JSON(500, gin.H{"code": 500, "message": "服务器错误"})
		return
	}
	result, orderID, err := s.judgeNotification(c.Request.Context(), n, parseErr, record.SourceIP, now)
	if err != nil {
		log.Printf("payment notify %s: processing failed; left for reconciliation", record.ID)
		c.JSON(500, gin.H{"code": 500, "message": "服务器错误"})
		return
	}
	if e := s.database(c).Model(&record).Updates(map[string]any{"result": result, "order_id": orderID}).Error; e != nil {
		log.Printf("payment notify %s: result %s could not be recorded", record.ID, result)
	}
	if result == model.NotifyReview || result == model.NotifyIPRejected {
		log.Printf("payment notify %s: %s (trade …%s)", record.ID, result, tail(n.TradeNo))
	}
	c.JSON(200, gin.H{"code": 200, "message": "处理成功"})
}

func tail(tradeNo string) string {
	if len(tradeNo) <= 6 {
		return tradeNo
	}
	return tradeNo[len(tradeNo)-6:]
}

// judgeNotification decides one callback. It returns the logged result and the matched
// order id; err is only a storage failure.
func (s *Server) judgeNotification(ctx context.Context, n payment.Notification, parseErr error, source string, now time.Time) (string, *string, error) {
	if parseErr != nil {
		return model.NotifyInvalid, nil, nil
	}
	trusted := len(s.cfg.Payment.NotifyAllowed) > 0
	if trusted && !s.cfg.Payment.AllowsNotifySource(source) {
		return model.NotifyIPRejected, nil, nil
	}
	var head model.Order
	err := s.db.WithContext(ctx).Select("id", "user_id").Where("trade_no = ?", n.TradeNo).Take(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.NotifyUnknownOrder, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	result := model.NotifyIgnored
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock order: User -> Order -> Coupon -> Subscription -> Period.
		user, e := lockUserRow(tx, head.UserID)
		if e != nil {
			return e
		}
		order, e := lockOrder(tx, head.ID)
		if e != nil || order == nil {
			return e
		}
		if order.TradeNo == nil || *order.TradeNo != n.TradeNo {
			return nil
		}
		if order.Status == model.OrderPaid {
			result = model.NotifyDuplicate
			return nil
		}
		if n.TradeStatus == payment.TradeFailed {
			if order.Status == model.OrderPending {
				result = model.NotifyAccepted
				return tx.Model(order).Updates(map[string]any{"status": model.OrderFailed, "updated_at": now}).Error
			}
			return nil
		}
		if !successProcessable(*order) {
			return nil
		}
		paid := n.Cents
		review := func(reason string) error {
			result = model.NotifyReview
			return tx.Model(order).Updates(map[string]any{"status": model.OrderReview, "review_reason": reason, "paid_cents": paid, "updated_at": now}).Error
		}
		if paid < order.AmountCents || paid > order.AmountCents+s.cfg.Payment.ToleranceCents {
			return review(model.ReviewAmountMismatch)
		}
		if !trusted {
			return review(model.ReviewUnverifiedSource)
		}
		if user == nil || user.Status != "active" {
			return review(model.ReviewStateConflict)
		}
		ok, e := s.fulfilOrder(tx, order, paid, now)
		if e != nil {
			return e
		}
		if !ok {
			return review(model.ReviewStateConflict)
		}
		result = model.NotifyAccepted
		return nil
	})
	if err != nil {
		return "", &head.ID, err
	}
	return result, &head.ID, nil
}

// successProcessable: states a genuine success callback may still settle. A late
// payment for an expired or failed order is honoured (the money was taken); an order a
// callback already put under review keeps that verdict for the administrator.
func successProcessable(o model.Order) bool {
	switch o.Status {
	case model.OrderPending, model.OrderExpired:
		return true
	case model.OrderFailed:
		return o.ReviewReason != model.ReviewClosed
	case model.OrderReview:
		return o.ReviewReason == model.ReviewCallbackMissing || o.ReviewReason == model.ReviewGatewayUnreachable
	}
	return false
}

// fulfilOrder marks a locked order paid and applies its plan, in the caller's
// transaction (which holds the user and order locks). It returns false, changing
// nothing, when the purchase can no longer apply (for example the plan changed
// meanwhile); the caller then puts the order under review.
func (s *Server) fulfilOrder(tx *gorm.DB, order *model.Order, paidCents int64, now time.Time) (bool, error) {
	var plan model.Plan
	if err := tx.Where("id = ?", order.PlanID).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	// Deliver exactly what was sold: the order's own snapshot, not today's plan terms.
	plan.Name, plan.DurationDays, plan.TrafficBytes = order.PlanName, order.DurationDays, order.TrafficBytes
	var coupon *model.Coupon
	if order.CouponID != nil {
		coupon = &model.Coupon{}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(coupon, "id = ?", *order.CouponID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			coupon = nil
		} else if err != nil {
			return false, err
		}
	}
	if err := tx.SavePoint("fulfil").Error; err != nil {
		return false, err
	}
	if _, err := applyPlan(tx, order.UserID, plan, now, false); err != nil {
		var business *apiError
		if errors.As(err, &business) {
			return false, tx.RollbackTo("fulfil").Error
		}
		return false, err
	}
	if coupon != nil {
		if err := tx.Model(coupon).Update("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
			return false, err
		}
	}
	return true, tx.Model(order).Updates(map[string]any{"status": model.OrderPaid, "paid_cents": paidCents, "paid_at": now, "review_reason": "", "updated_at": now}).Error
}

// Reconciliation (handoff_payment_account.md 6.4).
const (
	reconcileBatch    = 50
	reconcileInterval = 30 * time.Second
	reconcileGiveUp   = 30 * time.Minute
)

// ReconcilePayments settles gateway orders whose local window has passed. It runs in
// every API instance; a claim on gateway_checked_at (SKIP LOCKED) keeps instances from
// querying the same order at once. It never fulfils: a gateway "paid" without a
// trusted callback becomes an order under review.
func ReconcilePayments(ctx context.Context, db *gorm.DB, cfg config.Config) (int, error) {
	s := &Server{db: db, cfg: cfg}
	return s.reconcilePayments(ctx)
}

type claimedOrder struct {
	ID        string
	TradeNo   *string
	CreatedAt time.Time
}

func (s *Server) reconcilePayments(ctx context.Context) (int, error) {
	if !s.cfg.Payment.Enabled {
		return 0, nil
	}
	now := s.now()
	db := s.db.WithContext(ctx)
	orders := s.cfg.Schema + ".orders"
	var claimed []claimedOrder
	err := db.Raw(`UPDATE `+orders+` SET gateway_checked_at = ? WHERE id IN (
		SELECT id FROM `+orders+` WHERE status = ? AND trade_no IS NOT NULL AND expires_at <= ? AND (gateway_checked_at IS NULL OR gateway_checked_at <= ?)
		ORDER BY expires_at LIMIT ? FOR UPDATE SKIP LOCKED) RETURNING id, trade_no, created_at`,
		now, model.OrderPending, now, now.Add(-reconcileInterval), reconcileBatch).Scan(&claimed).Error
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, head := range claimed {
		if ctx.Err() != nil {
			break
		}
		result, gatewayErr := s.gateway().Status(ctx, *head.TradeNo)
		next, reason := "", ""
		switch {
		case gatewayErr != nil:
			if now.Sub(head.CreatedAt) > reconcileGiveUp {
				next, reason = model.OrderReview, model.ReviewGatewayUnreachable
			}
		case result.Status == payment.StatusPaid:
			next, reason = model.OrderReview, model.ReviewCallbackMissing
		case result.Status == payment.StatusTimeout || result.Status == payment.StatusNotFound || result.Expired:
			next = model.OrderExpired
		case now.Sub(head.CreatedAt) > reconcileGiveUp:
			next, reason = model.OrderReview, model.ReviewGatewayUnreachable
		}
		if next == "" {
			continue
		}
		moved, e := s.settlePending(ctx, head.ID, next, reason, now)
		if moved {
			changed++
		}
		err = e
		if err != nil {
			return changed, err
		}
	}
	// Payment limit buckets are only read inside their window; drop expired ones.
	if e := db.Exec(`DELETE FROM `+s.cfg.Schema+`.auth_rate_buckets WHERE scope IN (?, ?) AND expires_at < ?`, scopePayOrder, scopePayCoupon, now).Error; e != nil && err == nil {
		err = e
	}
	return changed, err
}

// settlePending moves an order that is still pending to next, under the user and order
// locks, so it cannot race a callback or a manual confirmation: whichever commits second
// finds the order already settled. It reports whether it changed the order.
func (s *Server) settlePending(ctx context.Context, id, next, reason string, now time.Time) (bool, error) {
	moved := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if e := tx.Select("user_id").First(&o, "id = ?", id).Error; e != nil {
			return e
		}
		if _, e := lockUserRow(tx, o.UserID); e != nil {
			return e
		}
		order, e := lockOrder(tx, id)
		if e != nil || order == nil || order.Status != model.OrderPending {
			return e
		}
		moved = true
		return tx.Model(order).Updates(map[string]any{"status": next, "review_reason": reason, "updated_at": now}).Error
	})
	return moved && err == nil, err
}
