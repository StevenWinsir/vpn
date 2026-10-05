package operations

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/model"
)

var ErrAdmin = errors.New("administrator operation failed; verify an active account, a unique email and migrated schema")

func NormalizeEmail(input string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(input))
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email && len(email) <= 254 && strings.Contains(email, ".")
}

// CreateAdmin is a privileged console operation, never a public registration route.
// It refuses to overwrite an existing account, password, role or subscription.
func CreateAdmin(ctx context.Context, db *gorm.DB, email, password string, cost int) error {
	email, valid := NormalizeEmail(email)
	if !valid || len(password) < 16 || len(password) > 72 || cost < 10 || cost > 15 {
		return ErrAdmin
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return ErrAdmin
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user := model.User{ID: uuid.NewString(), Email: email, Name: "Administrator", PasswordHash: string(hash), Role: "admin", Status: "active"}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: user.ID, TargetID: user.ID, Action: "admin.created.cli", Version: 1}).Error
	})
	if err != nil {
		return ErrAdmin
	}
	return nil
}

func GrantAdmin(ctx context.Context, db *gorm.DB, email string) error {
	email, valid := NormalizeEmail(email)
	if !valid {
		return ErrAdmin
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("email = ? AND status = ?", email, "active").First(&user).Error; err != nil {
			return err
		}
		if user.Role == "admin" {
			return nil
		}
		if err := tx.Model(&user).Update("role", "admin").Error; err != nil {
			return err
		}
		return tx.Create(&model.AdminAudit{ID: uuid.NewString(), ActorID: user.ID, TargetID: user.ID, Action: "admin.granted.cli", Version: 1}).Error
	})
	if err != nil {
		return ErrAdmin
	}
	return nil
}
