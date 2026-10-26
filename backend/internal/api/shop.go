package api

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"vpn/backend/internal/model"
)

// The shop: billing cycles, the yearly price rule and the public catalogue
// (handoff_shop.md 5, 7.5). Every price is an integer number of cents computed here;
// no page and no request ever supplies one.

const (
	// maxMonthlyCents keeps monthly x 12 x 100 far inside int64 (decision in 5.2).
	maxMonthlyCents = int64(10_000_000)
	minDiscount     = 50
	maxDiscount     = 100
)

var errCycleUnavailable = &apiError{409, "cycle_not_available", "该套餐暂不提供所选付费周期"}

// parseCycle maps the optional request field to a cycle; an absent field is monthly,
// so a frontend that predates yearly billing keeps working.
func parseCycle(raw string) (string, bool) {
	switch raw {
	case "", model.CycleMonthly:
		return model.CycleMonthly, true
	case model.CycleYearly:
		return model.CycleYearly, true
	}
	return "", false
}

// cycleTerms is what one billing cycle of a plan sells.
type cycleTerms struct {
	Cycle string
	// PriceCents is the cycle's list price; OriginalCents is twelve months at the
	// monthly price for yearly (equal to PriceCents for monthly).
	PriceCents, OriginalCents, SavingCents, MonthlyEquivCents int64
	DiscountPercent                                           int
	DurationDays                                              int
	// TrafficBytes is what the cycle is worth in traffic: the monthly allowance, or for
	// yearly the nominal twelve months (what an older binary grants at once). A yearly
	// purchase actually grants QuotaCycleBytes again every QuotaCycleDays (D2 revised).
	TrafficBytes    int64
	QuotaCycleDays  int
	QuotaCycleBytes int64
}

// grantBytes is the allowance one purchase adds now: the whole monthly allowance, or
// the first cycle of a yearly one.
func (t cycleTerms) grantBytes() int64 {
	if t.QuotaCycleDays > 0 {
		return t.QuotaCycleBytes
	}
	return t.TrafficBytes
}

// yearlyPrice is decision D3: original = monthly x 12, price = original x pct / 100
// rounded down to the cent, equivalent monthly = price / 12 rounded down.
func yearlyPrice(monthly int64, pct int) (original, price, monthlyEquiv int64) {
	original = monthly * 12
	price = original * int64(pct) / 100
	return original, price, price / 12
}

func effectiveDiscount(plan model.Plan, global int) int {
	if plan.YearlyDiscountPercent != nil {
		return *plan.YearlyDiscountPercent
	}
	return global
}

// termsFor returns the plan's terms for a cycle, or errCycleUnavailable.
func termsFor(plan model.Plan, cycle string, global int) (cycleTerms, error) {
	if cycle != model.CycleYearly {
		return cycleTerms{Cycle: model.CycleMonthly, PriceCents: plan.PriceCents, OriginalCents: plan.PriceCents, MonthlyEquivCents: plan.PriceCents,
			DiscountPercent: 100, DurationDays: plan.DurationDays, TrafficBytes: plan.TrafficBytes}, nil
	}
	pct := effectiveDiscount(plan, global)
	if !plan.YearlyEnabled || plan.YearlyDurationDays < 1 || plan.YearlyDurationDays > 365 || pct < minDiscount || pct > maxDiscount ||
		plan.PriceCents < 1 || plan.PriceCents > maxMonthlyCents || plan.TrafficBytes < 1 || plan.TrafficBytes > maxPlanBytes {
		return cycleTerms{}, errCycleUnavailable
	}
	// Every 30 days the monthly allowance again; the nominal year is bounded like any
	// allowance so an older binary can still fulfil the order. plans.yearly_traffic_bytes
	// is no longer read.
	nominal := min(plan.TrafficBytes*12, maxPlanBytes)
	original, price, equiv := yearlyPrice(plan.PriceCents, pct)
	if price < 1 {
		return cycleTerms{}, errCycleUnavailable
	}
	return cycleTerms{Cycle: model.CycleYearly, PriceCents: price, OriginalCents: original, SavingCents: original - price, MonthlyEquivCents: equiv,
		DiscountPercent: pct, DurationDays: plan.YearlyDurationDays, TrafficBytes: nominal, QuotaCycleDays: model.QuotaCycleDays, QuotaCycleBytes: plan.TrafficBytes}, nil
}

// forCycle is the plan as one purchase of that cycle: the planDecision/applyPlan code
// reads price, days and traffic from the plan, so they are swapped in on a copy (the
// traffic is what the purchase adds now, see grantBytes).
func (t cycleTerms) forCycle(plan model.Plan) model.Plan {
	plan.PriceCents, plan.DurationDays, plan.TrafficBytes = t.PriceCents, t.DurationDays, t.grantBytes()
	return plan
}

