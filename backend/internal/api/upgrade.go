package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/model"
	"vpn/backend/internal/payment"
)

// Mid-period upgrades (handoff_plans.md): a paid, valid subscription may move to a
// higher plan for the rest of its period. Expiry and period stay; the price is the new
// plan's value for the remaining days less what is left of the money paid for them
// (billing/upgrade.go). Downgrades and other changes wait for the expiry, with no refund.

const upgradeNotUpgradable = "not_upgradable"

var (
	errNoUpgradeBase    = &apiError{409, upgradeNotUpgradable, "当前没有可升级的有效套餐，请直接购买"}
	errUpgradeCurrent   = &apiError{409, upgradeNotUpgradable, "这是你当前的套餐，请使用续费"}
	errUpgradeUnknown   = &apiError{409, upgradeNotUpgradable, "当前套餐数据不完整，请联系客服"}
	errUpgradeTooShort  = &apiError{409, "remaining_too_short", "剩余不足 1 天，请到期后直接购买新套餐"}
	errPendingOrder     = &apiError{409, "pending_order_exists", "你有未完成的订单，请先完成支付或等待约 2 分钟后再试"}
	errUpgradeAmountBad = &apiError{400, "invalid_expected_amount", "请先确认升级报价"}
)

// expiryDay is the date shown in the refusals (UTC+8, like the console).
func expiryDay(t time.Time) string { return t.In(consoleLocation).Format("2006-01-02") }

func upgradeDisabledError(sub *model.Subscription) *apiError {
	return &apiError{409, "upgrade_disabled", fmt.Sprintf("暂未开放有效期内升级，请在 %s 到期后购买", expiryDay(sub.ExpiresAt))}
}

// relationError is the refusal for a plan that is not an upgrade of the current one.
func relationError(relation string, sub *model.Subscription, to model.Plan) *apiError {
	switch relation {
	case billing.RelationDowngrade:
		return &apiError{409, upgradeNotUpgradable, fmt.Sprintf("当前套餐有效期内不支持降级（降级不退还差价），请在 %s 到期后购买", expiryDay(sub.ExpiresAt))}
	case billing.RelationLateral:
		return &apiError{409, upgradeNotUpgradable, fmt.Sprintf("%s 与当前套餐权益不可直接升级，请在 %s 到期后购买", to.Name, expiryDay(sub.ExpiresAt))}
	case billing.RelationCurrent:
		return errUpgradeCurrent
	}
	return errNoUpgradeBase
}

// upgradeSettings is the shop's switch and minimum charge; a missing row reads as the
// defaults (doctor reports it).
type upgradeSettings struct {
	Enabled        bool
	MinChargeCents int64
}

func readUpgradeSettings(db *gorm.DB) (upgradeSettings, error) {
	var row model.ShopSettings
	err := db.Select("upgrade_enabled", "upgrade_min_charge_cents").First(&row, "id = 1").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return upgradeSettings{Enabled: true, MinChargeCents: model.DefaultUpgradeMinChargeCents}, nil
	}
	if err != nil {
		return upgradeSettings{}, err
	}
	return upgradeSettings{Enabled: row.UpgradeEnabled, MinChargeCents: row.UpgradeMinChargeCents}, nil
}

// upgradeSnapshot is what an upgrade order sold and why it cost what it did. It is stored
// on the order (never shown to the customer) and is all fulfilment reads.
type upgradeSnapshot struct {
	SubscriptionID   string                 `json:"subscription_id"`
	StartsAt         time.Time              `json:"starts_at"`
	ExpiresAt        time.Time              `json:"expires_at"`
	Refreshing       bool                   `json:"refreshing"`
	ChargeDays       int                    `json:"charge_days"`
	CreditDays       int                    `json:"credit_days"`
	ValueRate        billing.Rate           `json:"value_rate"`
	ValueCents       int64                  `json:"value_cents"`
	CreditCents      int64                  `json:"credit_cents"`
	MinChargeCents   int64                  `json:"min_charge_cents"`
	MinChargeApplied bool                   `json:"min_charge_applied"`
	ToTraffic        billing.Rate           `json:"to_traffic"`
	FromTraffic      billing.Rate           `json:"from_traffic"`
	NewCycleBytes    int64                  `json:"new_cycle_bytes,omitempty"`
	TrafficAddBytes  int64                  `json:"traffic_add_bytes"`
	Contributions    []billing.Contribution `json:"contributions"`
	LostGroups       []string               `json:"lost_groups,omitempty"`
}

