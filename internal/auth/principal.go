package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type principalSource string

const (
	principalBearer    principalSource = "bearer"
	principalJWTCookie principalSource = "jwt_cookie"
	principalSession   principalSource = "session"
)

type principal struct {
	User      *core.User
	LoginID   string
	ExpiresAt time.Time
	Source    principalSource
}

func resolvePrincipal(c echo.Context, db *gorm.DB, allowSession bool) (*principal, error) {
	if c == nil || c.Request() == nil {
		return nil, fmt.Errorf("missing request")
	}
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	db = db.WithContext(c.Request().Context())
	request := c.Request()

	if token := request.Header.Get("Authorization"); token != "" {
		if len(token) < len("Bearer ") || token[:len("Bearer ")] != "Bearer " {
			return nil, fmt.Errorf("invalid authorization header")
		}
		bearer := token[len("Bearer "):]
		// Access keys (jla_, jly_) call /api/agent/v1 only. They are never user
		// authentication credentials (ticket 22).
		if strings.HasPrefix(bearer, "jla_") || strings.HasPrefix(bearer, "jly_") {
			return nil, fmt.Errorf("access keys are not user authentication tokens")
		}
		return resolveJWTPrincipal(db, bearer, principalBearer)
	}

	if cookie, present := requestCookie(request, "jwt_token"); present {
		return resolveJWTPrincipal(db, cookie, principalJWTCookie)
	}
	if !allowSession {
		return nil, fmt.Errorf("JWT authorization missing")
	}
	return resolveSessionPrincipal(c, db)
}

func resolveJWTPrincipal(db *gorm.DB, tokenString string, source principalSource) (*principal, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("JWT authorization missing")
	}
	claims, err := ValidateJWT(tokenString)
	if err != nil {
		return nil, err
	}
	loginID, ok := claims["jti"].(string)
	if !ok || !validLoginID(loginID) {
		return nil, fmt.Errorf("invalid login ID")
	}
	userValue, ok := claims["user_id"].(float64)
	if !ok || userValue <= 0 || userValue > float64(^uint(0)) {
		return nil, fmt.Errorf("invalid user ID")
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 || exp > float64(int64(^uint64(0)>>1)) {
		return nil, fmt.Errorf("invalid expiration")
	}
	iat, ok := claims["iat"].(float64)
	if !ok || iat <= 0 || iat > float64(int64(^uint64(0)>>1)) {
		return nil, fmt.Errorf("invalid issued-at time")
	}
	return loadPrincipal(db, uint(userValue), loginID, time.Unix(int64(exp), 0), time.Unix(int64(iat), 0), source)
}

func resolveSessionPrincipal(c echo.Context, db *gorm.DB) (*principal, error) {
	session, err := GetSession(c)
	if err != nil {
		return nil, err
	}
	userID, ok := session.Values["user_id"].(uint)
	if !ok || userID == 0 {
		return nil, fmt.Errorf("user not authenticated")
	}
	expires, ok := session.Values["login_expires_at"].(int64)
	if !ok || expires <= time.Now().Unix() {
		return nil, fmt.Errorf("session expired")
	}
	loginID, _ := session.Values["login_id"].(string)
	if !validLoginID(loginID) {
		return nil, fmt.Errorf("invalid login ID")
	}
	expiresAt := time.Unix(expires, 0)
	return loadPrincipal(db, userID, loginID, expiresAt, expiresAt.Add(-7*24*time.Hour), principalSession)
}

