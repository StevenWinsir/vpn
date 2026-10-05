package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"vpn/backend/internal/config"
)

type Server struct {
	db        *gorm.DB
	cfg       config.Config
	dummyHash []byte
}
type apiError struct {
	Status        int
	Code, Message string
}

func (e *apiError) Error() string { return e.Message }
func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
func (s *Server) database(c *gin.Context) *gorm.DB { return s.db.WithContext(c.Request.Context()) }

func New(db *gorm.DB, cfg config.Config) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	hash, _ := bcrypt.GenerateFromPassword([]byte(uuid.NewString()), cfg.BcryptCost)
	s := &Server{db: db, cfg: cfg, dummyHash: hash}
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Header("X-Request-ID", uuid.NewString())
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cache-Control", "no-store")
		defer func() {
			if recover() != nil {
				fail(c, 500, "internal_error", "服务暂时不可用，请稍后重试")
			}
		}()
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.RequestTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		bodyLimit := int64(8192)
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/nodes") {
			bodyLimit = 128 << 10
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, bodyLimit)
		c.Next()
	})
	r.Use(s.corsAndCSRF())
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/readyz", func(c *gin.Context) {
		sqlDB, err := s.db.DB()
		if err != nil {
			fail(c, 503, "database_unavailable", "数据库不可用")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if sqlDB.PingContext(ctx) != nil {
			fail(c, 503, "database_unavailable", "数据库不可用")
			return
		}
		c.JSON(200, gin.H{"status": "ok", "database": "connected"})
	})
	v := r.Group("/api/v1")
	v.Use(rateLimit(cfg.APIRate))
	v.GET("/meta", func(c *gin.Context) {
		c.JSON(200, gin.H{"test_purchase_enabled": cfg.TestPurchase && cfg.Env != "production", "currency": "CNY", "traffic_unit": "GiB", "proxy_service_ready": false})
	})
	v.GET("/plans", s.plans)
	auth := v.Group("/auth")
	auth.Use(rateLimit(cfg.AuthRate))
	auth.POST("/register", s.register)
	auth.POST("/login", s.login)
	auth.POST("/refresh", s.refresh)
	auth.POST("/logout", s.logout)
	secured := v.Group("")
	secured.Use(s.requireAuth())
	secured.GET("/auth/me", s.me)
	secured.GET("/me/dashboard", s.dashboard)
	secured.GET("/orders", s.orders)
	secured.POST("/orders/test-purchase", s.testPurchase)
	secured.GET("/client/bootstrap", s.bootstrap)
	admin := secured.Group("/admin")
	admin.Use(s.requireAdmin())
	admin.GET("/nodes", s.adminNodeList)
	admin.GET("/nodes/:id", s.adminNodeDetail)
	admin.POST("/nodes", s.adminNodeSave)
	admin.POST("/nodes/:id", s.adminNodeSave)
	nativeLogin := v.Group("/client")
	nativeLogin.Use(rateLimit(cfg.AuthRate))
	nativeLogin.POST("/login", s.nativeLogin)
	native := v.Group("/client")
	native.Use(s.requireNativeSession())
	native.GET("/session", s.nativeStatus)
	native.GET("/config", s.nativeConfig)
	native.POST("/traffic", s.nativeTraffic)
	native.POST("/logout", s.nativeLogout)
	r.NoRoute(func(c *gin.Context) { fail(c, 404, "not_found", "接口不存在") })
	return r
}

func (s *Server) corsAndCSRF() gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, o := range s.cfg.Origins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		c.Header("Vary", "Origin")
		if origin != "" {
			if !allowed[origin] {
				fail(c, 403, "origin_forbidden", "请求来源不被允许")
				return
			}
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key")
		}
		if c.Request.Method == http.MethodOptions {
			if origin == "" {
				fail(c, 403, "origin_required", "缺少来源信息")
				return
			}
			c.Status(204)
			c.Abort()
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			if origin == "" && (hasCookie(c, "vpn_access") || hasCookie(c, "vpn_refresh")) {
				fail(c, 403, "origin_required", "Cookie 请求必须包含可信来源")
				return
			}
			if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
				fail(c, 415, "json_required", "请求必须使用 application/json")
				return
			}
		}
		c.Next()
	}
}
func hasCookie(c *gin.Context, name string) bool { v, e := c.Cookie(name); return e == nil && v != "" }
func decode(c *gin.Context, dest any) bool {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(dest) != nil {
		fail(c, 400, "invalid_json", "请求格式或字段不正确")
		return false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		fail(c, 400, "invalid_json", "请求仅允许一个 JSON 对象")
		return false
	}
	return true
}

type rateEntry struct {
	count int
	until time.Time
}

func rateLimit(max int) gin.HandlerFunc {
	var mu sync.Mutex
	entries := map[string]rateEntry{}
	lastCleanup := time.Now()
	return func(c *gin.Context) {
		now := time.Now()
		key := c.ClientIP()
		mu.Lock()
		if now.Sub(lastCleanup) > time.Minute {
			for k, v := range entries {
				if !now.Before(v.until) {
					delete(entries, k)
				}
			}
			lastCleanup = now
		}
		e, exists := entries[key]
		if !exists && len(entries) >= 10000 {
			mu.Unlock()
			c.Header("Retry-After", "60")
			fail(c, 429, "rate_limited", "请求过于频繁，请稍后重试")
			return
		}
		if !now.Before(e.until) {
			e = rateEntry{until: now.Add(time.Minute)}
		}
		e.count++
		entries[key] = e
		mu.Unlock()
		if e.count > max {
			c.Header("Retry-After", "60")
			fail(c, 429, "rate_limited", "请求过于频繁，请稍后重试")
			return
		}
		c.Next()
	}
}
