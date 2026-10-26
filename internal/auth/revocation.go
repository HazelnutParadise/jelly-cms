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

	hashes := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validLoginID(id) {
			continue
		}
		hash := hashLoginID(id)
		if !seen[hash] {
			seen[hash] = true
			hashes = append(hashes, hash)
		}
	}
	return data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&core.RevokedLogin{}).Error; err != nil {
			return err
		}
		return revokeLoginHashes(tx, hashes, expiresAt)
	})
}

// revokeLoginHashes marks the given login hashes as revoked until expiresAt
// and sets revoked_at on their login_sessions rows.
func revokeLoginHashes(db *gorm.DB, hashes []string, expiresAt time.Time) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	if !expiresAt.After(time.Now()) {
		return fmt.Errorf("revocation expiry must be in the future")
	}
	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		seen := make(map[string]bool, len(hashes))
		for _, hash := range hashes {
			if len(hash) != 64 {
				continue
			}
			if _, err := hex.DecodeString(hash); err != nil || seen[hash] {
				continue
			}
			seen[hash] = true
			record := core.RevokedLogin{ID: hash, ExpiresAt: expiresAt}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.Assignments(map[string]any{"expires_at": gorm.Expr("GREATEST(revoked_logins.expires_at, EXCLUDED.expires_at)")}),
			}).Create(&record).Error; err != nil {
				return err
			}
		}
		if len(seen) == 0 {
			return nil
		}
		return tx.Model(&core.LoginSession{}).Where("id IN ? AND revoked_at IS NULL", mapKeys(seen)).Update("revoked_at", now).Error
	})
}

// RevokeUserLogins revokes every login of userID except exceptHash, which may
// be empty. The password_changed_at cutoff also invalidates legacy JWTs issued
// by GenerateJWT that predate login_sessions rows.
func RevokeUserLogins(db *gorm.DB, userID uint, exceptHash string) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	if userID == 0 {
		return fmt.Errorf("invalid user ID")
	}
	now := time.Now().UTC()
	// JWT iat claims have one-second precision. Move the legacy cutoff to the
	// next whole second so every token created before this operation is older
	// than the cutoff, including tokens created earlier in the same second.
	legacyCutoff := now.Truncate(time.Second).Add(time.Second)
	return db.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "password_changed_at").First(&user, userID).Error; err != nil {
			return err
		}
		// PasswordChangedAt also versions MFA challenges. Advance it even when
		// several revocations happen during the same second.
		if user.PasswordChangedAt != nil {
			minimum := user.PasswordChangedAt.UTC().Add(time.Microsecond)
			if minimum.After(legacyCutoff) {
				legacyCutoff = minimum
			}
		}
		var sessions []core.LoginSession
		query := tx.Where("user_id = ? AND revoked_at IS NULL", userID)
		if exceptHash != "" {
			query = query.Where("id <> ?", exceptHash)
		}
		if err := query.Find(&sessions).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(sessions))
		seen := make(map[string]bool, len(sessions))
		for _, login := range sessions {
			if login.ExpiresAt.After(now) && !seen[login.ID] {
				ids = append(ids, login.ID)
				seen[login.ID] = true
			}
		}
		if len(ids) > 0 {
			if err := revokeLoginHashes(tx, ids, now.Add(7*24*time.Hour)); err != nil {
				return err
			}
		}
		if err := tx.Model(&core.User{}).Where("id = ?", userID).Update("password_changed_at", legacyCutoff).Error; err != nil {
			return err
		}
		return nil
	})
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