// upgradeEval is one evaluation of moving sub to a plan at now. Problem is nil when the
// upgrade can be bought; the figures are filled in as far as they could be computed.
type upgradeEval struct {
	Relation        string
	Problem         *apiError
	From, To        model.Plan
	Snap            upgradeSnapshot
	ListAmountCents int64
	AmountCents     int64
	TestPrice       bool
	Cycle           string
	CyclePercent    int
}

// periodOrders are the paid orders of sub's current period that carry money.
func periodOrders(tx *gorm.DB, sub *model.Subscription) ([]model.Order, error) {
	var rows []model.Order
	err := tx.Where("user_id = ? AND status = ? AND provider <> ? AND paid_at >= ?", sub.UserID, model.OrderPaid, "test", sub.StartsAt).
		Order("paid_at, id").Find(&rows).Error
	return rows, err
}

// orderWindow is the time an order's value covers: its recorded window, or for an order
// fulfilled without one [paid_at, paid_at + days], which never starts later than the
// real window and so can only credit less (handoff_plans.md 6.4).
func orderWindow(o model.Order) (time.Time, time.Time, bool) {
	if o.CoversFrom != nil && o.CoversTo != nil && o.CoversTo.After(*o.CoversFrom) {
		return *o.CoversFrom, *o.CoversTo, true
	}
	if o.PaidAt != nil && o.DurationDays > 0 && o.Kind != model.OrderKindUpgrade {
		return *o.PaidAt, o.PaidAt.Add(time.Duration(o.DurationDays) * 24 * time.Hour), true
	}
	return time.Time{}, time.Time{}, false
}

// orderCents is the money an order brought in: its price, or less if less was paid.
func orderCents(o model.Order) int64 {
	cents := o.AmountCents
	if o.PaidCents != nil && *o.PaidCents < cents {
		cents = *o.PaidCents
	}
	return cents
}

// soldTraffic is the traffic per day an order sold for its plan, if known.
func soldTraffic(o model.Order) (billing.Rate, bool) {
	if o.Kind == model.OrderKindUpgrade {
		var snap upgradeSnapshot
		if json.Unmarshal([]byte(o.UpgradeSnapshot), &snap) != nil || !snap.ToTraffic.Valid() {
			return billing.Rate{}, false
		}
		return snap.ToTraffic, true
	}
	if o.QuotaCycleDays > 0 && o.QuotaCycleBytes > 0 {
		return billing.Rate{Amount: o.QuotaCycleBytes, Days: o.QuotaCycleDays}, true
	}
	if o.DurationDays > 0 && o.TrafficBytes > 0 {
		return billing.Rate{Amount: o.TrafficBytes, Days: o.DurationDays}, true
	}
	return billing.Rate{}, false
}

// cycleDaysLeft is the current refresh cycle's remaining time in days, rounded up.
func cycleDaysLeft(sub *model.Subscription, now time.Time) int {
	end := sub.ExpiresAt
	if next := billing.NextQuotaReset(sub); next != nil {
		end = *next
	}
	return billing.CeilDays(end.Sub(now))
}

