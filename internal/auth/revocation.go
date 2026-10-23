package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const loginIDByteLength = 32

func newLoginID() (string, error) {
	buf := make([]byte, loginIDByteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func validLoginID(id string) bool {
	if id == "" {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return false
	}
	if len(decoded) < loginIDByteLength {
		return false
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != id {
		return false
	}
	return true
}

func hashLoginID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func isLoginRevoked(loginID string) (bool, error) {
	if !validLoginID(loginID) {
		return false, fmt.Errorf("invalid login ID")
	}
	if data.DB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	var record core.RevokedLogin
	err := data.DB.First(&record, "id = ?", hashLoginID(loginID)).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func revokeLoginIDs(ids []string) error {
	if data.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour)

	return data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&core.RevokedLogin{}).Error; err != nil {
			return err
		}
		seen := make(map[string]bool)
		for _, id := range ids {
			if !validLoginID(id) {
				continue
			}
			hash := hashLoginID(id)
			if seen[hash] {
				continue
			}
			seen[hash] = true
			record := core.RevokedLogin{ID: hash, ExpiresAt: expiresAt}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// revokeLoginHashes marks the given login hashes as revoked until expiresAt
// and sets revoked_at on their login_sessions rows. Stub: returns nil.
func revokeLoginHashes(db *gorm.DB, hashes []string, expiresAt time.Time) error {
	_ = db
	_ = hashes
	_ = expiresAt
	return nil
}

// RevokeUserLogins revokes every login of userID except exceptHash, which may
// be empty. Used by password changes, status changes and "sign out everywhere"
// (ticket 06). Stub: returns nil.
func RevokeUserLogins(db *gorm.DB, userID uint, exceptHash string) error {
	_ = db
	_ = userID
	_ = exceptHash
	return nil
}
