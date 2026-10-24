package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type accountTokenResponse struct {
	ID               uint             `json:"id"`
	Name             string           `json:"name"`
	Note             string           `json:"note"`
	Last4            string           `json:"last4"`
	Scopes           core.StringArray `json:"scopes"`
	ExpiresAt        *time.Time       `json:"expires_at"`
	LastUsedAt       *time.Time       `json:"last_used_at"`
	LastUsedIPMasked string           `json:"last_used_ip_masked"`
	Status           string           `json:"status"`
}

func listAccountTokens(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var tokens []core.APIToken
	if err := data.DB.WithContext(c.Request().Context()).Where("user_id = ?", user.ID).Order("created_at DESC, id DESC").Find(&tokens).Error; err != nil {
		return accountServerError(c, "Unable to load API tokens")
	}
	now := time.Now()
	result := make([]accountTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		status := "active"
		if token.RevokedAt != nil {
			status = "revoked"
		} else if token.ExpiresAt != nil && !token.ExpiresAt.After(now) {
			status = "expired"
		}
		result = append(result, accountTokenResponse{
			ID: token.ID, Name: token.Name, Note: token.Note, Last4: token.Last4,
			Scopes: token.Scopes, ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt,
			LastUsedIPMasked: token.LastUsedIPMasked, Status: status,
		})
	}
	return c.JSON(http.StatusOK, result)
}

func createAccountToken(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		Name          string   `json:"name"`
		Note          string   `json:"note"`
		ExpiresInDays *int     `json:"expires_in_days"`
		Scopes        []string `json:"scopes"`
	}
	if err := c.Bind(&req); err != nil {
		return accountBadRequest(c, "Invalid request")
	}
	req.Name = strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(req.Name) < 1 || utf8.RuneCountInString(req.Name) > 80 {
		return accountBadRequest(c, "name must be 1 to 80 characters")
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays != 7 && *req.ExpiresInDays != 30 && *req.ExpiresInDays != 90 && *req.ExpiresInDays != 365 {
		return accountBadRequest(c, "expires_in_days must be 7, 30, 90, 365 or null")
	}
	allowed := make(map[string]struct{})
	for _, scope := range auth.AllowedAPITokenScopes(user.Role) {
		allowed[scope] = struct{}{}
	}
	if len(req.Scopes) == 0 {
		return accountBadRequest(c, "At least one scope is required")
	}
	seen := make(map[string]struct{}, len(req.Scopes))
	for _, scope := range req.Scopes {
		if _, ok := allowed[scope]; !ok {
			return accountBadRequest(c, "One or more scopes exceed the owner's permissions")
		}
		if _, duplicate := seen[scope]; duplicate {
			return accountBadRequest(c, "Duplicate scopes are not allowed")
		}
		seen[scope] = struct{}{}
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return accountServerError(c, "Unable to create API token")
	}
	raw := "jly_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	var expiresAt *time.Time
	if req.ExpiresInDays != nil {
		expires := time.Now().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &expires
	}
	token := core.APIToken{
		UserID: user.ID, Name: req.Name, Note: req.Note, TokenHash: hash,
		Last4: raw[len(raw)-4:], Scopes: core.StringArray(req.Scopes), ExpiresAt: expiresAt,
	}
	if err := data.DB.WithContext(c.Request().Context()).Create(&token).Error; err != nil {
		return accountServerError(c, "Unable to save API token")
	}
	return c.JSON(http.StatusCreated, map[string]any{
		"id": token.ID, "name": token.Name, "note": token.Note, "last4": token.Last4,
		"scopes": token.Scopes, "expires_at": token.ExpiresAt, "token": raw,
	})
}

func patchAccountToken(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	id, ok := accountAPIID(c, "id")
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Token not found"})
	}
	var req struct {
		Name *string `json:"name"`
		Note *string `json:"note"`
	}
	if err := c.Bind(&req); err != nil {
		return accountBadRequest(c, "Invalid request")
	}
	updates := make(map[string]any)
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
			return accountBadRequest(c, "name must be 1 to 80 characters")
		}
		updates["name"] = name
	}
	if req.Note != nil {
		updates["note"] = *req.Note
	}
	if len(updates) == 0 {
		return accountBadRequest(c, "No editable fields were provided")
	}
	result := data.DB.WithContext(c.Request().Context()).Model(&core.APIToken{}).Where("id = ? AND user_id = ?", id, user.ID).Updates(updates)
	if result.Error != nil {
		return accountServerError(c, "Unable to update API token")
	}
	if result.RowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Token not found"})
	}
	var token core.APIToken
	if err := data.DB.WithContext(c.Request().Context()).Where("id = ? AND user_id = ?", id, user.ID).First(&token).Error; err != nil {
		return accountServerError(c, "Unable to load API token")
	}
	return c.JSON(http.StatusOK, accountTokenResponse{
		ID: token.ID, Name: token.Name, Note: token.Note, Last4: token.Last4,
		Scopes: token.Scopes, ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt,
		LastUsedIPMasked: token.LastUsedIPMasked, Status: accountTokenStatus(token, time.Now()),
	})
}

func changeAccountTokenRevocation(c echo.Context, revoke bool) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	id, ok := accountAPIID(c, "id")
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Token not found"})
	}
	var token core.APIToken
	if err := data.DB.WithContext(c.Request().Context()).Where("id = ? AND user_id = ?", id, user.ID).First(&token).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Token not found"})
		}
		return accountServerError(c, "Unable to load API token")
	}
	if revoke {
		if token.RevokedAt != nil {
			return c.JSON(http.StatusConflict, map[string]string{"error": "Token is already revoked"})
		}
		now := time.Now()
		if err := data.DB.WithContext(c.Request().Context()).Model(&core.APIToken{}).Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, user.ID).Update("revoked_at", now).Error; err != nil {
			return accountServerError(c, "Unable to revoke API token")
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "Token revoked"})
	}
	if token.RevokedAt == nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": "Token is not revoked"})
	}
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.APIToken{}).Where("id = ? AND user_id = ? AND revoked_at IS NOT NULL", id, user.ID).Update("revoked_at", nil).Error; err != nil {
		return accountServerError(c, "Unable to restore API token")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Token restored"})
}

func deleteAccountToken(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	id, ok := accountAPIID(c, "id")
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Token not found"})
	}
	var token core.APIToken
	if err := data.DB.WithContext(c.Request().Context()).Where("id = ? AND user_id = ?", id, user.ID).First(&token).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Token not found"})
		}
		return accountServerError(c, "Unable to load API token")
	}
	if token.RevokedAt == nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": "Revoke token before deleting it"})
	}
	if err := data.DB.WithContext(c.Request().Context()).Delete(&token).Error; err != nil {
		return accountServerError(c, "Unable to delete API token")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Token deleted"})
}

func accountTokenStatus(token core.APIToken, now time.Time) string {
	if token.RevokedAt != nil {
		return "revoked"
	}
	if token.ExpiresAt != nil && !token.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}