// evaluateUpgrade prices moving sub (read at now) to plan to. withGroups also lists the
// line groups the customer would lose (one more query; the order and the quote need it).
func (s *Server) evaluateUpgrade(tx *gorm.DB, user model.User, sub *model.Subscription, to model.Plan, settings upgradeSettings, global int, now time.Time, withGroups bool) (upgradeEval, error) {
	ev := upgradeEval{To: to, Relation: billing.RelationNew, Problem: errNoUpgradeBase}
	if sub == nil || sub.IsTest || now.Before(sub.StartsAt) || !now.Before(sub.ExpiresAt) {
		return ev, nil
	}
	if to.ID == sub.PlanID {
		ev.Relation, ev.Problem = billing.RelationCurrent, errUpgradeCurrent
		return ev, nil
	}
	ev.Relation, ev.Problem = billing.RelationLateral, errUpgradeUnknown
	err := tx.Where("id = ?", sub.PlanID).First(&ev.From).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ev, nil
	}
	if err != nil {
		return ev, err
	}
	orders, err := periodOrders(tx, sub)
	if err != nil {
		return ev, err
	}
	refreshing := billing.QuotaCycleActive(sub)
	snap := upgradeSnapshot{SubscriptionID: sub.ID, StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt, Refreshing: refreshing, MinChargeCents: settings.MinChargeCents}
	// The traffic held: per cycle for a refreshing allowance, otherwise the largest per-day
	// traffic among the plan's terms and what this period's orders sold for it (the
	// largest leaves the least to add).
	if refreshing {
		snap.FromTraffic = billing.Rate{Amount: max(sub.QuotaCycleBytes, ev.From.TrafficBytes), Days: sub.QuotaCycleDays}
		snap.ToTraffic = billing.Rate{Amount: to.TrafficBytes, Days: sub.QuotaCycleDays}
		snap.NewCycleBytes = to.TrafficBytes
	} else {
		snap.FromTraffic = billing.Rate{Amount: ev.From.TrafficBytes, Days: ev.From.DurationDays}
		for _, o := range orders {
			if rate, ok := soldTraffic(o); ok && o.PlanID == sub.PlanID && !snap.FromTraffic.AtLeast(rate) {
				snap.FromTraffic = rate
			}
		}
		snap.ToTraffic = billing.Rate{Amount: to.TrafficBytes, Days: to.DurationDays}
	}
	ev.Relation = billing.UpgradeRelation(
		billing.Holding{PriceCents: ev.From.PriceCents, Traffic: snap.FromTraffic, MaxDevices: sub.MaxDevices, AllowDedicated: sub.AllowDedicated},
		billing.Target{PriceCents: to.PriceCents, Traffic: snap.ToTraffic, MaxDevices: to.MaxDevices, AllowDedicated: to.AllowDedicated})
	ev.Snap = snap
	if ev.Relation != billing.RelationUpgrade {
		ev.Problem = relationError(ev.Relation, sub, to)
		return ev, nil
	}
	ev.Problem = nil
	if to.PriceCents <= 0 || !usablePlan(to) || !to.Active || to.DeletedAt != nil {
		ev.Problem = &apiError{409, "plan_not_purchasable", "此套餐暂不支持在线购买"}
		return ev, nil
	}
	// The value of the remaining days: the cycle held (yearly terms for a refreshing
	// allowance when the plan sells them, else monthly, which costs more per day).
	ev.Cycle, ev.CyclePercent = model.CycleMonthly, 100
	snap.ValueRate = billing.Rate{Amount: to.PriceCents, Days: to.DurationDays}
	if refreshing {
		ev.Cycle = model.CycleYearly
		if terms, e := termsFor(to, model.CycleYearly, global); e == nil {
			snap.ValueRate, ev.CyclePercent = billing.Rate{Amount: terms.PriceCents, Days: terms.DurationDays}, terms.DiscountPercent
		}
	}
	snap.ChargeDays, snap.CreditDays = billing.UpgradeDays(now, sub.ExpiresAt)
	value, ok := billing.ProrateUp(snap.ValueRate, snap.ChargeDays)
	if !ok {
		return ev, errors.New("upgrade value overflow")
	}
	windows := []billing.PaidWindow{}
	for _, o := range orders {
		if from, until, ok := orderWindow(o); ok {
			windows = append(windows, billing.PaidWindow{OrderID: o.ID, Cents: orderCents(o), From: from, To: until})
		}
	}
	snap.ValueCents = value
	snap.CreditCents, snap.Contributions = billing.UpgradeCredit(windows, sub.ExpiresAt, snap.CreditDays)
	ev.ListAmountCents, snap.MinChargeApplied = billing.UpgradeCharge(snap.ValueCents, snap.CreditCents, settings.MinChargeCents)
	days := snap.ChargeDays
	if refreshing {
		days = cycleDaysLeft(sub, now)
	}
	if snap.TrafficAddBytes, ok = billing.TrafficAdd(snap.ToTraffic, snap.FromTraffic, days); !ok {
		return ev, errors.New("upgrade traffic overflow")
	}
	ev.AmountCents = ev.ListAmountCents
	if s.cfg.Payment.TestPriceFor(user.Email) {
		ev.AmountCents, ev.TestPrice = s.cfg.Payment.TestPriceCents, true
	}
	if withGroups {
		if snap.LostGroups, err = lostGroups(tx, sub, to); err != nil {
			return ev, err
		}
	}
	ev.Snap = snap
	switch {
	case !settings.Enabled:
		ev.Problem = upgradeDisabledError(sub)
	case snap.CreditDays < 1:
		ev.Problem = errUpgradeTooShort
	case sub.TrafficLimitBytes > maxPlanBytes-snap.TrafficAddBytes:
		ev.Problem = errRenewalLimit
	}
	return ev, nil
}

