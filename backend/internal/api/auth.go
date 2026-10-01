package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

type claims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}
type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}
type registerInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

func validEmail(input string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(input))
	m, err := mail.ParseAddress(e)
	return e, err == nil && m.Address == e && len(e) <= 254 && strings.Contains(e, ".")
}
func refreshHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}

func (s *Server) register(c *gin.Context) {
	var in registerInput
	if !decode(c, &in) {
		return
	}
	email, ok := validEmail(in.Email)
	name := strings.TrimSpace(in.Name)
	if !ok || utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 40 || len(in.Password) < 10 || len(in.Password) > 72 {
		fail(c, 400, "invalid_registration", "请输入有效邮箱、2–40 字姓名和 10–72 字节密码")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.cfg.BcryptCost)
	if err != nil {
		fail(c, 500, "password_error", "无法处理密码")
		return
	}
	user := model.User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: string(hash), Role: "user", Status: "active"}
	var sess model.Session
	var raw string
	err = s.database(c).Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(&user).Error; e != nil {
			return e
		}
		var e error
		sess, raw, e = s.createSession(tx, user.ID, in.Remember)
		return e
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		fail(c, 409, "email_exists", "此邮箱已注册，请直接登录")
		return
	}
	if err != nil {
		fail(c, 500, "registration_failed", "注册暂时不可用")
		return
	}
	if err = s.writeSession(c, sess, raw); err != nil {
		fail(c, 500, "session_failed", "创建登录会话失败，请重新登录")
		return
	}
	c.JSON(201, gin.H{"user": user})
}
func (s *Server) login(c *gin.Context) {
	var in loginInput
	if !decode(c, &in) {
		return
	}
	email, ok := validEmail(in.Email)
	if !ok || len(in.Password) > 72 {
		fail(c, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	var user model.User
	err := s.database(c).Where("email = ?", email).First(&user).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 503, "auth_unavailable", "登录服务暂时不可用")
		return
	}
	hash := s.dummyHash
	if err == nil {
		hash = []byte(user.PasswordHash)
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) == nil
	if err != nil || !match || user.Status != "active" {
		fail(c, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	sess, raw, err := s.createSession(s.database(c), user.ID, in.Remember)
	if err != nil {
		fail(c, 500, "session_failed", "无法创建登录会话")
		return
	}
	if s.writeSession(c, sess, raw) != nil {
		fail(c, 500, "session_failed", "无法创建登录会话")
		return
	}
	c.JSON(200, gin.H{"user": user})
}
func (s *Server) createSession(db *gorm.DB, userID string, remember bool) (model.Session, string, error) {
	raw, err := randomToken()
	if err != nil {
		return model.Session{}, "", err
	}
	ttl := s.cfg.SessionTTL
	if remember {
		ttl = s.cfg.RememberTTL
	}
	sess := model.Session{ID: uuid.NewString(), UserID: userID, RefreshHash: refreshHash(raw), Remember: remember, ExpiresAt: time.Now().UTC().Add(ttl)}
	err = db.Create(&sess).Error
	return sess, raw, err
}
func (s *Server) writeSession(c *gin.Context, sess model.Session, raw string) error {
	now := time.Now().UTC()
	end := now.Add(s.cfg.AccessTTL)
	if end.After(sess.ExpiresAt) {
		end = sess.ExpiresAt
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{SessionID: sess.ID, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.cfg.Issuer, Subject: sess.UserID, Audience: jwt.ClaimStrings{s.cfg.Audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(end), ID: uuid.NewString()}})
	access, err := token.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return err
	}
	s.cookie(c, "vpn_access", access, end, sess.Remember)
	s.cookie(c, "vpn_refresh", raw, sess.ExpiresAt, sess.Remember)
	return nil
}
func (s *Server) cookie(c *gin.Context, name, value string, expires time.Time, persist bool) {
	cookie := &http.Cookie{Name: name, Value: value, Path: "/api/v1", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
	if persist {
		cookie.Expires = expires
		cookie.MaxAge = int(time.Until(expires).Seconds())
		if cookie.MaxAge < 1 {
			cookie.MaxAge = 1
		}
	}
	http.SetCookie(c.Writer, cookie)
}
func (s *Server) clearCookies(c *gin.Context) {
	for _, name := range []string{"vpn_access", "vpn_refresh"} {
		http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: "", Path: "/api/v1", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	}
}
func (s *Server) refresh(c *gin.Context) {
	raw, e := c.Cookie("vpn_refresh")
	if e != nil || len(raw) > 128 {
		s.clearCookies(c)
		fail(c, 401, "session_expired", "登录已过期，请重新登录")
		return
	}
	next, e := randomToken()
	if e != nil {
		fail(c, 500, "session_failed", "无法更新会话")
		return
	}
	var sess model.Session
	err := s.database(c).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("refresh_hash = ? AND revoked_at IS NULL AND expires_at > ?", refreshHash(raw), time.Now().UTC()).First(&sess).Error; err != nil {
			return err
		}
		var u model.User
		if err := tx.Where("id = ? AND status = ?", sess.UserID, "active").First(&u).Error; err != nil {
			return err
		}
		return tx.Model(&sess).Update("refresh_hash", refreshHash(next)).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.clearCookies(c)
		fail(c, 401, "session_expired", "登录已过期，请重新登录")
		return
	}
	if err != nil {
		fail(c, 503, "auth_unavailable", "会话服务暂时不可用")
		return
	}
	if s.writeSession(c, sess, next) != nil {
		fail(c, 500, "session_failed", "无法更新会话")
		return
	}
	c.JSON(200, gin.H{"refreshed": true})
}
func (s *Server) logout(c *gin.Context) {
	raw, err := c.Cookie("vpn_refresh")
	if err == nil && len(raw) <= 128 {
		if e := s.database(c).Model(&model.Session{}).Where("refresh_hash = ? AND revoked_at IS NULL", refreshHash(raw)).Update("revoked_at", time.Now().UTC()).Error; e != nil {
			fail(c, 503, "logout_failed", "退出暂时失败，请重试")
			return
		}
	}
	s.clearCookies(c)
	c.JSON(200, gin.H{"logged_out": true})
}
func (s *Server) parseAccess(raw string) (*claims, error) {
	out := &claims{}
	_, err := jwt.ParseWithClaims(raw, out, func(t *jwt.Token) (any, error) { return []byte(s.cfg.JWTSecret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(s.cfg.Issuer), jwt.WithAudience(s.cfg.Audience), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if _, e := uuid.Parse(out.Subject); e != nil {
		return nil, e
	}
	if _, e := uuid.Parse(out.SessionID); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, _ := c.Cookie("vpn_access")
		if header := c.GetHeader("Authorization"); strings.HasPrefix(header, "Bearer ") {
			raw = strings.TrimPrefix(header, "Bearer ")
		}
		cl, err := s.parseAccess(raw)
		if err != nil {
			fail(c, 401, "unauthorized", "请先登录")
			return
		}
		var user model.User
		err = s.database(c).Table(s.cfg.Schema+".users AS u").Select("u.*").
			Joins("JOIN "+s.cfg.Schema+".sessions AS s ON s.user_id = u.id").
			Where("s.id = ? AND s.user_id = ? AND s.revoked_at IS NULL AND s.expires_at > ? AND u.status = ?", cl.SessionID, cl.Subject, time.Now().UTC(), "active").Take(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 401, "unauthorized", "登录会话已失效")
			return
		}
		if err != nil {
			fail(c, 503, "auth_unavailable", "鉴权服务暂时不可用")
			return
		}
		c.Set("user", user)
		c.Next()
	}
}
func currentUser(c *gin.Context) model.User { return c.MustGet("user").(model.User) }
func (s *Server) me(c *gin.Context)         { c.JSON(200, gin.H{"user": currentUser(c)}) }
