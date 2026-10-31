package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
	_ "golang.org/x/image/webp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxAvatarBytes = 2 << 20

var (
	errAccountAuthenticationChanged = errors.New("account authentication state changed")
	errAccountEmailInUse            = errors.New("email is already in use")
)

type accountProfile struct {
	ID            uint      `json:"id"`
	Username      string    `json:"username"`
	DisplayName   string    `json:"display_name"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	PendingEmail  string    `json:"pending_email"`
	AvatarURL     string    `json:"avatar_url"`
	Bio           string    `json:"bio"`
	Locale        string    `json:"locale"`
	UITheme       string    `json:"ui_theme"`
	Role          string    `json:"role"`
	CreatedAt     time.Time `json:"created_at"`
	PostCount     int64     `json:"post_count"`
	AuthorURL     string    `json:"author_url"`
}

func getAccountProfile(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var count int64
	if err := data.DB.Model(&core.Post{}).Where("author_id = ? AND type = ?", user.ID, "post").Count(&count).Error; err != nil {
		return accountServerError(c, "Unable to load profile")
	}
	return c.JSON(http.StatusOK, accountProfile{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Email: user.Email,
		EmailVerified: user.EmailVerifiedAt != nil, PendingEmail: user.PendingEmail, AvatarURL: user.AvatarURL,
		Bio: user.Bio, Locale: user.Locale, UITheme: user.UITheme, Role: user.Role, CreatedAt: user.CreatedAt,
		PostCount: count, AuthorURL: "/author/" + user.Username,
	})
}

func patchAccountProfile(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		DisplayName *string `json:"display_name"`
		Bio         *string `json:"bio"`
		Locale      *string `json:"locale"`
		UITheme     *string `json:"ui_theme"`
	}
	if err := c.Bind(&req); err != nil {
		return accountBadRequest(c, "Invalid profile")
	}
	updates := make(map[string]any)
	if req.DisplayName != nil {
		if utf8.RuneCountInString(*req.DisplayName) < 1 || utf8.RuneCountInString(*req.DisplayName) > 120 {
			return accountBadRequest(c, "display_name must be 1 to 120 characters")
		}
		updates["display_name"] = *req.DisplayName
	}
	if req.Bio != nil {
		if utf8.RuneCountInString(*req.Bio) > 500 {
			return accountBadRequest(c, "bio must be at most 500 characters")
		}
		updates["bio"] = *req.Bio
	}
	if req.Locale != nil {
		if *req.Locale != "zh-TW" && *req.Locale != "en" {
			return accountBadRequest(c, "locale must be zh-TW or en")
		}
		updates["locale"] = *req.Locale
	}
	if req.UITheme != nil {
		if *req.UITheme != "system" && *req.UITheme != "light" && *req.UITheme != "dark" {
			return accountBadRequest(c, "ui_theme must be system, light or dark")
		}
		updates["ui_theme"] = *req.UITheme
	}
	if len(updates) > 0 {
		if err := data.DB.WithContext(c.Request().Context()).Model(&core.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return accountServerError(c, "Unable to update profile")
		}
	}
	return getAccountProfile(c)
}

func uploadAccountAvatar(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxAvatarBytes+256*1024)
	file, err := c.FormFile("avatar")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "avatar exceeds 2 MB"})
		}
		return accountBadRequest(c, "avatar file is required")
	}
	if file.Size > maxAvatarBytes {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "avatar exceeds 2 MB"})
	}
	src, err := file.Open()
	if err != nil {
		return accountServerError(c, "Unable to read avatar")
	}
	defer src.Close()
	content, err := io.ReadAll(io.LimitReader(src, maxAvatarBytes+1))
	if err != nil {
		return accountServerError(c, "Unable to read avatar")
	}
	if len(content) == 0 || len(content) > maxAvatarBytes {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "avatar exceeds 2 MB"})
	}
	format, err := validateAvatar(content)
	if err != nil {
		return accountBadRequest(c, "avatar must be a valid PNG, JPEG or WebP image")
	}
	if err := os.MkdirAll("data/uploads", 0755); err != nil {
		return accountServerError(c, "Unable to save avatar")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return accountServerError(c, "Unable to save avatar")
	}
	name := fmt.Sprintf("avatar-%d-%s.%s", user.ID, hex.EncodeToString(nonce[:]), format)
	path := filepath.Join("data/uploads", name)
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return accountServerError(c, "Unable to save avatar")
	}
	_, writeErr := dst.Write(content)
	closeErr := dst.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return accountServerError(c, "Unable to save avatar")
	}
	avatarURL := "/uploads/" + name
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.User{}).Where("id = ?", user.ID).Update("avatar_url", avatarURL).Error; err != nil {
		_ = os.Remove(path)
		return accountServerError(c, "Unable to update avatar")
	}
	removeLocalAvatar(user.AvatarURL, user.ID)
	return c.JSON(http.StatusOK, map[string]string{"avatar_url": avatarURL})
}

func validateAvatar(content []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	extension, ok := map[string]string{"png": "png", "jpeg": "jpg", "webp": "webp"}[format]
	if !ok {
		return "", fmt.Errorf("unsupported image format")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return "", fmt.Errorf("image dimensions are not supported")
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	if decodedFormat != format {
		return "", fmt.Errorf("invalid image data")
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 || bounds.Dx() > 4096 || bounds.Dy() > 4096 {
		return "", fmt.Errorf("image dimensions are not supported")
	}
	return extension, nil
}

func removeLocalAvatar(value string, userID uint) {
	const prefix = "/uploads/"
	if !strings.HasPrefix(value, prefix) {
		return
	}
	name := strings.TrimPrefix(value, prefix)
	if filepath.Base(name) != name || strings.ContainsAny(name, "?#\\") {
		return
	}
	ownedPrefix := fmt.Sprintf("avatar-%d-", userID)
	if !strings.HasPrefix(name, ownedPrefix) {
		return
	}
	extension := filepath.Ext(name)
	if extension != ".png" && extension != ".jpg" && extension != ".webp" {
		return
	}
	randomPart := strings.TrimSuffix(strings.TrimPrefix(name, ownedPrefix), extension)
	if len(randomPart) != 32 || !isLowerHex(randomPart) {
		return
	}
	if data.DB == nil {
		log.Printf("account avatar cleanup skipped for user %d: database unavailable", userID)
		return
	}
	var references int64
	if err := data.DB.Model(&core.User{}).Where("id <> ? AND avatar_url = ?", userID, value).Count(&references).Error; err != nil {
		log.Printf("account avatar reference check failed for user %d: %v", userID, err)
		return
	}
	if references > 0 {
		return
	}
	path := filepath.Join("data/uploads", name)
	info, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("account avatar cleanup failed for %q: %v", name, err)
		}
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("account avatar cleanup failed for %q: %v", name, err)
	}
}

func deleteAccountAvatar(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.User{}).Where("id = ?", user.ID).Update("avatar_url", "").Error; err != nil {
		return accountServerError(c, "Unable to remove avatar")
	}
	removeLocalAvatar(user.AvatarURL, user.ID)
	return c.JSON(http.StatusOK, map[string]string{"avatar_url": ""})
}

func requestAccountEmailChange(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		NewEmail        string `json:"new_email"`
		CurrentPassword string `json:"current_password"`
	}
	if err := c.Bind(&req); err != nil {
		return accountBadRequest(c, "Invalid request")
	}
	email := strings.ToLower(strings.TrimSpace(req.NewEmail))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 255 {
		return accountBadRequest(c, "Invalid email address")
	}
	if user.Password == "" || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.CurrentPassword)) != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Current password is invalid"})
	}
	currentHash, ok := auth.CurrentLoginHash(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}
	secret, hash, err := newAccountOneTimeSecret()
	if err != nil {
		return accountServerError(c, "Unable to create confirmation link")
	}
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := lockVerifiedAccountLogin(tx, user, currentHash); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&core.User{}).Where("lower(email) = lower(?) AND id <> ?", email, user.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errAccountEmailInUse
		}
		now := time.Now()
		expires := now.Add(24 * time.Hour)
		if err := tx.Model(&core.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"pending_email": email, "pending_email_hash": hash, "pending_email_expires_at": expires,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&core.UserInvite{}).Where("user_id = ? AND kind = ? AND accepted_at IS NULL AND revoked_at IS NULL", user.ID, "email").Update("revoked_at", now).Error; err != nil {
			return err
		}
		invite := core.UserInvite{UserID: user.ID, Kind: "email", TokenHash: hash, ExpiresAt: expires}
		return tx.Create(&invite).Error
	})
	if err != nil {
		if errors.Is(err, errAccountAuthenticationChanged) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Account credentials or login changed"})
		}
		if errors.Is(err, errAccountEmailInUse) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "Email is already in use"})
		}
		if isUniqueViolation(err) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "Email is already in use"})
		}
		return accountServerError(c, "Unable to request email change")
	}
	confirmURL := accountBaseURL(c) + "/account/email/confirm?token=" + secret
	return c.JSON(http.StatusAccepted, map[string]string{"confirm_url": confirmURL})
}

func confirmAccountEmail(c echo.Context) error {
	raw := c.QueryParam("token")
	if raw == "" {
		return accountBadRequest(c, "Confirmation token is required")
	}
	hash := accountSecretHash(raw)
	var candidate core.UserInvite
	if err := data.DB.WithContext(c.Request().Context()).
		Select("id", "user_id").
		Where("kind = ? AND token_hash = ?", "email", hash).
		First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusGone, map[string]string{"error": "Confirmation link is invalid or expired"})
		}
		return accountServerError(c, "Unable to confirm email")
	}
	var user core.User
	err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, candidate.UserID).Error; err != nil {
			return err
		}
		var invite core.UserInvite
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ? AND kind = ? AND token_hash = ?", candidate.ID, candidate.UserID, "email", hash).
			First(&invite).Error; err != nil {
			return err
		}
		now := time.Now()
		if invite.AcceptedAt != nil || invite.RevokedAt != nil || !invite.ExpiresAt.After(now) {
			return gorm.ErrRecordNotFound
		}
		if user.PendingEmailHash != hash || user.PendingEmail == "" || user.PendingEmailExpiresAt == nil || !user.PendingEmailExpiresAt.After(now) {
			return gorm.ErrRecordNotFound
		}
		var duplicate int64
		if err := tx.Model(&core.User{}).Where("lower(email) = lower(?) AND id <> ?", user.PendingEmail, user.ID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return gorm.ErrDuplicatedKey
		}
		verified := now
		if err := tx.Model(&core.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"email": user.PendingEmail, "email_verified_at": verified, "pending_email": "",
			"pending_email_hash": "", "pending_email_expires_at": nil,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&core.UserInvite{}).Where("id = ?", invite.ID).Update("accepted_at", now).Error; err != nil {
			return err
		}
		user.Email = user.PendingEmail
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueViolation(err) {
		return c.JSON(http.StatusGone, map[string]string{"error": "Confirmation link is invalid or expired"})
	}
	if err != nil {
		return accountServerError(c, "Unable to confirm email")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Email confirmed"})
}

func changeAccountPassword(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.Bind(&req); err != nil || utf8.RuneCountInString(req.NewPassword) < 8 || len(req.NewPassword) > 72 {
		return accountBadRequest(c, "new_password must be at least 8 characters")
	}
	if user.Password != "" && bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.CurrentPassword)) != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Current password is invalid"})
	}
	currentHash, ok := auth.CurrentLoginHash(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return accountServerError(c, "Unable to update password")
	}
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := lockVerifiedAccountLogin(tx, user, currentHash); err != nil {
			return err
		}
		if err := tx.Model(&core.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"password": string(hash), "password_changed_at": time.Now(),
		}).Error; err != nil {
			return err
		}
		return auth.RevokeUserLogins(tx, user.ID, currentHash)
	})
	if err != nil {
		if errors.Is(err, errAccountAuthenticationChanged) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Account credentials or login changed"})
		}
		return accountServerError(c, "Unable to update password")
	}
	if err := notify.Emit(c.Request().Context(), data.DB, notify.Event{
		Kind: notify.KindSecurityPasswordChanged, Category: notify.CategorySecurity,
		Params: map[string]any{}, Recipients: notify.Recipients{UserIDs: []uint{user.ID}},
	}); err != nil {
		return accountServerError(c, "Password changed but security notification failed")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Password updated"})
}

func lockVerifiedAccountLogin(tx *gorm.DB, snapshot *core.User, loginHash string) (*core.User, error) {
	var current core.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, snapshot.ID).Error; err != nil {
		return nil, err
	}
	if current.Status != "active" || current.Password != snapshot.Password ||
		!samePasswordChangedAt(current.PasswordChangedAt, snapshot.PasswordChangedAt) {
		return nil, errAccountAuthenticationChanged
	}
	if loginHash == "" {
		return nil, errAccountAuthenticationChanged
	}
	now := time.Now()
	var login core.LoginSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?", loginHash, current.ID, now).
		First(&login).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errAccountAuthenticationChanged
		}
		return nil, err
	}
	var revoked core.RevokedLogin
	if err := tx.First(&revoked, "id = ?", loginHash).Error; err == nil {
		return nil, errAccountAuthenticationChanged
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &current, nil
}

func samePasswordChangedAt(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func accountUser(c echo.Context) (*core.User, error) {
	if user, ok := auth.GetUserFromContext(c); ok && user != nil {
		return user, nil
	}
	user, err := auth.GetCurrentUser(c)
	if err != nil {
		return nil, c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}
	return user, nil
}

func currentAccountLoginHash(c echo.Context) string {
	if hash, ok := auth.CurrentLoginHash(c); ok {
		return hash
	}
	return ""
}

func accountSecretHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newAccountOneTimeSecret() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw[:])
	return token, accountSecretHash(token), nil
}

func accountBaseURL(c echo.Context) string {
	if value := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/"); value != "" {
		return value
	}
	return c.Scheme() + "://" + c.Request().Host
}

func accountBadRequest(c echo.Context, message string) error {
	return c.JSON(http.StatusBadRequest, map[string]string{"error": message})
}

func accountServerError(c echo.Context, message string) error {
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": message})
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key") || strings.Contains(message, "sqlstate 23505")
}
