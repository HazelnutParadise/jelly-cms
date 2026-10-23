package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/gorilla/sessions"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const oauthStateLifetimeSeconds = 600

func configureAuthCookieStore(store *sessions.CookieStore, maxAge int) {
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   os.Getenv("APP_ENV") == "production",
		SameSite: http.SameSiteLaxMode,
	}
	store.MaxAge(maxAge)
}

func consumeOAuthState(ctx context.Context, provider, state string) (bool, error) {
	if data.DB == nil {
		return false, errors.New("OAuth state database unavailable")
	}

	now := time.Now().UTC()
	digest := sha256.Sum256([]byte("oauth-state\x00" + provider + "\x00" + state))
	row := core.RevokedLogin{
		ID: hex.EncodeToString(digest[:]),
		// Keep the marker through securecookie's inclusive final whole-second window.
		ExpiresAt: now.Add((oauthStateLifetimeSeconds + 1) * time.Second),
	}
	consumed := false
	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&core.RevokedLogin{}).Error; err != nil {
			return err
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		consumed = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return consumed, nil
}