// snapshotCycle records on an order what it sold: the cycle, its discount and original
// price, and the plan's devices and line type at order time (handoff_shop.md 4.3, D6).
func snapshotCycle(order *model.Order, terms cycleTerms, plan model.Plan) {
	devices, dedicated := plan.MaxDevices, plan.AllowDedicated
	order.BillingCycle, order.CycleDiscountPercent, order.OriginalCents = terms.Cycle, terms.DiscountPercent, terms.OriginalCents
	order.EntMaxDevices, order.EntAllowDedicated = &devices, &dedicated
	order.QuotaCycleDays, order.QuotaCycleBytes = terms.QuotaCycleDays, terms.QuotaCycleBytes
}

// globalDiscount reads the shop-wide yearly percentage. A missing row (someone deleted
// it) reads as the default; doctor reports it.
func globalDiscount(db *gorm.DB) (int, error) {
	var row model.ShopSettings
	err := db.First(&row, "id = 1").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.DefaultYearlyDiscountPercent, nil
	}
	if err != nil {
		return 0, err
	}
	return row.YearlyDiscountPercent, nil
}

// deviceCap is what a user can really have online with this plan (D10).
func (s *Server) deviceCap(planDevices int) int { return min(planDevices, s.cfg.MaxOnlineDevices()) }

// publicTier is one cycle of a plan in the public catalogue.
type publicTier struct {
	PriceCents        int64 `json:"price_cents"`
	OriginalCents     int64 `json:"original_cents"`
	SavingCents       int64 `json:"saving_cents"`
	DiscountPercent   int   `json:"discount_percent"`
	DurationDays      int   `json:"duration_days"`
	TrafficBytes      int64 `json:"traffic_bytes"`
	MonthlyEquivCents int64 `json:"monthly_equivalent_cents"`
	// QuotaCycleDays > 0: QuotaCycleBytes is refreshed every QuotaCycleDays (yearly).
	QuotaCycleDays  int   `json:"quota_cycle_days"`
	QuotaCycleBytes int64 `json:"quota_cycle_bytes"`
}

func tierOf(t cycleTerms) *publicTier {
	return &publicTier{PriceCents: t.PriceCents, OriginalCents: t.OriginalCents, SavingCents: t.SavingCents, DiscountPercent: t.DiscountPercent,
		DurationDays: t.DurationDays, TrafficBytes: t.TrafficBytes, MonthlyEquivCents: t.MonthlyEquivCents, QuotaCycleDays: t.QuotaCycleDays, QuotaCycleBytes: t.QuotaCycleBytes}
}

// publicPlan is the whole public view of a plan: a fixed list of fields, never the model
// (so a new internal column cannot leak). The top level keeps the monthly terms for
// frontends that predate the yearly tier.
type publicPlan struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	PriceCents     int64       `json:"price_cents"`
	DurationDays   int         `json:"duration_days"`
	TrafficBytes   int64       `json:"traffic_bytes"`
	MaxDevices     int         `json:"max_devices"`
	AllowDedicated bool        `json:"allow_dedicated"`
	Featured       bool        `json:"featured"`
	Monthly        *publicTier `json:"monthly"`
	Yearly         *publicTier `json:"yearly"`
}

func (s *Server) plans(c *gin.Context) {
	rows := []model.Plan{}
	if s.database(c).Where("active = ? AND deleted_at IS NULL", true).Order("sort asc, created_at asc, id asc").Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "套餐暂时不可用")
		return
	}
	global, err := globalDiscount(s.database(c))
	if err != nil {
		fail(c, 503, "database_unavailable", "套餐暂时不可用")
		return
	}
	out := make([]publicPlan, 0, len(rows))
	maxSaving := 0
	for _, plan := range rows {
		monthly, _ := termsFor(plan, model.CycleMonthly, global)
		view := publicPlan{ID: plan.ID, Name: plan.Name, Description: plan.Description, PriceCents: plan.PriceCents, DurationDays: plan.DurationDays, TrafficBytes: plan.TrafficBytes,
			MaxDevices: s.deviceCap(plan.MaxDevices), AllowDedicated: plan.AllowDedicated, Featured: plan.Featured, Monthly: tierOf(monthly)}
		if yearly, e := termsFor(plan, model.CycleYearly, global); e == nil {
			view.Yearly = tierOf(yearly)
			maxSaving = max(maxSaving, 100-yearly.DiscountPercent)
		}
		out = append(out, view)
	}
	c.JSON(200, gin.H{"plans": out, "yearly_discount_percent": global, "max_yearly_saving_percent": maxSaving})
}

// detailedError is a business error that carries extra fields for the answer.
type detailedError struct {
	apiError
	Extra gin.H
}

func (e *detailedError) Error() string { return e.Message }
func (e *detailedError) Unwrap() error { return &e.apiError }

// planFailure answers a plan-console error, with the field or the blocking facts when
// the error carries them.
func planFailure(c *gin.Context, err error) bool {
	var detailed *detailedError
	if errors.As(err, &detailed) {
		body := gin.H{"code": detailed.Code, "message": detailed.Message}
		for k, v := range detailed.Extra {
			body[k] = v
		}
		c.AbortWithStatusJSON(detailed.Status, gin.H{"error": body})
		return true
	}
	return consoleFailure(c, err)
}

func fieldError(field, message string) error {
	return &detailedError{apiError{400, "invalid_plan", message}, gin.H{"field": field}}
}