// lostGroups names the enabled line groups sub can use that the plan cannot.
func lostGroups(tx *gorm.DB, sub *model.Subscription, to model.Plan) ([]string, error) {
	var groups []model.NodeGroup
	if err := tx.Where("enabled = ?", true).Order("sort asc, name asc").Find(&groups).Error; err != nil {
		return nil, err
	}
	lost := []string{}
	for _, g := range groups {
		now, e1 := billing.GroupVisibleTo(g, sub.PlanID, sub.AllowDedicated)
		then, e2 := billing.GroupVisibleTo(g, to.ID, to.AllowDedicated)
		if e1 == nil && e2 == nil && now && !then {
			lost = append(lost, g.Name)
		}
	}
	return lost, nil
}

// planChangeProblem refines the purchase refusal for a different plan: an upgrade
// points at the upgrade, the rest wait for the expiry (code unchanged, so an older
// website still shows the message).
func (s *Server) planChangeProblem(tx *gorm.DB, user model.User, sub *model.Subscription, plan model.Plan, now time.Time) error {
	global, err := globalDiscount(tx)
	if err != nil {
		return err
	}
	ev, err := s.evaluateUpgrade(tx, user, sub, plan, upgradeSettings{Enabled: true, MinChargeCents: model.DefaultUpgradeMinChargeCents}, global, now, false)
	if err != nil {
		return err
	}
	switch ev.Relation {
	case billing.RelationUpgrade:
		return &apiError{409, errPlanChange.Code, fmt.Sprintf("当前套餐有效期内可通过“升级”补差价更换到%s", plan.Name)}
	case billing.RelationDowngrade, billing.RelationLateral:
		return &apiError{409, errPlanChange.Code, relationError(ev.Relation, sub, plan).Message}
	}
	return errPlanChange
}

// upgradeOption is one plan's relation in the upgrade options.
type upgradeOption struct {
	PlanID          string `json:"plan_id"`
	PlanName        string `json:"plan_name"`
	Relation        string `json:"relation"`
	Purchasable     bool   `json:"purchasable"`
	Reason          string `json:"reason,omitempty"`
	Message         string `json:"message,omitempty"`
	AmountCents     int64  `json:"amount_cents,omitempty"`
	UpgradeDays     int    `json:"upgrade_days,omitempty"`
	TrafficAddBytes int64  `json:"traffic_add_bytes,omitempty"`
}

type upgradeCurrent struct {
	PlanID           string    `json:"plan_id"`
	PlanName         string    `json:"plan_name"`
	ExpiresAt        time.Time `json:"expires_at"`
	BillingCycle     string    `json:"billing_cycle"`
	RemainingSeconds int64     `json:"remaining_seconds"`
}

// readUpgradeBase reads the inputs shared by the upgrade endpoints.
func (s *Server) readUpgradeBase(tx *gorm.DB, userID string, now time.Time) (*model.Subscription, upgradeSettings, int, error) {
	settings, err := readUpgradeSettings(tx)
	if err != nil {
		return nil, settings, 0, err
	}
	global, err := globalDiscount(tx)
	if err != nil {
		return nil, settings, 0, err
	}
	sub, err := readSubscription(tx, userID, now)
	return sub, settings, global, err
}