func loadPrincipal(db *gorm.DB, userID uint, loginID string, expiresAt, issuedAt time.Time, source principalSource) (*principal, error) {
	if userID == 0 || !validLoginID(loginID) || !expiresAt.After(time.Now()) {
		return nil, fmt.Errorf("invalid login")
	}
	var revoked core.RevokedLogin
	err := db.First(&revoked, "id = ?", hashLoginID(loginID)).Error
	if err == nil {
		return nil, fmt.Errorf("login revoked")
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var user core.User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, fmt.Errorf("account is not active")
	}
	hash := hashLoginID(loginID)
	var login core.LoginSession
	loginErr := db.First(&login, "id = ?", hash).Error
	if loginErr != nil && !errors.Is(loginErr, gorm.ErrRecordNotFound) {
		return nil, loginErr
	}
	if loginErr == nil {
		if login.UserID != userID || login.RevokedAt != nil || !login.ExpiresAt.After(time.Now()) {
			return nil, fmt.Errorf("login revoked or expired")
		}
		// Bound writes from frequently used sessions while still recording recent activity.
		if login.LastSeenAt.Before(time.Now().Add(-5 * time.Minute)) {
			if err := db.Model(&core.LoginSession{}).Where("id = ? AND (last_seen_at IS NULL OR last_seen_at < ?)", hash, time.Now().Add(-5*time.Minute)).Update("last_seen_at", time.Now()).Error; err != nil {
				return nil, err
			}
		}
	} else if user.PasswordChangedAt != nil {
		// Compatibility for JWTs created by GenerateJWT before login_sessions was
		// introduced. New logins have a row and are revoked by its state instead.
		if issuedAt.Before(*user.PasswordChangedAt) {
			return nil, fmt.Errorf("login predates account security change")
		}
	}
	if loginErr != nil {
		// Older JWTs issued through GenerateJWT have no session row. Once their
		// account cutoff has been checked, lazily track them so future revoke-all
		// operations can preserve the current login by hash.
		if err := db.Transaction(func(tx *gorm.DB) error {
			var latest core.User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&latest, userID).Error; err != nil {
				return err
			}
			if latest.Status != "active" {
				return fmt.Errorf("account is not active")
			}
			if latest.PasswordChangedAt != nil && issuedAt.Before(*latest.PasswordChangedAt) {
				return fmt.Errorf("login predates account security change")
			}
			legacy := core.LoginSession{ID: hash, UserID: userID, Method: "legacy", ExpiresAt: expiresAt}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&legacy).Error; err != nil {
				return err
			}
			if err := tx.First(&login, "id = ?", hash).Error; err != nil {
				return err
			}
			if login.UserID != userID || login.RevokedAt != nil || !login.ExpiresAt.After(time.Now()) {
				return fmt.Errorf("login revoked or expired")
			}
			user = latest
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if !expiresAt.After(time.Now()) {
		return nil, fmt.Errorf("login expired")
	}
	return &principal{User: &user, LoginID: loginID, ExpiresAt: expiresAt, Source: source}, nil
}

func requestCookie(request *http.Request, name string) (string, bool) {
	for _, cookie := range request.Cookies() {
		if cookie.Name == name {
			return cookie.Value, true
		}
	}
	return "", false
}

func principalMatchesIntent(actual *principal, intent oauthLinkIntent) bool {
	return actual != nil && actual.User != nil && actual.User.ID == intent.UserID && actual.LoginID == intent.LoginID &&
		actual.Source == intent.Source && actual.ExpiresAt.Unix() == intent.ExpiresAt
}

// PrincipalInfo describes the authenticated request. LoginID is present only
// for cookie/JWT logins.
type PrincipalInfo struct {
	User    *core.User
	Source  string
	LoginID string
}

// GetCurrentPrincipal resolves the current request's authentication source.
func GetCurrentPrincipal(c echo.Context) (*PrincipalInfo, error) {
	if c == nil || c.Request() == nil {
		return nil, fmt.Errorf("missing request")
	}
	if cached, ok := c.Get("auth.principal").(*principal); ok && cached != nil {
		return principalInfo(cached), nil
	}
	p, err := resolvePrincipal(c, data.DB, true)
	if err != nil {
		return nil, err
	}
	return principalInfo(p), nil
}

func principalInfo(p *principal) *PrincipalInfo {
	return &PrincipalInfo{User: projectedUser(p), Source: string(p.Source), LoginID: p.LoginID}
}

// CurrentLoginHash returns the hashed session identifier needed for except-current
// revocation without exposing a raw login ID to server handlers.
func CurrentLoginHash(c echo.Context) (string, bool) {
	p, err := GetCurrentPrincipal(c)
	if err != nil || !validLoginID(p.LoginID) {
		return "", false
	}
	return hashLoginID(p.LoginID), true
}

// projectedUser returns a copy of the principal's user. service.go still calls
// this name. The role projection for personal tokens was removed (ticket 22).
func projectedUser(p *principal) *core.User {
	if p == nil || p.User == nil {
		return nil
	}
	user := *p.User
	return &user
}
