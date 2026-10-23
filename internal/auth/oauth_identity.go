package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/markbates/goth"
	"gorm.io/gorm"
)

const (
	oauthIdentityProviderLimit = 255
	oauthUsernameLimit         = 50
	oauthUsernameAttempts      = 16
)

type oauthIdentityHTTPError struct {
	status int
}

func (e oauthIdentityHTTPError) Error() string { return "invalid OAuth identity" }

func oauthIdentityHTTPStatus(err error) int {
	var identityErr oauthIdentityHTTPError
	if errors.As(err, &identityErr) {
		return identityErr.status
	}
	return 503
}

func invalidOAuthIdentity() error {
	return oauthIdentityHTTPError{status: 400}
}

func conflictingOAuthIdentity() error {
	return oauthIdentityHTTPError{status: 409}
}

func resolveOAuthUser(ctx context.Context, profile goth.User) (*core.User, error) {
	if ctx == nil || (profile.Provider != "google" && profile.Provider != "github") ||
		strings.TrimSpace(profile.UserID) == "" || utf8.RuneCountInString(profile.UserID) > oauthIdentityProviderLimit {
		return nil, invalidOAuthIdentity()
	}
	if data.DB == nil {
		return nil, fmt.Errorf("database unavailable")
	}

	db := oauthIdentityDB(ctx)
	identity, err := findOAuthIdentity(db, profile.Provider, profile.UserID)
	if err == nil {
		return loadOAuthIdentityUser(db, identity.UserID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if strings.TrimSpace(profile.Email) == "" {
		return nil, invalidOAuthIdentity()
	}

	// Existing accounts can only gain OAuth access through the explicit linking flow.
	// Recheck the stable identity if the email is now present: another request for
	// this same subject may have committed both rows since the first lookup.
	exists, err := oauthUserExists(db, "email", profile.Email)
	if err != nil {
		return nil, err
	}
	if exists {
		identity, lookupErr := findOAuthIdentity(db, profile.Provider, profile.UserID)
		if lookupErr == nil {
			return loadOAuthIdentityUser(db, identity.UserID)
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, lookupErr
		}
		return nil, conflictingOAuthIdentity()
	}

	baseUsername := oauthUsernameBase(profile.NickName, profile.Name)
	for attempt := 0; attempt < oauthUsernameAttempts; attempt++ {
		username, err := oauthUsernameCandidate(baseUsername, attempt)
		if err != nil {
			return nil, err
		}

		created, err := createOAuthUser(db, profile, username)
		if err == nil {
			return created, nil
		}

		if !isUniqueViolation(err) {
			return nil, err
		}

		// A failed transaction cannot be queried. Resolve races only after the
		// transaction has rolled back, and only the exact identity winner may log in.
		identity, lookupErr := findOAuthIdentity(db, profile.Provider, profile.UserID)
		if lookupErr == nil {
			return loadOAuthIdentityUser(db, identity.UserID)
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, lookupErr
		}

		exists, err = oauthUserExists(db, "email", profile.Email)
		if err != nil {
			return nil, err
		}
		if exists {
			identity, lookupErr = findOAuthIdentity(db, profile.Provider, profile.UserID)
			if lookupErr == nil {
				return loadOAuthIdentityUser(db, identity.UserID)
			}
			if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
				return nil, lookupErr
			}
			return nil, conflictingOAuthIdentity()
		}

		exists, err = oauthUserExists(db, "username", username)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		return nil, fmt.Errorf("OAuth identity uniqueness conflict")
	}

	return nil, fmt.Errorf("could not allocate OAuth username")
}

func findOAuthIdentity(db *gorm.DB, provider, subject string) (*core.OAuthIdentity, error) {
	var identity core.OAuthIdentity
	if err := db.Where("provider = ? AND subject = ?", provider, subject).First(&identity).Error; err != nil {
		return nil, err
	}
	return &identity, nil
}

func oauthUserExists(db *gorm.DB, column, value string) (bool, error) {
	var count int64
	if err := db.Model(&core.User{}).Where(column+" = ?", value).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func loadOAuthIdentityUser(db *gorm.DB, userID uint) (*core.User, error) {
	var user core.User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func createOAuthUser(db *gorm.DB, profile goth.User, username string) (*core.User, error) {
	var user core.User
	err := db.Transaction(func(tx *gorm.DB) error {
		user = core.User{
			Username:  username,
			Email:     profile.Email,
			AvatarURL: profile.AvatarURL,
			Provider:  profile.Provider,
			Role:      "author",
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}

		identity := core.OAuthIdentity{
			UserID:   user.ID,
			Provider: profile.Provider,
			Subject:  profile.UserID,
		}
		if err := tx.Create(&identity).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func oauthUsernameBase(nickname, name string) string {
	username := nickname
	if strings.TrimSpace(username) == "" {
		username = name
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "user"
	}
	return truncateRunes(username, oauthUsernameLimit)
}

func oauthUsernameCandidate(base string, attempt int) (string, error) {
	if attempt == 0 {
		return base, nil
	}

	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate OAuth username")
	}
	suffix := "-" + hex.EncodeToString(random)
	return truncateRunes(base, oauthUsernameLimit-utf8.RuneCountInString(suffix)) + suffix, nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func isUniqueViolation(err error) bool {
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "23505"
}