func (s *Server) upgradeOptions(c *gin.Context) {
	if !s.requirePayment(c) {
		return
	}
	user, now := currentUser(c), s.now()
	out := gin.H{}
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		sub, settings, global, err := s.readUpgradeBase(tx, user.ID, now)
		if err != nil {
			return err
		}
		var plans []model.Plan
		if err = tx.Where("active = ? AND deleted_at IS NULL", true).Order("sort asc, created_at asc, id asc").Find(&plans).Error; err != nil {
			return err
		}
		options := make([]upgradeOption, 0, len(plans))
		base := sub != nil && !sub.IsTest && !now.Before(sub.StartsAt) && now.Before(sub.ExpiresAt)
		for _, plan := range plans {
			ev, err := s.evaluateUpgrade(tx, user, sub, plan, settings, global, now, false)
			if err != nil {
				return err
			}
			option := upgradeOption{PlanID: plan.ID, PlanName: plan.Name, Relation: ev.Relation, Purchasable: ev.Problem == nil}
			if ev.Problem != nil && base {
				option.Reason, option.Message = ev.Problem.Code, ev.Problem.Message
			}
			if ev.Relation == billing.RelationUpgrade && ev.Snap.ChargeDays > 0 {
				option.AmountCents, option.UpgradeDays, option.TrafficAddBytes = ev.AmountCents, ev.Snap.ChargeDays, ev.Snap.TrafficAddBytes
			}
			options = append(options, option)
		}
		out["enabled"], out["plans"], out["current"] = settings.Enabled, options, nil
		if base {
			cycle := model.CycleMonthly
			if billing.QuotaCycleActive(sub) {
				cycle = model.CycleYearly
			}
			out["current"] = upgradeCurrent{PlanID: sub.PlanID, PlanName: sub.PlanName, ExpiresAt: sub.ExpiresAt, BillingCycle: cycle, RemainingSeconds: int64(sub.ExpiresAt.Sub(now) / time.Second)}
		}
		return nil
	})
	if paymentFailure(c, err) {
		return
	}
	c.JSON(200, out)
}

type upgradeQuoteInput struct {
	PlanID string `json:"plan_id"`
}

// upgradeQuoteView is the customer's view of an upgrade price (handoff_plans.md 10).
type upgradeQuoteView struct {
	FromPlanID       string    `json:"from_plan_id"`
	FromPlanName     string    `json:"from_plan_name"`
	PlanID           string    `json:"plan_id"`
	PlanName         string    `json:"plan_name"`
	Relation         string    `json:"relation"`
	BillingCycle     string    `json:"billing_cycle"`
	ExpiresAt        time.Time `json:"expires_at"`
	UpgradeDays      int       `json:"upgrade_days"`
	CreditedDays     int       `json:"credited_days"`
	ValueCents       int64     `json:"value_cents"`
	CreditCents      int64     `json:"credit_cents"`
	MinChargeCents   int64     `json:"min_charge_cents"`
	MinChargeApplied bool      `json:"min_charge_applied"`
	ListAmountCents  int64     `json:"list_amount_cents"`
	AmountCents      int64     `json:"amount_cents"`
	TestPrice        bool      `json:"test_price"`
	TrafficAddBytes  int64     `json:"traffic_add_bytes"`
	NewCycleBytes    int64     `json:"new_cycle_bytes"`
	DeviceLimit      int       `json:"device_limit"`
	AllowDedicated   bool      `json:"allow_dedicated"`
	LostGroups       []string  `json:"lost_groups"`
	ReauthRequired   bool      `json:"reauth_required"`
	Purchasable      bool      `json:"purchasable"`
	Reason           string    `json:"reason,omitempty"`
	Message          string    `json:"message,omitempty"`
}

func (s *Server) upgradeQuote(c *gin.Context) {
	if !s.requirePayment(c) {
		return
	}
	var in upgradeQuoteInput
	if !decode(c, &in) {
		return
	}
	if !validPlanID(in.PlanID) {
		fail(c, 400, "invalid_plan", "请选择有效套餐")
		return
	}
	user, now := currentUser(c), s.now()
	var view upgradeQuoteView
	var pending int64
	var refused *upgradeEval
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		plan, err := loadPurchasablePlan(tx, in.PlanID, false)
		if err != nil {
			return err
		}
		sub, settings, global, err := s.readUpgradeBase(tx, user.ID, now)
		if err != nil {
			return err
		}
		ev, err := s.evaluateUpgrade(tx, user, sub, plan, settings, global, now, true)
		if err != nil {
			return err
		}
		if ev.Relation != billing.RelationUpgrade {
			refused = &ev
			return nil
		}
		view = quoteViewOf(ev, s.deviceCap(plan.MaxDevices), sub.ExpiresAt)
		return livePending(tx.Model(&model.Order{}), now).Where("user_id = ?", user.ID).Count(&pending).Error
	})
	if paymentFailure(c, err) {
		return
	}
	if refused != nil {
		// Not an upgrade: the refusal names the relation so the page can say why.
		c.AbortWithStatusJSON(refused.Problem.Status, gin.H{"error": gin.H{"code": refused.Problem.Code, "message": refused.Problem.Message, "relation": refused.Relation}})
		return
	}
	c.JSON(200, gin.H{"quote": view, "pending_orders": pending})
}

