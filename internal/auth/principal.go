package auth

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
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
		return resolveJWTPrincipal(db, token[len("Bearer "):], principalBearer)
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
	return loadPrincipal(db, uint(userValue), loginID, time.Unix(int64(exp), 0), source)
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
	return loadPrincipal(db, userID, loginID, time.Unix(expires, 0), principalSession)
}

func loadPrincipal(db *gorm.DB, userID uint, loginID string, expiresAt time.Time, source principalSource) (*principal, error) {
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
