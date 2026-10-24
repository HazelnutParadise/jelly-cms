package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureCustomer finds the user with this email, or creates a dormant customer
// account. Existing accounts are returned unchanged.
func EnsureCustomer(tx *gorm.DB, email, phone string) (User, error) {
	if tx == nil {
		return User{}, errors.New("nil tx")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return User{}, errors.New("email required")
	}
	normalizedEmail := strings.ToLower(email)
	phone = strings.TrimSpace(phone)

	var user User
	err := tx.Transaction(func(tx *gorm.DB) error {
		// Serialize case-insensitive lookups for this address within PostgreSQL.
		// The hash collision case only adds harmless serialization between emails.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", normalizedEmail).Error; err != nil {
			return err
		}
		if err := tx.Where("LOWER(email) = ?", normalizedEmail).Order("id").First(&user).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		digest := sha256.Sum256([]byte(normalizedEmail))
		baseUsername := "customer-" + hex.EncodeToString(digest[:8])
		for attempt := 0; attempt < 32; attempt++ {
			username := baseUsername
			if attempt > 0 {
				username = fmt.Sprintf("%.40s-%d", baseUsername, attempt)
			}
			candidate := User{
				Username:  username,
				Email:     normalizedEmail,
				Password:  "",
				Role:      "customer",
				Provider:  "local",
				Status:    "invited",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				user = candidate
				return nil
			}
			// A conflict can be either an email race or a username collision.
			// Recheck email first; do not mask unrelated database errors.
			if err := tx.Where("LOWER(email) = ?", normalizedEmail).Order("id").First(&user).Error; err == nil {
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		return errors.New("could not allocate a unique customer username")
	})
	if err != nil {
		return User{}, err
	}
	return user, nil
}
