package api

import (
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/billing"
	"vpn/backend/internal/model"
)

func (s *Server) allowTest() bool { return s.cfg.TestPurchase && s.cfg.Env != "production" }
func (s *Server) plans(c *gin.Context) {
	rows := []model.Plan{}
	if s.database(c).Where("active = ?", true).Order("sort asc").Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "套餐暂时不可用")
		return
	}
	c.JSON(200, gin.H{"plans": rows})
}
func (s *Server) subscription(c *gin.Context) (*model.Subscription, error) {
	var sub model.Subscription
	err := s.database(c).Where("user_id = ?", currentUser(c).ID).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sub, err
}
func (s *Server) dashboard(c *gin.Context) {
	sub, err := s.subscription(c)
	if err != nil {
		fail(c, 503, "database_unavailable", "用户数据暂时不可用")
		return
	}
	var count int64
	if s.database(c).Model(&model.Order{}).Where("user_id = ?", currentUser(c).ID).Count(&count).Error != nil {
		fail(c, 503, "database_unavailable", "订单数据暂时不可用")
		return
	}
	c.JSON(200, gin.H{"user": currentUser(c), "subscription": sub, "entitlement_active": billing.Eligible(sub, time.Now().UTC(), s.allowTest()), "order_count": count, "metering_connected": false, "proxy_service_ready": false})
}
func (s *Server) orders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		fail(c, 400, "invalid_page", "页码超出范围")
		return
	}
	rows := []model.Order{}
	db := s.database(c).Model(&model.Order{}).Where("user_id = ?", currentUser(c).ID)
	var total int64
	if db.Count(&total).Error != nil || db.Order("created_at desc").Limit(20).Offset((page-1)*20).Find(&rows).Error != nil {
		fail(c, 503, "database_unavailable", "订单暂时不可用")
		return
	}
	c.JSON(200, gin.H{"orders": rows, "total": total, "page": page, "page_size": 20})
}
func (s *Server) testPurchase(c *gin.Context) {
	if !s.allowTest() {
		fail(c, 403, "test_purchase_disabled", "测试购买已关闭")
		return
	}
	var in struct {
		PlanID string `json:"plan_id"`
	}
	if !decode(c, &in) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if _, err := uuid.Parse(key); err != nil {
		fail(c, 400, "idempotency_required", "请提供 UUID 格式的 Idempotency-Key")
		return
	}
	if len(in.PlanID) > 32 || in.PlanID == "" {
		fail(c, 400, "invalid_plan", "请选择有效套餐")
		return
	}
	user := currentUser(c)
	var order model.Order
	replayed := false
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		// All entitlement writes for a user share this lock, including concurrent distinct keys.
		var locked model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", user.ID).Error; err != nil {
			return err
		}
		if locked.Status != "active" {
			return &apiError{403, "account_disabled", "账号已停用"}
		}
		err := tx.Where("user_id = ? AND idempotency_key = ?", user.ID, key).First(&order).Error
		if err == nil {
			if order.PlanID != in.PlanID {
				return &apiError{409, "idempotency_conflict", "此请求标识已用于其他套餐"}
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var plan model.Plan
		if err := tx.Where("id = ? AND active = ?", in.PlanID, true).First(&plan).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &apiError{404, "plan_not_found", "套餐不存在或已下架"}
			}
			return err
		}
		if plan.DurationDays <= 0 || plan.DurationDays > 365 || plan.TrafficBytes <= 0 || plan.TrafficBytes > 1<<50 || plan.PriceCents < 0 {
			return &apiError{409, "invalid_plan", "套餐配置不可用"}
		}
		now := time.Now().UTC()
		var sub model.Subscription
		err = tx.Where("user_id = ?", user.ID).First(&sub).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		active := err == nil && now.Before(sub.ExpiresAt)
		if active && (!sub.IsTest || sub.PlanID != plan.ID) {
			return &apiError{409, "plan_change_not_supported", "有效套餐期间仅支持续购相同的测试套餐"}
		}
		if !active {
			sub = model.Subscription{ID: sub.ID, CreatedAt: sub.CreatedAt, UserID: user.ID, PlanID: plan.ID, PlanName: plan.Name, StartsAt: now, ExpiresAt: now, TrafficLimitBytes: 0, MaxDevices: plan.MaxDevices, AllowDedicated: plan.AllowDedicated, IsTest: true}
			if sub.ID == "" {
				sub.ID = uuid.NewString()
			}
		}
		if sub.TrafficLimitBytes > math.MaxInt64/1000-plan.TrafficBytes || sub.TrafficLimitBytes+plan.TrafficBytes > 1<<50 || sub.ExpiresAt.After(now.AddDate(10, 0, 0)) {
			return &apiError{409, "renewal_limit", "续购超过允许的套餐上限"}
		}
		sub.TrafficLimitBytes += plan.TrafficBytes
		sub.ExpiresAt = sub.ExpiresAt.AddDate(0, 0, plan.DurationDays)
		if err := tx.Omit("User", "Plan").Save(&sub).Error; err != nil {
			return err
		}
		order = model.Order{ID: uuid.NewString(), UserID: user.ID, IdempotencyKey: key, PlanID: plan.ID, PlanName: plan.Name, PriceCents: plan.PriceCents, DurationDays: plan.DurationDays, TrafficBytes: plan.TrafficBytes, Status: "paid_test", Provider: "test", PaidAt: now}
		return tx.Omit("User").Create(&order).Error
	})
	if err != nil {
		var business *apiError
		if errors.As(err, &business) {
			fail(c, business.Status, business.Code, business.Message)
			return
		}
		fail(c, 503, "purchase_failed", "购买暂时失败；重试时请保留相同请求标识")
		return
	}
	status := 201
	if replayed {
		status = 200
	}
	c.JSON(status, gin.H{"order": order, "replayed": replayed, "message": "模拟购买成功，未发生真实扣款，不代表代理线路已开通"})
}
func (s *Server) bootstrap(c *gin.Context) {
	sub, err := s.subscription(c)
	if err != nil {
		fail(c, 503, "database_unavailable", "权益暂时不可用")
		return
	}
	if !billing.Eligible(sub, time.Now().UTC(), s.allowTest()) {
		fail(c, 403, "entitlement_inactive", "套餐不存在、已过期或流量不足")
		return
	}
	nodes := []model.Node{}
	if s.cfg.ClientNodeCatalog {
		nodes, err = s.catalogMetadata(s.database(c), sub)
		if err != nil {
			fail(c, 503, "database_unavailable", "节点信息暂时不可用")
			return
		}
		c.JSON(200, gin.H{"entitlement_active": true, "expires_at": sub.ExpiresAt, "is_test": sub.IsTest, "nodes": nodes, "proxy_service_ready": false, "metering_connected": true, "metering_source": "client_reported", "message": "节点目录已接入；客户端需单独登录授权，尚无节点侧强制配额"})
		return
	}
	query := s.database(c).Where("enabled = ?", true)
	if !sub.AllowDedicated {
		query = query.Where("line_type = ?", "direct")
	}
	if query.Order("name asc").Find(&nodes).Error != nil {
		fail(c, 503, "database_unavailable", "节点信息暂时不可用")
		return
	}
	// No YAML, server address, password or other proxy credentials are returned.
	c.JSON(200, gin.H{"entitlement_active": true, "expires_at": sub.ExpiresAt, "is_test": sub.IsTest, "nodes": nodes, "proxy_service_ready": false, "metering_connected": false, "message": "当前仅提供权益校验和节点元数据；尚未接入代理控制平面"})
}