func quoteViewOf(ev upgradeEval, devices int, expires time.Time) upgradeQuoteView {
	lost := ev.Snap.LostGroups
	if lost == nil {
		lost = []string{}
	}
	v := upgradeQuoteView{FromPlanID: ev.From.ID, FromPlanName: ev.From.Name, PlanID: ev.To.ID, PlanName: ev.To.Name, Relation: ev.Relation, BillingCycle: ev.Cycle, ExpiresAt: expires,
		UpgradeDays: ev.Snap.ChargeDays, CreditedDays: ev.Snap.CreditDays, ValueCents: ev.Snap.ValueCents, CreditCents: ev.Snap.CreditCents, MinChargeCents: ev.Snap.MinChargeCents,
		MinChargeApplied: ev.Snap.MinChargeApplied, ListAmountCents: ev.ListAmountCents, AmountCents: ev.AmountCents, TestPrice: ev.TestPrice, TrafficAddBytes: ev.Snap.TrafficAddBytes,
		NewCycleBytes: ev.Snap.NewCycleBytes, DeviceLimit: devices, AllowDedicated: ev.To.AllowDedicated, LostGroups: lost, ReauthRequired: true, Purchasable: ev.Problem == nil}
	if ev.Problem != nil {
		v.Reason, v.Message = ev.Problem.Code, ev.Problem.Message
	}
	return v
}

type upgradeOrderInput struct {
	PlanID              string `json:"plan_id"`
	PayType             string `json:"pay_type"`
	ExpectedAmountCents *int64 `json:"expected_amount_cents"`
}

func (s *Server) createUpgradeOrder(c *gin.Context) {
	if !s.requirePayment(c) {
		return
	}
	var in upgradeOrderInput
	if !decode(c, &in) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if _, err := uuid.Parse(key); err != nil {
		fail(c, 400, "idempotency_required", "请提供 UUID 格式的 Idempotency-Key")
		return
	}
	if !validPlanID(in.PlanID) {
		fail(c, 400, "invalid_plan", "请选择有效套餐")
		return
	}
	if !payment.ValidType(in.PayType) {
		fail(c, 400, "invalid_pay_type", "请选择微信支付或支付宝支付")
		return
	}
	if in.ExpectedAmountCents == nil || *in.ExpectedAmountCents <= 0 {
		paymentFailure(c, errUpgradeAmountBad)
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
	var order model.Order
	replayed := false
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		var retryAt *time.Time
		var e error
		order, replayed, retryAt, e = s.placeUpgradeOrder(tx, user.ID, in, key, now)
		if retryAt != nil {
			retryAfter(c, *retryAt, now)
		}
		return e
	})
	if paymentFailure(c, err) {
		return
	}
	status := 201
	if replayed {
		status = 200
	}
	c.JSON(status, gin.H{"order": orderView{Order: order}, "pay_url": s.payURL(order), "replayed": replayed, "other_pending_orders": 0})
}

// placeUpgradeOrder writes one upgrade order in tx (or finds the one the key already
// made). retryAt is set when another unfinished order blocks it.
func (s *Server) placeUpgradeOrder(tx *gorm.DB, userID string, in upgradeOrderInput, key string, now time.Time) (order model.Order, replayed bool, retryAt *time.Time, err error) {
	// Lock order: User -> Plan (shared) -> reads -> Order insert.
	locked, err := lockUserRow(tx, userID)
	if err != nil {
		return order, false, nil, err
	}
	if locked == nil || locked.Status != "active" {
		return order, false, nil, &apiError{403, "account_disabled", "账号已停用"}
	}
	err = tx.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&order).Error
	if err == nil {
		if order.TradeNo == nil || order.Kind != model.OrderKindUpgrade || order.PlanID != in.PlanID || order.PayType != in.PayType {
			return order, false, nil, &apiError{409, "idempotency_conflict", "此请求标识已用于其他订单"}
		}
		return order, true, nil, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return order, false, nil, err
	}
	// The price depends on the subscription as it stands: no upgrade next to another
	// unfinished order (D9).
	var live []model.Order
	if err = livePending(tx.Select("id", "expires_at"), now).Where("user_id = ?", userID).Order("expires_at asc").Find(&live).Error; err != nil {
		return order, false, nil, err
	}
	if len(live) > 0 {
		return order, false, live[0].ExpiresAt, errPendingOrder
	}
	plan, err := loadPurchasablePlan(tx, in.PlanID, true)
	if err != nil {
		return order, false, nil, err
	}
	sub, settings, global, err := s.readUpgradeBase(tx, userID, now)
	if err != nil {
		return order, false, nil, err
	}
	ev, err := s.evaluateUpgrade(tx, *locked, sub, plan, settings, global, now, true)
	if err != nil {
		return order, false, nil, err
	}
	if ev.Problem != nil {
		return order, false, nil, ev.Problem
	}
	if in.ExpectedAmountCents == nil || *in.ExpectedAmountCents != ev.AmountCents {
		return order, false, nil, &apiError{409, "price_changed", "价格已更新，请确认后再支付"}
	}
	snapshot, err := json.Marshal(ev.Snap)
	if err != nil {
		return order, false, nil, err
	}
	tradeNo, err := s.newTradeNo(tx, now)
	if err != nil {
		return order, false, nil, err
	}
	source := priceList
	if ev.TestPrice {
		source = priceTest
	}
	expires := now.Add(s.cfg.Payment.Window())
	devices, dedicated := plan.MaxDevices, plan.AllowDedicated
	order = model.Order{ID: uuid.NewString(), UserID: userID, IdempotencyKey: key, PlanID: plan.ID, PlanName: plan.Name, PriceCents: ev.AmountCents, Status: model.OrderPending, Provider: "gateway",
		CreatedAt: now, TradeNo: &tradeNo, PayType: in.PayType, ListPriceCents: ev.ListAmountCents, AmountCents: ev.AmountCents, PriceSource: source, ExpiresAt: &expires, UpdatedAt: &now,
		BillingCycle: ev.Cycle, CycleDiscountPercent: ev.CyclePercent, OriginalCents: ev.Snap.ValueCents, EntMaxDevices: &devices, EntAllowDedicated: &dedicated,
		Kind: model.OrderKindUpgrade, FromPlanID: ev.From.ID, FromPlanName: ev.From.Name, CreditCents: ev.Snap.CreditCents, UpgradeDays: ev.Snap.ChargeDays,
		UpgradeTrafficBytes: ev.Snap.TrafficAddBytes, UpgradeSnapshot: string(snapshot)}
	return order, false, nil, tx.Omit("User").Create(&order).Error
}

// applyUpgrade fulfils a locked upgrade order in the caller's transaction. It changes
// nothing and reports false when the subscription is no longer the one the order was
// priced on (handoff_plans.md D10); the caller then puts the order under review. The
// traffic is recomputed from the sold rates for the time actually left, so a late
// payment can only grant less.
func applyUpgrade(tx *gorm.DB, order *model.Order, now time.Time) (*model.Subscription, int64, bool, error) {
	var snap upgradeSnapshot
	if json.Unmarshal([]byte(order.UpgradeSnapshot), &snap) != nil || order.EntMaxDevices == nil || order.EntAllowDedicated == nil {
		return nil, 0, false, nil
	}
	sub, err := readSubscription(tx, order.UserID, now)
	if err != nil {
		return nil, 0, false, err
	}
	if sub == nil || sub.ID != snap.SubscriptionID || !sub.StartsAt.Equal(snap.StartsAt) || !sub.ExpiresAt.Equal(snap.ExpiresAt) || sub.PlanID != order.FromPlanID ||
		sub.IsTest || !now.Before(sub.ExpiresAt) || billing.QuotaCycleActive(sub) != snap.Refreshing {
		return nil, 0, false, nil
	}
	var plan model.Plan
	err = tx.Select("id").Where("id = ?", order.PlanID).First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	days := min(snap.ChargeDays, billing.CeilDays(sub.ExpiresAt.Sub(now)))
	if snap.Refreshing {
		days = cycleDaysLeft(sub, now)
		sub.QuotaCycleBytes = snap.NewCycleBytes
	}
	add, ok := billing.TrafficAdd(snap.ToTraffic, snap.FromTraffic, days)
	if !ok || sub.TrafficLimitBytes > maxPlanBytes-add {
		return nil, 0, false, nil
	}
	sub.TrafficLimitBytes += add
	sub.PlanID, sub.PlanName, sub.MaxDevices, sub.AllowDedicated = order.PlanID, order.PlanName, *order.EntMaxDevices, *order.EntAllowDedicated
	sub.Version++
	saved, err := saveSubscription(tx, sub)
	return saved, add, err == nil, err
}
